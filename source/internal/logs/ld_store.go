package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"logdir/internal/core"
)

const ldRing = 4000

var ldStats struct {
	received, bytes, parsed, refused, dropped, writeErrs atomic.Uint64
	ruleDropped                                          atomic.Uint64 // by drop rules (ld_drop.go)
	udp, tcp, tls                                        atomic.Uint64
	traps                                                atomic.Uint64 // SNMP traps kept (ld_trap.go)
	trapUnread, trapCommunity                            atomic.Uint64 // not read (bad, SNMPv3, not a trap); refused for their community
}

type LdStore struct {
	mu     sync.Mutex
	dir    string
	dirErr string
	node   string

	cur     *os.File
	curW    *bufio.Writer
	curHour int64
	meta    map[int64]*core.LdHourMeta // by hour (unix seconds / 3600)
	ring    []core.LdMsg               // the newest, oldest first
	cache   *ldCache                   // closed hours, parsed, under a memory cap
	sumMu   sync.Mutex
	sums    map[int64]ldSumEntry // closed hours' rollups (ld_rollup.go), by hour
	sumMake sync.Mutex
	// word indexes (ld_wix.go)
	wixMu    sync.Mutex
	wixes    map[int64]ldWixEntry
	wixBytes int
	hitMu    sync.Mutex // what filters with words found in closed hours (ld_hits.go)
	hits     map[ldHitKey]*ldHits
	hitBytes int
	// the values of a field per closed hour (ld_hits.go: fieldPut), under hitMu
	fparts     map[ldFieldPartKey]*ldFieldPart
	fpartBytes int
	wixMake    sync.Mutex
	dirty      bool
	// the rollup of the newest hour, kept as messages are added (ld_rollup.go: liveSum): liveN is how many it counts, which is the
	// hour's whole count only when every message of the hour was added since the start
	live     ldSumMaker
	liveHour int64
	liveN    uint64

	capPct, retDays int
	dirBytes        uint64
	capBytes        uint64
	removed         uint64
	Now             func() time.Time `json:"-"`
	volSize         func(string) (uint64, error)
}

func NewLdStore(dir, node string) *LdStore {
	return &LdStore{cache: newLdCache(), sums: map[int64]ldSumEntry{}, wixes: map[int64]ldWixEntry{}, hits: map[ldHitKey]*ldHits{}, dir: dir, node: node, meta: map[int64]*core.LdHourMeta{}, Now: time.Now, volSize: ldVolumeSize}
}

func ldVolumeSize(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Blocks) * uint64(st.Bsize), nil
}

func ldHourName(h int64) string { return time.Unix(h*3600, 0).UTC().Format("2006010215") }

func (s *LdStore) path(h int64, gz bool) string {
	n := ldHourName(h) + ".jsonl"
	if gz {
		n += ".lz"
	}
	return filepath.Join(s.dir, n)
}

func parseHourName(n string) (int64, bool) {
	n = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(n, ".wix"), ".sum"), ".idx"), ".lz"), ".jsonl")
	t, err := time.ParseInLocation("2006010215", n, time.UTC)
	if err != nil || len(n) != 10 {
		return 0, false
	}
	return t.Unix() / 3600, true
}

// Load makes the folder, reads the index of every hour (building the ones that are missing) and the newest messages.
func (s *LdStore) Load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		s.dirErr = "no folder"
		return
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		s.dirErr = err.Error()
		return
	}
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		s.dirErr = err.Error()
		return
	}
	var hours []int64
	seen := map[int64]bool{}
	for _, e := range ents {
		if h, ok := parseHourName(e.Name()); ok && !strings.HasSuffix(e.Name(), ".idx") && !strings.HasSuffix(e.Name(), ".sum") && !strings.HasSuffix(e.Name(), ".wix") && !seen[h] {
			seen[h] = true
			hours = append(hours, h)
		}
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	for _, h := range hours {
		s.meta[h] = s.loadMetaLocked(h)
	}
	// the newest messages, for tail
	for i := len(hours) - 1; i >= 0 && len(s.ring) < ldRing; i-- {
		var msgs []core.LdMsg
		s.scanFile(hours[i], func(m *core.LdMsg) bool { msgs = append(msgs, *m); return true })
		s.ring = append(msgs, s.ring...)
	}
	if len(s.ring) > ldRing {
		s.ring = s.ring[len(s.ring)-ldRing:]
	}
	s.dirty = false
}

func (s *LdStore) loadMetaLocked(h int64) *core.LdHourMeta {
	if b, err := os.ReadFile(s.path(h, true) + ".idx"); err == nil {
		var m core.LdHourMeta
		if json.Unmarshal(b, &m) == nil {
			m.Hosts, m.Progs = map[string]struct{}{}, map[string]struct{}{}
			for _, x := range m.HostsL {
				m.Hosts[x] = struct{}{}
			}
			for _, x := range m.ProgsL {
				m.Progs[x] = struct{}{}
			}
			if m.Full {
				m.Hosts, m.Progs = nil, nil
			}
			return &m
		}
	}
	m := &core.LdHourMeta{}
	s.scanFile(h, func(x *core.LdMsg) bool { m.Add(x); return true })
	return m
}

