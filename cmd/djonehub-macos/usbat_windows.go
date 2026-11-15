//go:build windows

package main

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// On Windows the module's AT interface runs on Microsoft's own WinUSB
// driver: DJOneHub selects the signed in-box winusb.inf for that interface
// (as Device Manager's "pick from a list" does), so no third-party or
// self-signed driver is involved. Selecting it needs administrator rights,
// which the DJOneHub service has.

const (
	djiUSBVendorID      = 0x2ca3
	djiUSBProductID     = 0x4006
	quectelUSBVendorID  = 0x2c7c
	quectelUSBProductID = 0x0125
)

func isSupportedUSBModuleIdentity(vendorID, productID int) bool {
	return (vendorID == djiUSBVendorID && productID == djiUSBProductID) ||
		(vendorID == quectelUSBVendorID && productID == quectelUSBProductID)
}

var (
	// usbDeviceClass is the setup class of WinUSB devices.
	usbDeviceClass = windows.GUID{Data1: 0x88BAE032, Data2: 0x5A81, Data3: 0x49f0, Data4: [8]byte{0xBC, 0x3D, 0xA4, 0xFF, 0x13, 0x82, 0x16, 0xD6}}
	// moduleATInterfaceGUID is the device interface DJOneHub registers for a
	// module AT interface it bound to WinUSB.
	moduleATInterfaceGUID = windows.GUID{Data1: 0x6d4f2b1a, Data2: 0x8c3e, Data3: 0x4f7a, Data4: [8]byte{0x9b, 0x21, 0xd3, 0x0e, 0x5a, 0x77, 0x4c, 0x01}}

	newdev           = windows.NewLazySystemDLL("newdev.dll")
	diInstallDevice  = newdev.NewProc("DiInstallDevice")
	winusbDLL        = windows.NewLazySystemDLL("winusb.dll")
	winusbInitialize = winusbDLL.NewProc("WinUsb_Initialize")
	winusbFree       = winusbDLL.NewProc("WinUsb_Free")
	winusbQueryIface = winusbDLL.NewProc("WinUsb_QueryInterfaceSettings")
	winusbQueryPipe  = winusbDLL.NewProc("WinUsb_QueryPipe")
	winusbWritePipe  = winusbDLL.NewProc("WinUsb_WritePipe")
	winusbReadPipe   = winusbDLL.NewProc("WinUsb_ReadPipe")
	winusbSetPolicy  = winusbDLL.NewProc("WinUsb_SetPipePolicy")
)

// The AT interface is 2 in ECM mode and 4 in RNDIS mode; 3 and 5 are the
// other serial functions. Try the likely ones first.
var atInterfaceOrder = []int{4, 2, 3, 5}

type usbAT struct {
	file        windows.Handle
	handle      uintptr
	vendorID    int
	productID   int
	iface       int
	endpointIn  byte
	endpointOut byte
	mu          sync.Mutex
}

type winusbInterfaceDescriptor struct {
	Length, DescriptorType, InterfaceNumber, AlternateSetting, NumEndpoints, Class, SubClass, Protocol, Iface uint8
}

type winusbPipeInformation struct {
	PipeType uint32
	PipeID   uint8
	MaxPkt   uint16
	Interval uint8
}

// isVendorSerialInterface reports whether the interface node is a
// vendor-specific function (class ff/00/00), the kind the AT port is. ECM,
// RNDIS and audio interfaces are never bound.
func isVendorSerialInterface(instance string) bool {
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
		if strings.EqualFold(id, `USB\Class_ff&SubClass_00&Prot_00`) {
			return true
		}
	}
	return false
}

