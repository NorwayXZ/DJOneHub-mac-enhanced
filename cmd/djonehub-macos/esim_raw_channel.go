package main

import "fmt"

// Logical channels driven with raw APDUs (ISO 7816-4), shared by the module's
// AT+CSIM path and USB smart-card readers (PC/SC).

// apduSender sends one APDU and returns its data and status word.
type apduSender func(apdu []byte) ([]byte, uint16, error)

// sendFull sends an APDU and follows 61xx (GET RESPONSE) and 6Cxx (wrong Le),
// returning all response data and the final status.
func sendFull(send apduSender, apdu []byte, channel byte) ([]byte, uint16, error) {
	data, sw, err := send(apdu)
	if err != nil {
		return nil, 0, err
	}
	if sw>>8 == 0x6C {
		// Resend with the length the card asked for: replace Le, or add it.
		retry := append([]byte{}, apdu...)
		if len(apdu) == 5 {
			retry[4] = byte(sw)
		} else {
			retry = append(retry, byte(sw))
		}
		if data, sw, err = send(retry); err != nil {
			return nil, 0, err
		}
	}
	for i := 0; sw>>8 == 0x61 && i < 64; i++ {
		more, next, err := send([]byte{channelCLA(0x00, channel), 0xC0, 0x00, 0x00, byte(sw)})
		if err != nil {
			return nil, 0, err
		}
		data, sw = append(data, more...), next
	}
	return data, sw, nil
}

// openRawChannel opens a logical channel with MANAGE CHANNEL and selects aid on it.
func openRawChannel(send apduSender, aid []byte) (byte, error) {
	data, sw, err := send([]byte{0x00, 0x70, 0x00, 0x00, 0x01})
	if err != nil {
		return 0, err
	}
	if sw != 0x9000 || len(data) != 1 || data[0] == 0 {
		return 0, fmt.Errorf("MANAGE CHANNEL %04X", sw)
	}
	channel := data[0]
	selectAPDU := append([]byte{channelCLA(0x00, channel), 0xA4, 0x04, 0x00, byte(len(aid))}, aid...)
	if _, sw, err = sendFull(send, selectAPDU, channel); err != nil || (sw != 0x9000 && sw>>8 != 0x62 && sw>>8 != 0x63) {
		_ = closeRawChannel(send, channel)
		if err == nil {
			err = fmt.Errorf("SELECT %04X", sw)
		}
		return 0, err
	}
	return channel, nil
}

func closeRawChannel(send apduSender, channel byte) error {
	_, _, err := send([]byte{0x00, 0x70, 0x80, channel, 0x00})
	return err
}

// transmitOnChannel sends command on a logical channel; the reply ends with SW.
func transmitOnChannel(send apduSender, channel byte, command []byte) ([]byte, error) {
	if len(command) < 4 {
		return nil, fmt.Errorf("APDU 太短")
	}
	apdu := append([]byte{}, command...)
	apdu[0] = channelCLA(apdu[0], channel)
	data, sw, err := sendFull(send, apdu, channel)
	if err != nil {
		return nil, fmt.Errorf("APDU 透传失败: %w", err)
	}
	return append(data, byte(sw>>8), byte(sw)), nil
}