func (s *LdStore) saveMeta(h int64, m *core.LdHourMeta) {
	c := *m
	c.HostsL, c.ProgsL = nil, nil
	for x := range m.Hosts {
		c.HostsL = append(c.HostsL, x)
	}
	for x := range m.Progs {
		c.ProgsL = append(c.ProgsL, x)
	}
	sort.Strings(c.HostsL)
	sort.Strings(c.ProgsL)
	b, _ := json.Marshal(&c)
	tmp := s.path(h, true) + ".idx.tmp"
	if os.WriteFile(tmp, b, 0o640) == nil {
		os.Rename(tmp, s.path(h, true)+".idx")
	}
}

// scanFile reads one hour's messages (the plain file, or the compressed one) in the order they were written; f returns false to stop.
// readHour parses a whole hour (nil when it has no file).
func (s *LdStore) readHour(h int64) []core.LdMsg { return s.readHourN(h, 0) }

// hourCount is how many messages the index says hour h holds (0: none or unknown); s.mu must not be held.
func (s *LdStore) hourCount(h int64) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if hm := s.meta[h]; hm != nil {
		return hm.Count
	}
	return 0
}

// readHourN is readHour of an hour the index says holds n messages (0: unknown).
func (s *LdStore) readHourN(h int64, n uint64) []core.LdMsg {
	var msgs []core.LdMsg
	if n > 0 && n < 1<<26 {
		msgs = make([]core.LdMsg, 0, n) // the size it will be: growing by doubling leaves up to half the array unused
	}
	in := map[string]string{} // the words that repeat in every message (host, program, address, node...) are kept once per hour
	same := func(p *string) {
		if len(*p) == 0 || len(*p) > 64 {
			return
		}
		if v, ok := in[*p]; ok {
			*p = v
		} else if len(in) < 4096 {
			in[*p] = *p
		}
	}
	if !s.streamFile(h, func(m *core.LdMsg) bool {
		same(&m.Host)
		same(&m.Prog)
		same(&m.Src)
		same(&m.Via)
		same(&m.Node)
		msgs = append(msgs, *m)
		return true
	}) {
		return nil
	}
	if cap(msgs) > len(msgs)+len(msgs)/8+64 { // the index was wrong: keep only what is used
		msgs = append([]core.LdMsg(nil), msgs...)
	}
	return msgs
}

// scanFile calls f for each message of an hour until it returns false.  A closed hour comes from the cache, and goes into it when
// it fits under the memory cap; the hour being written, and one that does not fit, is read from its file.
func (s *LdStore) scanFile(h int64, f func(*core.LdMsg) bool) { s.scanFileN(h, 0, f) }

// scanFileN is scanFile of an hour the index says holds n messages (0: unknown, as under s.mu): an hour the cache would not keep
// is read line by line instead of whole.
func (s *LdStore) scanFileN(h int64, n uint64, f func(*core.LdMsg) bool) {
	if s.closedHour(h) {
		msgs, ok := s.cache.get(h)
		if !ok {
			if n > 0 && !s.cache.admits(h, n) {
				s.streamFile(h, f)
				return
			}
			if msgs = s.readHourN(h, n); msgs == nil {
				return
			}
			s.cache.put(h, msgs) // when it does not fit it is used once and let go
		}
		for i := range msgs {
			if !f(&msgs[i]) {
				return
			}
		}
		return
	}
	s.streamFile(h, f)
}

// streamFile reads an hour's file message by message; false when there is no file.
func (s *LdStore) streamFile(h int64, f func(*core.LdMsg) bool) bool {
	return s.streamFileRaw(h, nil, f)
}

// scanHour is scanFile for a search or a count: an hour that is not in the cache, when the filter can rule lines out from their
// bytes (a severity, as for the Errors and Warnings tiles, or words: rawOK), is read straight from its file and only the lines that can match are parsed;
// it is not put in the cache then, so a long range does not push the newest hours out.
func (s *LdStore) scanHour(h int64, flt *core.LdFilter, f func(*core.LdMsg) bool) {
	if !flt.RawUseful() {
		s.scanFileN(h, s.hourCount(h), f)
		return
	}
	if s.closedHour(h) {
		if msgs, ok := s.cache.get(h); ok {
			for i := range msgs {
				if !f(&msgs[i]) {
					return
				}
			}
			return
		}
	}
	s.streamFileRaw(h, flt.RawOK, f)
}

// scanHourOnce is scanHour for a read that should not fill the cache (one of many hours read at once): a cached hour from the
// cache, any other line by line from its file.
func (s *LdStore) scanHourOnce(h int64, flt *core.LdFilter, f func(*core.LdMsg) bool) {
	if s.closedHour(h) {
		if msgs, ok := s.cache.get(h); ok {
			for i := range msgs {
				if !f(&msgs[i]) {
					return
				}
			}
			return
		}
	}
	var pre func([]byte) bool
	if flt.RawUseful() {
		pre = flt.RawOK
	}
	s.streamFileRaw(h, pre, f)
}

