package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// SIM keep-alive: many prepaid SIMs (foreign ones especially) are recycled
// after months without a chargeable event. For each SIM the user opts in,
// the module holding it sends one SMS every N days. A SIM that is due but not
// in any module gets a push reminder instead. Plans live in
// Application Support/DJOneHub/sim-keepalive.json, keyed by ICCID, shared by
// every module worker.
const (
	keepAliveCheckEvery = 30 * time.Minute
	// A SIM not seen by any module for this long counts as not inserted.
	keepAliveSeenWithin = 2 * time.Hour
	keepAliveGrace      = 3 * 24 * time.Hour
	defaultKeepAliveDay = 90
	defaultKeepAliveMsg = "保号"
)

type keepAlivePlan struct {
	Enabled bool   `json:"enabled"`
	Days    int    `json:"every_days"`
	To      string `json:"to"`
	Text    string `json:"text"`
	// Phone is the SIM's own number when the module knows it, for display.
	Phone string `json:"phone,omitempty"`
	// LastDone: last keep-alive SMS or any SMS sent from this SIM.
	LastDone   string `json:"last_done,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	LastSeen   string `json:"last_seen,omitempty"`
	LastRemind string `json:"last_remind,omitempty"`
	// Profile is the eSIM Profile name, for numbers on an eSIM card.
	Profile string `json:"profile,omitempty"`
}

type keepAliveFile struct {
	Plans map[string]keepAlivePlan `json:"plans"`
	// Returns: per eSIM card (EID), the Profile to switch back to after a
	// keep-alive run on another Profile.
	Returns map[string]esimReturn `json:"returns,omitempty"`
}

var keepAlive = settingsFile[keepAliveFile]{name: "sim-keepalive.json"}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// due reports whether the plan needs an SMS now: never done, or the
// interval has passed.
func (p keepAlivePlan) due(now time.Time) bool {
	if !p.Enabled {
		return false
	}
	last := parseTime(p.LastDone)
	return last.IsZero() || now.Sub(last) >= time.Duration(p.days())*24*time.Hour
}

func (p keepAlivePlan) days() int {
	if p.Days <= 0 {
		return defaultKeepAliveDay
	}
	return p.Days
}

func (p keepAlivePlan) nextDue() time.Time {
	last := parseTime(p.LastDone)
	if last.IsZero() {
		return time.Time{}
	}
	return last.Add(time.Duration(p.days()) * 24 * time.Hour)
}

func updatePlan(iccid string, change func(*keepAlivePlan)) error {
	return keepAlive.update(func(f *keepAliveFile) {
		if f.Plans == nil {
			f.Plans = map[string]keepAlivePlan{}
		}
		p := f.Plans[iccid]
		change(&p)
		f.Plans[iccid] = p
	})
}

// noteSIMActivity records an SMS the user sent, which also keeps the SIM alive.
func noteSIMActivity(iccid string, now time.Time) {
	if iccid == "" {
		return
	}
	if _, ok := keepAlive.get().Plans[iccid]; !ok {
		return
	}
	_ = updatePlan(iccid, func(p *keepAlivePlan) { p.LastDone, p.LastError = now.UTC().Format(time.RFC3339), "" })
}

func (a *app) runSIMKeepAlive(ctx context.Context) {
	if a.demo {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute): // let the module settle after startup
	}
	ticker := time.NewTicker(keepAliveCheckEvery)
	defer ticker.Stop()
	for {
		a.keepAliveOnce(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *app) keepAliveOnce(now time.Time) {
	if rotatingICCID() != "" {
		return // an eSIM run is switching Profiles right now
	}
	normalizePlanKeys()
	iccid := a.simICCID()
	if iccid != "" {
		_ = updatePlan(iccid, func(p *keepAlivePlan) { p.LastSeen = now.UTC().Format(time.RFC3339) })
		if keepAlive.get().Plans[iccid].due(now) {
			if _, err := a.runKeepAlive(iccid, now); err != nil {
				log.Printf("SIM keep-alive for %s failed: %v", shortICCID(iccid), err)
			}
		}
	}
	a.keepAliveESIM(now, iccid)
	a.remindAbsentSIMs(now)
}

// runKeepAlive sends the plan's SMS from the SIM in this module.
func (a *app) runKeepAlive(iccid string, now time.Time) (keepAlivePlan, error) {
	plan := keepAlive.get().Plans[iccid]
	if c := cardCapabilityFor(iccid); c != nil && c.Kind == "data_only" {
		err := fmt.Errorf("这是流量卡，没有短信功能，发不了保号短信")
		_ = updatePlan(iccid, func(p *keepAlivePlan) { p.LastError = err.Error() })
		return plan, err
	}
	to := strings.TrimSpace(plan.To)
	if to == "" {
		to = a.ownNumber()
	}
	if to == "" {
		err := fmt.Errorf("没有设置发送号码，也读不到本机号码")
		_ = updatePlan(iccid, func(p *keepAlivePlan) { p.LastError = err.Error() })
		return plan, err
	}
	text := strings.TrimSpace(plan.Text)
	if text == "" {
		text = defaultKeepAliveMsg
	}
	if !a.tryModuleOperation() {
		return plan, fmt.Errorf("%s", errModuleBusy)
	}
	_, err := a.sendTextSMS(to, text)
	a.moduleOperationMu.Unlock()
	stamp := now.UTC().Format(time.RFC3339)
	_ = updatePlan(iccid, func(p *keepAlivePlan) {
		if err != nil {
			p.LastError = "发送失败：" + err.Error()
			return
		}
		p.LastDone, p.LastError = stamp, ""
	})
	settings := forwarding.get()
	if err == nil {
		log.Printf("SIM keep-alive sent for %s", shortICCID(iccid))
		go deliverPush(settings, "SIM 保号已完成", fmt.Sprintf("卡 %s%s 已发送保号短信给 %s；下次在 %d 天后。", shortICCID(iccid), phoneSuffix(plan.Phone), to, plan.days()), 2)
	} else {
		go deliverPush(settings, "SIM 保号失败", fmt.Sprintf("卡 %s%s 发送保号短信失败：%v。请检查余额或信号。", shortICCID(iccid), phoneSuffix(plan.Phone), err), 2)
	}
	return keepAlive.get().Plans[iccid], err
}

// remindAbsentSIMs pushes a reminder, at most once a day, for SIMs that are
// overdue and not in any module. Every module worker may notice; the shared
// file makes sure only one reminder goes out.
func (a *app) remindAbsentSIMs(now time.Time) {
	settings := forwarding.get()
	if !settings.telegramOn() && !settings.weChatOn() {
		return
	}
	for iccid, plan := range keepAlive.get().Plans {
		next := plan.nextDue()
		if !plan.Enabled || next.IsZero() || now.Sub(next) < keepAliveGrace || now.Sub(parseTime(plan.LastSeen)) < keepAliveSeenWithin {
			continue
		}
		if now.Sub(parseTime(plan.LastRemind)) < 24*time.Hour {
			continue
		}
		claimed := false
		_ = updatePlan(iccid, func(p *keepAlivePlan) {
			if now.Sub(parseTime(p.LastRemind)) >= 24*time.Hour {
				p.LastRemind, claimed = now.UTC().Format(time.RFC3339), true
			}
		})
		if claimed {
			overdue := int(now.Sub(next).Hours() / 24)
			go deliverPush(settings, "SIM 需要保号",
				fmt.Sprintf("卡 %s%s 已超过保号期限 %d 天，但没有插在任何模块上。请插入任一模块，DJOneHub 会自动发送保号短信。", shortICCID(iccid), phoneSuffix(plan.Phone), overdue), 2)
		}
	}
}

// simICCID is the SIM's ICCID as keep-alive plans key it. The module pads an
// odd-length ICCID with a trailing F (AT+QCCID) that the eSIM card's Profile
// list does not have, so the padding is dropped.
func (a *app) simICCID() string { return normICCID(a.currentICCID()) }

func normICCID(iccid string) string {
	return strings.TrimRight(strings.TrimSpace(iccid), "Ff")
}

// normalizePlanKeys merges plans saved under a padded ICCID.
func normalizePlanKeys() {
	padded := false
	for iccid := range keepAlive.get().Plans {
		padded = padded || normICCID(iccid) != iccid
	}
	if !padded {
		return
	}
	_ = keepAlive.update(func(f *keepAliveFile) {
		for iccid, plan := range f.Plans {
			if key := normICCID(iccid); key != iccid {
				delete(f.Plans, iccid)
				if existing, ok := f.Plans[key]; !ok || (!existing.Enabled && plan.Enabled) {
					f.Plans[key] = plan
				}
			}
		}
	})
}

func (a *app) ownNumber() string {
	response, err := a.runATCommand("AT+CNUM", 3*time.Second)
	if err != nil {
		return ""
	}
	return parseCNUM(response)
}

func shortICCID(iccid string) string {
	if len(iccid) <= 6 {
		return iccid
	}
	return "…" + iccid[len(iccid)-6:]
}

func phoneSuffix(phone string) string {
	if phone == "" {
		return ""
	}
	return "（" + phone + "）"
}

type keepAliveCard struct {
	ICCID   string        `json:"iccid"`
	Current bool          `json:"current"`
	Plan    keepAlivePlan `json:"plan"`
	NextDue string        `json:"next_due,omitempty"`
	// OnCard: another Profile on the eSIM card in this module.
	OnCard  bool   `json:"on_card,omitempty"`
	Profile string `json:"profile,omitempty"`
}

type keepAliveView struct {
	CurrentICCID string          `json:"current_iccid"`
	OwnNumber    string          `json:"own_number"`
	Cards        []keepAliveCard `json:"cards"`
	// Rotating is the eSIM Profile being kept alive right now.
	Rotating string `json:"rotating,omitempty"`
}

func (a *app) keepAliveView() keepAliveView {
	normalizePlanKeys()
	view := keepAliveView{CurrentICCID: a.simICCID(), Cards: []keepAliveCard{}}
	if view.CurrentICCID != "" {
		view.OwnNumber = a.ownNumber()
		if view.OwnNumber != "" {
			_ = updatePlan(view.CurrentICCID, func(p *keepAlivePlan) { p.Phone = view.OwnNumber })
		}
	}
	view.Rotating = rotatingICCID()
	names := map[string]string{}
	onCard := map[string]bool{}
	if view.Rotating == "" {
		for _, p := range a.cardProfiles(view.CurrentICCID) {
			names[p.ICCID] = p.Name
			onCard[p.ICCID] = p.ICCID != view.CurrentICCID
		}
	}
	plans := keepAlive.get().Plans
	fresh := func(iccid string, current bool) keepAliveCard {
		card := keepAliveCard{ICCID: iccid, Current: current, Plan: keepAlivePlan{Days: defaultKeepAliveDay, Text: defaultKeepAliveMsg}}
		if current {
			card.Plan.Phone = view.OwnNumber
		}
		return card
	}
	if _, ok := plans[view.CurrentICCID]; !ok && view.CurrentICCID != "" {
		view.Cards = append(view.Cards, fresh(view.CurrentICCID, true))
	}
	for iccid, other := range onCard {
		if _, ok := plans[iccid]; !ok && other {
			view.Cards = append(view.Cards, fresh(iccid, false))
		}
	}
	for iccid, plan := range plans {
		card := keepAliveCard{ICCID: iccid, Current: iccid == view.CurrentICCID, Plan: plan}
		if next := plan.nextDue(); !next.IsZero() {
			card.NextDue = next.Format(time.RFC3339)
		}
		view.Cards = append(view.Cards, card)
	}
	for i := range view.Cards {
		c := &view.Cards[i]
		c.OnCard = onCard[c.ICCID]
		if c.Profile = names[c.ICCID]; c.Profile == "" {
			c.Profile = c.Plan.Profile
		}
	}
	sort.SliceStable(view.Cards, func(i, j int) bool {
		if view.Cards[i].Current != view.Cards[j].Current {
			return view.Cards[i].Current
		}
		if view.Cards[i].OnCard != view.Cards[j].OnCard {
			return view.Cards[i].OnCard
		}
		return view.Cards[i].ICCID < view.Cards[j].ICCID
	})
	return view
}

// simKeepAliveAPI: GET lists the plans (the SIM in this module first).
// POST {iccid, enabled, every_days, to, text} saves a plan; {action:"run"}
// sends the keep-alive SMS from the SIM in this module now (with the iccid of
// another Profile on its eSIM card: switches to it, sends, switches back); {action:"delete",
// iccid} forgets a SIM.
func (a *app) simKeepAliveAPI(w http.ResponseWriter, r *http.Request) {
	if a.demo {
		writeError(w, 409, "演示模式下不能使用 SIM 保号")
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, 200, a.keepAliveView())
		return
	}
	var body struct {
		Action  string `json:"action"`
		ICCID   string `json:"iccid"`
		Enabled bool   `json:"enabled"`
		Days    int    `json:"every_days"`
		To      string `json:"to"`
		Text    string `json:"text"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	current := a.simICCID()
	switch body.Action {
	case "run":
		if body.ICCID != "" && body.ICCID != current {
			// Another Profile on this eSIM card: switch, send, switch back.
			if err := a.startProfileKeepAlive(body.ICCID, true); err != nil {
				writeError(w, 409, err.Error())
				return
			}
			writeJSON(w, 202, a.keepAliveView())
			return
		}
		if current == "" {
			writeError(w, 409, "读不到 SIM 卡，请确认卡已插好")
			return
		}
		if _, err := a.runKeepAlive(current, time.Now()); err != nil {
			writeError(w, 502, err.Error())
			return
		}
	case "delete":
		if err := keepAlive.update(func(f *keepAliveFile) { delete(f.Plans, body.ICCID) }); err != nil {
			writeError(w, 500, "保存失败："+err.Error())
			return
		}
	default:
		iccid := body.ICCID
		if iccid == "" {
			iccid = current
		}
		if iccid == "" {
			writeError(w, 409, "读不到 SIM 卡，请确认卡已插好")
			return
		}
		if body.Days < 1 || body.Days > 365 {
			writeError(w, 400, "间隔要在 1 到 365 天之间")
			return
		}
		to := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(body.To), " ", ""), "-", "")
		if to != "" && !forwardNumber.MatchString(to) {
			writeError(w, 400, "号码格式不对：只填数字，国际号码前加 +")
			return
		}
		if err := updatePlan(iccid, func(p *keepAlivePlan) {
			p.Enabled, p.Days, p.To, p.Text = body.Enabled, body.Days, to, strings.TrimSpace(body.Text)
		}); err != nil {
			writeError(w, 500, "保存失败："+err.Error())
			return
		}
	}
	writeJSON(w, 200, a.keepAliveView())
}
