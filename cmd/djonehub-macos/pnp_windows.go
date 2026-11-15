//go:build windows

package main

import (
	"errors"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Windows has no libusb session IDs or IOKit location IDs. A module is its
// USB composite device node (USB\VID_xxxx&PID_yyyy\<instance>); its session
// changes on every arrival, like a macOS USB session does, and its location
// is a hash of the port path.

var (
	cfgmgr32           = windows.NewLazySystemDLL("cfgmgr32.dll")
	cmLocateDevNode    = cfgmgr32.NewProc("CM_Locate_DevNodeW")
	cmGetParent        = cfgmgr32.NewProc("CM_Get_Parent")
	cmGetDeviceID      = cfgmgr32.NewProc("CM_Get_Device_IDW")
	cmGetNodeProperty  = cfgmgr32.NewProc("CM_Get_DevNode_PropertyW")
	devpkeyLastArrival = devPropKey{windows.GUID{Data1: 0x83da6326, Data2: 0x97a6, Data3: 0x4088, Data4: [8]byte{0x94, 0x53, 0xa1, 0x92, 0x3f, 0x57, 0x3b, 0x29}}, 102}
	devpkeyLocation    = devPropKey{windows.GUID{Data1: 0xa45c254e, Data2: 0xdf1c, Data3: 0x4efd, Data4: [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0}}, 37}
	devpkeyBusDesc     = devPropKey{windows.GUID{Data1: 0x540b947e, Data2: 0x8b40, Data3: 0x45bc, Data4: [8]byte{0xa8, 0xa2, 0x6a, 0x0b, 0x89, 0x4c, 0xbd, 0xa2}}, 4}
)

type devPropKey struct {
	fmtid windows.GUID
	pid   uint32
}

// winModule is one connected module and its interface device nodes.
type winModule struct {
	Instance   string
	VendorID   int
	ProductID  int
	Session    uint64
	Location   uint32
	Serial     string
	Product    string
	Interfaces map[int]string // interface number -> device instance ID
}

func devNode(instance string) (uint32, error) {
	id, err := windows.UTF16PtrFromString(instance)
	if err != nil {
		return 0, err
	}
	var node uint32
	if r, _, _ := cmLocateDevNode.Call(uintptr(unsafe.Pointer(&node)), uintptr(unsafe.Pointer(id)), 0); r != 0 {
		return 0, fmt.Errorf("device %s not present (CR %d)", instance, r)
	}
	return node, nil
}

func devNodeID(node uint32) string {
	buf := make([]uint16, 512)
	if r, _, _ := cmGetDeviceID.Call(uintptr(node), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0); r != 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}

// parentInstance is the device instance ID of instance's parent node.
func parentInstance(instance string) string {
	node, err := devNode(instance)
	if err != nil {
		return ""
	}
	var parent uint32
	if r, _, _ := cmGetParent.Call(uintptr(unsafe.Pointer(&parent)), uintptr(node), 0); r != 0 {
		return ""
	}
	return devNodeID(parent)
}

func nodeProperty(node uint32, key devPropKey) []byte {
	var typ uint32
	size := uint32(0)
	cmGetNodeProperty.Call(uintptr(node), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&typ)), 0, uintptr(unsafe.Pointer(&size)), 0)
	if size == 0 {
		return nil
	}
	buf := make([]byte, size)
	if r, _, _ := cmGetNodeProperty.Call(uintptr(node), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0); r != 0 {
		return nil
	}
	return buf[:size]
}

func utf16Bytes(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	return windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&b[0])), len(b)/2))
}

