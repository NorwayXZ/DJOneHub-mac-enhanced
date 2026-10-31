//go:build !darwin || !cgo

package main

import "errors"

func discoverFleetDevices() ([]fleetDevice, error) {
	return nil, errors.New("multi-module USB inventory requires macOS with cgo")
}
func fleetSessionForInterface(string) uint64 { return 0 }

func lockFleetState(string) (func(), error) {
	return nil, errors.New("module supervision requires macOS")
}
