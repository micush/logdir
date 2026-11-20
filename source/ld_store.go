package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// The log store of a node: what this node received, as one file per hour (UTC) of JSON lines, `2026112005.jsonl`.  The open hour is
// appended to through a buffer that is flushed every second (and by Stop, which the update's re-exec calls first); a closed hour is
// compressed to `.jsonl.lz` with a small index file beside it (`.idx`: which hosts, programs and severities the hour holds, and
// how many messages), so a search skips the hours that cannot contain what it asks for without reading them.
//
// Every message is kept on the node that received it.  No message is on two nodes unless a sender sends to two of them, and the
// merge of a cluster search drops that copy (ld_cluster.go).

const (
	ldMetaNames = 2000 // hosts or programs an hour's index names; more and the index no longer rules an hour out by name
	ldRing      = 4000 // the newest messages, kept in memory for tail
)

var ldStats struct {
	received, bytes, parsed, refused, dropped, writeErrs atomic.Uint64
	udp, tcp, tls                                        atomic.Uint64
}

type ldHourMeta struct {
	Count  uint64              `json:"count"`
	Sev    uint8               `json:"sev"` // bit n: a message of severity n
	Hosts  map[string]struct{} `json:"-"`
	Progs  map[string]struct{} `json:"-"`
	HostsL []string            `json:"hosts,omitempty"`
	ProgsL []string            `json:"progs,omitempty"`
	Full   bool                `json:"full,omitempty"` // more names than ldMetaNames: names rule nothing out
	First  int64               `json:"first,omitempty"`
	Last   int64               `json:"last,omitempty"`
}

func (h *ldHourMeta) add(m *LdMsg) {
	h.Count++
	h.Sev |= 1 << uint(m.Sev&7)
	if h.First == 0 || m.T < h.First {
		h.First = m.T
	}
	if m.T > h.Last {
		h.Last = m.T
	}
	if h.Hosts == nil {
		h.Hosts, h.Progs = map[string]struct{}{}, map[string]struct{}{}
	}
	if !h.Full {
		h.Hosts[strings.ToLower(m.Host)] = struct{}{}
		if m.Prog != "" {
			h.Progs[strings.ToLower(m.Prog)] = struct{}{}
		}
		if len(h.Hosts) > ldMetaNames || len(h.Progs) > ldMetaNames {
			h.Full, h.Hosts, h.Progs = true, nil, nil
		}
	}
}

// ldFilter is what a search asks of one message.
type ldFilter struct {
	expr   matchExpr
	host   string // lower case; an exact host, or a leading/trailing * wildcard
	prog   string
	minSev int // -1: any
	src    string
	srcSet map[string]bool // senders picked by name: nil is no such filter, an empty set matches nothing
}

func (f *ldFilter) hostOK(h string) bool { return f.host == "" || ldGlob(f.host, strings.ToLower(h)) }
func (f *ldFilter) progOK(p string) bool { return f.prog == "" || ldGlob(f.prog, strings.ToLower(p)) }

// ldGlob matches s against a pattern with * in it (anywhere), case already folded.
func ldGlob(p, s string) bool {
	if !strings.Contains(p, "*") {
		return p == s
	}
	parts := strings.Split(p, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		j := strings.Index(s, parts[i])
		if j < 0 {
			return false
		}
		s = s[j+len(parts[i]):]
	}
	last := parts[len(parts)-1]
	return len(parts) == 1 || strings.HasSuffix(s, last)
}

func (f *ldFilter) match(m *LdMsg) bool {
	if f.minSev >= 0 && int(m.Sev) > f.minSev {
		return false
	}
	if f.src != "" && m.Src != f.src {
		return false
	}
	if f.srcSet != nil && !f.srcSet[m.Src] {
		return false
	}
	if !f.hostOK(m.Host) || !f.progOK(m.Prog) {
		return false
	}
	return len(f.expr) == 0 || f.expr.matches(strings.ToLower(m.text()))
}

// hourOK says whether an hour could hold a message the filter wants (false: skip the hour).
func (f *ldFilter) hourOK(h *ldHourMeta) bool {
	if h == nil {
		return true
	}
	if h.Count == 0 {
		return false
	}
	if f.minSev >= 0 && h.Sev&(uint8(1<<uint(f.minSev+1))-1) == 0 {
		return false
	}
	if !h.Full && h.Hosts != nil {
		if f.host != "" && !strings.Contains(f.host, "*") {
			if _, ok := h.Hosts[f.host]; !ok {
				return false
			}
		}
		if f.prog != "" && !strings.Contains(f.prog, "*") {
			if _, ok := h.Progs[f.prog]; !ok {
				return false
			}
		}
	}
	return true
}

