package main

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"
)

// benchStore holds hours hours of perHour messages like a firewall's: a long key=value line with a few varying words.
func benchStore(b *testing.B, hours, perHour int) (*ldStore, time.Time) {
	now := ldNow
	s := newLdStore(b.TempDir(), "n1")
	s.now = func() time.Time { return now }
	s.Load()
	base := now.Add(-time.Duration(hours) * time.Hour)
	for h := 0; h < hours; h++ {
		for i := 0; i < perHour; i++ {
			at := base.Add(time.Duration(h)*time.Hour + time.Duration(i)*time.Hour/time.Duration(perHour))
			s.Add(LdMsg{T: at.UnixMilli(), Host: fmt.Sprintf("fw%d", i%4), Prog: "fortigate", Sev: int8(i % 8), Fac: 1, Src: fmt.Sprintf("10.1.1.%d", i%4),
				Msg: fmt.Sprintf(`date=2026-11-21 time=20:30:56 devname="fw%d" logid="00000000%02d" type="traffic" subtype="forward" level="warning" srcip=192.168.%d.%d srcport=%d dstip=151.101.%d.%d dstport=443 action="ip-conn" policyid=1 sessionid=%d`, i%4, i%50, i%200, i%250, 1024+i%60000, i%200, i%250, 17000000+i), Via: "udp"})
		}
	}
	s.Flush()
	return s, now
}

func BenchmarkAdd(b *testing.B) {
	now := ldNow
	s := newLdStore(b.TempDir(), "n1")
	s.now = func() time.Time { return now }
	s.Load()
	m := LdMsg{T: now.UnixMilli(), Host: "fw1", Prog: "fortigate", Sev: 4, Fac: 1, Src: "10.1.1.5", Via: "udp",
		Msg: `date=2026-11-21 time=20:30:56 devname="fw1" logid="0000000011" type="traffic" subtype="forward" level="warning" srcip=192.168.193.29 srcport=40657 dstip=151.101.193.140 dstport=443 action="ip-conn" policyid=1`}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Add(m)
	}
}

func benchSearch(b *testing.B, q string) {
	s, now := benchStore(b, 6, 30000)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := s.Search(ctx, LdQuery{From: now.Add(-24 * time.Hour).UnixMilli(), MinSev: -1, Limit: 1000, Q: q})
		_ = a
	}
}

func BenchmarkSearchWordMiss(b *testing.B) { benchSearch(b, "nosuchword") }
func BenchmarkSearchWordHit(b *testing.B)  { benchSearch(b, "dstport=443") }
func BenchmarkSearchNot(b *testing.B)      { benchSearch(b, "!fw1") }

func BenchmarkStats(b *testing.B) {
	s, now := benchStore(b, 6, 30000)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Stats(ctx, LdQuery{From: now.Add(-24 * time.Hour).UnixMilli(), MinSev: -1}, 3600)
	}
}

// BenchmarkHeldHour reports what a cached hour of 30000 messages holds in memory.
func BenchmarkHeldHour(b *testing.B) {
	s, _ := benchStore(b, 2, 30000)
	h := s.Oldest()/1000/3600 + 0
	var before, after runtime.MemStats
	var keep [][]LdMsg
	for i := 0; i < b.N; i++ {
		runtime.GC()
		runtime.ReadMemStats(&before)
		keep = append(keep, s.readHour(h))
		runtime.GC()
		runtime.ReadMemStats(&after)
		b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/float64(len(keep[len(keep)-1])), "bytes/msg")
	}
	_ = keep
}
