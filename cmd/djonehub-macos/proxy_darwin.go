//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <stdlib.h>
static int proxyDJIInterface(const char *name) {
 io_service_t entry = IOServiceGetMatchingService(kIOMainPortDefault, IOBSDNameMatching(kIOMainPortDefault, 0, name));
 if (!entry) return 0;
 CFTypeRef v = IORegistryEntrySearchCFProperty(entry, kIOServicePlane, CFSTR("idVendor"), kCFAllocatorDefault, kIORegistryIterateParents | kIORegistryIterateRecursively);
 CFTypeRef p = IORegistryEntrySearchCFProperty(entry, kIOServicePlane, CFSTR("idProduct"), kCFAllocatorDefault, kIORegistryIterateParents | kIORegistryIterateRecursively);
 int vendor = 0, product = 0;
 if (v && CFGetTypeID(v) == CFNumberGetTypeID()) CFNumberGetValue((CFNumberRef)v, kCFNumberIntType, &vendor);
 if (p && CFGetTypeID(p) == CFNumberGetTypeID()) CFNumberGetValue((CFNumberRef)p, kCFNumberIntType, &product);
 if (v) CFRelease(v); if (p) CFRelease(p); IOObjectRelease(entry);
 return (vendor == 0x2ca3 && product == 0x4006) || (vendor == 0x2c7c && product == 0x0125);
}
*/
import "C"

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"unsafe"
)

func proxyInterface() (string, error) {
	var found string
	for _, item := range discoverMacNetworkInterfaces() {
		if item.Status != "active" || net.ParseIP(item.IPv4).To4() == nil {
			continue
		}
		if scopedUSBSession != 0 && fleetSessionForInterface(item.Name) != scopedUSBSession {
			continue
		}
		name := C.CString(item.Name)
		valid := C.proxyDJIInterface(name) != 0
		C.free(unsafe.Pointer(name))
		if valid {
			if found != "" {
				if scopedUSBSession != 0 {
					return "", errors.New("所选模块存在多个可用 IPv4 网卡，无法确定出口")
				}
				return "", errors.New("检测到多个模块，请在设置中启用多模块模式")
			}
			found = item.Name
		}
	}
	if found == "" {
		return "", errors.New("未找到模块的蜂窝网卡，请检查模块是否已连接并完成上网初始化")
	}
	route := discoverMacDefaultRoute()
	if route.Interface == "" || route.Interface == found {
		return "", errors.New("需要宽带或 Wi-Fi 作为 Mac 的默认网络，蜂窝只给代理使用")
	}
	return found, nil
}

func boundProxyDial(ctx context.Context, iface, address string) (net.Conn, error) {
	network := "tcp4"
	if host, _, err := net.SplitHostPort(address); err == nil && net.ParseIP(host).To4() == nil {
		network = "tcp6"
	}
	return boundInterfaceDial(ctx, iface, network, address)
}

// localCellularIPv6 is the source address the kernel picks for IPv6 over
// iface, which is the IPv6 exit (carriers do not NAT IPv6). Connecting a UDP
// socket sends nothing.
func localCellularIPv6(iface string) string {
	c, err := boundInterfaceDial(context.Background(), iface, "udp6", "[2400:3200::1]:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	if addr, ok := c.LocalAddr().(*net.UDPAddr); ok && isPublicIPv6(addr.IP) {
		return addr.IP.String()
	}
	return ""
}

// cellularHasIPv6 reports whether the module handed this Mac a global IPv6
// address (SLAAC from the carrier prefix), i.e. the bearer carries IPv6.
func cellularHasIPv6(iface string) bool {
	i, err := net.InterfaceByName(iface)
	if err != nil {
		return false
	}
	addrs, err := i.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if n, ok := addr.(*net.IPNet); ok && isPublicIPv6(n.IP) {
			return true
		}
	}
	return false
}
func boundInterfaceDial(ctx context.Context, iface, network, address string) (net.Conn, error) {
	current, err := proxyInterface()
	if err != nil || current != iface {
		return nil, errors.New("蜂窝网卡已变化或宽带默认网络不可用")
	}
	i, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, err
	}
	noteCellularActivity()
	d := net.Dialer{Control: func(network, _ string, raw syscall.RawConn) error {
		var bindErr error
		err := raw.Control(func(fd uintptr) {
			if strings.HasSuffix(network, "6") {
				bindErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_BOUND_IF, i.Index)
				return
			}
			bindErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_BOUND_IF, i.Index)
		})
		if err != nil {
			return err
		}
		return bindErr
	}}
	return d.DialContext(ctx, network, address)
}