// streamFileRaw is streamFile with a test on each line's bytes first (nil: every line is parsed).  f's message is the same
// variable for every line: f must copy what it keeps.
func (s *LdStore) streamFileRaw(h int64, pre func([]byte) bool, f func(*core.LdMsg) bool) bool {
	var r io.Reader
	for _, gz := range []bool{false, true} {
		fh, err := os.Open(s.path(h, gz))
		if err != nil {
			continue
		}
		defer fh.Close()
		r = fh
		if gz {
			zr, err := ldLzGet(fh)
			if err != nil {
				return false
			}
			defer ldLzPut(zr)
			r = zr
		}
		break
	}
	if r == nil {
		return false
	}
	br := ldLinePool.Get().(*bufio.Reader)
	br.Reset(r)
	defer func() { br.Reset(nil); ldLinePool.Put(br) }()
	in := map[string]string{} // host, program, address... kept once for the hour (ldDecodeLine)
	var long []byte
	var m core.LdMsg // one for every line: f gets it only until it returns
	for {
		line, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull { // a line longer than the buffer
			long = append(long[:0], line...)
			for err == bufio.ErrBufferFull {
				line, err = br.ReadSlice('\n')
				long = append(long, line...)
			}
			line = long
		}
		if len(line) > 1 && (pre == nil || pre(line)) {
			ok := ldDecodeLine(line, &m, in)
			if !ok {
				m = core.LdMsg{}
				ok = json.Unmarshal(line, &m) == nil
			}
			if ok && !f(&m) {
				return true
			}
		}
		if err != nil {
			return true
		}
	}
}

// ldLinePool keeps the 1 MB line readers of streamFileRaw between reads.
var ldLinePool = sync.Pool{New: func() any { return bufio.NewReaderSize(nil, 1<<20) }}

// Add stores one message.
func (s *LdStore) Add(m core.LdMsg) {
	if m.Node == "" {
		m.Node = s.node
	}
	b, _ := json.Marshal(&m)
	b = append(b, '\n')
	h := m.T / 1000 / 3600
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir != "" && s.dirErr == "" {
		if s.cur == nil || s.curHour != h {
			s.rotateLocked(h)
		}
		if s.curW != nil {
			if _, err := s.curW.Write(b); err != nil {
				ldStats.writeErrs.Add(1)
			}
		}
	}
	hm := s.meta[h]
	if hm == nil {
		hm = &core.LdHourMeta{}
		s.meta[h] = hm
	}
	hm.Add(&m)
	s.liveAdd(h, &m)
	s.ring = append(s.ring, m)
	if len(s.ring) > ldRing+ldRing/4 {
		s.ring = append([]core.LdMsg(nil), s.ring[len(s.ring)-ldRing:]...)
	}
	s.dirty = true
}

func (s *LdStore) rotateLocked(h int64) {
	s.closeCurLocked()
	f, err := os.OpenFile(s.path(h, false), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		ldStats.writeErrs.Add(1)
		return
	}
	s.cur, s.curW, s.curHour = f, bufio.NewWriterSize(f, 256<<10), h
}

func (s *LdStore) closeCurLocked() {
	if s.cur == nil {
		return
	}
	s.curW.Flush()
	s.cur.Close()
	s.cur, s.curW = nil, nil
}

// Flush writes what is buffered.
func (s *LdStore) Flush() {
	s.mu.Lock()
	if s.curW != nil {
		if err := s.curW.Flush(); err != nil {
			ldStats.writeErrs.Add(1)
		}
	}
	s.mu.Unlock()
}

// Close writes everything and closes the open file.
func (s *LdStore) Close() {
	s.mu.Lock()
	s.closeCurLocked()
	s.mu.Unlock()
}

// Maintain (every minute): compresses the closed hours, applies the retention and the disk cap.
func (s *LdStore) Maintain() {
	s.mu.Lock()
	if s.dir == "" || s.dirErr != "" {
		s.mu.Unlock()
		return
	}
	nowH := s.Now().Unix() / 3600
	if s.curW != nil {
		s.curW.Flush()
	}
	var todo []int64
	for h := range s.meta {
		if h < nowH-1 || (h < nowH && s.curHour != h) {
			if _, err := os.Stat(s.path(h, false)); err == nil && h != s.curHour {
				todo = append(todo, h)
			}
		}
	}
	s.mu.Unlock()
	sort.Slice(todo, func(i, j int) bool { return todo[i] < todo[j] })
	for _, h := range todo {
		s.compress(h)
	}
	s.enforce()
}

func (s *LdStore) compress(h int64) {
	src, dst := s.path(h, false), s.path(h, true)
	in, err := os.Open(src)
	if err != nil {
		return
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		in.Close()
		return
	}
	bw := bufio.NewWriterSize(out, 1<<18)
	e1 := ldLzWrite(bw, in)
	e2 := bw.Flush()
	e3 := out.Close()
	in.Close()
	if e1 != nil || e2 != nil || e3 != nil {
		os.Remove(tmp)
		return
	}
	if os.Rename(tmp, dst) != nil {
		return
	}
	os.Remove(src)
	s.mu.Lock()
	m := s.meta[h]
	s.mu.Unlock()
	if m != nil {
		s.saveMeta(h, m)
		s.mu.Lock()
		n := m.Count
		s.mu.Unlock()
		if s.closedHour(h) {
			s.hourSum(h, n)       // the rollup, while the hour is fresh in the page cache
			s.hourWix(h, n, true) // and the word index
		}
	}
}

