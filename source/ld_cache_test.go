package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestHourCacheServesClosedHoursUnderACap(t *testing.T) {
	now := ldNow
	s := newTestStore(t, &now)
	s.cache.total = func() uint64 { return 1 << 30 }
	for h := 4; h >= 0; h-- { // oldest first, as messages arrive
		for i := 29; i >= 0; i-- {
			addAt(s, now.Add(-time.Duration(h)*time.Hour-time.Duration(i)*time.Minute), "sw1", "kernel", 6, fmt.Sprintf("h%d i%d", h, i))
		}
	}
	q := LdQuery{From: now.Add(-6 * time.Hour).UnixMilli(), MinSev: -1, Limit: 1000}
	first := s.Search(context.Background(), q)
	st := s.cache.stat()
	if st.Hours < 3 || st.Misses == 0 {
		t.Fatalf("closed hours should have been cached on the first search: %+v", st)
	}
	hits := st.Hits
	again := s.Search(context.Background(), q)
	if len(again.Msgs) != len(first.Msgs) || len(first.Msgs) != 150 {
		t.Fatalf("answers differ: %d then %d", len(first.Msgs), len(again.Msgs))
	}
	for i := range first.Msgs {
		if first.Msgs[i].Msg != again.Msgs[i].Msg {
			t.Fatalf("message %d differs", i)
		}
	}
	if s.cache.stat().Hits <= hits {
		t.Errorf("the second search read the files again: %+v", s.cache.stat())
	}
	if _, ok := s.cache.get(s.curHour); ok {
		t.Errorf("the hour being written must not be cached")
	}
	// a cap too small for an hour: nothing is kept, answers stay right
	s.cache.mu.Lock()
	s.cache.hours, s.cache.bytes = map[int64]*ldCachedHour{}, 0
	s.cache.mu.Unlock()
	s.cache.set(0, 0)
	s.cache.total = func() uint64 { return 100 }
	small := s.Search(context.Background(), q)
	if len(small.Msgs) != 150 || s.cache.stat().Hours != 0 || s.cache.stat().Rejected == 0 {
		t.Errorf("with a tiny cap: %d messages, %+v", len(small.Msgs), s.cache.stat())
	}
	// room for about two hours: the newest are kept, the oldest go
	one := ldHourBytes(first.Msgs[:30])
	s.cache.total = func() uint64 { return (one*2 + one/2) * 100 / 90 }
	s.cache.set(0, 0)
	s.Search(context.Background(), q)
	st = s.cache.stat()
	if st.Hours < 1 || st.Hours > 3 || st.Bytes > st.Cap {
		t.Errorf("cap not held: %+v", st)
	}
	// an hour removed by the retention leaves the cache
	for h := range s.meta {
		s.cache.put(h, []LdMsg{{T: 1}})
	}
	s.cache.drop(s.curHour - 1)
	if _, ok := s.cache.get(s.curHour - 1); ok {
		t.Errorf("drop kept the hour")
	}
}

func TestHourCacheWarmReadsTheNewestHours(t *testing.T) {
	now := ldNow
	s := newTestStore(t, &now)
	s.cache.total = func() uint64 { return 1 << 30 }
	for h := 2; h >= 0; h-- {
		addAt(s, now.Add(-time.Duration(h)*time.Hour), "sw1", "kernel", 6, "x")
	}
	s.Warm()
	if st := s.cache.stat(); st.Hours != 2 {
		t.Errorf("warm cached %d hours, want the 2 closed ones", st.Hours)
	}
}
