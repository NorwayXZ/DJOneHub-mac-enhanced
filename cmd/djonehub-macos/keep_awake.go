package main

import "log"

func logKeepAwake(on bool) {
	if on {
		log.Printf("keeping the computer awake while the cellular proxy is on")
	} else {
		log.Printf("cellular proxy off, the computer may sleep again")
	}
}

// wantsAwake: the user wants this proxy up (auto-start), or it is running.
func (p *cellularProxy) wantsAwake() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadAutoStartLocked()
	return p.state.AutoStart || p.listener != nil
}