// parseModuleInstance reads VID, PID and (for an interface node) the
// interface number from a USB device instance ID.
func parseModuleInstance(instance string) (vid, pid, iface int, ok bool) {
	parts := strings.Split(strings.ToUpper(instance), `\`)
	if len(parts) != 3 || parts[0] != "USB" {
		return 0, 0, -1, false
	}
	iface = -1
	for _, field := range strings.Split(parts[1], "&") {
		switch {
		case strings.HasPrefix(field, "VID_"):
			v, err := strconv.ParseUint(field[4:], 16, 16)
			if err != nil {
				return 0, 0, -1, false
			}
			vid = int(v)
		case strings.HasPrefix(field, "PID_"):
			v, err := strconv.ParseUint(field[4:], 16, 16)
			if err != nil {
				return 0, 0, -1, false
			}
			pid = int(v)
		case strings.HasPrefix(field, "MI_"):
			v, err := strconv.ParseUint(field[3:], 16, 8)
			if err != nil {
				return 0, 0, -1, false
			}
			iface = int(v)
		}
	}
	return vid, pid, iface, isSupportedUSBModuleIdentity(vid, pid)
}

// discoverWinModules lists the connected modules with their interfaces.
func discoverWinModules() ([]winModule, error) {
	set, err := windows.SetupDiGetClassDevsEx(nil, "USB", 0, windows.DIGCF_PRESENT|windows.DIGCF_ALLCLASSES, 0, "")
	if err != nil {
		return nil, err
	}
	defer set.Close()
	modules := map[string]*winModule{}
	var interfaces []string
	for i := 0; ; i++ {
		dev, err := set.EnumDeviceInfo(i)
		if err != nil {
			break
		}
		instance, err := set.DeviceInstanceID(dev)
		if err != nil {
			continue
		}
		vid, pid, iface, ok := parseModuleInstance(instance)
		if !ok {
			continue
		}
		if iface >= 0 {
			interfaces = append(interfaces, instance)
			continue
		}
		m := &winModule{Instance: instance, VendorID: vid, ProductID: pid, Interfaces: map[int]string{}}
		node, err := devNode(instance)
		if err != nil {
			continue
		}
		h := fnv.New64a()
		h.Write([]byte(strings.ToUpper(instance)))
		h.Write(nodeProperty(node, devpkeyLastArrival))
		// Keep it below 2^53 so it survives JSON number handling.
		m.Session = h.Sum64()>>12 | 1
		location := utf16Bytes(nodeProperty(node, devpkeyLocation))
		if location == "" {
			location = instance
		}
		m.Location = crc32.ChecksumIEEE([]byte(strings.ToUpper(location))) | 1
		m.Product = utf16Bytes(nodeProperty(node, devpkeyBusDesc))
		// A device with a USB serial number uses it as its instance suffix;
		// without one Windows makes up a suffix containing '&'.
		if suffix := instance[strings.LastIndex(instance, `\`)+1:]; !strings.Contains(suffix, "&") {
			m.Serial = suffix
		}
		modules[strings.ToUpper(instance)] = m
	}
	for _, instance := range interfaces {
		parent := strings.ToUpper(parentInstance(instance))
		m := modules[parent]
		if m == nil {
			continue
		}
		_, _, iface, _ := parseModuleInstance(instance)
		m.Interfaces[iface] = instance
	}
	out := make([]winModule, 0, len(modules))
	for _, m := range modules {
		out = append(out, *m)
	}
	return out, nil
}

func discoverFleetDevices() ([]fleetDevice, error) {
	modules, err := discoverWinModules()
	if err != nil {
		return nil, err
	}
	adapters := discoverWindowsAdapters()
	devices := make([]fleetDevice, 0, len(modules))
	for _, m := range modules {
		d := fleetDevice{Session: strconv.FormatUint(m.Session, 10), Location: m.Location, Serial: m.Serial, Product: m.Product, VendorID: m.VendorID, ProductID: m.ProductID}
		for _, a := range adapters {
			if adapterSession(a.GUID) == m.Session {
				d.Interfaces = append(d.Interfaces, a.Name)
			}
		}
		devices = append(devices, d)
	}
	return devices, nil
}

// adapterSession is the session of the module whose interface carries the
// network adapter with this GUID, or 0.
func adapterSession(guid string) uint64 {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Network\{4D36E972-E325-11CE-BFC1-08002BE10318}\`+guid+`\Connection`, registry.QUERY_VALUE)
	if err != nil {
		return 0
	}
	defer key.Close()
	pnp, _, err := key.GetStringValue("PnPInstanceId")
	if err != nil {
		return 0
	}
	if _, _, _, ok := parseModuleInstance(pnp); !ok {
		return 0
	}
	parent := parentInstance(pnp)
	modules, err := discoverWinModules()
	if err != nil {
		return 0
	}
	for _, m := range modules {
		if strings.EqualFold(m.Instance, parent) {
			return m.Session
		}
	}
	return 0
}

func fleetSessionForInterface(name string) uint64 {
	for _, a := range discoverWindowsAdapters() {
		if a.Name == name {
			return adapterSession(a.GUID)
		}
	}
	return 0
}

// findWinModule is the module with this session, or the first one when
// session is 0.
func findWinModule(session uint64) (winModule, error) {
	modules, err := discoverWinModules()
	if err != nil {
		return winModule{}, err
	}
	for _, m := range modules {
		if session == 0 || m.Session == session {
			return m, nil
		}
	}
	return winModule{}, errors.New("DJI/Quectel USB AT device (2ca3:4006 or 2c7c:0125) not found")
}

// moduleUSBInstance is the composite device instance of this worker's module;
// the tray app matches it to the module's Windows audio devices.
func moduleUSBInstance() string {
	m, err := findWinModule(scopedUSBSession)
	if err != nil {
		return ""
	}
	return m.Instance
}
