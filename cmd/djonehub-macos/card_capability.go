package main

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iniwex5/vohive/internal/modem"
)

// Many travel eSIMs are data only: the operator gives them no SMS centre and
// no voice service, so verification codes never arrive. cardCapability tells
// the user which kind the card is, so a silent SMS inbox does not look like a
// broken module. Results are kept per ICCID, so disabled eSIM Profiles still
// show what was learned while they were enabled.
type cardCapability struct {
	Kind      string    `json:"kind"` // data_only, voice_sms
	Title     string    `json:"title"`
	Detail    string    `json:"detail"`
	SMSC      string    `json:"smsc,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

type cardCapabilityFile struct {
	Cards map[string]cardCapability `json:"cards"`
}

var cardCapabilities = settingsFile[cardCapabilityFile]{name: "card-capability.json"}

var (
	capabilityCheckedMu sync.Mutex
	// capabilityChecked: ICCIDs this process already probed while registered.
	capabilityChecked = map[string]bool{}
)

func cardCapabilityFor(iccid string) *cardCapability {
	iccid = normICCID(iccid)
	if iccid == "" {
		return nil
	}
	if c, ok := cardCapabilities.get().Cards[iccid]; ok {
		return &c
	}
	return nil
}

func allCardCapabilities() map[string]cardCapability {
	return cardCapabilities.get().Cards
}

// noteCardCapability probes the card in the module once per ICCID, after it
// has registered (an SMS centre read before that can be empty on any card).
func (a *app) noteCardCapability(iccid, cregResp, ceregResp string) {
	iccid = normICCID(iccid)
	cs, ps := registrationState(cregResp, "+CREG:"), registrationState(ceregResp, "+CEREG:")
	if iccid == "" || !(ps == 1 || ps == 5 || cs == 1 || cs == 5) {
		return
	}
	capabilityCheckedMu.Lock()
	done := capabilityChecked[iccid]
	capabilityChecked[iccid] = true
	capabilityCheckedMu.Unlock()
	if done {
		return
	}
	cscaResp, err := a.usbAT.Command("AT+CSCA?", 3*time.Second)
	if err != nil {
		capabilityCheckedMu.Lock()
		delete(capabilityChecked, iccid)
		capabilityCheckedMu.Unlock()
		return
	}
	c := classifyCard(parseCSCA(cscaResp), cs, ps)
	c.CheckedAt = time.Now().UTC()
	_ = cardCapabilities.update(func(f *cardCapabilityFile) {
		if f.Cards == nil {
			f.Cards = map[string]cardCapability{}
		}
		f.Cards[iccid] = c
	})
}

var cscaRe = regexp.MustCompile(`\+CSCA:\s*"([^"]*)"`)

func parseCSCA(resp string) string {
	if m := cscaRe.FindStringSubmatch(resp); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func registrationState(resp, prefix string) int {
	for _, line := range splitATLines(resp) {
		if !strings.HasPrefix(strings.ToUpper(line), prefix) {
			continue
		}
		fields := strings.Split(strings.TrimSpace(line[len(prefix):]), ",")
		if len(fields) >= 2 {
			if n, err := strconv.Atoi(strings.TrimSpace(fields[1])); err == nil {
				return n
			}
		}
	}
	return 0
}

// classifyCard: cs and ps are the +CREG (voice / SMS) and +CEREG (data)
// registration states.
func classifyCard(smsc string, cs, ps int) cardCapability {
	digits := 0
	for _, r := range smsc {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	csOK := cs == 1 || cs == 5
	psOK := ps == 1 || ps == 5
	switch {
	case digits < 6:
		return cardCapability{Kind: "data_only", SMSC: smsc,
			Title:  "流量卡（只能上网）",
			Detail: "运营商没有给这张卡开短信功能（卡里没有有效的短信中心号码），所以收不到验证码短信、发不了短信，也不能打电话。上网和代理不受影响。这是卡的类型决定的，不是模块坏了。"}
	case psOK && !csOK:
		return cardCapability{Kind: "data_only", SMSC: smsc,
			Title:  "流量卡（只能上网）",
			Detail: "这张卡只注册了上网网络，没有注册通话和短信网络，所以收不到短信、也不能打电话。上网和代理不受影响。这是卡的类型决定的，不是模块坏了。"}
	default:
		return cardCapability{Kind: "voice_sms", SMSC: smsc,
			Title:  "能上网，也能收发短信",
			Detail: "这张卡有短信中心号码，也注册了通话和短信网络，可以收验证码短信。能不能打电话、有没有本地号码，以套餐说明为准。"}
	}
}

// Status reads take about a second of serial AT commands, and every page of
// the app asks for them. One shared read serves everyone for a few seconds.
const statusCacheFor = 3 * time.Second

type statusCache struct {
	mu   sync.Mutex
	at   time.Time
	data modem.DeviceStatus
}

func (a *app) usbATStatus() (modem.DeviceStatus, error) {
	a.statusCache.mu.Lock()
	defer a.statusCache.mu.Unlock()
	if !a.statusCache.at.IsZero() && time.Since(a.statusCache.at) < statusCacheFor {
		return a.statusCache.data, nil
	}
	status, err := a.readUSBATStatus()
	if err != nil {
		a.statusCache.at = time.Time{}
		return status, err
	}
	a.statusCache.data, a.statusCache.at = status, time.Now()
	return status, nil
}

// forgetStatus makes the next status read go to the module, after an action
// that changes the SIM or the network.
func (a *app) forgetStatus() {
	a.statusCache.mu.Lock()
	a.statusCache.at = time.Time{}
	a.statusCache.mu.Unlock()
	a.forgetCardICCID()
}
