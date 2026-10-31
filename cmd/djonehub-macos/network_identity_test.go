package main

import "testing"

func TestVerifiedNetworkSelectionIgnoresRandomMAC(t *testing.T) {
	interfaces := []macNetInterface{{Name: "en5", Kind: "ethernet", MAC: "22:7a:e5:07:20:3a"}, {Name: "en12", Kind: "ethernet"}}
	identity := func(name string) uint64 {
		if name == "en12" {
			return 100
		}
		return 0
	}
	if got := selectVerifiedUnprovisioned(interfaces, nil, identity); got != "en12" {
		t.Fatal(got)
	}
	services := []macNetworkService{{Name: "User named modem", Device: "en12"}}
	if got := selectVerifiedUnprovisioned(interfaces, services, identity); got != "" {
		t.Fatal("existing hardware interface duplicated", got)
	}
}
func TestNetworkCreationChecksActualService(t *testing.T) {
	if err := verifyCreatedNetworkService("Baiwang", "en5", nil, "Could not find hardware port"); err == nil {
		t.Fatal("false-success accepted")
	}
	if err := verifyCreatedNetworkService("Baiwang", "en12", []macNetworkService{{Name: "Baiwang", Device: "en12"}}, ""); err != nil {
		t.Fatal(err)
	}
}
