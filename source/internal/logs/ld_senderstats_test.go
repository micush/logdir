package logs

import (
	"context"
	"testing"
	"time"

	"logdir/internal/core"
)

// Stats counts messages per sender, per bucket per sender, and per severity class (info or less / warning / error or worse).
func TestStoreStatsPerSenderAndSeverity(t *testing.T) {
	now := ldNow
	s := newTestStore(t, &now)
	add := func(ago time.Duration, src string, sev int8) {
		s.Add(core.LdMsg{T: now.Add(-ago).UnixMilli(), Host: "h", Sev: sev, Fac: 1, Msg: "m", Src: src, Via: "udp"})
	}
	add(30*time.Minute, "10.0.0.1", 6)
	add(30*time.Minute, "10.0.0.1", 4)
	add(30*time.Minute, "10.0.0.2", 3)
	add(10*time.Minute, "10.0.0.2", 0)
	add(10*time.Minute, "10.0.0.2", 7)
	q := core.LdQuery{From: now.Add(-time.Hour).UnixMilli(), MinSev: -1}
	a := s.Stats(context.Background(), q, 1800)
	if a.Total != 5 || a.BySrc["10.0.0.1"] != 2 || a.BySrc["10.0.0.2"] != 3 {
		t.Fatalf("by source: %+v", a.BySrc)
	}
	sum := func(m map[int64]uint64) (n uint64) {
		for _, v := range m {
			n += v
		}
		return
	}
	if sum(a.SevSer[0]) != 2 || sum(a.SevSer[1]) != 1 || sum(a.SevSer[2]) != 2 {
		t.Fatalf("severity classes: %v", a.SevSer)
	}
	if sum(a.SrcSer["10.0.0.1"]) != 2 || sum(a.SrcSer["10.0.0.2"]) != 3 || len(a.SrcSer) != 2 {
		t.Fatalf("source series: %v", a.SrcSer)
	}
	if b := s.Stats(context.Background(), q, 0); b.SrcSer != nil || b.BySrc["10.0.0.2"] != 3 {
		t.Fatalf("without a step there is no series, only the totals: %+v", b)
	}
}
