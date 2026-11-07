//go:build windows

package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// ipUnicastIf pins a socket's unicast traffic to one interface (ws2ipdef.h);
// the IPv4 value is the index in network byte order, the IPv6 one is not.
const ipUnicastIf = 31

type windowsAdapter struct {
	Name  string // friendly name, the same name net.InterfaceByName uses
	GUID  string
	Index uint32
	IPv4  net.IP
}

func discoverWindowsAdapters() []windowsAdapter {
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
	var adapters []windowsAdapter
	for aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); aa != nil; aa = aa.Next {
		if aa.OperStatus != windows.IfOperStatusUp {
			continue
		}
		item := windowsAdapter{
			Name:  windows.UTF16PtrToString(aa.FriendlyName),
			GUID:  windows.BytePtrToString(aa.AdapterName),
			Index: aa.IfIndex,
		}
		for ua := aa.FirstUnicastAddress; ua != nil; ua = ua.Next {
			if ip := ua.Address.IP(); ip.To4() != nil && !ip.IsLinkLocalUnicast() {
				item.IPv4 = ip.To4()
				break
			}
		}
		adapters = append(adapters, item)
	}
	return adapters
}

// isModuleAdapter reports whether the adapter's PnP device is the DJI module,
// read from the network class key Windows keeps for every adapter.
func isModuleAdapter(guid string) bool {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Network\{4D36E972-E325-11CE-BFC1-08002BE10318}\`+guid+`\Connection`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	pnp, _, err := key.GetStringValue("PnPInstanceId")
	if err != nil {
		return false
	}
	pnp = strings.ToUpper(pnp)
	return strings.Contains(pnp, "VID_2CA3&PID_4006") || strings.Contains(pnp, "VID_2C7C&PID_0125")
}

func cellularAdapter() (windowsAdapter, error) {
	var found []windowsAdapter
	for _, item := range discoverWindowsAdapters() {
		if item.IPv4 != nil && isModuleAdapter(item.GUID) {
			found = append(found, item)
		}
	}
	switch len(found) {
	case 0:
		return windowsAdapter{}, errors.New("未找到模块的蜂窝网卡，请检查模块是否已连接并完成上网初始化")
	case 1:
	default:
		return windowsAdapter{}, errors.New("检测到多个模块，Windows 版目前只支持一个模块")
	}
	var best uint32
	if err := windows.GetBestInterfaceEx(&windows.SockaddrInet4{Addr: [4]byte{8, 8, 8, 8}}, &best); err != nil || best == found[0].Index {
		return windowsAdapter{}, errors.New("需要宽带或 Wi-Fi 作为电脑的默认网络，蜂窝只给代理使用")
	}
	return found[0], nil
}

func proxyInterface() (string, error) {
	adapter, err := cellularAdapter()
	if err != nil {
		return "", err
	}
	return adapter.Name, nil
}

func boundProxyDial(ctx context.Context, iface, address string) (net.Conn, error) {
	network := "tcp4"
	if host, _, err := net.SplitHostPort(address); err == nil && net.ParseIP(host).To4() == nil {
		network = "tcp6"
	}
	return boundInterfaceDial(ctx, iface, network, address)
}

// localCellularIPv6 is the source address Windows picks for IPv6 over iface,
// which is the IPv6 exit. Connecting a UDP socket sends nothing.
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
	adapter, err := cellularAdapter()
	if err != nil || adapter.Name != iface {
		return nil, errors.New("蜂窝网卡已变化或宽带默认网络不可用")
	}
	noteCellularActivity()
	d := net.Dialer{Control: func(network, _ string, raw syscall.RawConn) error {
		var bindErr error
		err := raw.Control(func(fd uintptr) {
			if strings.HasSuffix(network, "6") {
				bindErr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IPV6, ipUnicastIf, int(adapter.Index))
				return
			}
			index := adapter.Index
			beIndex := index>>24 | index>>8&0xff00 | index<<8&0xff0000 | index<<24
			bindErr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, ipUnicastIf, int(int32(beIndex)))
		})
		if err != nil {
			return err
		}
		return bindErr
	}}
	// Windows sends from the interface that owns the source address, so a
	// bound source keeps IPv4 on the module even where IP_UNICAST_IF is ignored.
	if !strings.HasSuffix(network, "6") {
		if strings.HasPrefix(network, "udp") {
			d.LocalAddr = &net.UDPAddr{IP: adapter.IPv4}
		} else {
			d.LocalAddr = &net.TCPAddr{IP: adapter.IPv4}
		}
	}
	return d.DialContext(ctx, network, address)
}