type ldStore struct {
	mu     sync.Mutex
	dir    string
	dirErr string
	node   string

	cur     *os.File
	curW    *bufio.Writer
	curHour int64
	meta    map[int64]*ldHourMeta // by hour (unix seconds / 3600)
	ring    []LdMsg               // the newest, oldest first
	dirty   bool

	capPct, retDays int
	dirBytes        uint64
	capBytes        uint64
	removed         uint64
	now             func() time.Time
	volSize         func(string) (uint64, error)
}

func newLdStore(dir, node string) *ldStore {
	return &ldStore{dir: dir, node: node, meta: map[int64]*ldHourMeta{}, now: time.Now, volSize: ldVolumeSize}
}

func ldVolumeSize(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Blocks) * uint64(st.Bsize), nil
}

func ldHourName(h int64) string { return time.Unix(h*3600, 0).UTC().Format("2006010215") }

func (s *ldStore) path(h int64, gz bool) string {
	n := ldHourName(h) + ".jsonl"
	if gz {
		n += ".lz"
	}
	return filepath.Join(s.dir, n)
}

func parseHourName(n string) (int64, bool) {
	n = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(n, ".idx"), ".lz"), ".jsonl")
	t, err := time.ParseInLocation("2006010215", n, time.UTC)
	if err != nil || len(n) != 10 {
		return 0, false
	}
	return t.Unix() / 3600, true
}

// Load makes the folder, reads the index of every hour (building the ones that are missing) and the newest messages.
func (s *ldStore) Load() {
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
		if h, ok := parseHourName(e.Name()); ok && !strings.HasSuffix(e.Name(), ".idx") && !seen[h] {
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
		var msgs []LdMsg
		s.scanFile(hours[i], func(m *LdMsg) bool { msgs = append(msgs, *m); return true })
		s.ring = append(msgs, s.ring...)
	}
	if len(s.ring) > ldRing {
		s.ring = s.ring[len(s.ring)-ldRing:]
	}
	s.dirty = false
}

