//go:build darwin && cgo

package main

/*
#cgo pkg-config: libusb-1.0
#include <stdlib.h>
#include <libusb.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// adbClient speaks the ADB wire protocol to the module's USB ADB interface
// (interface 6, bInterfaceSubClass 66 = adb). It is a direct port of MaVo's
// ADBModuleController + ADBProtocol; only shell/push are needed for the
// module-side voice route.
type adbClient struct {
	server           *adbServerTarget
	ctx              *C.libusb_context
	handle           *C.libusb_device_handle
	iface            int
	endpointIn       byte
	endpointOut      byte
	mu               sync.Mutex
	remoteMaxPayload int
	nextLocalID      uint32
	connected        bool
}

func openDJIUSBADB() (*adbClient, error) {
	var ctx *C.libusb_context
	if rc := C.libusb_init(&ctx); rc != 0 {
		return nil, fmt.Errorf("libusb init: %s", usbErrorName(rc))
	}
	handle, identity := openSupportedUSBModuleDevice(ctx)
	if handle == nil {
		C.libusb_exit(ctx)
		return nil, errors.New("DJI/Quectel USB ADB device (2ca3:4006 or 2c7c:0125) not found")
	}
	dev := C.libusb_get_device(handle)
	if dev == nil {
		C.libusb_close(handle)
		C.libusb_exit(ctx)
		return nil, errors.New("libusb device handle has no device")
	}
	var config *C.struct_libusb_config_descriptor
	if rc := C.libusb_get_active_config_descriptor(dev, &config); rc != 0 {
		C.libusb_close(handle)
		C.libusb_exit(ctx)
		return nil, fmt.Errorf("get active USB config descriptor: %s", usbErrorName(rc))
	}
	defer C.libusb_free_config_descriptor(config)

	var target *usbATCandidate
	interfaces := unsafe.Slice(config._interface, int(config.bNumInterfaces))
	for _, intf := range interfaces {
		altsettings := unsafe.Slice(intf.altsetting, int(intf.num_altsetting))
		for _, alt := range altsettings {
			if byte(alt.bInterfaceClass) != 255 || byte(alt.bInterfaceSubClass) != 66 || byte(alt.bInterfaceProtocol) != 1 {
				continue
			}
			var endpointIn, endpointOut byte
			endpoints := unsafe.Slice(alt.endpoint, int(alt.bNumEndpoints))
			for _, ep := range endpoints {
				attrs := byte(ep.bmAttributes) & byte(C.LIBUSB_TRANSFER_TYPE_MASK)
				if attrs != byte(C.LIBUSB_TRANSFER_TYPE_BULK) {
					continue
				}
				addr := byte(ep.bEndpointAddress)
				if addr&byte(C.LIBUSB_ENDPOINT_IN) != 0 {
					endpointIn = addr
				} else {
					endpointOut = addr
				}
			}
			if endpointIn != 0 && endpointOut != 0 {
				target = &usbATCandidate{
					iface:       int(alt.bInterfaceNumber),
					endpointIn:  endpointIn,
					endpointOut: endpointOut,
				}
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		C.libusb_close(handle)
		C.libusb_exit(ctx)
		return nil, fmt.Errorf("USB ADB interface (6/adb) not found on %s", identity.String())
	}
	if rc := C.libusb_claim_interface(handle, C.int(target.iface)); rc != 0 {
		session := uint64(C.libusb_get_session_data(dev))
		C.libusb_close(handle)
		C.libusb_exit(ctx)
		if rc == C.LIBUSB_ERROR_ACCESS || rc == C.LIBUSB_ERROR_BUSY {
			if server, err := openADBServerTarget(session); err == nil {
				return &adbClient{server: server}, nil
			} else {
				return nil, fmt.Errorf("USB ADB occupied; local ADB server fallback: %w", err)
			}
		}
		return nil, fmt.Errorf("claim USB ADB interface %d: %s", target.iface, usbErrorName(rc))
	}
	return &adbClient{
		ctx:              ctx,
		handle:           handle,
		iface:            target.iface,
		endpointIn:       target.endpointIn,
		endpointOut:      target.endpointOut,
		remoteMaxPayload: adbMaxPayload,
		nextLocalID:      1,
	}, nil
}

func (a *adbClient) Close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.server = nil
	if a.handle == nil {
		return
	}
	C.libusb_release_interface(a.handle, C.int(a.iface))
	C.libusb_close(a.handle)
	C.libusb_exit(a.ctx)
	a.handle = nil
	a.ctx = nil
}

func (a *adbClient) usbOpen() bool { return a.handle != nil }

func (a *adbClient) isOpen() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.handle != nil || a.server != nil
}

func (a *adbClient) bulkWrite(payload []byte, timeout time.Duration) error {
	if len(payload) == 0 {
		return nil
	}
	var transferred C.int
	rc := C.libusb_bulk_transfer(
		a.handle,
		C.uchar(a.endpointOut),
		(*C.uchar)(unsafe.Pointer(&payload[0])),
		C.int(len(payload)),
		&transferred,
		C.uint(timeout.Milliseconds()),
	)
	if rc != 0 {
		return fmt.Errorf("USB ADB bulk write: %s", usbErrorName(rc))
	}
	if int(transferred) != len(payload) {
		return fmt.Errorf("USB ADB bulk write short transfer: %d/%d", int(transferred), len(payload))
	}
	return nil
}

func (a *adbClient) bulkRead(buf []byte, timeout time.Duration) (int, error) {
	var transferred C.int
	rc := C.libusb_bulk_transfer(
		a.handle,
		C.uchar(a.endpointIn),
		(*C.uchar)(unsafe.Pointer(&buf[0])),
		C.int(len(buf)),
		&transferred,
		C.uint(timeout.Milliseconds()),
	)
	if rc != 0 {
		if rc == C.LIBUSB_ERROR_TIMEOUT {
			return 0, errADBTimeout
		}
		return 0, fmt.Errorf("USB ADB bulk read: %s", usbErrorName(rc))
	}
	return int(transferred), nil
}

// ---- ADB wire protocol ----
