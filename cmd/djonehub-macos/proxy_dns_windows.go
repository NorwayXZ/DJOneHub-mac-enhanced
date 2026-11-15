//go:build windows

package main

import (
	"context"
	"errors"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// adapterDNSServers lists the DNS servers Windows learned for the adapter
// (DHCP on the module's RNDIS network, DHCPv6/RA for IPv6).
func adapterDNSServers(iface string) []net.IP {
	size := uint32(16 << 10)
	var buf []byte
	for range 3 {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST,
			0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return nil
		}
		buf = nil
	}
	if buf == nil {
		return nil
	}
	for aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); aa != nil; aa = aa.Next {
		if windows.UTF16PtrToString(aa.FriendlyName) != iface {
			continue
		}
		var ips []net.IP
		for dns := aa.FirstDnsServerAddress; dns != nil; dns = dns.Next {
			if ip := dns.Address.IP(); ip != nil {
				ips = append(ips, ip)
			}
		}
		return ips
	}
	return nil
}

func cellularDNSServer(_ context.Context, iface string) (string, error) {
	for _, ip := range adapterDNSServers(iface) {
		if ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return net.JoinHostPort(ip.String(), "53"), nil
		}
	}
	return "", errors.New("模块尚未提供可用的 IPv4 DNS，请检查 USB DHCP")
}

func cellularDNSv6Servers(_ context.Context, iface string) []string {
	servers := []string{}
	for _, ip := range adapterDNSServers(iface) {
		if ip.To4() == nil && ip.IsGlobalUnicast() {
			servers = append(servers, net.JoinHostPort(ip.String(), "53"))
		}
	}
	return servers
}
