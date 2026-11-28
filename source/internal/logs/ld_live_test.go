package logs

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"testing"
	"time"

	"logdir/internal/core"
)

// TestLiveSum: the newest hour's counts come from the rollup kept as messages are added, and they are the same as reading the hour;
// after a restart during the hour the hour is read as before.
func TestLiveSum(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 26, 10, 40, 0, 0, time.UTC)
	s := NewLdStore(dir, "n1")
	s.Now = func() time.Time { return now }
	s.Load()
	add := func(st *LdStore, from time.Time, n int) {
		for i := 0; i < n; i++ {
			st.Add(core.LdMsg{T: from.Add(time.Duration(i) * time.Second).UnixMilli(), Host: fmt.Sprintf("sw%d", i%7), Prog: []string{"ifmgr", "stp"}[i%2], Src: fmt.Sprintf("10.0.0.%d", i%7), Sev: int8(i % 8), Msg: fmt.Sprintf("port %d down", i)})
		}
	}
	add(s, now.Add(-3*time.Hour), 5000)
	add(s, now.Truncate(time.Hour), 1500) // the newest hour: 10:00 to 10:25
	s.Flush()
	if h, sum := func() (int64, *ldHourSum) { s.mu.Lock(); defer s.mu.Unlock(); return s.liveSnapshot() }(); sum == nil || h != now.Unix()/3600 || sum.count != 1500 {
		t.Fatalf("live rollup: hour %d, %+v", h, sum)
	}
	from := now.Add(-24 * time.Hour).UnixMilli()
	for _, q := range []core.LdQuery{{From: from, MinSev: -1}, {From: from, MinSev: 4}, {From: from, MinSev: -1, Host: "sw3"}, {From: from, MinSev: -1, Q: "down"}} {
		got := s.Stats(context.Background(), q, 3600)
		q2 := q
		q2.To = now.UnixMilli() + 1 // a range with an end: the newest hour is read, not taken from its rollup
		want := s.Stats(context.Background(), q2, 3600)
		got.Scanned, want.Scanned = 0, 0
		if !reflect.DeepEqual(got, want) || got.Total == 0 {
			t.Fatalf("%+v: live %+v\nread %+v", q, got, want)
		}
	}
	// the newest hour without words is not read: only its rollup is used (its file moved away, the counts are still there)
	q := core.LdQuery{From: now.Truncate(time.Hour).UnixMilli(), MinSev: -1}
	hp := s.path(now.Unix()/3600, false)
	s.mu.Lock()
	s.closeCurLocked()
	s.mu.Unlock()
	if err := os.Rename(hp, hp+".away"); err != nil {
		t.Fatal(err)
	}
	if st := s.Stats(context.Background(), q, 3600); st.Total != 1500 || st.Series[now.Truncate(time.Hour).Unix()] != 1500 {
		t.Fatalf("newest hour from its rollup: %d %v", st.Total, st.Series)
	}
	if st := s.Stats(context.Background(), core.LdQuery{From: q.From, MinSev: -1, Q: "down"}, 3600); st.Total != 0 {
		t.Fatalf("words are looked for in the file: %d", st.Total)
	}
	os.Rename(hp+".away", hp)
	s.Close()
	// a restart in the middle of the hour: the rollup misses what came before, so it is not used
	s2 := NewLdStore(dir, "n1")
	s2.Now = func() time.Time { return now }
	s2.Load()
	add(s2, now.Truncate(time.Hour).Add(30*time.Minute), 10)
	s2.mu.Lock()
	_, sum := s2.liveSnapshot()
	s2.mu.Unlock()
	if sum != nil {
		t.Fatal("live rollup used after a restart during the hour")
	}
	if st := s2.Stats(context.Background(), q, 3600); st.Total != 1510 {
		t.Fatalf("after restart: %d", st.Total)
	}
}

