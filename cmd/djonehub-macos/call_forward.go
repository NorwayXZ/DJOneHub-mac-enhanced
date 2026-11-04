package main

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Call forwarding is set in the carrier network with AT+CCFC, so calls ring
// the user's phone even when the Mac is off. Reasons: 0 unconditional,
// 1 busy, 2 no reply, 3 not reachable. The SIM's own forwarding before the
// first change (typically no reply → voicemail) is saved per ICCID and put
// back when forwarding is turned off.
const ccfcTimeout = 40 * time.Second

var (
	ccfcLine      = regexp.MustCompile(`\+CCFC:\s*(\d+),(\d+)(?:,"([^"]*)",(\d+)(?:,[^,]*,[^,]*(?:,(\d+))?)?)?`)
	forwardNumber = regexp.MustCompile(`^\+?[0-9]{3,20}$`)
)

type ccfcEntry struct {
	Reason int    `json:"reason"`
	Active bool   `json:"active"`
	Number string `json:"number,omitempty"`
	Type   int    `json:"type,omitempty"`
	Time   int    `json:"time,omitempty"`
}

type callForwardSaved struct {
	Mode     string      `json:"mode"` // "all" | "conditional"
	Number   string      `json:"number"`
	Original []ccfcEntry `json:"original"`
}

// parseCCFC reads a query answer; only the voice class counts.
func parseCCFC(reason int, response string) (ccfcEntry, error) {
	if strings.Contains(response, "ERROR") {
		return ccfcEntry{Reason: reason}, ccfcError(response)
	}
	entry := ccfcEntry{Reason: reason}
	for _, m := range ccfcLine.FindAllStringSubmatch(response, -1) {
		class, _ := strconv.Atoi(m[2])
		if m[1] != "1" || class&1 == 0 {
			continue
		}
		entry.Active = true
		entry.Number = m[3]
		entry.Type, _ = strconv.Atoi(m[4])
		entry.Time, _ = strconv.Atoi(m[5])
	}
	return entry, nil
}

func ccfcError(response string) error {
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "+CME ERROR") {
			return errors.New("运营商拒绝了呼叫转移（" + line + "）。这张卡或当前网络可能不支持，或需先开通该业务")
		}
	}
	return errors.New("运营商拒绝了呼叫转移，这张卡或当前网络可能不支持")
}

func numberType(number string) int {
	if strings.HasPrefix(number, "+") {
		return 145
	}
	return 129
}

// ccfcSetCommand registers forwarding for reason; no reply also gets its ring time.
func ccfcSetCommand(e ccfcEntry) string {
	if !e.Active {
		return fmt.Sprintf("AT+CCFC=%d,4", e.Reason)
	}
	t := e.Type
	if t == 0 {
		t = numberType(e.Number)
	}
	if e.Reason == 2 && e.Time > 0 {
		return fmt.Sprintf(`AT+CCFC=%d,3,"%s",%d,1,,,%d`, e.Reason, e.Number, t, e.Time)
	}
	return fmt.Sprintf(`AT+CCFC=%d,3,"%s",%d`, e.Reason, e.Number, t)
}

func (a *app) queryCCFC(reason int) (ccfcEntry, error) {
	response, err := a.runATCommand(fmt.Sprintf("AT+CCFC=%d,2", reason), ccfcTimeout)
	if err != nil {
		return ccfcEntry{Reason: reason}, err
	}
	return parseCCFC(reason, response)
}

func (a *app) applyCCFC(e ccfcEntry) error {
	response, err := a.runATCommand(ccfcSetCommand(e), ccfcTimeout)
	if err != nil {
		return err
	}
	if strings.Contains(response, "ERROR") {
		return ccfcError(response)
	}
	return nil
}