// enforce deletes the hours past the retention, then the oldest ones while the folder is over its cap.
func (s *LdStore) enforce() {
	s.mu.Lock()
	dir, ret, pct := s.dir, s.retDays, s.capPct
	curH := s.curHour
	s.mu.Unlock()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type fe struct {
		h    int64
		size uint64
		path []string
	}
	by := map[int64]*fe{}
	var total uint64
	for _, e := range ents {
		h, ok := parseHourName(e.Name())
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		f := by[h]
		if f == nil {
			f = &fe{h: h}
			by[h] = f
		}
		f.size += uint64(info.Size())
		f.path = append(f.path, filepath.Join(dir, e.Name()))
		total += uint64(info.Size())
	}
	list := make([]*fe, 0, len(by))
	for _, f := range by {
		list = append(list, f)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].h < list[j].h })
	if pct <= 0 {
		pct = defaultLogDirPct
	}
	var limit uint64
	if v, err := s.volSize(dir); err == nil && v > 0 {
		limit = v / 100 * uint64(pct)
	}
	cutoff := int64(-1)
	if ret > 0 {
		cutoff = s.Now().Add(-time.Duration(ret)*24*time.Hour).Unix() / 3600
	}
	var removed uint64
	drop := func(f *fe) {
		for _, p := range f.path {
			os.Remove(p)
		}
		total -= f.size
		removed++
		s.mu.Lock()
		delete(s.meta, f.h)
		s.mu.Unlock()
		s.cache.drop(f.h)
		s.dropSum(f.h)
		s.dropWix(f.h)
		s.hitsDrop(f.h)
	}
	for _, f := range list {
		if f.h == curH {
			continue
		}
		if (cutoff >= 0 && f.h < cutoff) || (limit > 0 && total > limit) {
			drop(f)
		}
	}
	s.mu.Lock()
	s.dirBytes, s.capBytes = total, limit
	s.removed += removed
	s.mu.Unlock()
}

const ldListDefault = 1000

// LdAnswer is what one node found.
type LdAnswer struct {
	Node      string       `json:"node"`
	Msgs      []core.LdMsg `json:"msgs"`
	Scanned   uint64       `json:"scanned"`
	Truncated bool         `json:"truncated,omitempty"` // more matches than the limit
	Partial   bool         `json:"partial,omitempty"`   // the search ran out of time or hours
}

// Search finds up to q.Limit messages, the newest (or the oldest) first or last as asked; the list is in time order, oldest first.
func (s *LdStore) Search(ctx context.Context, q core.LdQuery) LdAnswer {
	f := q.Filter()
	a := LdAnswer{Node: s.node}
	to := q.To
	if to == 0 {
		to = s.Now().UnixMilli()
	}
	s.mu.Lock()
	if s.curW != nil {
		s.curW.Flush()
	}
	var hours []int64
	meta := map[int64]*core.LdHourMeta{}
	for h, m := range s.meta {
		if h*3600*1000 <= to && (h+1)*3600*1000 > q.From {
			hours = append(hours, h)
			c := *m
			meta[h] = &c
		}
	}
	s.mu.Unlock()
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	if !q.Oldest {
		for i, j := 0, len(hours)-1; i < j; i, j = i+1, j-1 {
			hours[i], hours[j] = hours[j], hours[i]
		}
	}
	var out []core.LdMsg
	var todo []int64 // the hours that can hold a match, in the order they are wanted
	for _, h := range hours {
		if f.HourOK(meta[h]) && s.wixOK(h, meta[h], f) {
			todo = append(todo, h)
		}
	}
	// newest first, the hours are read a few at a time (1, 2, 4... up to ldScanWorkers): a common word is found in the first
	// hour, a rare one needs them all
	type hourRes struct {
		hm      []core.LdMsg
		scanned uint64
		partial bool
	}
	batch := 1
	for len(todo) > 0 {
		if ctx.Err() != nil {
			a.Partial = true
			break
		}
		n := min(batch, len(todo))
		rs := make([]hourRes, n)
		if n == 1 {
			r := &rs[0]
			r.hm, r.scanned, r.partial = s.searchHour(ctx, f, q, to, todo[0], meta[todo[0]].Count, q.Limit+1-len(out))
		} else {
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(r *hourRes, h int64) {
					defer wg.Done()
					r.hm, r.scanned, r.partial = s.searchHour(ctx, q.Filter(), q, to, h, meta[h].Count, 0) // a filter is one goroutine's
				}(&rs[i], todo[i])
			}
			wg.Wait()
		}
		todo = todo[n:]
		done := false
		for i := range rs {
			a.Scanned += rs[i].scanned
			hm := rs[i].hm
			if q.Oldest {
				out = append(out, hm...)
				if len(out) > q.Limit {
					out, a.Truncated, done = out[:q.Limit], true, true
				}
			} else {
				out = append(hm, out...)
				if len(out) > q.Limit {
					out, a.Truncated, done = out[len(out)-q.Limit:], true, true
				}
			}
			if rs[i].partial {
				a.Partial, done = true, true
			}
			if done {
				break
			}
		}
		if done {
			break
		}
		if !q.Oldest { // oldest first stops reading an hour once it has enough, which needs the hours one by one
			batch = min(batch*2, ldScanWorkers())
		}
	}
	a.Msgs = out
	return a
}

