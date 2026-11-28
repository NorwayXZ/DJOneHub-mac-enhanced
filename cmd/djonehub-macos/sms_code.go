package main

import (
	"regexp"
	"strings"
)

var (
	// smsCodeKeyword marks a message as carrying a one-time code.
	smsCodeKeyword = regexp.MustCompile(`(?i)验证码|校验码|动态码|验证代码|确认码|登录码|安全码|激活码|verification|verify|security\s*code|one[-\s]?time|otp|passcode|pass\s*code|login\s*code|sign[-\s]?in|temporary\s*code|access\s*code|auth(?:entication)?\s*code|your\s*code|\bcode\b|\bpin\b|コード|인증`)
	// smsCodeCandidate: 4-8 digits, or two groups of 3-4 joined by "-" or a
	// space (623-819), optionally after a letter prefix (G-469200).
	smsCodeCandidate = regexp.MustCompile(`(?:^|[^0-9A-Za-z+])((?:[A-Z]{1,3}-)?(?:\d{3,4}[- ]\d{3,4}|\d{4,8}))(?:$|[^0-9A-Za-z])`)
	// Numbers that are not codes even next to a keyword.
	smsCodeNotCode = regexp.MustCompile(`(?i)(?:ending\s*(?:in|with)?|last\s*\d?\s*digits|尾号|末尾|卡号|账号|余额|金额|\$|¥|￥|tel|call)\s*[:：]?\s*$`)
)

// extractSMSCode returns the one-time code in a message, digits only
// (G-469200 -> 469200, 623-819 -> 623819), or "" when the message has no
// OTP-style keyword. Of several numbers, the one nearest a keyword wins, so
// phone numbers, balances and order IDs in the same message are skipped.
func extractSMSCode(content string) string {
	keywords := smsCodeKeyword.FindAllStringIndex(content, -1)
	if len(keywords) == 0 {
		return ""
	}
	best, bestDistance := "", 1<<30
	for _, m := range smsCodeCandidate.FindAllStringSubmatchIndex(content, -1) {
		start, end := m[2], m[3]
		if smsCodeNotCode.MatchString(content[max(0, start-24):start]) {
			continue
		}
		token := content[start:end]
		if i := strings.LastIndex(token, "-"); i > 0 && i <= 3 && strings.ToUpper(token[:i]) == token[:i] && !strings.ContainsAny(token[:i], "0123456789") {
			token = token[i+1:] // letter prefix such as "G-"
		}
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, token)
		if len(digits) < 4 || len(digits) > 8 {
			continue
		}
		for _, k := range keywords {
			distance := 0
			switch {
			case k[1] <= start:
				distance = start - k[1]
			case k[0] >= end:
				distance = k[0] - end
			}
			if distance <= 80 && distance < bestDistance {
				best, bestDistance = digits, distance
			}
		}
	}
	return best
}
