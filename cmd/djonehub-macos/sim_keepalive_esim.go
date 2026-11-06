package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Keep-alive for every number on an eSIM card. Only one Profile is enabled
// at a time, so a disabled Profile that is due is switched in, sends its
// keep-alive SMS, and the card is switched back. That takes the module
// offline for a few minutes twice, so the automatic run waits for the night
// (keepAliveQuietFrom–keepAliveQuietTo, local time); "立即保号" runs it now.
// The Profile to return to is written to sim-keepalive.json before switching
// away, so a crash or module drop mid-way is undone by the next check.
const (
	keepAliveQuietFrom   = 3
	keepAliveQuietTo     = 6
	profileRegisterLimit = 5 * time.Minute
	profileCacheFor      = 10 * time.Minute
)

type esimCardProfile struct {
	ICCID  string
	Name   string
	AID    string
	EID    string
	Active bool
}

type esimReturn struct {
	ICCID string `json:"iccid"`
	AID   string `json:"aid"`
	Since string `json:"since"`
}

var (
	profileCacheMu   sync.Mutex
	profileCache     []esimCardProfile
	profileCacheKey  string
	profileCacheTime time.Time

	keepAliveRotatingMu sync.Mutex
	keepAliveRotating   string
)

func rotatingICCID() string {
	keepAliveRotatingMu.Lock()
	defer keepAliveRotatingMu.Unlock()
	return keepAliveRotating
}

// claimRotation marks iccid as being kept alive; false if another rotation runs.
func claimRotation(iccid string) bool {
	keepAliveRotatingMu.Lock()
	defer keepAliveRotatingMu.Unlock()
	if keepAliveRotating != "" {
		return false
	}
	keepAliveRotating = iccid
	return true
}

func releaseRotation() {
	keepAliveRotatingMu.Lock()
	keepAliveRotating = ""
	keepAliveRotatingMu.Unlock()
}

// cardProfiles lists the Profiles on the eSIM card in this module; nil for a
// physical SIM. The list is cached per active ICCID.
func (a *app) cardProfiles(current string) []esimCardProfile {
	profileCacheMu.Lock()
	defer profileCacheMu.Unlock()
	if profileCacheKey == current && time.Since(profileCacheTime) < profileCacheFor {
		return profileCache
	}
	profileCache, profileCacheKey, profileCacheTime = nil, current, time.Now()
	manager, _ := a.currentESIMManager()
	if manager == nil || current == "" {
		return nil
	}
	overview, err := manager.GetEsimOverview()
	if err != nil {
		return nil
	}
	for _, group := range overview.Profiles {
		for _, p := range group.Profiles {
			name := strings.TrimSpace(p.Name)
			if name == "" {
				name = strings.TrimSpace(p.ServiceProviderName)
			}
			profileCache = append(profileCache, esimCardProfile{ICCID: normICCID(p.ICCID), Name: name, AID: group.AIDHex, EID: group.EID, Active: p.State == 1})
		}
	}
	return profileCache
}

func forgetCardProfiles() {
	profileCacheMu.Lock()
	profileCacheKey = ""
	profileCacheMu.Unlock()
}

func inQuietHours(now time.Time) bool {
	h := now.Local().Hour()
	return h >= keepAliveQuietFrom && h < keepAliveQuietTo
}

// keepAliveESIM marks the card's Profiles as present, finishes an interrupted
// rotation, and in quiet hours keeps one due disabled Profile alive.
func (a *app) keepAliveESIM(now time.Time, current string) {
	if current == "" || rotatingICCID() != "" {
		return
	}
	file := keepAlive.get()
	needed := len(file.Returns) > 0
	for iccid, plan := range file.Plans {
		if plan.Enabled && iccid != current {
			needed = true
		}
	}
	if !needed {
		return // nothing to do on another Profile: don't read the card
	}
	profiles := a.cardProfiles(current)
	if len(profiles) == 0 {
		return
	}
	stamp := now.UTC().Format(time.RFC3339)
	for _, p := range profiles {
		if _, ok := file.Plans[p.ICCID]; ok {
			_ = updatePlan(p.ICCID, func(plan *keepAlivePlan) { plan.LastSeen, plan.Profile = stamp, p.Name })
		}
	}
	eid := profiles[0].EID
	if back, ok := file.Returns[eid]; ok {
		if back.ICCID == current {
			_ = keepAlive.update(func(f *keepAliveFile) { delete(f.Returns, eid) })
		} else if claimRotation(back.ICCID) {
			log.Printf("SIM keep-alive: switching the eSIM card back to %s after an interrupted run", shortICCID(back.ICCID))
			go func() {
				defer releaseRotation()
				a.restoreProfile(eid, esimCardProfile{ICCID: back.ICCID, AID: back.AID, EID: eid})
			}()
		}
		return
	}
	if !inQuietHours(now) {
		return
	}
	for _, p := range profiles {
		if !p.Active && keepAlive.get().Plans[p.ICCID].due(now) {
			if err := a.startProfileKeepAlive(p.ICCID, false); err != nil {
				log.Printf("SIM keep-alive for eSIM %s: %v", shortICCID(p.ICCID), err)
			}
			return // one Profile per check
		}
	}
}

