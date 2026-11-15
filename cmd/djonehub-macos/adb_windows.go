//go:build windows

package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// adbClient speaks the ADB wire protocol (adb_protocol.go) over the module's
// ADB interface (class ff/42/01), opened with WinUSB like the AT interface.
type adbClient struct {
	server           *adbServerTarget
	usb              *usbAT
	mu               sync.Mutex
	remoteMaxPayload int
	nextLocalID      uint32
	connected        bool
}

func isADBInterface(instance string) bool {
	set, err := windows.SetupDiGetClassDevsEx(nil, instance, 0, windows.DIGCF_PRESENT|windows.DIGCF_ALLCLASSES|windows.DIGCF_DEVICEINTERFACE, 0, "")
	if err != nil {
		return false
	}
	defer set.Close()
	dev, err := set.EnumDeviceInfo(0)
	if err != nil {
		return false
	}
	value, err := set.DeviceRegistryProperty(dev, windows.SPDRP_COMPATIBLEIDS)
	if err != nil {
		return false
	}
	ids, _ := value.([]string)
	for _, id := range ids {
		if strings.EqualFold(id, `USB\Class_ff&SubClass_42&Prot_01`) {
			return true
		}
	}
	return false
}

// winusbPathAnyGUID finds the WinUSB path of an interface node, whether
// DJOneHub bound it or Windows bound it automatically with its own GUID.
func winusbPathAnyGUID(instance string) string {
	if p := atInterfacePath(instance); p != "" {
		return p
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Enum\`+instance+`\Device Parameters`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	var guids []string
	if v, _, err := key.GetStringsValue("DeviceInterfaceGUIDs"); err == nil {
		guids = append(guids, v...)
	}
	if v, _, err := key.GetStringValue("DeviceInterfaceGUID"); err == nil {
		guids = append(guids, v)
	}
	for _, g := range guids {
		guid, err := windows.GUIDFromString(g)
		if err != nil {
			continue
		}
		paths, err := windows.CM_Get_Device_Interface_List(instance, &guid, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
		if err != nil {
			continue
		}
		for _, p := range paths {
			if p != "" {
				return p
			}
		}
	}
	return ""
}

var errNoModuleADB = errors.New("这个模块的 USB 配置没有开启 ADB 通道，通话音频需要它")

func openDJIUSBADB() (*adbClient, error) {
	if coordinatorOnly {
		return nil, errors.New("module control belongs to isolated workers")
	}
	m, err := findWinModule(scopedUSBSession)
	if err != nil {
		return nil, err
	}
	for number, instance := range m.Interfaces {
		if instance == "" || !isADBInterface(instance) {
			continue
		}
		path := winusbPathAnyGUID(instance)
		if path == "" {
			if err := bindWinUSB(instance); err != nil {
				return nil, err
			}
			if path = winusbPathAnyGUID(instance); path == "" {
				return nil, fmt.Errorf("ADB interface %d did not come up after binding WinUSB", number)
			}
		}
		dev, err := openWinUSB(path, m, number)
		if err != nil {
			// Another program (an adb server) holds it; reuse that server.
			if server, serr := openADBServerTarget(scopedUSBSession); serr == nil {
				return &adbClient{server: server}, nil
			}
			return nil, err
		}
		return &adbClient{usb: dev, remoteMaxPayload: adbMaxPayload, nextLocalID: 1}, nil
	}
	return nil, errNoModuleADB
}

func (a *adbClient) usbOpen() bool { return a.usb != nil }

func (a *adbClient) Close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.server = nil
	if a.usb != nil {
		a.usb.Close()
		a.usb = nil
	}
}

func (a *adbClient) isOpen() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.usb != nil || a.server != nil
}

func (a *adbClient) bulkWrite(payload []byte, timeout time.Duration) error {
	if len(payload) == 0 {
		return nil
	}
	a.usb.mu.Lock()
	defer a.usb.mu.Unlock()
	return a.usb.bulkWriteLocked(payload, timeout)
}

func (a *adbClient) bulkRead(buf []byte, timeout time.Duration) (int, error) {
	a.usb.mu.Lock()
	defer a.usb.mu.Unlock()
	data, err := a.usb.bulkReadSizedLocked(len(buf), timeout)
	if err == errUSBTimeout {
		return 0, errADBTimeout
	}
	if err != nil {
		return 0, err
	}
	return copy(buf, data), nil
}
