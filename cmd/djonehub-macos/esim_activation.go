package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// parseActivationCode reads an eSIM QR code (GSMA SGP.22 activation code):
// LPA:1$<SM-DP+ address>$<Matching ID>[$<OID>[$<confirmation code flag>]].
func parseActivationCode(code string) (smdp, matchingID string, err error) {
	code = strings.TrimSpace(code)
	if len(code) >= 4 && strings.EqualFold(code[:4], "LPA:") {
		code = code[4:]
	}
	parts := strings.Split(code, "$")
	if len(parts) < 2 || parts[0] != "1" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("不是有效的 eSIM 激活码（应为 LPA:1$服务器地址$激活码）")
	}
	smdp = strings.TrimSpace(parts[1])
	if len(parts) > 2 {
		matchingID = strings.TrimSpace(parts[2])
	}
	return smdp, matchingID, nil
}

var imeiDigits = regexp.MustCompile(`\b(\d{15})\b`)

// moduleIMEI reads the module's IMEI, which the SM-DP+ may bind the Profile to.
func (a *app) moduleIMEI() string {
	for _, command := range []string{"AT+CGSN", "AT+GSN"} {
		if resp, err := a.runATCommand(command, 3*time.Second); err == nil {
			if m := imeiDigits.FindStringSubmatch(resp); m != nil {
				return m[1]
			}
		}
	}
	return ""
}