// atInterfacePath is the WinUSB device path of an interface DJOneHub bound.
func atInterfacePath(instance string) string {
	paths, err := windows.CM_Get_Device_Interface_List(instance, &moduleATInterfaceGUID, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil {
		return ""
	}
	for _, p := range paths {
		if p != "" {
			return p
		}
	}
	return ""
}

// bindWinUSB installs Microsoft's in-box WinUSB driver on one interface node
// and registers DJOneHub's interface GUID for it.
func bindWinUSB(instance string) error {
	set, err := windows.SetupDiGetClassDevsEx(nil, instance, 0, windows.DIGCF_PRESENT|windows.DIGCF_ALLCLASSES|windows.DIGCF_DEVICEINTERFACE, 0, "")
	if err != nil {
		return err
	}
	defer set.Close()
	dev, err := set.EnumDeviceInfo(0)
	if err != nil {
		return err
	}
	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Enum\`+instance+`\Device Parameters`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("需要管理员权限才能安装模块驱动: %w", err)
	}
	err = key.SetStringsValue("DeviceInterfaceGUIDs", []string{moduleATInterfaceGUID.String()})
	key.Close()
	if err != nil {
		return err
	}
	dev.ClassGUID = usbDeviceClass
	params, err := set.DeviceInstallParams(dev)
	if err != nil {
		return err
	}
	params.FlagsEx |= windows.DI_FLAGSEX_ALLOWEXCLUDEDDRVS
	params.Flags |= windows.DI_ENUMSINGLEINF
	if err := params.SetDriverPath(`C:\Windows\INF\winusb.inf`); err != nil {
		return err
	}
	if err := set.SetDeviceInstallParams(dev, params); err != nil {
		return err
	}
	if err := set.BuildDriverInfoList(dev, windows.SPDIT_CLASSDRIVER); err != nil {
		return err
	}
	var pick *windows.DrvInfoData
	for j := 0; ; j++ {
		d, err := set.EnumDriverInfo(dev, windows.SPDIT_CLASSDRIVER, j)
		if err != nil {
			break
		}
		// The generic model is listed first; "WinUSB" in every language.
		if strings.Contains(strings.ToLower(d.Description()), "winusb") {
			pick = d
			break
		}
	}
	if pick == nil {
		return errors.New("winusb.inf has no WinUSB entry")
	}
	if err := set.SetSelectedDriver(dev, pick); err != nil {
		return err
	}
	var reboot int32
	if r, _, e := diInstallDevice.Call(0, uintptr(set), uintptr(unsafe.Pointer(dev)), uintptr(unsafe.Pointer(pick)), 0, uintptr(unsafe.Pointer(&reboot))); r == 0 {
		return fmt.Errorf("install WinUSB on %s: %v", instance, e)
	}
	log.Printf("module AT interface bound to WinUSB: %s", instance)
	// The interface appears once the driver has started.
	for i := 0; i < 30 && atInterfacePath(instance) == ""; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func openDJIUSBAT() (*usbAT, error) {
	if coordinatorOnly {
		return nil, errors.New("module control belongs to isolated workers")
	}
	m, err := findWinModule(scopedUSBSession)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, number := range atInterfaceOrder {
		instance := m.Interfaces[number]
		if instance == "" {
			continue
		}
		path := atInterfacePath(instance)
		if path == "" {
			if !isVendorSerialInterface(instance) {
				continue
			}
			if err := bindWinUSB(instance); err != nil {
				lastErr = err
				continue
			}
			if path = atInterfacePath(instance); path == "" {
				lastErr = fmt.Errorf("interface %d did not come up after binding WinUSB", number)
				continue
			}
		}
		dev, err := openWinUSB(path, m, number)
		if err != nil {
			lastErr = err
			continue
		}
		if response, err := dev.Command("AT", 900*time.Millisecond); err == nil && atProbeSucceeded(response) {
			return dev, nil
		} else if err == nil {
			lastErr = fmt.Errorf("probe USB AT interface %d: unexpected response %q", number, response)
		} else {
			lastErr = fmt.Errorf("probe USB AT interface %d out 0x%02x in 0x%02x: %w", number, dev.endpointOut, dev.endpointIn, err)
		}
		dev.Close()
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("no USB bulk interface candidates found for DJI AT bridge")
}

func openWinUSB(path string, m winModule, number int) (*usbAT, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	file, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, fmt.Errorf("open USB AT interface %d: %w", number, err)
	}
	var handle uintptr
	if r, _, e := winusbInitialize.Call(uintptr(file), uintptr(unsafe.Pointer(&handle))); r == 0 {
		windows.CloseHandle(file)
		return nil, fmt.Errorf("WinUSB initialise interface %d: %v", number, e)
	}
	u := &usbAT{file: file, handle: handle, vendorID: m.VendorID, productID: m.ProductID, iface: number}
	var desc winusbInterfaceDescriptor
	winusbQueryIface.Call(handle, 0, uintptr(unsafe.Pointer(&desc)))
	for k := uint8(0); k < desc.NumEndpoints; k++ {
		var pipe winusbPipeInformation
		if r, _, _ := winusbQueryPipe.Call(handle, 0, uintptr(k), uintptr(unsafe.Pointer(&pipe))); r == 0 || pipe.PipeType != 2 {
			continue
		}
		if pipe.PipeID&0x80 != 0 {
			u.endpointIn = pipe.PipeID
		} else {
			u.endpointOut = pipe.PipeID
		}
	}
	if u.endpointIn == 0 || u.endpointOut == 0 {
		u.Close()
		return nil, fmt.Errorf("USB AT interface %d has no bulk endpoints", number)
	}
	return u, nil
}

// usbATCloseWait bounds how long Close waits for the driver after an unplug.
const usbATCloseWait = 2 * time.Second

func (u *usbAT) Close() {
	if u == nil {
		return
	}
	u.mu.Lock()
	handle, file := u.handle, u.file
	u.handle, u.file = 0, 0
	u.mu.Unlock()
	if handle == 0 {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		winusbFree.Call(handle)
		windows.CloseHandle(file)
	}()
	select {
	case <-done:
	case <-time.After(usbATCloseWait):
		log.Printf("USB AT cleanup still running after %s; continuing without it", usbATCloseWait)
	}
}

func (u *usbAT) Command(cmd string, timeout time.Duration) (string, error) {
	if u == nil {
		return "", errors.New("USB AT device is not open")
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", errors.New("AT command is empty")
	}
	if !strings.HasPrefix(strings.ToUpper(cmd), "AT") {
		return "", errors.New("command must start with AT")
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.handle == 0 {
		return "", errors.New("USB AT device is not open")
	}
	u.drainLocked()
	if err := u.bulkWriteLocked([]byte(cmd+"\r"), timeout); err != nil {
		return "", err
	}
	deadline := time.Now().Add(timeout)
	var chunks []string
	for time.Now().Before(deadline) {
		data, err := u.bulkReadLocked(min(time.Until(deadline), 900*time.Millisecond))
		if err != nil {
			if errors.Is(err, errUSBTimeout) {
				continue
			}
			return strings.Join(chunks, ""), err
		}
		if len(data) == 0 {
			continue
		}
		chunks = append(chunks, string(data))
		if joined := strings.Join(chunks, ""); atResponseComplete(joined) {
			return normalizeATResponse(joined), nil
		}
	}
	if len(chunks) == 0 {
		return "", errors.New("USB AT command timed out without response")
	}
	return normalizeATResponse(strings.Join(chunks, "")), nil
}

// CommandWithPrompt executes an AT command that enters an interactive input
// state, then submits followUp after the modem returns its ">" prompt.
func (u *usbAT) CommandWithPrompt(cmd string, followUp []byte, timeout time.Duration) (string, error) {
	if u == nil {
		return "", errors.New("USB AT device is not open")
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || !strings.HasPrefix(strings.ToUpper(cmd), "AT") {
		return "", errors.New("command must start with AT")
	}
	if len(followUp) == 0 {
		return "", errors.New("interactive AT follow-up is empty")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.handle == 0 {
		return "", errors.New("USB AT device is not open")
	}
	u.drainLocked()
	if err := u.bulkWriteLocked([]byte(cmd+"\r"), timeout); err != nil {
		return "", err
	}
	deadline := time.Now().Add(timeout)
	var response strings.Builder
	promptReceived := false
	for time.Now().Before(deadline) {
		data, err := u.bulkReadLocked(min(time.Until(deadline), 900*time.Millisecond))
		if err != nil {
			if errors.Is(err, errUSBTimeout) {
				continue
			}
			return normalizeATResponse(response.String()), err
		}
		if len(data) == 0 {
			continue
		}
		response.Write(data)
		joined := response.String()
		if !promptReceived {
			if atResponseIsError(joined) {
				return normalizeATResponse(joined), nil
			}
			if !atResponseHasPrompt(joined) {
				continue
			}
			if err := u.bulkWriteLocked(followUp, time.Until(deadline)); err != nil {
				return normalizeATResponse(joined), err
			}
			promptReceived = true
			continue
		}
		if atResponseComplete(joined) {
			return normalizeATResponse(joined), nil
		}
	}
	if promptReceived {
		_ = u.bulkWriteLocked([]byte{0x1b}, 300*time.Millisecond)
	}
	if response.Len() == 0 {
		return "", errors.New("USB interactive AT command timed out without response")
	}
	return normalizeATResponse(response.String()), errors.New("USB interactive AT command timed out before completion")
}

var errUSBTimeout = errors.New("usb timeout")

func (u *usbAT) drainLocked() {
	for {
		if _, err := u.bulkReadLocked(80 * time.Millisecond); err != nil {
			return
		}
	}
}

func (u *usbAT) Description() string {
	if u == nil {
		return "USB AT"
	}
	return fmt.Sprintf("USB AT · %04x:%04x interface %d out 0x%02x in 0x%02x", u.vendorID, u.productID, u.iface, u.endpointOut, u.endpointIn)
}

func (u *usbAT) setTimeoutLocked(endpoint byte, timeout time.Duration) {
	ms := uint32(max(timeout.Milliseconds(), 1))
	const pipeTransferTimeout = 0x03
	winusbSetPolicy.Call(u.handle, uintptr(endpoint), pipeTransferTimeout, 4, uintptr(unsafe.Pointer(&ms)))
}

// usbGone turns a driver error into the text main.go treats as an unplug.
func usbGone(op string, e error) error {
	switch e {
	case windows.ERROR_DEVICE_NOT_CONNECTED, windows.ERROR_BAD_COMMAND, windows.ERROR_GEN_FAILURE, windows.ERROR_FILE_NOT_FOUND, windows.ERROR_INVALID_HANDLE:
		return fmt.Errorf("USB bulk %s: LIBUSB_ERROR_NO_DEVICE (%v)", op, e)
	}
	return fmt.Errorf("USB bulk %s: %v", op, e)
}

func (u *usbAT) bulkWriteLocked(payload []byte, timeout time.Duration) error {
	u.setTimeoutLocked(u.endpointOut, timeout)
	var n uint32
	if r, _, e := winusbWritePipe.Call(u.handle, uintptr(u.endpointOut), uintptr(unsafe.Pointer(&payload[0])), uintptr(len(payload)), uintptr(unsafe.Pointer(&n)), 0); r == 0 {
		return usbGone("write", e)
	}
	if int(n) != len(payload) {
		return fmt.Errorf("USB bulk write short transfer: %d/%d", n, len(payload))
	}
	return nil
}

func (u *usbAT) bulkReadLocked(timeout time.Duration) ([]byte, error) {
	return u.bulkReadSizedLocked(512, timeout)
}

func (u *usbAT) bulkReadSizedLocked(size int, timeout time.Duration) ([]byte, error) {
	u.setTimeoutLocked(u.endpointIn, timeout)
	buf := make([]byte, max(size, 512))
	var n uint32
	if r, _, e := winusbReadPipe.Call(u.handle, uintptr(u.endpointIn), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n)), 0); r == 0 {
		if e == windows.ERROR_SEM_TIMEOUT {
			return nil, errUSBTimeout
		}
		return nil, usbGone("read", e)
	}
	return buf[:n], nil
}

func normalizeATResponse(resp string) string {
	resp = strings.ReplaceAll(resp, "\r\r\n", "\r\n")
	resp = strings.TrimSpace(resp)
	lines := strings.Split(resp, "\n")
	filtered := lines[:0]
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.Join(filtered, "\r\n")
}
