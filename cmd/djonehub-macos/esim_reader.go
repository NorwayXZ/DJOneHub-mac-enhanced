package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/damonto/euicc-go/driver"
	"github.com/iniwex5/vohive/internal/esim"
)

// eSIM requests with ?source=reader act on the card in a USB smart-card
// reader instead of the module's: write numbers to a new card before it goes
// into a module, or manage a card that is not in one. The reader card is not
// online, so switching it needs no module restart.

func isReaderRequest(r *http.Request) bool { return r.URL.Query().Get("source") == "reader" }

// readerWithCard returns the first reader that holds a card.
func readerWithCard() (string, error) {
	readers, err := listCardReaders()
	if err != nil {
		return "", err
	}
	if len(readers) == 0 {
		return "", fmt.Errorf("没有接读卡器")
	}
	for _, r := range readers {
		if r.CardPresent {
			return r.Name, nil
		}
	}
	return "", fmt.Errorf("读卡器里没有卡")
}

func newReaderESIMManager() (*esim.Manager, error) {
	reader, err := readerWithCard()
	if err != nil {
		return nil, err
	}
	return esim.NewManager(esim.ManagerOptions{
		DeviceID:  "mac-reader",
		Transport: "custom",
		SmartCardChannelFactory: func() (driver.SmartCardChannel, error) {
			return newReaderESIMChannel(reader)
		},
	})
}

// esimManagerFor picks the card a request acts on; nil means unavailable.
func (a *app) esimManagerFor(w http.ResponseWriter, r *http.Request) (*esim.Manager, bool) {
	if !isReaderRequest(r) {
		m, _ := a.currentESIMManager()
		return m, true
	}
	m, err := newReaderESIMManager()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return nil, false
	}
	return m, true
}

// lockModuleUnlessReader takes the module operation lock for module-card
// requests; reader requests do not touch the module.
func (a *app) lockModuleUnlessReader(w http.ResponseWriter, r *http.Request) (func(), bool) {
	if isReaderRequest(r) {
		return func() {}, true
	}
	if !a.waitModuleOperation(time.Minute) {
		writeError(w, 409, errModuleBusy)
		return nil, false
	}
	return a.moduleOperationMu.Unlock, true
}

func (a *app) cardReadersAPI(w http.ResponseWriter, _ *http.Request) {
	readers, err := listCardReaders()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"readers": readers})
}
