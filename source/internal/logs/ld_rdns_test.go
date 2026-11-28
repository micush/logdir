package logs

import (
	"context"
	"net"
	"testing"
	"time"

	"logdir/internal/core"
)

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
	names, err := core.RawPTR(ctx, "192.168.5.11", []string{fakeRdnsServer(t, 0, "")})
	if err != nil || len(names) != 1 || names[0] != "media1.cush.local" || hostPart(names[0]) != "media1" {
		t.Fatalf("%v %v", names, err)
	}
	_, err = core.RawPTR(ctx, "192.168.5.12", []string{fakeRdnsServer(t, 3, "")})
	if de, ok := err.(*net.DNSError); !ok || !de.IsNotFound {
		t.Fatalf("want not found, got %v", err)
	}
	if _, err = core.RawPTR(ctx, "192.168.5.12", nil); err == nil {
		t.Fatal("no server must be an error")
	}
}
