package main

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

type usbATESIMChannel struct {
	command func(string, time.Duration) (string, error)
	channel byte
	// viaCSIM: this module rejects AT+CCHO/AT+CGLA (e.g. the DJI Baiwang
	// QDC507), so the channel is opened, used and closed with raw APDUs over
	// AT+CSIM instead.
	viaCSIM bool
	mu      sync.Mutex
}

func newUSBATESIMChannel(command func(string, time.Duration) (string, error)) *usbATESIMChannel {
	return &usbATESIMChannel{command: command}
}

func (c *usbATESIMChannel) CurrentChannel() byte {
	return c.channel
}

func (c *usbATESIMChannel) Connect() error {
	return nil
}

func (c *usbATESIMChannel) Disconnect() error {
	return nil
}

func (c *usbATESIMChannel) OpenLogicalChannel(aid []byte) (byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	aidHex := strings.ToUpper(hex.EncodeToString(aid))
	resp, err := c.command(fmt.Sprintf(`AT+CCHO="%s"`, aidHex), 8*time.Second)
	if err == nil {
		if channel, ok := parseUSBCCHO(resp); ok {
			c.channel, c.viaCSIM = byte(channel), false
			return c.channel, nil
		}
	}
	channel, csimErr := c.openViaCSIM(aid)
	if csimErr != nil {
		// Keep "AT+CCHO ... ERROR" in the message: a plain SIM fails both ways,
		// and the caller recognises it by that text.
		return 0, fmt.Errorf("打开 eUICC logical channel 失败 (AID=%s): AT+CCHO ERROR; AT+CSIM: %v", aidHex, csimErr)
	}
	c.channel, c.viaCSIM = channel, true
	return c.channel, nil
}

// openViaCSIM opens a logical channel with MANAGE CHANNEL and selects aid on it.
func (c *usbATESIMChannel) openViaCSIM(aid []byte) (byte, error) {
	return openRawChannel(c.csim, aid)
}

// channelCLA puts a logical channel number into a class byte (ISO 7816-4):
// channels 1–3 in the low bits, 4–19 in the further interindustry encoding.
func channelCLA(cla, channel byte) byte {
	if channel <= 3 {
		return cla&^0x03 | channel
	}
	return cla&0x80 | 0x40 | (channel-4)&0x0F
}

// csim sends one APDU over AT+CSIM and splits the reply into data and status.
func (c *usbATESIMChannel) csim(apdu []byte) ([]byte, uint16, error) {
	cmdHex := strings.ToUpper(hex.EncodeToString(apdu))
	resp, err := c.command(fmt.Sprintf(`AT+CSIM=%d,"%s"`, len(cmdHex), cmdHex), 15*time.Second)
	if err != nil {
		return nil, 0, err
	}
	respHex, ok := parseUSBCSIM(resp)
	if !ok {
		return nil, 0, fmt.Errorf("解析 CSIM 响应失败: %s", strings.TrimSpace(resp))
	}
	out, err := hex.DecodeString(respHex)
	if err != nil || len(out) < 2 {
		return nil, 0, fmt.Errorf("CSIM 响应无效: %s", respHex)
	}
	return out[:len(out)-2], uint16(out[len(out)-2])<<8 | uint16(out[len(out)-1]), nil
}

// csimFull sends an APDU and follows 61xx and 6Cxx the way AT+CGLA would.
func (c *usbATESIMChannel) csimFull(apdu []byte, channel byte) ([]byte, uint16, error) {
	return sendFull(c.csim, apdu, channel)
}

func parseUSBCSIM(resp string) (string, bool) {
	re := regexp.MustCompile(`\+CSIM:\s*\d+\s*,\s*"?([0-9A-Fa-f]+)"?`)
	match := re.FindStringSubmatch(resp)
	if len(match) != 2 {
		return "", false
	}
	return strings.ToUpper(match[1]), true
}

func (c *usbATESIMChannel) Transmit(command []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.channel == 0 {
		return nil, fmt.Errorf("eUICC logical channel 尚未打开")
	}
	if c.viaCSIM {
		return transmitOnChannel(c.csim, c.channel, command)
	}
	cmdHex := strings.ToUpper(hex.EncodeToString(command))
	resp, err := c.command(fmt.Sprintf(`AT+CGLA=%d,%d,"%s"`, c.channel, len(cmdHex), cmdHex), 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("APDU 透传失败: %w", err)
	}
	respHex, ok := parseUSBCGLA(resp)
	if !ok {
		return nil, fmt.Errorf("解析 CGLA 响应失败: %s", resp)
	}
	out, err := hex.DecodeString(respHex)
	if err != nil {
		return nil, fmt.Errorf("解析 APDU 响应 hex 失败: %w", err)
	}
	return out, nil
}

func (c *usbATESIMChannel) CloseLogicalChannel(channel byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var err error
	if c.viaCSIM {
		err = closeRawChannel(c.csim, channel)
	} else {
		_, err = c.command(fmt.Sprintf("AT+CCHC=%d", channel), 8*time.Second)
	}
	if err != nil {
		return fmt.Errorf("关闭 eUICC logical channel %d 失败: %w", channel, err)
	}
	if c.channel == channel {
		c.channel = 0
	}
	return nil
}

func parseUSBCCHO(resp string) (int, bool) {
	re := regexp.MustCompile(`\+CCHO:\s*(\d+)`)
	match := re.FindStringSubmatch(resp)
	if len(match) != 2 {
		return 0, false
	}
	var channel int
	if _, err := fmt.Sscanf(match[1], "%d", &channel); err != nil {
		return 0, false
	}
	return channel, true
}

func parseUSBCGLA(resp string) (string, bool) {
	re := regexp.MustCompile(`\+CGLA:\s*\d+\s*,\s*"?([0-9A-Fa-f]+)"?`)
	match := re.FindStringSubmatch(resp)
	if len(match) != 2 {
		return "", false
	}
	return strings.ToUpper(match[1]), true
}
