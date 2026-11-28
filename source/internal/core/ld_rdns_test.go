package core

import (
	"net/netip"
	"testing"
)

func TestPTRName(t *testing.T) {
	if got := ptrName(netip.MustParseAddr("192.168.5.11")); got != "11.5.168.192.in-addr.arpa." {
		t.Fatal(got)
	}
	if got := ptrName(netip.MustParseAddr("2001:db8::1")); got != "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa." {
		t.Fatal(got)
	}
}

func TestParsePTRAnswerNeverPanics(t *testing.T) {
	good := []byte{0x4c, 0x44, 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0, 1, 'x', 0, 0, 12, 0, 1, 0xc0, 12, 0, 12, 0, 1, 0, 0, 0, 1, 0, 3, 1, 'a', 0}
	for i := 0; i <= len(good); i++ {
		parsePTRAnswer(good[:i], []byte{0x4c, 0x44})
	}
	loop := append([]byte{}, good...)
	loop[len(loop)-3], loop[len(loop)-2] = 0xc0, byte(len(loop)-3) // a pointer to itself
	parsePTRAnswer(loop, []byte{0x4c, 0x44})
	if n, _, err := parsePTRAnswer(good, []byte{0x4c, 0x44}); err != nil || len(n) != 1 || n[0] != "a" {
		t.Fatalf("%v %v", n, err)
	}
}
