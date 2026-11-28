package logs

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"time"

	"logdir/internal/core"
)

// Hour rollups: for each closed hour, how many messages it holds of each (severity, host, program, sender).  The Logs page's
// counts and chart over a long range (7d, 30d, all) need nothing else when no words are typed, so they are added up from these
// instead of reading, decompressing and parsing every message of every hour again.  A rollup is a small file beside the hour
// (`2026112005.jsonl.lz.sum`), made when the hour is compressed (and, for hours from before this release, by a pass after start),
// and kept in memory once read.  A rollup is used only while the number of messages it counts is the hour's count in the index,
// so an hour that changed after it was made is read again (and its rollup made again).  An hour with more distinct rows than
// ldSumMax has no rollup and is read as before.

const ldSumMax = 20000

type ldSumRow struct {
	host, prog, src uint32 // index into names
	sev             int8
	n               uint64
}

type ldHourSum struct {
	count uint64
	names []string
	rows  []ldSumRow
}

// the file form: {"count":n,"names":[...],"rows":[[host,prog,src,sev,n],...]}; "full": too many rows, the hour has no rollup
type ldSumFile struct {
	Count uint64      `json:"count"`
	Full  bool        `json:"full,omitempty"`
	Names []string    `json:"names,omitempty"`
	Rows  [][5]uint64 `json:"rows,omitempty"`
}

func (s *LdStore) sumPath(h int64) string { return s.path(h, true) + ".sum" }

// buildSum counts an hour's messages by row: nil when the hour has no file (ok false) or too many rows (full true).  total is the
// hour's messages either way.
func (s *LdStore) buildSum(h int64) (sum *ldHourSum, total uint64, full, ok bool) {
	type key struct {
		host, prog, src string
		sev             int8
	}
	counts := map[key]uint64{}
	each := func(m *core.LdMsg) bool {
		total++
		if !full {
			counts[key{m.Host, m.Prog, m.Src, m.Sev & 7}]++
			if len(counts) > ldSumMax {
				full, counts = true, nil
			}
		}
		return true
	}
	if msgs, ok := s.cache.get(h); ok { // already parsed: no need to read the file
		for i := range msgs {
			each(&msgs[i])
		}
	} else if !s.streamFile(h, each) { // straight from the file: a pass over many hours must not push the newest out of the cache
		return nil, 0, false, false
	}
	if full {
		return nil, total, true, true
	}
	sum = &ldHourSum{count: total, rows: make([]ldSumRow, 0, len(counts))}
	idx := map[string]uint32{}
	name := func(n string) uint32 {
		if i, ok := idx[n]; ok {
			return i
		}
		i := uint32(len(sum.names))
		idx[n] = i
		sum.names = append(sum.names, n)
		return i
	}
	for k, n := range counts {
		sum.rows = append(sum.rows, ldSumRow{host: name(k.host), prog: name(k.prog), src: name(k.src), sev: k.sev, n: n})
	}
	return sum, total, false, true
}

func (s *LdStore) saveSum(h int64, sum *ldHourSum, count uint64, full bool) {
	f := ldSumFile{Count: count, Full: full}
	if sum != nil {
		f.Names = sum.names
		f.Rows = make([][5]uint64, len(sum.rows))
		for i, r := range sum.rows {
			f.Rows[i] = [5]uint64{uint64(r.host), uint64(r.prog), uint64(r.src), uint64(r.sev), r.n}
		}
	}
	b, err := json.Marshal(&f)
	if err != nil {
		return
	}
	tmp := s.sumPath(h) + ".tmp"
	if os.WriteFile(tmp, b, 0o640) == nil {
		if os.Rename(tmp, s.sumPath(h)) != nil {
			os.Remove(tmp)
		}
	}
}

// loadSum reads an hour's rollup file: the rollup (nil when the hour is full), the count it was made from, and ok when there is
// a readable file.
func (s *LdStore) loadSum(h int64) (sum *ldHourSum, count uint64, full, ok bool) {
	b, err := os.ReadFile(s.sumPath(h))
	if err != nil {
		return nil, 0, false, false
	}
	var f ldSumFile
	if json.Unmarshal(b, &f) != nil {
		return nil, 0, false, false
	}
	if f.Full {
		return nil, f.Count, true, true
	}
	sum = &ldHourSum{count: f.Count, names: f.Names, rows: make([]ldSumRow, 0, len(f.Rows))}
	n := uint64(len(f.Names))
	for _, r := range f.Rows {
		if r[0] >= n || r[1] >= n || r[2] >= n || r[3] > 7 {
			return nil, 0, false, false
		}
		sum.rows = append(sum.rows, ldSumRow{host: uint32(r[0]), prog: uint32(r[1]), src: uint32(r[2]), sev: int8(r[3]), n: r[4]})
	}
	return sum, f.Count, false, true
}