func (a *app) queryAllCCFC() ([]ccfcEntry, error) {
	entries := make([]ccfcEntry, 0, 4)
	for reason := 0; reason <= 3; reason++ {
		e, err := a.queryCCFC(reason)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

type callForwardView struct {
	ICCID   string            `json:"iccid"`
	Entries []ccfcEntry       `json:"entries"`
	Saved   *callForwardSaved `json:"saved,omitempty"`
	Error   string            `json:"error,omitempty"`
}

func (a *app) callForwardView(iccid string) callForwardView {
	view := callForwardView{ICCID: iccid}
	if saved, ok := forwarding.get().CallForward[iccid]; ok {
		saved.Original = nil
		view.Saved = &saved
	}
	entries, err := a.queryAllCCFC()
	if err != nil {
		view.Error = err.Error()
	}
	view.Entries = entries
	return view
}

// callForwardAPI: GET queries the network. POST {action:"set", mode, number}
// turns forwarding on; {action:"clear"} restores the SIM's own forwarding.
func (a *app) callForwardAPI(w http.ResponseWriter, r *http.Request) {
	if a.demo {
		writeError(w, 409, "演示模式下不能设置呼叫转移")
		return
	}
	if !a.tryModuleOperation() {
		writeError(w, 409, errModuleBusy)
		return
	}
	defer a.moduleOperationMu.Unlock()
	iccid := a.currentICCID()
	if iccid == "" {
		writeError(w, 409, "读不到 SIM 卡，请确认卡已插好")
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, 200, a.callForwardView(iccid))
		return
	}
	var body struct {
		Action string `json:"action"`
		Mode   string `json:"mode"`
		Number string `json:"number"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	saved, hasSaved := forwarding.get().CallForward[iccid]
	switch body.Action {
	case "set":
		number := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(body.Number), " ", ""), "-", "")
		if !forwardNumber.MatchString(number) {
			writeError(w, 400, "号码格式不对：只填数字，国际号码前加 +，例如 +8613800138000")
			return
		}
		if body.Mode != "all" && body.Mode != "conditional" {
			writeError(w, 400, "请选择转移方式")
			return
		}
		if !hasSaved {
			original, err := a.queryAllCCFC()
			if err != nil {
				writeError(w, 502, "读取这张卡原有的转移设置失败："+err.Error())
				return
			}
			saved = callForwardSaved{Original: original}
		}
		target := []ccfcEntry{{Reason: 0, Active: true, Number: number}}
		if body.Mode == "conditional" {
			target = []ccfcEntry{originalFor(saved, 0), {Reason: 1, Active: true, Number: number}, {Reason: 2, Active: true, Number: number, Time: 20}, {Reason: 3, Active: true, Number: number}}
		}
		saved.Mode, saved.Number = body.Mode, number
		// Save before touching the network so the original survives a failure halfway.
		if err := forwarding.update(func(s *forwardSettings) {
			if s.CallForward == nil {
				s.CallForward = map[string]callForwardSaved{}
			}
			s.CallForward[iccid] = saved
		}); err != nil {
			writeError(w, 500, "保存失败："+err.Error())
			return
		}
		for _, e := range target {
			if err := a.applyCCFC(e); err != nil {
				writeError(w, 502, err.Error())
				return
			}
		}
	case "clear":
		if hasSaved {
			for _, e := range saved.Original {
				if err := a.applyCCFC(e); err != nil {
					writeError(w, 502, "恢复原有设置失败："+err.Error())
					return
				}
			}
		} else if err := a.applyCCFC(ccfcEntry{Reason: 0}); err != nil {
			writeError(w, 502, err.Error())
			return
		}
		if err := forwarding.update(func(s *forwardSettings) { delete(s.CallForward, iccid) }); err != nil {
			writeError(w, 500, "保存失败："+err.Error())
			return
		}
	default:
		writeError(w, 400, "未知的操作")
		return
	}
	writeJSON(w, 200, a.callForwardView(iccid))
}

func originalFor(saved callForwardSaved, reason int) ccfcEntry {
	for _, e := range saved.Original {
		if e.Reason == reason {
			return e
		}
	}
	return ccfcEntry{Reason: reason}
}
