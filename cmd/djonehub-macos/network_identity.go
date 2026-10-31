package main

import "fmt"

func isHardwareCellularService(service macNetworkService) bool {
	session := fleetSessionForInterface(service.Device)
	return session != 0 && (scopedUSBSession == 0 || session == scopedUSBSession)
}

func selectVerifiedUnprovisioned(interfaces []macNetInterface, services []macNetworkService, identity func(string) uint64) string {
	existing := map[string]bool{}
	for _, service := range services {
		existing[service.Device] = true
	}
	selected := ""
	for _, iface := range interfaces {
		if identity(iface.Name) == 0 || existing[iface.Name] {
			continue
		}
		if selected != "" {
			return ""
		}
		selected = iface.Name
	}
	return selected
}

func verifyCreatedNetworkService(name, device string, services []macNetworkService, output string) error {
	for _, service := range services {
		if service.Name == name && service.Device == device {
			return nil
		}
	}
	return fmt.Errorf("networksetup 未实际创建 %s (%s): %s", name, device, output)
}
