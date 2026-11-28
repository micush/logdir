package core

import (
	"testing"
)

func TestFilterAndOr(t *testing.T) {
	cases := []struct {
		text string
		name string
		want bool
	}{
		{"ubuntu", "ntp.ubuntu.com", true},
		{"ubuntu AND ntp", "ntp.ubuntu.com", true},
		{"ubuntu AND pool", "ntp.ubuntu.com", false},
		{"pool OR ubuntu", "ntp.ubuntu.com", true},
		{"pool or debian", "ntp.ubuntu.com", false},
		{"pool AND org OR ubuntu AND com", "ntp.ubuntu.com", true},
		{"pool AND com OR ubuntu AND org", "ntp.ubuntu.com", false},
		{"ubuntu AND", "ntp.ubuntu.com", true},
		{"ubuntu AND !pool", "ntp.ubuntu.com", true},
		{"ubuntu AND !ntp", "ntp.ubuntu.com", false},
		{"ubuntu !ntp", "ntp.ubuntu.com", false},
		{"!pool", "ntp.ubuntu.com", true},
		{"!ubuntu OR com", "ntp.ubuntu.com", true},
		{"!ubuntu AND !com", "ntp.ubuntu.com", false},
	}
	for _, c := range cases {
		if got := parseMatch(c.text).matches(c.name); got != c.want {
			t.Errorf("%q on %q: %v, want %v", c.text, c.name, got, c.want)
		}
	}
	if parseMatch("   ") != nil || parseMatch("AND OR") != nil {
		t.Error("a text with no word is a filter")
	}
}

func TestLogFilterNot(t *testing.T) {
	line := "2026-11-16 phx dns timeout"
	for text, want := range map[string]bool{"phx AND !dns": false, "phx AND !ntp": true, "phx !dns": false, "!dns": false, "phx AND NOT dns": false, "phx NOT ntp": true, "NOT NOT dns": true, "not !dns": true, "phx AND not": true, "!ntp": true, "phx dns !ntp": true, "q OR !dns": false, "q OR phx !ntp": true} {
		if got := logMatches(parseMatch(text), line); got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
}

func TestWordWildcard(t *testing.T) {
	cases := []struct {
		text, name string
		want       bool
	}{
		{"fw1*", "devname=\"fw1\" level=warning", true},
		{"!fw1*", "devname=\"fw1\" level=warning", false},
		{"!fw1*", "devname=\"sw2\"", true},
		{"*.cox.net", "host a.cox.net up", true},
		{"*.cox.net", "host a.cox.org up", false},
		{"fa*ed", "login failed", true},
		{"ed*fa", "login failed", false},
		{"fw1*", "fw", false},
		{"*", "anything", true},
	}
	for _, c := range cases {
		if got := parseMatch(c.text).matches(c.name); got != c.want {
			t.Errorf("%q on %q: %v, want %v", c.text, c.name, got, c.want)
		}
	}
}