func (s *ldStore) loadMetaLocked(h int64) *ldHourMeta {
	if b, err := os.ReadFile(s.path(h, true) + ".idx"); err == nil {
		var m ldHourMeta
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
	m := &ldHourMeta{}
	s.scanFile(h, func(x *LdMsg) bool { m.add(x); return true })
	return m
}

func (s *ldStore) saveMeta(h int64, m *ldHourMeta) {
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
func (s *ldStore) scanFile(h int64, f func(*LdMsg) bool) {
	var r io.Reader
	for _, gz := range []bool{false, true} {
		fh, err := os.Open(s.path(h, gz))
		if err != nil {
			continue
		}
		defer fh.Close()
		r = fh
		if gz {
			zr, err := ldLzReader(fh)
			if err != nil {
				return
			}
			r = zr
		}
		break
	}
	if r == nil {
		return
	}
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 1 {
			var m LdMsg
			if json.Unmarshal(line, &m) == nil && !f(&m) {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// Add stores one message.
func (s *ldStore) Add(m LdMsg) {
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
		hm = &ldHourMeta{}
		s.meta[h] = hm
	}
	hm.add(&m)
	s.ring = append(s.ring, m)
	if len(s.ring) > ldRing+ldRing/4 {
		s.ring = append([]LdMsg(nil), s.ring[len(s.ring)-ldRing:]...)
	}
	s.dirty = true
}

func (s *ldStore) rotateLocked(h int64) {
	s.closeCurLocked()
	f, err := os.OpenFile(s.path(h, false), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		ldStats.writeErrs.Add(1)
		return
	}
	s.cur, s.curW, s.curHour = f, bufio.NewWriterSize(f, 256<<10), h
}

func (s *ldStore) closeCurLocked() {
	if s.cur == nil {
		return
	}
	s.curW.Flush()
	s.cur.Close()
	s.cur, s.curW = nil, nil
}

// Flush writes what is buffered.
func (s *ldStore) Flush() {
	s.mu.Lock()
	if s.curW != nil {
		if err := s.curW.Flush(); err != nil {
			ldStats.writeErrs.Add(1)
		}
	}
	s.mu.Unlock()
}

// Close writes everything and closes the open file.
func (s *ldStore) Close() {
	s.mu.Lock()
	s.closeCurLocked()
	s.mu.Unlock()
}

// Maintain (every minute): compresses the closed hours, applies the retention and the disk cap.
func (s *ldStore) Maintain() {
	s.mu.Lock()
	if s.dir == "" || s.dirErr != "" {
		s.mu.Unlock()
		return
	}
	nowH := s.now().Unix() / 3600
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

func (s *ldStore) compress(h int64) {
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
	}
}

// enforce deletes the hours past the retention, then the oldest ones while the folder is over its cap.
func (s *ldStore) enforce() {
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
		cutoff = s.now().Add(-time.Duration(ret)*24*time.Hour).Unix() / 3600
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

// ── search ──────────────────────────────────────────────────────────────────

// LdQuery is a search.  From and To are milliseconds (To 0: now); Limit counts messages.
type LdQuery struct {
	From   int64  `json:"from"`
	To     int64  `json:"to"`
	Q      string `json:"q,omitempty"`
	Host   string `json:"host,omitempty"`
	Prog   string `json:"prog,omitempty"`
	MinSev int    `json:"min_severity"` // -1 any
	Src    string `json:"src,omitempty"`
	// SrcName picks senders by name (the configured name, the reverse name or the host name; * wildcards) instead of by address: every node
	// turns it into the addresses it knows (srcSet) when it runs the query.
	SrcName string `json:"src_name,omitempty"`
	srcSet  map[string]bool
	Limit   int  `json:"limit"`
	Oldest  bool `json:"oldest_first,omitempty"` // the oldest matches (default: the newest)
}

const (
	ldMaxLimit     = 5000
	ldDefaultLimit = 200
)

func (q *LdQuery) Validate() error {
	if q.Limit <= 0 {
		q.Limit = ldDefaultLimit
	}
	if q.Limit > ldMaxLimit {
		q.Limit = ldMaxLimit
	}
	if q.MinSev < -1 || q.MinSev > 7 {
		return errors.New("min_severity must be 0 to 7")
	}
	if q.To != 0 && q.To < q.From {
		return errors.New("to is before from")
	}
	if len(q.Q) > 2000 || len(q.Host) > 255 || len(q.Prog) > 255 {
		return errors.New("query too long")
	}
	return nil
}

func (q *LdQuery) filter() *ldFilter {
	return &ldFilter{expr: parseMatch(q.Q), host: strings.ToLower(strings.TrimSpace(q.Host)), prog: strings.ToLower(strings.TrimSpace(q.Prog)), minSev: q.MinSev, src: q.Src, srcSet: q.srcSet}
}

// LdAnswer is what one node found.
type LdAnswer struct {
	Node      string  `json:"node"`
	Msgs      []LdMsg `json:"msgs"`
	Scanned   uint64  `json:"scanned"`
	Truncated bool    `json:"truncated,omitempty"` // more matches than the limit
	Partial   bool    `json:"partial,omitempty"`   // the search ran out of time or hours
}

// Search finds up to q.Limit messages, the newest (or the oldest) first or last as asked; the list is in time order, oldest first.
func (s *ldStore) Search(ctx context.Context, q LdQuery) LdAnswer {
	f := q.filter()
	a := LdAnswer{Node: s.node}
	to := q.To
	if to == 0 {
		to = s.now().UnixMilli()
	}
	s.mu.Lock()
	if s.curW != nil {
		s.curW.Flush()
	}
	var hours []int64
	meta := map[int64]*ldHourMeta{}
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
	var out []LdMsg
	for _, h := range hours {
		if ctx.Err() != nil {
			a.Partial = true
			break
		}
		if !f.hourOK(meta[h]) {
			continue
		}
		var hm []LdMsg
		s.scanFile(h, func(m *LdMsg) bool {
			a.Scanned++
			if a.Scanned&0x3fff == 0 && ctx.Err() != nil {
				a.Partial = true
				return false
			}
			if m.T < q.From || m.T > to || !f.match(m) {
				return true
			}
			hm = append(hm, *m)
			if q.Oldest && len(out)+len(hm) > q.Limit {
				return false
			}
			return true
		})
		sort.SliceStable(hm, func(i, j int) bool { return hm[i].T < hm[j].T })
		if q.Oldest {
			out = append(out, hm...)
			if len(out) > q.Limit {
				out, a.Truncated = out[:q.Limit], true
				break
			}
		} else {
			out = append(hm, out...)
			if len(out) > q.Limit {
				out, a.Truncated = out[len(out)-q.Limit:], true
				break
			}
		}
		if a.Partial {
			break
		}
	}
	a.Msgs = out
	return a
}

// Tail is the newest messages that match, from the memory ring.
func (s *ldStore) Tail(q LdQuery) LdAnswer {
	f := q.filter()
	a := LdAnswer{Node: s.node}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.ring) - 1; i >= 0 && len(a.Msgs) < q.Limit; i-- {
		m := &s.ring[i]
		a.Scanned++
		if m.T <= q.From || (q.To > 0 && m.T > q.To) || !f.match(m) {
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
	Node    string            `json:"node"`
	Total   uint64            `json:"total"`
	BySev   [8]uint64         `json:"by_severity"`
	ByHost  map[string]uint64 `json:"by_host,omitempty"`
	ByProg  map[string]uint64 `json:"by_program,omitempty"`
	Step    int64             `json:"step_s"`
	Series  map[int64]uint64  `json:"series,omitempty"` // bucket start (seconds) -> count
	Scanned uint64            `json:"scanned"`
	Partial bool              `json:"partial,omitempty"`
}

func (s *ldStore) Stats(ctx context.Context, q LdQuery, step int64) LdStatsAnswer {
	f := q.filter()
	r := LdStatsAnswer{Node: s.node, ByHost: map[string]uint64{}, ByProg: map[string]uint64{}, Step: step, Series: map[int64]uint64{}}
	to := q.To
	if to == 0 {
		to = s.now().UnixMilli()
	}
	s.mu.Lock()
	if s.curW != nil {
		s.curW.Flush()
	}
	var hours []int64
	meta := map[int64]*ldHourMeta{}
	for h, m := range s.meta {
		if h*3600*1000 <= to && (h+1)*3600*1000 > q.From {
			hours = append(hours, h)
			c := *m
			meta[h] = &c
		}
	}
	s.mu.Unlock()
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	for _, h := range hours {
		if ctx.Err() != nil {
			r.Partial = true
			break
		}
		if !f.hourOK(meta[h]) {
			continue
		}
		s.scanFile(h, func(m *LdMsg) bool {
			r.Scanned++
			if r.Scanned&0x3fff == 0 && ctx.Err() != nil {
				r.Partial = true
				return false
			}
			if m.T < q.From || m.T > to || !f.match(m) {
				return true
			}
			r.Total++
			r.BySev[m.Sev&7]++
			if len(r.ByHost) < 5000 || r.ByHost[m.Host] > 0 {
				r.ByHost[m.Host]++
			}
			if m.Prog != "" && (len(r.ByProg) < 5000 || r.ByProg[m.Prog] > 0) {
				r.ByProg[m.Prog]++
			}
			if step > 0 {
				r.Series[(m.T/1000)/step*step]++
			}
			return true
		})
		if r.Partial {
			break
		}
	}
	return r
}

// Oldest is the time of the oldest message this node holds (0: none).
func (s *ldStore) Oldest() int64 {
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

func (s *ldStore) Count() (hours int, msgs uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.meta {
		hours++
		msgs += m.Count
	}
	return
}

func (s *ldStore) String() string { return fmt.Sprintf("ldStore(%s)", s.dir) }

// CountSince counts the messages that match q (From to now) on this node: from the memory ring when it reaches back to From, else
// by reading the hours.
func (s *ldStore) CountSince(ctx context.Context, q LdQuery) uint64 {
	f := q.filter()
	s.mu.Lock()
	if len(s.ring) > 0 && (s.ring[0].T <= q.From || len(s.ring) < ldRing) {
		var n uint64
		for i := len(s.ring) - 1; i >= 0; i-- {
			m := &s.ring[i]
			if m.T < q.From {
				break
			}
			if f.match(m) {
				n++
			}
		}
		s.mu.Unlock()
		return n
	}
	s.mu.Unlock()
	return s.Stats(ctx, q, 0).Total
}
