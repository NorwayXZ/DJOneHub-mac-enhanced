package main

import "testing"

func TestParseActivationCode(t *testing.T) {
	cases := map[string][2]string{
		"LPA:1$rsp.truphone.com$QR-G-5C-1LS-1W1Z9P7": {"rsp.truphone.com", "QR-G-5C-1LS-1W1Z9P7"},
		" lpa:1$smdp.io$ABC$1.3.6.1.4.1.31746$1 ":    {"smdp.io", "ABC"},
		"1$consumer.e-sim.global$":                   {"consumer.e-sim.global", ""},
	}
	for code, want := range cases {
		smdp, id, err := parseActivationCode(code)
		if err != nil || smdp != want[0] || id != want[1] {
			t.Errorf("%q → %q %q %v", code, smdp, id, err)
		}
	}
	for _, bad := range []string{"", "https://example.com", "LPA:2$a$b", "LPA:1$$x"} {
		if _, _, err := parseActivationCode(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
