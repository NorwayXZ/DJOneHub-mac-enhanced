//go:build !(darwin && cgo)

package main

import (
	"fmt"

	"github.com/damonto/euicc-go/driver"
)

type cardReader struct {
	Name        string `json:"name"`
	CardPresent bool   `json:"card_present"`
}

func listCardReaders() ([]cardReader, error) { return []cardReader{}, nil }

func newReaderESIMChannel(string) (driver.SmartCardChannel, error) {
	return nil, fmt.Errorf("这个系统不支持读卡器")
}
