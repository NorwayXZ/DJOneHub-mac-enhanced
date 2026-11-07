//go:build !windows && (!darwin || !cgo)

package main

import "errors"

func interfaceCounters(string) (interfaceCounterReading, error) {
	return interfaceCounterReading{}, errors.New("interface counters require macOS with cgo")
}
