//go:build windows

package main

import (
	"net"

	"golang.org/x/sys/windows"
)

func interfaceCounters(iface string) (interfaceCounterReading, error) {
	i, err := net.InterfaceByName(iface)
	if err != nil {
		return interfaceCounterReading{}, err
	}
	row := windows.MibIfRow2{InterfaceIndex: uint32(i.Index)}
	if err := windows.GetIfEntry2Ex(windows.MibIfTableNormal, &row); err != nil {
		return interfaceCounterReading{}, err
	}
	return interfaceCounterReading{
		Rx: row.InOctets, Tx: row.OutOctets,
		RxPkts: row.InUcastPkts + row.InNUcastPkts, TxPkts: row.OutUcastPkts + row.OutNUcastPkts,
	}, nil
}
