package main

import (
	"strings"
	"sync"
	"time"
)

// callAudioCheck caches whether this module can carry call sound to the
// computer, so the call screen can warn before the user answers.
var callAudioCheck struct {
	sync.Mutex
	at      time.Time
	warning string
}

func (a *app) callAudioWarning() string {
	if a.demo {
		return ""
	}
	a.moduleVoiceMu.Lock()
	voiceErr := a.moduleVoiceErr
	a.moduleVoiceMu.Unlock()
	if strings.Contains(voiceErr, "ADB interface") && strings.Contains(voiceErr, "not found") {
		return noCallAudioSummary
	}
	callAudioCheck.Lock()
	defer callAudioCheck.Unlock()
	if !callAudioCheck.at.IsZero() && time.Since(callAudioCheck.at) < 10*time.Minute {
		return callAudioCheck.warning
	}
	response, err := a.runATCommand(`AT+QCFG="USBCFG"`, 3*time.Second)
	if err != nil {
		return callAudioCheck.warning
	}
	callAudioCheck.at = time.Now()
	callAudioCheck.warning = ""
	if composition, err := parseUSBComposition(response); err == nil && !composition.hasADB() {
		callAudioCheck.warning = "这个模块还没开通话支持，接了会没有声音。可以在 设置 › 通话支持 里开启"
		if composition.isLegacyUACTarget() {
			callAudioCheck.warning = noCallAudioSummary
		}
	}
	return callAudioCheck.warning
}
