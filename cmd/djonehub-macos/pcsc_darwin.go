//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework PCSC
#include <stdlib.h>
#include <PCSC/winscard.h>
#include <PCSC/wintypes.h>

static SCARD_IO_REQUEST pciFor(DWORD proto) {
	SCARD_IO_REQUEST pci = { proto, sizeof(SCARD_IO_REQUEST) };
	return pci;
}
*/
import "C"

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/damonto/euicc-go/driver"
)

// USB smart-card readers (CCID, e.g. SCM SCR3512) through macOS PC/SC.

type cardReader struct {
	Name        string `json:"name"`
	CardPresent bool   `json:"card_present"`
}

func pcscError(what string, rc C.LONG) error {
	switch uint32(rc) {
	case 0x8010000C: // SCARD_E_NO_SMARTCARD
		return fmt.Errorf("读卡器里没有卡")
	case 0x80100069: // SCARD_W_REMOVED_CARD
		return fmt.Errorf("卡被拔出了")
	case 0x8010002E: // SCARD_E_NO_READERS_AVAILABLE
		return fmt.Errorf("没有接读卡器")
	case 0x8010000B: // SCARD_E_SHARING_VIOLATION
		return fmt.Errorf("读卡器正被其他软件占用（如 NekokoLPA），请先关闭它")
	}
	return fmt.Errorf("%s 失败 (0x%08X)", what, uint32(rc))
}

func listCardReaders() ([]cardReader, error) {
	var ctx C.SCARDCONTEXT
	if rc := C.SCardEstablishContext(C.SCARD_SCOPE_SYSTEM, nil, nil, &ctx); rc != 0 {
		return nil, pcscError("连接智能卡服务", rc)
	}
	defer C.SCardReleaseContext(ctx)
	var size C.DWORD
	if rc := C.SCardListReaders(ctx, nil, nil, &size); rc != 0 {
		if uint32(rc) == 0x8010002E {
			return []cardReader{}, nil
		}
		return nil, pcscError("列出读卡器", rc)
	}
	buf := make([]byte, size)
	if rc := C.SCardListReaders(ctx, nil, (*C.char)(unsafe.Pointer(&buf[0])), &size); rc != 0 {
		return nil, pcscError("列出读卡器", rc)
	}
	readers := []cardReader{}
	for _, name := range strings.Split(string(buf[:size]), "\x00") {
		if name == "" {
			continue
		}
		cname := C.CString(name)
		state := C.SCARD_READERSTATE{szReader: cname, dwCurrentState: C.SCARD_STATE_UNAWARE}
		C.SCardGetStatusChange(ctx, 0, &state, 1)
		C.free(unsafe.Pointer(cname))
		readers = append(readers, cardReader{Name: name, CardPresent: state.dwEventState&C.SCARD_STATE_PRESENT != 0})
	}
	return readers, nil
}

// pcscESIMChannel talks to the card in a reader with raw APDUs.
type pcscESIMChannel struct {
	reader  string
	mu      sync.Mutex
	ctx     C.SCARDCONTEXT
	card    C.SCARDHANDLE
	proto   C.DWORD
	open    bool
	channel byte
}

func newPCSCESIMChannel(reader string) *pcscESIMChannel { return &pcscESIMChannel{reader: reader} }

func (c *pcscESIMChannel) CurrentChannel() byte { return c.channel }

func (c *pcscESIMChannel) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectLocked()
}

func (c *pcscESIMChannel) connectLocked() error {
	if c.open {
		return nil
	}
	if rc := C.SCardEstablishContext(C.SCARD_SCOPE_SYSTEM, nil, nil, &c.ctx); rc != 0 {
		return pcscError("连接智能卡服务", rc)
	}
	name := C.CString(c.reader)
	defer C.free(unsafe.Pointer(name))
	if rc := C.SCardConnect(c.ctx, name, C.SCARD_SHARE_SHARED, C.SCARD_PROTOCOL_T0|C.SCARD_PROTOCOL_T1, &c.card, &c.proto); rc != 0 {
		C.SCardReleaseContext(c.ctx)
		return pcscError("连接读卡器里的卡", rc)
	}
	c.open = true
	return nil
}

func (c *pcscESIMChannel) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.open {
		return nil
	}
	C.SCardDisconnect(c.card, C.SCARD_LEAVE_CARD)
	C.SCardReleaseContext(c.ctx)
	c.open, c.channel = false, 0
	return nil
}

// send is the apduSender for this reader; the caller holds c.mu.
func (c *pcscESIMChannel) send(apdu []byte) ([]byte, uint16, error) {
	if err := c.connectLocked(); err != nil {
		return nil, 0, err
	}
	pci := C.pciFor(c.proto)
	out := make([]byte, 260)
	size := C.DWORD(len(out))
	rc := C.SCardTransmit(c.card, &pci, (*C.uchar)(unsafe.Pointer(&apdu[0])), C.DWORD(len(apdu)), nil, (*C.uchar)(unsafe.Pointer(&out[0])), &size)
	if rc != 0 {
		return nil, 0, pcscError("向卡发送指令", rc)
	}
	if size < 2 {
		return nil, 0, fmt.Errorf("卡的响应无效")
	}
	out = out[:size]
	return out[:size-2], uint16(out[size-2])<<8 | uint16(out[size-1]), nil
}

func (c *pcscESIMChannel) OpenLogicalChannel(aid []byte) (byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	channel, err := openRawChannel(c.send, aid)
	if err != nil {
		return 0, fmt.Errorf("打开 eUICC logical channel 失败: %w", err)
	}
	c.channel = channel
	return channel, nil
}

func (c *pcscESIMChannel) Transmit(command []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.channel == 0 {
		return nil, fmt.Errorf("eUICC logical channel 尚未打开")
	}
	return transmitOnChannel(c.send, c.channel, command)
}

func (c *pcscESIMChannel) CloseLogicalChannel(channel byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := closeRawChannel(c.send, channel)
	if c.channel == channel {
		c.channel = 0
	}
	return err
}

func newReaderESIMChannel(reader string) (driver.SmartCardChannel, error) {
	return newPCSCESIMChannel(reader), nil
}
