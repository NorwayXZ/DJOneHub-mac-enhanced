//go:build darwin && cgo

package main

/*
#include <net/if.h>
#include <net/route.h>
#include <stdlib.h>
#include <sys/socket.h>
#include <sys/sysctl.h>

// 64-bit interface counters (getifaddrs only has 32-bit ones, which wrap at 4 GB).
static int djInterfaceCounters(unsigned int index, unsigned long long out[4]) {
	int mib[6] = {CTL_NET, PF_ROUTE, 0, 0, NET_RT_IFLIST2, (int)index};
	size_t len = 0;
	if (sysctl(mib, 6, NULL, &len, NULL, 0) < 0 || len == 0) return -1;
	char *buf = malloc(len);
	if (!buf) return -1;
	if (sysctl(mib, 6, buf, &len, NULL, 0) < 0) { free(buf); return -1; }
	int found = -1;
	for (char *next = buf; next + sizeof(struct if_msghdr) <= buf + len;) {
		struct if_msghdr *ifm = (struct if_msghdr *)next;
		if (ifm->ifm_msglen == 0) break;
		if (ifm->ifm_type == RTM_IFINFO2) {
			struct if_msghdr2 *if2 = (struct if_msghdr2 *)ifm;
			if (if2->ifm_index == index) {
				out[0] = if2->ifm_data.ifi_ibytes;
				out[1] = if2->ifm_data.ifi_obytes;
				out[2] = if2->ifm_data.ifi_ipackets;
				out[3] = if2->ifm_data.ifi_opackets;
				found = 0;
				break;
			}
		}
		next += ifm->ifm_msglen;
	}
	free(buf);
	return found;
}
*/
import "C"

import (
	"errors"
	"net"
)

func interfaceCounters(name string) (interfaceCounterReading, error) {
	i, err := net.InterfaceByName(name)
	if err != nil {
		return interfaceCounterReading{}, err
	}
	var out [4]C.ulonglong
	if C.djInterfaceCounters(C.uint(i.Index), &out[0]) != 0 {
		return interfaceCounterReading{}, errors.New("interface counters unavailable")
	}
	return interfaceCounterReading{Rx: uint64(out[0]), Tx: uint64(out[1]), RxPkts: uint64(out[2]), TxPkts: uint64(out[3])}, nil
}