// TestStatsParallel: hours read several at a time add up to the same answer as one at a time: counts, series, the values of a
// field, how many different (exact and estimated) and sums, with and without words.
func TestStatsParallel(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 26, 10, 40, 0, 0, time.UTC)
	s := NewLdStore(dir, "n1")
	s.Now = func() time.Time { return now }
	s.Load()
	s.cache.set(0, 1) // nothing fits: every hour is read from its file
	start := now.Add(-9 * time.Hour)
	for i := 0; i < 9*3600/4; i++ {
		src := fmt.Sprintf("10.0.0.%d", i%9)
		port := 1 + i%40
		if i%9 == 0 {
			port = 1 + i%2000 // one source reaches many ports: an estimate
		}
		s.Add(core.LdMsg{T: start.Add(time.Duration(i*4) * time.Second).UnixMilli(), Host: fmt.Sprintf("fw%d", i%3), Prog: "filterlog", Src: "10.1.1.1", Sev: int8(4 + i%3),
			Msg: fmt.Sprintf("action=%s src_ip=%s dst_port=%d bytes=%d", []string{"accept", "deny"}[i%2], src, port, 100+i%77)})
	}
	s.Flush()
	from := now.Add(-24 * time.Hour).UnixMilli()
	qs := []core.LdQuery{
		{From: from, MinSev: -1, Field: "src_ip", Measure: "distinct", MField: "dst_port", Values: []string{"10.0.0.0", "10.0.0.3"}},
		{From: from, MinSev: -1, Field: "host", Measure: "sum", MField: "bytes"},
		{From: from, MinSev: -1, Q: "deny", Field: "dst_port"},
		{From: from, MinSev: 5, Q: "accept"},
	}
	run := func(procs int, q core.LdQuery) LdStatsAnswer {
		old := runtime.GOMAXPROCS(procs)
		defer runtime.GOMAXPROCS(old)
		s.hits = map[ldHitKey]*ldHits{} // words counted before would be taken from what was kept
		r := s.Stats(context.Background(), q, 3600)
		for _, a := range r.FieldAgg {
			a.seal()
			a.set, a.reg = nil, nil
		}
		return r
	}
	for _, q := range qs {
		one, many := run(1, q), run(8, q)
		if ldScanWorkers() < 1 || one.Total == 0 || one.Partial || many.Partial {
			t.Fatalf("%+v: %d %v %v", q, one.Total, one.Partial, many.Partial)
		}
		if !reflect.DeepEqual(one, many) {
			t.Fatalf("%+v:\none at a time %+v\nseveral       %+v", q, one, many)
		}
	}
	// the parts of closed hours are kept: asked again (the page's refresh), or with bars of two hours, the answer is the same as
	// reading every hour, and the closed hours are not read
	for _, q := range qs[:3] {
		for _, step := range []int64{3600, 7200, 0} {
			s.fparts, s.fpartBytes = nil, 0
			s.hits = map[ldHitKey]*ldHits{}
			fresh := s.Stats(context.Background(), q, step)
			if step != 3600 {
				s.fparts, s.fpartBytes = nil, 0
				s.Stats(context.Background(), q, 3600) // the parts are made with hour bars, then used with others
			}
			kept := len(s.fparts)
			if step > 0 && kept < 8 {
				t.Fatalf("%+v: %d hours kept", q, kept)
			}
			// the closed hours' files moved away: only what was kept can answer for them
			var moved []string
			if step > 0 {
				for h := range s.meta {
					if s.closedHour(h) {
						for _, p := range []string{s.path(h, true), s.path(h, false)} {
							if os.Rename(p, p+".away") == nil {
								moved = append(moved, p)
							}
						}
					}
				}
				if len(moved) < 8 {
					t.Fatalf("%d files moved", len(moved))
				}
			}
			again := s.Stats(context.Background(), q, step)
			for _, p := range moved {
				os.Rename(p+".away", p)
			}
			for _, x := range []*LdStatsAnswer{&fresh, &again} {
				x.Scanned = 0
				for _, a := range x.FieldAgg {
					a.seal()
					a.set, a.reg = nil, nil
				}
			}
			if !reflect.DeepEqual(fresh, again) {
				t.Fatalf("%+v step %d:\nread %+v\nkept %+v", q, step, fresh, again)
			}
		}
	}
	// the Values card's chart (Over time: the top values' series, nothing measured) comes from the parts the card's count kept
	moveAway := func() func() {
		var moved []string
		for h := range s.meta {
			if s.closedHour(h) {
				for _, p := range []string{s.path(h, true), s.path(h, false)} {
					if os.Rename(p, p+".away") == nil {
						moved = append(moved, p)
					}
				}
			}
		}
		return func() {
			for _, p := range moved {
				os.Rename(p+".away", p)
			}
		}
	}
	over := core.LdQuery{From: from, MinSev: -1, Field: "src_ip", Values: []string{"10.0.0.3", "10.0.0.0", "10.9.9.9"}}
	s.fparts, s.fpartBytes = nil, 0
	fresh := s.Stats(context.Background(), over, 3600)
	s.fparts, s.fpartBytes = nil, 0
	s.Stats(context.Background(), qs[0], 3600) // the count, with how many different
	back := moveAway()
	again := s.Stats(context.Background(), over, 3600)
	back()
	fresh.Scanned, again.Scanned = 0, 0
	if !reflect.DeepEqual(fresh, again) || len(again.ValSer["10.0.0.3"]) < 9 {
		t.Fatalf("over time from kept parts:\nread %+v\nkept %+v", fresh.ValSer, again.ValSer)
	}
	// a part that measured nothing does not answer a question that measures
	s.fparts, s.fpartBytes = nil, 0
	s.Stats(context.Background(), over, 3600)
	if p := s.fieldGet(ldFieldKey(qs[0].Filter(), qs[0], true), now.Add(-2*time.Hour).Unix()/3600, s.meta[now.Add(-2*time.Hour).Unix()/3600].Count, "distinct", "dst_port"); p != nil {
		t.Fatal("a count-only part answered how many different")
	}
	if r := run(8, qs[0]); r.FieldAgg["10.0.0.0"] == nil || r.FieldAgg["10.0.0.0"].HLL == "" || r.FieldAgg["10.0.0.3"].Vals == nil || len(r.ValSer["10.0.0.3"]) < 9 {
		t.Fatalf("estimate, exact set and series expected: %+v", r.FieldAgg["10.0.0.0"])
	}
}
