package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

// rollupStore holds hours hours of messages, the closed hours compressed as Maintain leaves them.
func rollupStore(t testing.TB, dir string, hours, perHour int) (*ldStore, time.Time) {
	now := ldNow
	s := newLdStore(dir, "n1")
	s.now = func() time.Time { return now }
	s.Load()
	base := now.Add(-time.Duration(hours) * time.Hour)
	for h := 0; h <= hours; h++ {
		for i := 0; i < perHour; i++ {
			at := base.Add(time.Duration(h)*time.Hour + time.Duration(i)*time.Hour/time.Duration(perHour))
			if at.After(now) {
				break
			}
			s.Add(LdMsg{T: at.UnixMilli(), Host: fmt.Sprintf("fw%d", i%5), Prog: []string{"sshd", "kernel", ""}[i%3], Sev: int8(i % 8), Fac: 1,
				Src: fmt.Sprintf("10.1.1.%d", i%7), Msg: fmt.Sprintf("message %d port %d", i, i%60), Via: "udp"})
		}
	}
	s.Flush()
	s.Maintain()
	return s, now
}

// scanStats is Stats with the rollups out of the way: every hour read message by message.
func scanStats(s *ldStore, q LdQuery, step int64) LdStatsAnswer {
	saved := s.sums
	s.sumMu.Lock()
	s.sums = map[int64]ldSumEntry{}
	s.sumMu.Unlock()
	q2 := q
	q2.Q = "not zzzznever"
	r := s.Stats(context.Background(), q2, step)
	s.sumMu.Lock()
	s.sums = saved
	s.sumMu.Unlock()
	return r
}

func TestRollupStatsMatchScan(t *testing.T) {
	s, now := rollupStore(t, t.TempDir(), 30, 300)
	from := now.Add(-26*time.Hour - 17*time.Minute).UnixMilli() // a partial first hour, and the open hour at the end
	cases := []LdQuery{
		{From: from, MinSev: -1},
		{From: from, MinSev: 3},
		{From: from, MinSev: 4, SevExact: true},
		{From: from, MinSev: -1, Host: "fw2"},
		{From: from, MinSev: -1, Host: "fw*"},
		{From: from, MinSev: -1, Prog: "sshd"},
		{From: from, MinSev: -1, Src: "10.1.1.3"},
		{From: from, MinSev: -1, srcSet: map[string]bool{"10.1.1.1": true, "10.1.1.4": true}},
		{From: from, MinSev: -1, Host: "nosuch", hostSrc: map[string]bool{"10.1.1.2": true}},
	}
	for _, step := range []int64{0, 3600, 10800} {
		for i, q := range cases {
			got := s.Stats(context.Background(), q, step)
			want := scanStats(s, q, step)
			got.Scanned, want.Scanned = 0, 0
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("step %d case %d: rollup %+v\nscan %+v", step, i, got, want)
			}
			if got.Total == 0 && i < 3 {
				t.Fatalf("step %d case %d: nothing counted", step, i)
			}
		}
	}
	// the rollups were written beside the hours, and a new store reads them instead of the hours
	h := now.Add(-5*time.Hour).Unix() / 3600
	if _, err := os.Stat(s.sumPath(h)); err != nil {
		t.Fatalf("no rollup file: %v", err)
	}
	s2 := newLdStore(s.dir, "n1")
	s2.now = s.now
	s2.Load()
	sum, n, full, ok := s2.loadSum(h)
	if !ok || full || sum == nil || n != s2.meta[h].Count {
		t.Fatalf("rollup file: ok %v full %v count %d want %d", ok, full, n, s2.meta[h].Count)
	}
	got := s2.Stats(context.Background(), cases[0], 3600)
	want := scanStats(s2, cases[0], 3600)
	got.Scanned, want.Scanned = 0, 0
	if !reflect.DeepEqual(got, want) {
		t.Fatal("a restarted store counts differently")
	}
}