// hourSum is the rollup of a closed hour that holds count messages, from memory, its file, or made now; nil when the hour has none
// (too many rows, or it no longer has count messages).
func (s *LdStore) hourSum(h int64, count uint64) *ldHourSum {
	s.sumMu.Lock()
	if x, ok := s.sums[h]; ok && x.count == count {
		s.sumMu.Unlock()
		return x.sum
	}
	s.sumMu.Unlock()
	// one maker at a time: two pages asking for the same 30 days read each hour once, not twice
	s.sumMake.Lock()
	defer s.sumMake.Unlock()
	s.sumMu.Lock()
	if x, ok := s.sums[h]; ok && x.count == count {
		s.sumMu.Unlock()
		return x.sum
	}
	s.sumMu.Unlock()
	sum, n, full, ok := s.loadSum(h)
	if !ok || n != count {
		if sum, n, full, ok = s.buildSum(h); !ok {
			return nil
		}
		if n != count { // the hour is not what the index says (still being written?): use nothing, keep nothing
			return nil
		}
		s.saveSum(h, sum, n, full)
	}
	s.sumMu.Lock()
	s.sums[h] = ldSumEntry{count: n, sum: sum}
	s.sumMu.Unlock()
	return sum
}

func (s *LdStore) dropSum(h int64) {
	s.sumMu.Lock()
	delete(s.sums, h)
	s.sumMu.Unlock()
}

// MakeSums makes the rollups the closed hours are missing (the newest first), after a start; it gives way to searches by doing one
// hour at a time.
func (s *LdStore) MakeSums(ctx context.Context) {
	s.mu.Lock()
	type hc struct {
		h int64
		n uint64
	}
	var hs []hc
	for h, m := range s.meta {
		if s.closedHour(h) && m.Count > 0 {
			hs = append(hs, hc{h, m.Count})
		}
	}
	s.mu.Unlock()
	sort.Slice(hs, func(i, j int) bool { return hs[i].h > hs[j].h })
	for _, x := range hs {
		if ctx.Err() != nil {
			return
		}
		s.hourSum(x.h, x.n)
		time.Sleep(time.Millisecond)
	}
}

type ldSumEntry struct {
	count uint64
	sum   *ldHourSum // nil: the hour has too many rows for a rollup
}

// sumHourOK says whether a stats question may take hour h from its rollup: no words to look for (a rollup has no text), a series
// whose buckets hold whole hours, and an hour that is over and wholly inside the range.
func sumHourOK(f *core.LdFilter, step int64, h, from, to int64) bool {
	return len(f.Expr) == 0 && (step == 0 || step%3600 == 0) && h*3600*1000 >= from && (h+1)*3600*1000-1 <= to
}

// liveAdd counts message m of hour h into the newest hour's rollup; s.mu is held.  A message of an older
// hour (late, or with its own time) leaves it alone: that hour is no longer the newest, and its rollup is made when it is compressed.
func (s *LdStore) liveAdd(h int64, m *core.LdMsg) {
	if h < s.liveHour {
		return
	}
	if h > s.liveHour {
		s.live, s.liveHour, s.liveN = ldSumMaker{}, h, 0
	}
	s.live.add(m)
	s.liveN++
}

// liveSnapshot is a copy of the newest hour's rollup and its hour, or nil when it does not count every message of that hour (the
// daemon started during the hour, or it has too many rows); s.mu is held.
func (s *LdStore) liveSnapshot() (int64, *ldHourSum) {
	hm := s.meta[s.liveHour]
	if hm == nil || s.live.full || s.liveN == 0 || s.liveN != hm.Count {
		return -1, nil
	}
	return s.liveHour, &ldHourSum{count: s.liveN, names: append([]string(nil), s.live.sum.names...), rows: append([]ldSumRow(nil), s.live.sum.rows...)}
}
