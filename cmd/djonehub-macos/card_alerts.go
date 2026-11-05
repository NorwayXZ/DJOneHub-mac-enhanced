package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Pushes for a module that lost its network: the plain-language reason once
// it has been offline for a while, and a note when it is back. A travel card
// that worked before and now is refused (or registers but passes no data) is
// reported as probably expired, since the user does not track every card's
// plan and start date.
type cardHealth struct {
	LastRegistered time.Time `json:"last_registered,omitempty"`
	LastDataOK     time.Time `json:"last_data_ok,omitempty"`
}

type cardHealthFile struct {
	Cards map[string]cardHealth `json:"cards"`
}

var cardHealthStore = settingsFile[cardHealthFile]{name: "card-health.json"}

func cardHealthFor(iccid string) cardHealth {
	return cardHealthStore.get().Cards[normICCID(iccid)]
}

// noteCardHealth saves what the card just did, at most hourly per card.
func noteCardHealth(iccid string, dataOK bool) {
	iccid = normICCID(iccid)
	if iccid == "" {
		return
	}
	now := time.Now().UTC()
	h := cardHealthFor(iccid)
	if now.Sub(h.LastRegistered) < time.Hour && (!dataOK || now.Sub(h.LastDataOK) < time.Hour) {
		return
	}
	_ = cardHealthStore.update(func(f *cardHealthFile) {
		if f.Cards == nil {
			f.Cards = map[string]cardHealth{}
		}
		h := f.Cards[iccid]
		h.LastRegistered = now
		if dataOK {
			h.LastDataOK = now
		}
		f.Cards[iccid] = h
	})
}

// cardICCID is the card in this module, read at most once a minute.
type iccidCache struct {
	mu    sync.Mutex
	value string
	at    time.Time
}

func (a *app) cardICCID() string {
	a.iccid.mu.Lock()
	defer a.iccid.mu.Unlock()
	if a.iccid.value != "" && time.Since(a.iccid.at) < time.Minute {
		return a.iccid.value
	}
	if v := normICCID(a.currentICCID()); v != "" {
		a.iccid.value, a.iccid.at = v, time.Now()
	}
	return a.iccid.value
}

func (a *app) forgetCardICCID() {
	a.iccid.mu.Lock()
	a.iccid.at = time.Time{}
	a.iccid.mu.Unlock()
}

// cardName: the eSIM Profile name when known, else the ICCID's last digits.
func cardName(iccid string) string {
	iccid = normICCID(iccid)
	if overview := loadSavedESIMOverview(); overview != nil {
		for _, g := range overview.Profiles {
			for _, p := range g.Profiles {
				if normICCID(p.ICCID) == iccid {
					if name := strings.TrimSpace(p.Name); name != "" {
						return name
					}
				}
			}
		}
	}
	if len(iccid) > 4 {
		return "尾号 " + iccid[len(iccid)-4:]
	}
	return "当前号码"
}

func pushModuleNote(title, body string) {
	settings := forwarding.get()
	if !settings.telegramOn() && !settings.weChatOn() {
		return
	}
	if module := moduleLabel(); module != "" {
		title = module + " · " + title
	}
	go sendForward(context.Background(), settings, title, body, 2)
}

func (a *app) pushNetworkIssue(issue *networkIssue) {
	minutes := int(time.Since(issue.Since).Minutes())
	pushModuleNote(issue.Title, fmt.Sprintf("%s（%s，已 %d 分钟）\n%s", issue.Title, cardName(a.cardICCID()), minutes, issue.Detail))
}

func (a *app) pushNetworkBack(offline time.Duration) {
	pushModuleNote("网络已恢复", fmt.Sprintf("%s 重新连上网络，掉线约 %d 分钟。", cardName(a.cardICCID()), int(offline.Minutes()+0.5)))
}

// noteRegistered runs after each check that found the module registered.
func (a *app) noteRegistered() {
	if offline, pushed := noteNetworkRegistered(); pushed {
		a.pushNetworkBack(offline)
	}
	noteCardHealth(a.cardICCID(), false)
	ledgerCard.Store(cardName(a.cardICCID()))
}

// noteLost runs after each check that found no registration.
func (a *app) noteLost(reg, signal int, ready bool) {
	worked := false
	if reg == 3 {
		worked = !cardHealthFor(a.cardICCID()).LastRegistered.IsZero()
	}
	noteNetworkLost(reg, signal, ready, worked)
	if issue := takeLossPush(time.Now()); issue != nil {
		a.pushNetworkIssue(issue)
	}
}

// noteExitCheck runs after each exit IP check through the cellular proxy.
func (a *app) noteExitCheck(ok bool) {
	iccid := a.cardICCID()
	issue, recovered := noteDataPath(ok, !cardHealthFor(iccid).LastDataOK.IsZero(), time.Now())
	if ok {
		noteCardHealth(iccid, true)
	}
	if issue != nil {
		a.pushNetworkIssue(issue)
	}
	if recovered {
		pushModuleNote("网络已恢复", cardName(iccid)+" 又能正常上网了。")
	}
}