// A rollup whose count is not the hour's is not used: the hour is read and the rollup made again.
func TestRollupStaleIsRebuilt(t *testing.T) {
	s, now := rollupStore(t, t.TempDir(), 4, 100)
	h := now.Add(-2*time.Hour).Unix() / 3600
	s.hourSum(h, s.meta[h].Count)
	s.saveSum(h, &ldHourSum{count: 1, names: []string{"x"}, rows: []ldSumRow{{sev: 1, n: 1}}}, 1, false)
	s.dropSum(h)
	q := LdQuery{From: h * 3600 * 1000, To: (h+1)*3600*1000 - 1, MinSev: -1}
	if r := s.Stats(context.Background(), q, 3600); r.Total != 100 {
		t.Fatalf("total %d, want 100", r.Total)
	}
	if _, n, _, _ := s.loadSum(h); n != 100 {
		t.Fatalf("rollup file not made again: count %d", n)
	}
}

// Words in the question: the rollups cannot answer, the hours are read.
func TestRollupNotUsedForWords(t *testing.T) {
	s, now := rollupStore(t, t.TempDir(), 4, 100)
	q := LdQuery{From: now.Add(-4 * time.Hour).UnixMilli(), MinSev: -1, Q: "port 7"}
	r := s.Stats(context.Background(), q, 3600)
	var want uint64
	for h := q.From / 3600000; h <= now.Unix()/3600; h++ {
		s.scanFile(h, func(m *LdMsg) bool {
			if m.T >= q.From && q.filter().match(m) {
				want++
			}
			return true
		})
	}
	if r.Total != want || want == 0 {
		t.Fatalf("total %d, want %d", r.Total, want)
	}
}

// Too many distinct rows: the hour gets no rollup, is marked as such, and is counted from its messages.
func TestRollupFull(t *testing.T) {
	now := ldNow
	s := newLdStore(t.TempDir(), "n1")
	s.now = func() time.Time { return now }
	s.Load()
	h := now.Add(-2*time.Hour).Unix() / 3600
	for i := 0; i < ldSumMax+10; i++ {
		s.Add(LdMsg{T: h*3600*1000 + int64(i), Host: fmt.Sprintf("h%d", i), Src: "10.0.0.1", Msg: "x"})
	}
	s.Add(LdMsg{T: now.UnixMilli(), Host: "now", Src: "10.0.0.1", Msg: "x"}) // the open hour moves on
	s.Flush()
	s.Maintain()
	if _, _, full, ok := s.loadSum(h); !ok || !full {
		t.Fatalf("want a full marker, ok %v full %v", ok, full)
	}
	r := s.Stats(context.Background(), LdQuery{From: h * 3600 * 1000, To: (h+1)*3600*1000 - 1, MinSev: -1}, 3600)
	if r.Total != uint64(ldSumMax+10) {
		t.Fatalf("total %d", r.Total)
	}
}

func TestRawSev(t *testing.T) {
	for _, m := range []LdMsg{{Sev: 3, Msg: "x"}, {Sev: 0, Host: `a,"s":7`, Prog: `p,"s":6`, PID: `,"s":5`, Msg: `,"s":4`}, {Sev: 7, Msg: ""}} {
		b, _ := json.Marshal(&m)
		if got := ldRawSev(b); got != int(m.Sev) {
			t.Errorf("%s: severity %d, want %d", b, got, m.Sev)
		}
	}
	for _, line := range []string{`{"t":1}`, `{"t":1,"s":12}`, `{"t":1,"s":`, `{"t":1,"s":x}`} {
		if got := ldRawSev([]byte(line)); got != -1 {
			t.Errorf("%s: severity %d, want -1", line, got)
		}
	}
}

// A severity search reads lines it can rule out without parsing them: it must find what reading every message finds.
func TestSeveritySearchSkipsLines(t *testing.T) {
	s, now := rollupStore(t, t.TempDir(), 30, 300)
	s.cache.set(0, 1) // nothing fits: every closed hour is read from its file
	for _, q := range []LdQuery{{MinSev: 3}, {MinSev: 4, SevExact: true}, {MinSev: 0}, {MinSev: 6, Host: "fw1"}} {
		q.From, q.Limit = now.Add(-26*time.Hour).UnixMilli(), 5000
		got := s.Search(context.Background(), q)
		var want []LdMsg
		f := q.filter()
		for h := q.From / 3600000; h <= now.Unix()/3600; h++ {
			s.streamFile(h, func(m *LdMsg) bool {
				if m.T >= q.From && f.match(m) {
					want = append(want, *m)
				}
				return true
			})
		}
		if len(want) == 0 || !reflect.DeepEqual(got.Msgs, want) {
			t.Fatalf("%+v: %d messages, want %d", q, len(got.Msgs), len(want))
		}
		st := s.Stats(context.Background(), LdQuery{From: q.From, MinSev: q.MinSev, SevExact: q.SevExact, Host: q.Host, Q: "not zzzznever"}, 600)
		if st.Total != uint64(len(want)) {
			t.Fatalf("%+v: stats %d, want %d", q, st.Total, len(want))
		}
	}
}

