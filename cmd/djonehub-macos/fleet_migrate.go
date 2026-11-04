package main

import (
	"log"
	"os"
	"path/filepath"
)

// seedModuleState gives a module worker the single-module state the first
// time it runs in multi-module mode: whether the proxy starts by itself goes
// to every module; the IP history and SIM usage go to the first module that
// asks only, so no module shows another SIM's records. The single-module
// files stay in place for switching back.
func seedModuleState(moduleDir, sharedDir, moduleID string) {
	if moduleDir == "" || sharedDir == "" || moduleDir == sharedDir {
		return
	}
	for _, f := range []struct {
		name  string
		claim bool
	}{{"proxy-autostart.json", false}, {"proxy-ip-history.json", true}, {"sim-usage.json", true}} {
		target := filepath.Join(moduleDir, f.name)
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sharedDir, f.name))
		if err != nil {
			continue
		}
		if f.claim {
			marker, err := os.OpenFile(filepath.Join(sharedDir, f.name+".claimed"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				continue // another module took it
			}
			_, _ = marker.WriteString(moduleID)
			marker.Close()
		}
		if err := os.MkdirAll(moduleDir, 0700); err != nil {
			continue
		}
		if err := os.WriteFile(target, data, 0600); err == nil {
			log.Printf("module %s: carried over %s from single-module mode", moduleID, f.name)
		}
	}
}