// ldScanWorkers is how many hours a search or a count reads at the same time: about half the cores (at most 8), so that
// receiving syslog is not held up.
func ldScanWorkers() int { return min(runtime.GOMAXPROCS(0)/2+1, 8) }

// searchHour gives an hour's matches within the range, in time order.  stop > 0 (oldest first): stop reading once there are that
// many.  A closed hour's matches for a filter with words are kept when there are few (ld_hits.go) and used next time.
func (s *LdStore) searchHour(ctx context.Context, f *core.LdFilter, q core.LdQuery, to, h int64, count uint64, stop int) (hm []core.LdMsg, scanned uint64, partial bool) {
	keep := f.HitsUseful() && s.closedHour(h)
	if keep {
		if x := s.hitsGet(f, h, false, count); x != nil {
			for i := range x.msgs {
				if m := &x.msgs[i]; m.T >= q.From && m.T <= to {
					hm = append(hm, *m)
				}
			}
			sort.SliceStable(hm, func(i, j int) bool { return hm[i].T < hm[j].T })
			return hm, 0, false
		}
	}
	var all []core.LdMsg // with keep: every match of the hour, whatever its time
	whole := true
	s.scanHour(h, f, func(m *core.LdMsg) bool {
		scanned++
		if scanned&0x3fff == 0 && ctx.Err() != nil {
			partial, whole = true, false
			return false
		}
		in := m.T >= q.From && m.T <= to
		if (!in && !keep) || !f.Match(m) {
			return true
		}
		if keep {
			if len(all) < ldHitsMsgs {
				all = append(all, *m)
			} else {
				keep, all = false, nil
			}
		}
		if !in {
			return true
		}
		hm = append(hm, *m)
		if q.Oldest && stop > 0 && len(hm) >= stop {
			whole = false
			return false
		}
		return true
	})
	if keep && whole {
		s.hitsPut(f, h, false, &ldHits{count: count, msgs: all[:len(all):len(all)]})
	}
	sort.SliceStable(hm, func(i, j int) bool { return hm[i].T < hm[j].T })
	return hm, scanned, partial
}

// Tail is the newest messages that match, from the memory ring.
func (s *LdStore) Tail(q core.LdQuery) LdAnswer {
	f := q.Filter()
	a := LdAnswer{Node: s.node}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.ring) - 1; i >= 0 && len(a.Msgs) < q.Limit; i-- {
		m := &s.ring[i]
		a.Scanned++
		if m.T <= q.From || (q.To > 0 && m.T > q.To) || !f.Match(m) {
			continue
		}
		a.Msgs = append(a.Msgs, *m)
	}
	for i, j := 0, len(a.Msgs)-1; i < j; i, j = i+1, j-1 {
		a.Msgs[i], a.Msgs[j] = a.Msgs[j], a.Msgs[i]
	}
	return a
}

// LdStatsAnswer counts what matches: by severity, by host, by program and per bucket.
type LdStatsAnswer struct {
	Node    string                      `json:"node"`
	Total   uint64                      `json:"total"`
	BySev   [8]uint64                   `json:"by_severity"`
	ByHost  map[string]uint64           `json:"by_host,omitempty"`
	ByProg  map[string]uint64           `json:"by_program,omitempty"`
	Step    int64                       `json:"step_s"`
	Series  map[int64]uint64            `json:"series,omitempty"`          // bucket start (seconds) -> count
	BySrc   map[string]uint64           `json:"by_source,omitempty"`       // the address each message came from -> count
	SrcSer  map[string]map[int64]uint64 `json:"source_series,omitempty"`   // per sender (at most ldSrcSeriesMax of them), as Series
	SevSer  [3]map[int64]uint64         `json:"severity_series,omitempty"` // info or less severe, warning, error or worse, as Series
	ByField map[string]uint64           `json:"by_field,omitempty"`        // the values of q.Field -> count (at most ldFieldValuesMax of them)
	// FieldAgg: per value of q.Field, what q.Measure counts (ld_agg.go); an empty map from a node that counted nothing, absent from
	// one that does not know measures (older than v42)
	FieldAgg map[string]*ldAgg           `json:"field_agg,omitempty"`
	ValSer   map[string]map[int64]uint64 `json:"value_series,omitempty"` // per value of q.Values, as Series
	Measured bool                        `json:"measured,omitempty"`     // q.Measure was counted (an empty FieldAgg is not sent)
	Scanned  uint64                      `json:"scanned"`
	Partial  bool                        `json:"partial,omitempty"`
}

// ldFieldValuesMax is how many different values of a field one node counts (the rest are not counted).
const ldFieldValuesMax = 5000

// ldSrcSeriesMax is how many senders get a time series of their own in one node's stats (the totals count them all).
const ldSrcSeriesMax = 500

// ldSevClass is the class of a severity for the severity chart: 0 info or less severe (notice, info, debug), 1 warning, 2 error or worse.
func ldSevClass(sev int8) int {
	switch {
	case sev <= 3:
		return 2
	case sev == 4:
		return 1
	}
	return 0
}