func TestSeriesStep(t *testing.T) {
	for _, c := range []struct {
		span time.Duration
		want int64
	}{{15 * time.Minute, 60}, {time.Hour, 60}, {6 * time.Hour, 360}, {24 * time.Hour, 1440}, {7 * 24 * time.Hour, 10800}, {30 * 24 * time.Hour, 43200}, {365 * 24 * time.Hour, 7 * 86400}} {
		if got := ldSeriesStep(c.span.Milliseconds()); got != c.want {
			t.Errorf("%v: step %d, want %d", c.span, got, c.want)
		}
	}
}

// BenchmarkStats30d is the Logs page's chart over 30 days of 20000 messages an hour (14.4 million).
func BenchmarkStats30d(b *testing.B) {
	if testing.Short() {
		b.Skip("slow to set up")
	}
	s, now := rollupStore(b, b.TempDir(), 30*24, 20000)
	s.cache.set(0, 64) // a small cache: 30 days do not fit in memory, as on a real node
	q := LdQuery{From: now.Add(-30 * 24 * time.Hour).UnixMilli(), MinSev: -1}
	step := ldSeriesStep(30 * 24 * 3600 * 1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Stats(context.Background(), q, step)
	}
}

// errStore: 30 days of 20000 messages an hour, errors rare (5 an hour), warnings 1%, and a 64 MB cache.
func errStore(b *testing.B) (*ldStore, time.Time) {
	now := ldNow
	s := newLdStore(b.TempDir(), "n1")
	s.now = func() time.Time { return now }
	s.Load()
	hours, per := 30*24, 20000
	base := now.Add(-time.Duration(hours) * time.Hour)
	for h := 0; h <= hours; h++ {
		for i := 0; i < per; i++ {
			at := base.Add(time.Duration(h)*time.Hour + time.Duration(i)*time.Hour/time.Duration(per))
			if at.After(now) {
				break
			}
			sev := int8(6)
			if i%4000 == 7 {
				sev = 3
			} else if i%100 == 5 {
				sev = 4
			}
			s.Add(LdMsg{T: at.UnixMilli(), Host: fmt.Sprintf("fw%d", i%4), Prog: "fortigate", Sev: sev, Fac: 1, Src: fmt.Sprintf("10.1.1.%d", i%4),
				Msg: fmt.Sprintf(`date=2026-11-21 time=20:30:56 devname="fw%d" logid="00000000%02d" type="traffic" subtype="forward" level="notice" srcip=192.168.%d.%d srcport=%d dstip=151.101.%d.%d dstport=443 action="ip-conn" policyid=1 sessionid=%d`, i%4, i%50, i%200, i%250, 1024+i%60000, i%200, i%250, 17000000+i), Via: "udp"})
		}
	}
	s.Flush()
	s.Maintain()
	s.cache.set(0, 64)
	return s, now
}

// BenchmarkSearchSeverity30d is the Errors and Warnings tiles of the Logs page (the newest 1000) over 1, 7 and 30 days.
func BenchmarkSearchSeverity30d(b *testing.B) {
	if testing.Short() {
		b.Skip("slow to set up")
	}
	s, now := errStore(b)
	for _, c := range []struct {
		name string
		sev  int
		ex   bool
		span time.Duration
	}{{"err1d", 3, false, 24 * time.Hour}, {"err7d", 3, false, 7 * 24 * time.Hour}, {"err30d", 3, false, 30 * 24 * time.Hour}, {"warn30d", 4, true, 30 * 24 * time.Hour}} {
		b.Run(c.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				a := s.Search(context.Background(), LdQuery{From: now.Add(-c.span).UnixMilli(), MinSev: c.sev, SevExact: c.ex, Limit: 1000})
				if i == 0 {
					b.Logf("%d msgs scanned %d", len(a.Msgs), a.Scanned)
				}
			}
		})
	}
}
