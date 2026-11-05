package main

import "testing"

func TestClassifyCardTellsDataOnlyFromSMS(t *testing.T) {
	cases := []struct {
		smsc   string
		cs, ps int
		want   string
	}{
		{"+8529204004401", 5, 5, "voice_sms"},
		{"+7", 5, 5, "data_only"},
		{"", 1, 1, "data_only"},
		{"+6596845999", 2, 5, "data_only"},
	}
	for _, c := range cases {
		if got := classifyCard(c.smsc, c.cs, c.ps).Kind; got != c.want {
			t.Errorf("classifyCard(%q, %d, %d) = %s, want %s", c.smsc, c.cs, c.ps, got, c.want)
		}
	}
	if got := parseCSCA("AT+CSCA?\r\n+CSCA: \"+7\",145\r\nOK"); got != "+7" {
		t.Errorf("parseCSCA = %q", got)
	}
	if got := registrationState("+CREG: 0,5\r\n+CEREG: 0,2", "+CEREG:"); got != 2 {
		t.Errorf("registrationState = %d", got)
	}
}
