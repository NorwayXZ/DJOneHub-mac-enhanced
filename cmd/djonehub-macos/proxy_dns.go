package main

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"strings"
)

func parseCellularDNSServer(output string) (string, error) {
	for _, field := range strings.Fields(output) {
		ip := net.ParseIP(strings.Trim(field, "{},"))
		if ip != nil && ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return net.JoinHostPort(ip.String(), "53"), nil
		}
	}
	return "", errors.New("模块尚未提供可用的 IPv4 DNS，请检查 USB DHCP")
}
func cellularDNSServer(ctx context.Context, iface string) (string, error) {
	out, err := exec.CommandContext(ctx, "/usr/sbin/ipconfig", "getoption", iface, "domain_name_server").Output()
	if err != nil {
		return "", errors.New("未能读取模块 DHCP DNS；不会回退到宽带 DNS")
	}
	return parseCellularDNSServer(string(out))
}