// newLdStatsAnswer is an empty answer to q with buckets of step seconds, and the values q wants a series of.
func newLdStatsAnswer(node string, q core.LdQuery, step int64) (LdStatsAnswer, map[string]bool) {
	r := LdStatsAnswer{Node: node, ByHost: map[string]uint64{}, ByProg: map[string]uint64{}, Step: step, Series: map[int64]uint64{}, BySrc: map[string]uint64{}}
	var want map[string]bool
	if q.Field != "" {
		r.ByField = map[string]uint64{}
		if q.Measure != "" {
			r.FieldAgg, r.Measured = map[string]*ldAgg{}, true
		}
		if step > 0 && len(q.Values) > 0 {
			r.ValSer, want = map[string]map[int64]uint64{}, map[string]bool{}
			for _, v := range q.Values {
				want[v] = true
			}
		}
	}
	if step > 0 {
		r.SrcSer = map[string]map[int64]uint64{}
		for i := range r.SevSer {
			r.SevSer[i] = map[int64]uint64{}
		}
	}
	return r, want
}

// addStats adds o (another part of the same question, over other hours) to r.  The caps on how many hosts, programs, senders and
// values are counted hold as when counting: a name past the cap that r has not seen is left out.
func addStats(r, o *LdStatsAnswer) {
	rb := func(b int64) int64 { return b } // a kept hour's part has buckets of an hour: into this answer's
	if r.Step > 0 && o.Step > 0 && o.Step != r.Step {
		rb = func(b int64) int64 { return b / r.Step * r.Step }
	}
	r.Total += o.Total
	r.Scanned += o.Scanned
	r.Partial = r.Partial || o.Partial
	for i := range r.BySev {
		r.BySev[i] += o.BySev[i]
	}
	add := func(dst, src map[string]uint64, max int) {
		for k, n := range src {
			if len(dst) < max || dst[k] > 0 {
				dst[k] += n
			}
		}
	}
	add(r.ByHost, o.ByHost, 5000)
	add(r.ByProg, o.ByProg, 5000)
	add(r.BySrc, o.BySrc, 5000)
	if r.Step > 0 {
		for b, n := range o.Series {
			r.Series[rb(b)] += n
		}
		for i := range o.SevSer {
			for b, n := range o.SevSer[i] {
				r.SevSer[i][rb(b)] += n
			}
		}
	}
	addSer := func(dst map[string]map[int64]uint64, src map[string]map[int64]uint64, max int) {
		for k, ser := range src {
			d := dst[k]
			if d == nil {
				if len(dst) >= max {
					continue
				}
				d = map[int64]uint64{}
				dst[k] = d
			}
			for b, n := range ser {
				d[rb(b)] += n
			}
		}
	}
	if r.SrcSer != nil && o.SrcSer != nil {
		addSer(r.SrcSer, o.SrcSer, ldSrcSeriesMax)
	}
	if r.ByField != nil {
		for v, n := range o.ByField {
			if len(r.ByField) >= ldFieldValuesMax && r.ByField[v] == 0 {
				continue
			}
			r.ByField[v] += n
			if x := o.FieldAgg[v]; x != nil && r.FieldAgg != nil {
				a := r.FieldAgg[v]
				if a == nil {
					a = &ldAgg{}
					r.FieldAgg[v] = a
				}
				a.mergeLive(x)
			}
		}
	}
	if r.ValSer != nil && o.ValSer != nil {
		addSer(r.ValSer, o.ValSer, 1<<30)
	}
}

