package main

import (
	"sync"
	"time"
)

// A card's own number. eSIM Profiles often leave the SIM's number field
// empty, so AT+CNUM answers nothing; the number typed in 模块资料 fills the
// gap. A number read from the SIM is kept on the card's keep-alive plan, so
// it is still known while another Profile is active.

// cardNumbersLocked merges both sources by normalized ICCID; the typed
// number wins. a.profileNotesMu must be held with the notes loaded.
func (a *app) cardNumbersLocked() map[string]string {
	out := map[string]string{}
	for iccid, plan := range keepAlive.get().Plans {
		if plan.Phone != "" {
			out[normICCID(iccid)] = plan.Phone
		}
	}
	for iccid, note := range a.profileNotes {
		if note.Phone != "" {
			out[normICCID(iccid)] = note.Phone
		}
	}
	return out
}

func (a *app) cardNumber(iccid string) string {
	iccid = normICCID(iccid)
	if iccid == "" {
		return ""
	}
	a.profileNotesMu.Lock()
	defer a.profileNotesMu.Unlock()
	if a.loadProfileNotesLocked() != nil {
		return keepAlive.get().Plans[iccid].Phone
	}
	return a.cardNumbersLocked()[iccid]
}

var cardNumberTries = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

// learnCardNumber reads the current card's number from the SIM at most every
// few minutes while none is known yet, and records it.
func (a *app) learnCardNumber(iccid string) string {
	iccid = normICCID(iccid)
	if iccid == "" {
		return ""
	}
	if known := a.cardNumber(iccid); known != "" {
		return known
	}
	cardNumberTries.Lock()
	recent := time.Since(cardNumberTries.at[iccid]) < 3*time.Minute
	if !recent {
		cardNumberTries.at[iccid] = time.Now()
	}
	cardNumberTries.Unlock()
	if recent {
		return ""
	}
	number := a.ownNumber()
	if number != "" {
		_ = updatePlan(iccid, func(p *keepAlivePlan) { p.Phone = number })
	}
	return number
}
