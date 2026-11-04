package main

import (
	"errors"
	"net/http"
	"time"
)

func (a *app) tryModuleOperation() bool {
	if !a.moduleOperationMu.TryLock() {
		return false
	}
	if a.updatePending.Load() {
		a.moduleOperationMu.Unlock()
		return false
	}
	return true
}
// endUpdateLease undoes beginUpdateLease when the restart it prepared for
// did not happen; the proxy watchdog restarts the proxy.
func (a *app) endUpdateLease() {
	a.updatePending.Store(false)
}

func (a *app) beginUpdateLease() error {
	if !a.tryModuleOperation() {
		return errors.New(errModuleBusy)
	}
	defer a.moduleOperationMu.Unlock()
	a.callMu.RLock()
	busy := a.activeCall != nil
	a.callMu.RUnlock()
	if busy {
		return errors.New(errCallActive)
	}
	a.updatePending.Store(true)
	a.proxy.stop()
	time.AfterFunc(90*time.Second, func() { a.updatePending.Store(false) })
	return nil
}
func (a *app) prepareUpdateAPI(w http.ResponseWriter, _ *http.Request) {
	if err := a.beginUpdateLease(); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ready": true})
}
