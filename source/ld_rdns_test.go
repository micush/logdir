package main

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestPTRName(t *testing.T) {
	if got := ptrName(netip.MustParseAddr("192.168.5.11")); got != "11.5.168.192.in-addr.arpa." {
		t.Fatal(got)
	}
	if got := ptrName(netip.MustParseAddr("2001:db8::1")); got != "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa." {
		t.Fatal(got)
	}
}

// fakeRdnsServer answers every query with the given rcode and, for rcode 0, a PTR to name (the owner name compressed to the question).
func fakeRdnsServer(t *testing.T, rcode byte, _ string) string {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback UDP")
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			q := append([]byte{}, buf[:n]...)
			r := append([]byte{}, q...)
			r[2], r[3] = 0x81, 0x80|rcode
			if rcode == 0 {
				r[7] = 1
				r = append(r, 0xc0, 12, 0, 12, 0, 1, 0, 0, 0, 60)
				var rd []byte
				for _, l := range []string{"media1", "cush", "local"} {
					rd = append(rd, byte(len(l)))
					rd = append(rd, l...)
				}
				rd = append(rd, 0)
				r = append(r, byte(len(rd)>>8), byte(len(rd)))
				r = append(r, rd...)
			}
			pc.WriteTo(r, from)
		}
	}()
	return pc.LocalAddr().String()
}

func TestRawPTRAnswersAndNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	names, err := rawPTR(ctx, "192.168.5.11", []string{fakeRdnsServer(t, 0, "")})
	if err != nil || len(names) != 1 || names[0] != "media1.cush.local" || hostPart(names[0]) != "media1" {
		t.Fatalf("%v %v", names, err)
	}
	_, err = rawPTR(ctx, "192.168.5.12", []string{fakeRdnsServer(t, 3, "")})
	if de, ok := err.(*net.DNSError); !ok || !de.IsNotFound {
		t.Fatalf("want not found, got %v", err)
	}
	if _, err = rawPTR(ctx, "192.168.5.12", nil); err == nil {
		t.Fatal("no server must be an error")
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
