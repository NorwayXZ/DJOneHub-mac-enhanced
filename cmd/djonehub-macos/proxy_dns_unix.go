//go:build !windows

package main

import (
	"context"
	"errors"
	"os/exec"
)

func cellularDNSServer(ctx context.Context, iface string) (string, error) {
	out, err := exec.CommandContext(ctx, "/usr/sbin/ipconfig", "getoption", iface, "domain_name_server").Output()
	if err != nil {
		return "", errors.New("未能读取模块 DHCP DNS；不会回退到宽带 DNS")
	}
	return parseCellularDNSServer(string(out))
}

func cellularDNSv6Servers(ctx context.Context, iface string) []string {
	out, err := exec.CommandContext(ctx, "/usr/sbin/ipconfig", "getv6packet", iface).Output()
	if err != nil {
		return nil
	}
	return parseCellularDNSv6Servers(string(out))
}