func (s *LdStore) Stats(ctx context.Context, q core.LdQuery, step int64) LdStatsAnswer {
	f := q.Filter()
	r, want := newLdStatsAnswer(s.node, q, step)
	to := q.To
	if to == 0 {
		to = s.Now().UnixMilli()
	}
	s.mu.Lock()
	if s.curW != nil {
		s.curW.Flush()
	}
	var hours []int64
	meta := map[int64]*core.LdHourMeta{}
	for h, m := range s.meta {
		if h*3600*1000 <= to && (h+1)*3600*1000 > q.From {
			hours = append(hours, h)
			c := *m
			meta[h] = &c
		}
	}
	liveH, liveSum := int64(-1), (*ldHourSum)(nil)
	if q.To == 0 { // the range runs to now: nothing of the newest hour is after its end
		liveH, liveSum = s.liveSnapshot()
	}
	s.mu.Unlock()
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	var scan []int64 // the hours to read
	// countInto adds n messages like m, at bucket b, to r; count to this answer
	countInto := func(r *LdStatsAnswer, m *core.LdMsg, n uint64, b int64) {
		r.Total += n
		r.BySev[m.Sev&7] += n
		if len(r.ByHost) < 5000 || r.ByHost[m.Host] > 0 {
			r.ByHost[m.Host] += n
		}
		if m.Prog != "" && (len(r.ByProg) < 5000 || r.ByProg[m.Prog] > 0) {
			r.ByProg[m.Prog] += n
		}
		if len(r.BySrc) < 5000 || r.BySrc[m.Src] > 0 {
			r.BySrc[m.Src] += n
		}
		if q.Field != "" {
			fs := core.LdFields(m)
			if v := ldFieldOf(m, fs, q.Field); v != "" && (len(r.ByField) < ldFieldValuesMax || r.ByField[v] > 0) {
				r.ByField[v] += n
				if r.FieldAgg != nil {
					a := r.FieldAgg[v]
					if a == nil {
						a = &ldAgg{}
						r.FieldAgg[v] = a
					}
					a.measure(q.Measure, q.MField, m, fs, n)
				}
				if want[v] && r.ValSer != nil {
					vs := r.ValSer[v]
					if vs == nil {
						vs = map[int64]uint64{}
						r.ValSer[v] = vs
					}
					vs[b] += n
				}
			}
		}
		if step > 0 {
			r.Series[b] += n
			r.SevSer[ldSevClass(m.Sev)][b] += n
			ss := r.SrcSer[m.Src]
			if ss == nil && len(r.SrcSer) < ldSrcSeriesMax {
				ss = map[int64]uint64{}
				r.SrcSer[m.Src] = ss
			}
			if ss != nil {
				ss[b] += n
			}
		}
	}
	count := func(m *core.LdMsg, n uint64, b int64) { countInto(&r, m, n, b) }
	// whole closed hours that a filter with words has not counted before: counted several at a time first, then taken like the
	// rest from what is kept (ld_hits.go)
	keepHour := func(h int64) bool {
		return f.HitsUseful() && s.closedHour(h) && q.Field == "" && (step == 0 || step%3600 == 0) && h*3600*1000 >= q.From && (h+1)*3600*1000-1 <= to
	}
	// whole closed hours of a question about a field's values: each hour's part of the answer is kept (ld_hits.go: fieldPut), so the
	// page's refresh reads only the hour being written
	fieldKeep := func(h int64) bool {
		return q.Field != "" && s.closedHour(h) && (step == 0 || step%3600 == 0) && h*3600*1000 >= q.From && (h+1)*3600*1000-1 <= to
	}
	fkey := ""
	if q.Field != "" {
		fkey = ldFieldKey(f, q, step > 0)
	}
	// addPart adds the part of hour h, and the hour's count of each value that has a series of its own
	addPart := func(r, p *LdStatsAnswer, h int64) {
		addStats(r, p)
		if r.ValSer != nil && step > 0 {
			b := h * 3600 / step * step
			for v := range want {
				if n := p.ByField[v]; n > 0 && (len(r.ValSer) < len(want) || r.ValSer[v] != nil) {
					vs := r.ValSer[v]
					if vs == nil {
						vs = map[int64]uint64{}
						r.ValSer[v] = vs
					}
					vs[b] += n
				}
			}
		}
	}
	var need []int64
	for _, h := range hours {
		if keepHour(h) && f.HourOK(meta[h]) && s.wixOK(h, meta[h], f) && s.hitsGet(f, h, true, meta[h].Count) == nil {
			need = append(need, h)
		}
	}
	if len(need) > 1 {
		n, partial := s.countHours(ctx, q, need, meta)
		r.Scanned += n
		r.Partial = r.Partial || partial
	}
	for _, h := range hours {
		if ctx.Err() != nil || r.Partial {
			r.Partial = true
			break
		}
		if !f.HourOK(meta[h]) || !s.wixOK(h, meta[h], f) {
			continue
		}
		// a whole closed hour, and nothing asked that a rollup cannot answer: add up its rollup instead of reading it; the newest
		// hour, when the range runs to now, from the rollup kept as its messages came in
		var sum *ldHourSum
		if q.Field == "" && h == liveH && liveSum != nil && sumHourOK(f, step, h, q.From, (h+1)*3600*1000-1) {
			sum = liveSum
		} else if s.closedHour(h) && q.Field == "" && sumHourOK(f, step, h, q.From, to) {
			sum = s.hourSum(h, meta[h].Count)
		}
		if sum != nil {
			var b int64
			if step > 0 {
				b = h * 3600 / step * step
			}
			var m core.LdMsg
			for i := range sum.rows {
				x := &sum.rows[i]
				m.Host, m.Prog, m.Src, m.Sev = sum.names[x.host], sum.names[x.prog], sum.names[x.src], x.sev
				if f.Match(&m) {
					count(&m, x.n, b)
				}
			}
			r.Scanned += sum.count
			continue
		}
		// a whole closed hour whose values of the field were counted before (ld_hits.go: fieldGet)
		if fieldKeep(h) {
			if p := s.fieldGet(fkey, h, meta[h].Count, q.Measure, q.MField); p != nil {
				addPart(&r, p, h)
				continue
			}
		}
		// a whole closed hour counted with words before: its counts are kept (ld_hits.go)
		keep := keepHour(h)
		if keep {
			if x := s.hitsGet(f, h, true, meta[h].Count); x != nil {
				var b int64
				if step > 0 {
					b = h * 3600 / step * step
				}
				var m core.LdMsg
				for i := range x.sum.rows {
					row := &x.sum.rows[i]
					m.Host, m.Prog, m.Src, m.Sev = x.sum.names[row.host], x.sum.names[row.prog], x.sum.names[row.src], row.sev
					count(&m, row.n, b)
				}
				continue
			}
		}
		scan = append(scan, h)
	}
	// the hours that must be read: one by one (an hour that fits is kept in the cache), or several at a time when there are more,
	// each worker with its own filter and its own part of the answer, added up at the end; those do not fill the cache, so a long
	// range does not push the newest hours out, and an hour not in it is read line by line instead of whole
	scanOne := func(into *LdStatsAnswer, f *core.LdFilter, h int64, keepCache bool) {
		keep := keepHour(h)
		var mk ldSumMaker
		// an hour whose part is kept is counted on its own (in buckets of an hour) and then added
		r, bs, fk := into, step, fieldKeep(h)
		var part LdStatsAnswer
		if fk {
			if step > 0 {
				bs = 3600
			}
			pq := q
			pq.Values = nil // the values' series come from the part's counts per value (addPart)
			part, _ = newLdStatsAnswer(s.node, pq, bs)
			r = &part
		}
		each := func(m *core.LdMsg) bool {
			r.Scanned++
			if r.Scanned&0x3fff == 0 && ctx.Err() != nil {
				r.Partial = true
				return false
			}
			if m.T < q.From || m.T > to || !f.Match(m) {
				return true
			}
			var b int64
			if bs > 0 {
				b = (m.T / 1000) / bs * bs
			}
			countInto(r, m, 1, b)
			if keep {
				mk.add(m)
			}
			return true
		}
		if keepCache {
			s.scanHour(h, f, each)
		} else {
			s.scanHourOnce(h, f, each)
		}
		if keep && !r.Partial {
			if sum := mk.done(meta[h].Count); sum != nil {
				s.hitsPut(f, h, true, &ldHits{count: meta[h].Count, sum: sum})
			}
		}
		if fk {
			if !part.Partial {
				s.fieldPut(fkey, h, meta[h].Count, q.Measure, q.MField, &part)
			}
			addPart(into, &part, h)
		}
	}
	if workers := min(ldScanWorkers(), len(scan)); workers <= 1 || r.Partial {
		for _, h := range scan {
			if r.Partial || ctx.Err() != nil {
				r.Partial = true
				break
			}
			scanOne(&r, f, h, true)
		}
	} else {
		parts := make([]LdStatsAnswer, workers)
		var mu sync.Mutex
		next := 0
		var wg sync.WaitGroup
		for w := range parts {
			parts[w], _ = newLdStatsAnswer(s.node, q, step)
			wg.Add(1)
			go func(p *LdStatsAnswer) {
				defer wg.Done()
				f := q.Filter() // a filter is one goroutine's
				for !p.Partial {
					mu.Lock()
					if next >= len(scan) {
						mu.Unlock()
						return
					}
					h := scan[next]
					next++
					mu.Unlock()
					if ctx.Err() != nil {
						p.Partial = true
						return
					}
					scanOne(p, f, h, false)
				}
			}(&parts[w])
		}
		wg.Wait()
		for i := range parts {
			addStats(&r, &parts[i])
		}
	}
	if r.FieldAgg != nil {
		ldAggTrim(r.FieldAgg, q.Measure)
	}
	return r
}

