package core

import (
	"strings"
	"testing"
)

func TestMCPACLNormalised(t *testing.T) {
	got, err := NormMCPACL([]string{" 10.1.1.50  RW", "10.1.0.0/24", "10.1.1.50", "Mon.Example.com.", "2001:db8::5 ro", "192.0.2.9 read-write"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "10.1.1.50 rw|10.1.0.0/24|mon.example.com|2001:db8::5|192.0.2.9 rw" {
		t.Fatalf("%q", got)
	}
	for _, bad := range []string{"10.1.0.0/24 rw", "mon.example.com rw", "10.1.1.50 admin", "10.1.1.50 rw extra", "not an address"} {
		if _, err := NormMCPACL([]string{bad}); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
