package logs

import (
	"fmt"
	"sort"

	"logdir/internal/core"
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
	count uint64       // the hour's count in the index when this was made
	msgs  []core.LdMsg // message list: every match of the hour, in file order (len == cap: never appended to)
	sum   *ldHourSum   // counts: the matches by row
}

func (s *LdStore) hitsGet(f *core.LdFilter, h int64, stats bool, count uint64) *ldHits {
	s.hitMu.Lock()
	defer s.hitMu.Unlock()
	x := s.hits[ldHitKey{f.Key(), h, stats}]
	if x == nil || x.count != count {
		return nil
	}
	return x
}

func (s *LdStore) hitsPut(f *core.LdFilter, h int64, stats bool, x *ldHits) {
	size := 128 + len(f.Key())
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
	k := ldHitKey{f.Key(), h, stats}
	if old := s.hits[k]; old != nil {
		return // made at the same time by another search: keep the first
	}
	s.hits[k] = x
	s.hitBytes += size
}

// hitsDrop lets go of everything kept for an hour (it is being removed).
func (s *LdStore) hitsDrop(h int64) {
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

func (b *ldSumMaker) add(m *core.LdMsg) {
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
func ldFieldKey(f *core.LdFilter, q core.LdQuery, series bool) string {
	return fmt.Sprintf("%s\x01%q\x00%v", f.Key(), q.Field, series)
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

func (s *LdStore) fieldBudget() int {
	limit, _ := s.cache.stat2()
	return max(ldFieldPartsMin, int(limit/10))
}

// fieldGet is the kept part of hour h for a question measuring measure of mfield ("": only counting).
func (s *LdStore) fieldGet(k string, h int64, count uint64, measure, mfield string) *LdStatsAnswer {
	s.hitMu.Lock()
	defer s.hitMu.Unlock()
	x := s.fparts[ldFieldPartKey{k, h}]
	if x == nil || x.count != count || (measure != "" && (x.measure != measure || x.mfield != mfield)) {
		return nil
	}
	return x.part
}

// fieldPut keeps the part of hour h; it takes the place of one that measured something else.
func (s *LdStore) fieldPut(k string, h int64, count uint64, measure, mfield string, p *LdStatsAnswer) {
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
