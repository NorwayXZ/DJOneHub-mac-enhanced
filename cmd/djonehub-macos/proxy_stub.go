//go:build !darwin || !cgo

package main

import (
	"context"
	"errors"
	"net"
)

func proxyInterface() (string, error) {
	return "", errors.New("cellular proxy requires macOS with cgo")
}
func boundProxyDial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("interface binding unavailable")
}

func boundInterfaceDial(context.Context, string, string, string) (net.Conn, error) {
	return nil, errors.New("interface binding unavailable")
}

func cellularHasIPv6(string) bool { return false }

func localCellularIPv6(string) string { return "" }
