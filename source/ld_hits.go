package main

// What a filter with words found in a closed hour, kept so that the Logs page's refresh (every 15 s, with the same filter) reads
// only the hours still being written.  A closed hour does not change, so its matches for a filter are the same next time; the
// counts and the chart need them per (host, program, sender, severity) only, and the message list needs the matches themselves
// when there are few (with many, the newest 1000 are found in the newest hour or two anyway).  An entry is used only while the
// hour's count in the index is the one it was made from.  The key is everything the filter matches on (ldFilter.key), sender
// names included, so a change of names or of what a word means gives other entries.  Filters without words have the rollups
// (ld_rollup.go) instead.

import (
	"fmt"
	"sort"
	"strings"
)

const (
	ldHitsMsgs  = 1000     // an hour's matches are kept for the message list when there are at most this many
	ldHitsBytes = 64 << 20 // about the most kept; when it is reached everything kept is let go
)

type ldHitKey struct {
	f     string
	h     int64
	stats bool
}

type ldHits struct {
	count uint64     // the hour's count in the index when this was made
	msgs  []LdMsg    // message list: every match of the hour, in file order (len == cap: never appended to)
	sum   *ldHourSum // counts: the matches by row
}

// key is a string that differs whenever the filter could match differently.
func (f *ldFilter) key() string {
	if f.hkey != "" {
		return f.hkey
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q\x00%q\x00%q\x00%d\x00%v\x00%v\x00%q\x00", f.expr, f.host, f.prog, f.minSev, f.sevExact, f.sevLess, f.src)
	set := func(m map[string]bool) {
		if m == nil {
			b.WriteString("-\x00")
			return
		}
		keys := make([]string, 0, len(m))
		for k, v := range m {
			if v {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "%q\x00", keys)
	}
	set(f.srcSet)
	set(f.hostSrc)
	keys := make([]string, 0, len(f.srcWords))
	for k := range f.srcWords {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%q=%q,", k, f.srcWords[k])
	}
	f.hkey = b.String()
	return f.hkey
}

// hitsUseful: is it worth keeping what this filter finds per hour (it has words; without, nothing is read that a rollup or the
// index cannot answer cheaply)?
func (f *ldFilter) hitsUseful() bool { return len(f.expr) > 0 }

func (s *ldStore) hitsGet(f *ldFilter, h int64, stats bool, count uint64) *ldHits {
	s.hitMu.Lock()
	defer s.hitMu.Unlock()
	x := s.hits[ldHitKey{f.key(), h, stats}]
	if x == nil || x.count != count {
		return nil
	}
	return x
}

func (s *ldStore) hitsPut(f *ldFilter, h int64, stats bool, x *ldHits) {
	size := 128 + len(f.key())
	for i := range x.msgs {
		m := &x.msgs[i]
		size += 160 + len(m.Msg) + len(m.SD) + len(m.Raw) + len(m.PID) + len(m.MID)
	}
	if x.sum != nil {
		size += 40 * len(x.sum.rows)
		for _, n := range x.sum.names {
			size += 16 + len(n)
		}
	}
	if size > ldHitsBytes/8 {
		return
	}
	s.hitMu.Lock()
	defer s.hitMu.Unlock()
	if s.hitBytes+size > ldHitsBytes {
		s.hits, s.hitBytes = map[ldHitKey]*ldHits{}, 0
	}
	k := ldHitKey{f.key(), h, stats}
	if old := s.hits[k]; old != nil {
		return // made at the same time by another search: keep the first
	}
	s.hits[k] = x
	s.hitBytes += size
}

// hitsDrop lets go of everything kept for an hour (it is being removed).
func (s *ldStore) hitsDrop(h int64) {
	s.hitMu.Lock()
	defer s.hitMu.Unlock()
	for k := range s.hits {
		if k.h == h {
			delete(s.hits, k)
		}
	}
}

// ldSumMaker counts messages by row into an ldHourSum (nil from done when there were more than ldSumMax rows).
type ldSumMaker struct {
	idx  map[[3]string]map[int8]int
	sum  ldHourSum
	name map[string]uint32
	full bool
}

func (b *ldSumMaker) add(m *LdMsg) {
	if b.full {
		return
	}
	if b.idx == nil {
		b.idx, b.name = map[[3]string]map[int8]int{}, map[string]uint32{}
	}
	k := [3]string{m.Host, m.Prog, m.Src}
	bySev := b.idx[k]
	if bySev == nil {
		bySev = map[int8]int{}
		b.idx[k] = bySev
	}
	sev := m.Sev & 7
	if i, ok := bySev[sev]; ok {
		b.sum.rows[i].n++
		return
	}
	if len(b.sum.rows) >= ldSumMax {
		b.full = true
		return
	}
	name := func(n string) uint32 {
		if i, ok := b.name[n]; ok {
			return i
		}
		i := uint32(len(b.sum.names))
		b.name[n] = i
		b.sum.names = append(b.sum.names, n)
		return i
	}
	bySev[sev] = len(b.sum.rows)
	b.sum.rows = append(b.sum.rows, ldSumRow{host: name(m.Host), prog: name(m.Prog), src: name(m.Src), sev: sev, n: 1})
}

func (b *ldSumMaker) done(count uint64) *ldHourSum {
	if b.full {
		return nil
	}
	b.sum.count = count
	return &b.sum
}
