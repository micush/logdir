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
	for k, x := range s.fparts {
		if k.h == h {
			s.fpartBytes -= x.size
			delete(s.fparts, k)
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

// ── the values of a field, per closed hour ───────────────────────────────────────────────────────────────────────────────────
// A question about a field's values (the Logs page's Values card: count, how many different, sum, over time) reads every message
// of the range, so each whole closed hour's part of the answer is kept, under its own budget, and the page's refresh reads only the
// hour being written.  A part is kept unsealed (exact sets and sketches), so parts add up exactly as reading the hours would.  When
// the budget is reached the oldest hours go first.

const ldFieldPartsMin = 64 << 20 // the budget is a tenth of the hour cache's cap, at least this

type ldFieldPart struct {
	count   uint64 // the hour's count in the index when this was made
	measure string // what was measured besides the count ("": nothing), of which field
	mfield  string
	part    *LdStatsAnswer
	size    int
}

type ldFieldPartKey struct {
	k string
	h int64
}

// ldFieldKey is what the part of an hour depends on besides the hour and what is measured: the filter, the field, and whether
// there are series at all.  A part made with a measure (how many different, a sum) also serves the same question without one,
// and the values' own series (Over time) come from its counts per value, so the Values card's chart reads nothing again.
func ldFieldKey(f *ldFilter, q LdQuery, series bool) string {
	return fmt.Sprintf("%s\x01%q\x00%v", f.key(), q.Field, series)
}

// ldPartSize estimates what a part holds.
func ldPartSize(p *LdStatsAnswer) int {
	size := 512
	for k := range p.ByField {
		size += 64 + len(k)
	}
	for _, a := range p.FieldAgg {
		size += 64
		if a.reg != nil {
			size += len(a.reg)
		}
		for v := range a.set {
			size += 32 + len(v)
		}
	}
	for k := range p.ByHost {
		size += 48 + len(k)
	}
	for k := range p.ByProg {
		size += 48 + len(k)
	}
	for k := range p.BySrc {
		size += 48 + len(k)
	}
	for _, ser := range p.SrcSer {
		size += 64 + 32*len(ser)
	}
	for _, ser := range p.ValSer {
		size += 64 + 32*len(ser)
	}
	return size
}

func (s *ldStore) fieldBudget() int {
	limit, _ := s.cache.stat2()
	return max(ldFieldPartsMin, int(limit/10))
}

// fieldGet is the kept part of hour h for a question measuring measure of mfield ("": only counting).
func (s *ldStore) fieldGet(k string, h int64, count uint64, measure, mfield string) *LdStatsAnswer {
	s.hitMu.Lock()
	defer s.hitMu.Unlock()
	x := s.fparts[ldFieldPartKey{k, h}]
	if x == nil || x.count != count || (measure != "" && (x.measure != measure || x.mfield != mfield)) {
		return nil
	}
	return x.part
}

// fieldPut keeps the part of hour h; it takes the place of one that measured something else.
func (s *ldStore) fieldPut(k string, h int64, count uint64, measure, mfield string, p *LdStatsAnswer) {
	size := ldPartSize(p) + len(k)
	budget := s.fieldBudget()
	if size > budget/8 {
		return
	}
	s.hitMu.Lock()
	defer s.hitMu.Unlock()
	if s.fparts == nil {
		s.fparts = map[ldFieldPartKey]*ldFieldPart{}
	}
	key := ldFieldPartKey{k, h}
	if old := s.fparts[key]; old != nil {
		if old.count == count && (old.measure == measure && old.mfield == mfield || measure == "") {
			return // made at the same time by another question, or one that measured more: keep that
		}
		s.fpartBytes -= old.size
		delete(s.fparts, key)
	}
	if s.fpartBytes+size > budget { // the oldest hours go first
		keys := make([]ldFieldPartKey, 0, len(s.fparts))
		for x := range s.fparts {
			keys = append(keys, x)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].h < keys[j].h })
		for _, x := range keys {
			if s.fpartBytes+size <= budget || x.h >= h {
				break
			}
			s.fpartBytes -= s.fparts[x].size
			delete(s.fparts, x)
		}
		if s.fpartBytes+size > budget {
			return
		}
	}
	s.fparts[key] = &ldFieldPart{count: count, measure: measure, mfield: mfield, part: p, size: size}
	s.fpartBytes += size
}
