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
		return "", errors.New("no hardware-verified DJI USB IPv4 interface")
	}
	route := discoverMacDefaultRoute()
	if route.Interface == "" || route.Interface == found {
		return "", errors.New("proxy-only mode requires broadband/Wi-Fi as the default route")
	}
	return found, nil
}

func boundProxyDial(ctx context.Context, iface, address string) (net.Conn, error) {
	return boundInterfaceDial(ctx, iface, "tcp4", address)
}
func boundInterfaceDial(ctx context.Context, iface, network, address string) (net.Conn, error) {
	current, err := proxyInterface()
	if err != nil || current != iface {
		return nil, errors.New("cellular interface changed or broadband default route unavailable")
	}
	i, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Control: func(_, _ string, raw syscall.RawConn) error {
		var bindErr error
		err := raw.Control(func(fd uintptr) {
			bindErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_BOUND_IF, i.Index)
		})
		if err != nil {
			return err
		}
		return bindErr
	}}
	return d.DialContext(ctx, network, address)
}