// startProfileKeepAlive keeps a disabled Profile on this module's eSIM card
// alive. With background it returns once the run has started.
func (a *app) startProfileKeepAlive(iccid string, background bool) error {
	current := a.simICCID()
	var target, back esimCardProfile
	for _, p := range a.cardProfiles(current) {
		if p.ICCID == iccid {
			target = p
		}
		if p.ICCID == current {
			back = p
		}
	}
	if target.ICCID == "" {
		return fmt.Errorf("这个号码不在当前模块的 eSIM 卡上")
	}
	if back.ICCID == "" {
		return fmt.Errorf("读不到当前启用的 eSIM 号码")
	}
	if !claimRotation(iccid) {
		return fmt.Errorf("另一个号码正在保号，请稍后")
	}
	run := func() {
		defer releaseRotation()
		a.rotateKeepAlive(target, back)
	}
	if background {
		go run()
	} else {
		run()
	}
	return nil
}

func (a *app) rotateKeepAlive(target, back esimCardProfile) {
	settings := forwarding.get()
	label := shortICCID(target.ICCID) + phoneSuffix(target.Name)
	fail := func(reason string) {
		_ = updatePlan(target.ICCID, func(p *keepAlivePlan) { p.LastError = reason })
		go deliverPush(settings, "SIM 保号失败", fmt.Sprintf("eSIM 号码 %s：%s", label, reason), 2)
	}
	err := keepAlive.update(func(f *keepAliveFile) {
		if f.Returns == nil {
			f.Returns = map[string]esimReturn{}
		}
		f.Returns[back.EID] = esimReturn{ICCID: back.ICCID, AID: back.AID, Since: time.Now().UTC().Format(time.RFC3339)}
	})
	if err != nil {
		fail("保存失败：" + err.Error())
		return
	}
	log.Printf("SIM keep-alive: switching the eSIM card to %s", shortICCID(target.ICCID))
	if err := a.switchProfileFor(target); err != nil {
		fail("切换号码失败：" + err.Error())
		a.restoreProfile(back.EID, back)
		return
	}
	if !a.waitOnProfile(target.ICCID, profileRegisterLimit) {
		fail("切换后没注册上网络（可能已停机或没有信号）")
	} else {
		time.Sleep(20 * time.Second) // let SMS service come up after registration
		if phone := a.ownNumber(); phone != "" {
			_ = updatePlan(target.ICCID, func(p *keepAlivePlan) { p.Phone = phone })
		}
		if _, err := a.runKeepAlive(target.ICCID, time.Now()); err != nil {
			log.Printf("SIM keep-alive for eSIM %s failed: %v", shortICCID(target.ICCID), err)
		}
	}
	a.restoreProfile(back.EID, back)
}

// restoreProfile switches the card back to the Profile that was in use.
func (a *app) restoreProfile(eid string, back esimCardProfile) {
	if a.simICCID() != back.ICCID {
		if err := a.switchProfileFor(back); err != nil {
			log.Printf("SIM keep-alive: switching back to %s failed: %v", shortICCID(back.ICCID), err)
			go deliverPush(forwarding.get(), "SIM 保号：切回失败",
				fmt.Sprintf("保号后没能切回 eSIM 号码 %s：%v。半小时后会再试，也可以在 App 里手动切换。", shortICCID(back.ICCID), err), 2)
			return
		}
		if !a.waitOnProfile(back.ICCID, profileRegisterLimit) {
			log.Printf("SIM keep-alive: %s is enabled again but not registered yet", shortICCID(back.ICCID))
		}
	}
	_ = keepAlive.update(func(f *keepAliveFile) { delete(f.Returns, eid) })
	log.Printf("SIM keep-alive: eSIM card is back on %s", shortICCID(back.ICCID))
}

func (a *app) switchProfileFor(p esimCardProfile) error {
	manager, _ := a.currentESIMManager()
	if manager == nil {
		return fmt.Errorf("eSIM 管理不可用")
	}
	for tries := 0; !a.tryModuleOperation(); tries++ {
		if tries >= 12 {
			return fmt.Errorf("%s", errModuleBusy)
		}
		time.Sleep(5 * time.Second)
	}
	defer a.moduleOperationMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	forgetCardProfiles()
	_, _, _, _, err := a.enableProfile(ctx, manager, p.ICCID, p.AID)
	return err
}

// waitOnProfile waits until the module runs on iccid and is registered.
func (a *app) waitOnProfile(iccid string, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	time.Sleep(10 * time.Second) // the module is restarting
	for time.Now().Before(deadline) {
		if a.simICCID() == iccid && a.registered() {
			return true
		}
		time.Sleep(5 * time.Second)
	}
	return false
}
