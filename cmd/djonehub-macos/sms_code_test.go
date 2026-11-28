package main

import "testing"

func TestExtractSMSCode(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "Chinese", content: "【服务】您的验证码为 482913，5 分钟内有效。", want: "482913"},
		{name: "English", content: "Your verification code is 123456. Do not share it.", want: "123456"},
		{name: "Code before keyword", content: "登录动态码：778899", want: "778899"},
		{name: "No keyword", content: "您的号码 13800138000，本月余额 128.50 元。", want: ""},
		{name: "Too short", content: "验证码 123", want: ""},
		{name: "Google prefix far from keyword", content: "G-469200 is your Google verification code. Don't share your code with anyone.", want: "469200"},
		{name: "Long gap after keyword", content: "Capital One won't call you for this code. The temporary code you requested to sign-in is 418189. Please don't share this code with anyone.", want: "418189"},
		{name: "Hyphenated", content: "您的 WhatsApp 验证码: 623-819 您可以点击此链接验证您的号码", want: "623819"},
		{name: "Card ending is not a code", content: "Hi, it's Capital One. Did you just try to make this purchase with your Platinum Card ending in 6862? Reply YES or NO.", want: ""},
		{name: "Phone number is skipped", content: "Call +12403486504 if this wasn't you. Your code is 5521.", want: "5521"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractSMSCode(test.content); got != test.want {
				t.Fatalf("extractSMSCode(%q) = %q, want %q", test.content, got, test.want)
			}
		})
	}
}
