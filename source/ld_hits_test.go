package main

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func statsKey(a LdStatsAnswer) LdStatsAnswer {
	a.Scanned, a.Partial = 0, false
	return a
}

// what is kept for a filter with words gives the same messages and counts as reading the hours again
func TestHitsSameAsReading(t *testing.T) {
	s, now := wordStore(t, t.TempDir(), 30, 300)
	names := map[string]string{"10.1.1.2": "edge tokzaxbq", "10.1.1.3": "core"}
	type tc struct {
		q     LdQuery
		names bool
	}
	var cases []tc
	for _, from := range []time.Time{now.Add(-31 * time.Hour), now.Add(-17*time.Hour - 20*time.Minute)} {
		for _, w := range []string{"ospf neighbor", "accept", "not accept", hourWord(9), hourWord(9) + " or " + hourWord(20), "dstport=3389", "action=deny or dead",
			"192.168.1", "fw1 fortigate", "edge", "core and accept", "not core"} {
			for _, oldest := range []bool{false, true} {
				q := LdQuery{From: from.UnixMilli(), MinSev: -1, Q: w, Limit: 200, Oldest: oldest}
				cases = append(cases, tc{q, false}, tc{q, true})
				q2 := q
				q2.Host = "fw2"
				cases = append(cases, tc{q2, false})
				q3 := q
				q3.MinSev = 6
				cases = append(cases, tc{q3, true})
			}
		}
	}
	// every filter read afresh, for its start and for one 7 minutes later (the page's range moves on between refreshes)
	type want struct {
		msgs string
		st   LdStatsAnswer
	}
	at := func(q LdQuery, shift bool) LdQuery {
		if shift {
			q.From += 7 * 60 * 1000
		}
		return q
	}
	fresh := map[int][2]want{}
	for i, c := range cases {
		q := c.q
		if c.names {
			q.srcWords = names
		}
		var w [2]want
		for j, shift := range []bool{false, true} {
			noHits(s)
			w[j] = want{msgKeys(s.Search(context.Background(), at(q, shift))), statsKey(s.Stats(context.Background(), at(q, shift), 3600))}
		}
		fresh[i] = w
	}
	// then all of them again with everything kept from all the others
	noHits(s)
	for round := 0; round < 3; round++ {
		for i, c := range cases {
			q := c.q
			if c.names {
				q.srcWords = names
			}
			for j, shift := range []bool{false, true} {
				if round == 0 && shift {
					continue // kept first for the earlier start, then asked for the later one
				}
				w := fresh[i][j]
				if got := msgKeys(s.Search(context.Background(), at(q, shift))); got != w.msgs {
					t.Errorf("%q oldest=%v host=%q sev=%d names=%v shift=%v round %d: search differs", q.Q, q.Oldest, q.Host, q.MinSev, c.names, shift, round)
				}
				if got := statsKey(s.Stats(context.Background(), at(q, shift), 3600)); !reflect.DeepEqual(got, w.st) {
					t.Errorf("%q host=%q sev=%d names=%v shift=%v round %d: stats differ: %d vs %d", q.Q, q.Host, q.MinSev, c.names, shift, round, got.Total, w.st.Total)
				}
			}
		}
	}
	if len(s.hits) < 50 {
		t.Errorf("only %d kept", len(s.hits))
	}
	// different sender names: other entries, not the ones kept for the old names
	q := LdQuery{From: now.Add(-31 * time.Hour).UnixMilli(), MinSev: -1, Q: "edge", Limit: 200}
	q.srcWords = names
	withEdge := s.Stats(context.Background(), q, 3600).Total
	q.srcWords = map[string]string{"10.1.1.2": "plain"}
	if n := s.Stats(context.Background(), q, 3600).Total; n == withEdge || withEdge == 0 {
		t.Errorf("renaming the sender: %d then %d", withEdge, n)
	}
	// an hour whose count changed is read again
	f := (&LdQuery{From: q.From, MinSev: -1, Q: "accept"}).filter()
	var h int64
	s.mu.Lock()
	for x := range s.meta {
		if s.closedHour(x) && (h == 0 || x < h) {
			h = x
		}
	}
	count := s.meta[h].Count
	s.mu.Unlock()
	s.Stats(context.Background(), LdQuery{From: q.From, MinSev: -1, Q: "accept"}, 3600)
	if s.hitsGet(f, h, true, count) == nil {
		t.Fatal("nothing kept for the oldest hour")
	}
	if s.hitsGet(f, h, true, count+1) != nil {
		t.Error("used with another count")
	}
	s.hitsDrop(h)
	if s.hitsGet(f, h, true, count) != nil {
		t.Error("kept after the hour was dropped")
	}
}

// the memory kept stays under its cap
func TestHitsCap(t *testing.T) {
	s := newLdStore(t.TempDir(), "n1")
	big := make([]LdMsg, 900)
	for i := range big {
		big[i].Msg = string(make([]byte, 6000))
	}
	for i := 0; i < 40; i++ {
		f := (&LdQuery{MinSev: -1, Q: hourWord(i)}).filter()
		s.hitsPut(f, int64(i), false, &ldHits{count: 1, msgs: big})
		if s.hitBytes > ldHitsBytes {
			t.Fatalf("%d bytes kept", s.hitBytes)
		}
	}
	if len(s.hits) == 0 || len(s.hits) >= 40 {
		t.Errorf("%d entries kept", len(s.hits))
	}
}
