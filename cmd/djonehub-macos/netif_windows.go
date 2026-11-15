//go:build windows

package main

import (
	"fmt"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// discoverMacNetworkInterfaces lists Windows adapters in the shape the macOS
// code uses: the friendly name (what net.InterfaceByName uses), "active"
// while the adapter is up, and its first IPv4 address, self-assigned
// 169.254 ones included.
func discoverMacNetworkInterfaces() []macNetInterface {
	size := uint32(16 << 10)
	var buf []byte
	for range 3 {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_INET, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER,
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
	var items []macNetInterface
	for aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); aa != nil; aa = aa.Next {
		if aa.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK || aa.IfType == windows.IF_TYPE_TUNNEL {
			continue
		}
		item := macNetInterface{Name: windows.UTF16PtrToString(aa.FriendlyName), Status: "inactive", Kind: "ethernet"}
		if aa.OperStatus == windows.IfOperStatusUp {
			item.Status = "active"
		}
		if aa.IfType == windows.IF_TYPE_IEEE80211 {
			item.Kind = "wifi"
		}
		if aa.PhysicalAddressLength > 0 {
			item.MAC = net.HardwareAddr(aa.PhysicalAddress[:aa.PhysicalAddressLength]).String()
		}
		for ua := aa.FirstUnicastAddress; ua != nil; ua = ua.Next {
			if ip := ua.Address.IP(); ip.To4() != nil {
				item.IPv4 = ip.String()
				break
			}
		}
		items = append(items, item)
	}
	return items
}

// discoverMacDefaultRoute is the adapter Windows sends internet traffic out of.
func discoverMacDefaultRoute() macDefaultRoute {
	var index uint32
	if windows.GetBestInterfaceEx(&windows.SockaddrInet4{Addr: [4]byte{8, 8, 8, 8}}, &index) != nil {
		return macDefaultRoute{}
	}
	if i, err := net.InterfaceByIndex(int(index)); err == nil {
		return macDefaultRoute{Interface: i.Name}
	}
	return macDefaultRoute{Interface: fmt.Sprint(index)}
}