// countHours counts the matches of whole closed hours by row, several hours at a time, and keeps the counts (ld_hits.go); it
// gives how many messages it read.
func (s *LdStore) countHours(ctx context.Context, q core.LdQuery, hours []int64, meta map[int64]*core.LdHourMeta) (scanned uint64, partial bool) {
	var mu sync.Mutex
	next := 0
	var wg sync.WaitGroup
	for w := 0; w < min(ldScanWorkers(), len(hours)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := q.Filter() // a filter is one goroutine's
			for {
				mu.Lock()
				if next >= len(hours) || partial {
					mu.Unlock()
					return
				}
				h := hours[next]
				next++
				mu.Unlock()
				var mk ldSumMaker
				var n uint64
				stopped := false
				s.scanHour(h, f, func(m *core.LdMsg) bool {
					n++
					if n&0x3fff == 0 && ctx.Err() != nil {
						stopped = true
						return false
					}
					if f.Match(m) {
						mk.add(m)
					}
					return true
				})
				if !stopped {
					if sum := mk.done(meta[h].Count); sum != nil {
						s.hitsPut(f, h, true, &ldHits{count: meta[h].Count, sum: sum})
					}
				}
				mu.Lock()
				scanned += n
				partial = partial || stopped
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return scanned, partial
}

// Oldest is the time of the oldest message this node holds (0: none).
func (s *LdStore) Oldest() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var o int64
	for h, m := range s.meta {
		if m.Count == 0 {
			continue
		}
		t := m.First
		if t == 0 {
			t = h * 3600 * 1000
		}
		if o == 0 || t < o {
			o = t
		}
	}
	return o
}

func (s *LdStore) Count() (hours int, msgs uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.meta {
		hours++
		msgs += m.Count
	}
	return
}

func (s *LdStore) String() string { return fmt.Sprintf("ldStore(%s)", s.dir) }

// CountSince counts the messages that match q (From to now) on this node: from the memory ring when it reaches back to From, else
// by reading the hours.
func (s *LdStore) CountSince(ctx context.Context, q core.LdQuery) uint64 {
	f := q.Filter()
	s.mu.Lock()
	if len(s.ring) > 0 && (s.ring[0].T <= q.From || len(s.ring) < ldRing) {
		var n uint64
		for i := len(s.ring) - 1; i >= 0; i-- {
			m := &s.ring[i]
			if m.T < q.From {
				break
			}
			if f.Match(m) {
				n++
			}
		}
		s.mu.Unlock()
		return n
	}
	s.mu.Unlock()
	return s.Stats(ctx, q, 0).Total
}
