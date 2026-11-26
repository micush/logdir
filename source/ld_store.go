package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"unicode/utf8"
	"unsafe"
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
	ruleDropped                                          atomic.Uint64 // by drop rules (ld_drop.go)
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
	expr     matchExpr
	groups   []ldGroup // expr compiled: text and field terms (ld_expr.go)
	host     string    // lower case; an exact host, or a leading/trailing * wildcard
	prog     string
	minSev   int  // -1: any
	sevExact bool // only this severity, not this severe or worse
	sevLess  bool // this severity or less severe
	src      string
	srcWords map[string]string // each sender's names: a word of the search matches them too
	buf      []byte            // scratch for the lower-cased text of the message being matched (a filter is used by one goroutine)
	hostSrc  map[string]bool   // senders whose name matches the host filter: their messages match it too
	srcSet   map[string]bool   // senders picked by name: nil is no such filter, an empty set matches nothing
	wixUse   int8              // the word index can rule hours out: 0 not worked out yet, 1 yes, -1 no
	raw      [][]string        // per OR group, pieces a stored line must hold (lower case) to match it (rawWordsOK)
	rawUse   int8              // the words can rule lines out from their bytes: 0 not worked out yet, 1 yes, -1 no
	rawField bool              // some of raw are field values (which a few formats make up: ldFieldsMadeUp)
	lower    []byte            // scratch: the lower-cased line
	hkey     string            // key, once worked out (ld_hits.go)
}

// sevOK: does a message of this severity pass the filter's severity?
func (f *ldFilter) sevOK(sev int) bool {
	switch {
	case f.minSev < 0:
		return true
	case f.sevExact:
		return sev == f.minSev
	case f.sevLess:
		return sev >= f.minSev
	}
	return sev <= f.minSev
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

// appendLower appends s in lower case: the same bytes strings.ToLower gives, without making a string when s is plain ASCII.
func appendLower(dst []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return append(dst, strings.ToLower(s)...)
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst = append(dst, c)
	}
	return dst
}

func (f *ldFilter) match(m *LdMsg) bool {
	if !f.sevOK(int(m.Sev)) {
		return false
	}
	if f.src != "" && m.Src != f.src {
		return false
	}
	if f.srcSet != nil && !f.srcSet[m.Src] {
		return false
	}
	if !(f.hostOK(m.Host) || f.hostSrc[m.Src]) || !f.progOK(m.Prog) {
		return false
	}
	if len(f.expr) == 0 {
		return true
	}
	if f.groups == nil {
		f.groups = ldCompile(f.expr)
	}
	// the text and the fields are made when a term needs them, once per message
	var text string
	var kv []ldKV
	haveText, haveKV := false, false
	for _, g := range f.groups {
		all := true
		for i := range g {
			t := &g[i]
			var found bool
			if t.field != nil {
				if t.field.lit && !haveKV {
					// the value must be in the text (or the structured data) to be a field: look for it there first, which is far
					// cheaper than reading the fields (unless the text has \u escapes, which JSON may use for any character)
					if !haveText {
						text, haveText = f.matchText(m), true
					}
					if !findTerm(text, t.field.val) && !strings.Contains(text, "\\u") && !ldFieldsMadeUp(text) && (m.SD == "" || !findTerm(strings.ToLower(m.SD), t.field.val)) {
						if t.neg {
							continue
						}
						all = false
						break
					}
				}
				if !haveKV {
					kv, haveKV = ldFields(m), true
				}
				found = t.field.matches(kv)
			} else {
				if !haveText {
					text, haveText = f.matchText(m), true
				}
				found = findTerm(text, t.text)
			}
			if found == t.neg {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// matchText is the lower-cased text a search looks in: host, program, message and the sender's names.  It is built in a buffer the
// filter keeps, not in new strings for every message scanned, so it lives only until the next message.
func (f *ldFilter) matchText(m *LdMsg) string {
	buf := appendLower(f.buf[:0], m.Host)
	buf = append(buf, ' ')
	buf = appendLower(buf, m.Prog)
	buf = append(buf, ' ')
	buf = appendLower(buf, m.Msg)
	if w := f.srcWords[m.Src]; w != "" {
		buf = append(append(buf, ' '), w...)
	}
	f.buf = buf
	return unsafe.String(unsafe.SliceData(buf), len(buf))
}

// hourOK says whether an hour could hold a message the filter wants (false: skip the hour).
func (f *ldFilter) hourOK(h *ldHourMeta) bool {
	if h == nil {
		return true
	}
	if h.Count == 0 {
		return false
	}
	if f.minSev >= 0 {
		mask := uint8(1<<uint(f.minSev+1)) - 1 // this severe or worse
		switch {
		case f.sevExact:
			mask = 1 << uint(f.minSev)
		case f.sevLess:
			mask = ^(uint8(1<<uint(f.minSev)) - 1)
		}
		if h.Sev&mask == 0 {
			return false
		}
	}
	if !h.Full && h.Hosts != nil {
		if f.host != "" && !strings.Contains(f.host, "*") && len(f.hostSrc) == 0 {
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
	cache   *ldCache              // closed hours, parsed, under a memory cap
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
	wixMake  sync.Mutex
	dirty    bool

	capPct, retDays int
	dirBytes        uint64
	capBytes        uint64
	removed         uint64
	now             func() time.Time
	volSize         func(string) (uint64, error)
}

func newLdStore(dir, node string) *ldStore {
	return &ldStore{cache: newLdCache(), sums: map[int64]ldSumEntry{}, wixes: map[int64]ldWixEntry{}, hits: map[ldHitKey]*ldHits{}, dir: dir, node: node, meta: map[int64]*ldHourMeta{}, now: time.Now, volSize: ldVolumeSize}
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
	n = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(n, ".wix"), ".sum"), ".idx"), ".lz"), ".jsonl")
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
// readHour parses a whole hour (nil when it has no file).
func (s *ldStore) readHour(h int64) []LdMsg {
	var msgs []LdMsg
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
	if !s.streamFile(h, func(m *LdMsg) bool {
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
	return msgs
}

// scanFile calls f for each message of an hour until it returns false.  A closed hour comes from the cache, and goes into it when
// it fits under the memory cap; the hour being written, and one that does not fit, is read from its file.
func (s *ldStore) scanFile(h int64, f func(*LdMsg) bool) {
	if s.closedHour(h) {
		msgs, ok := s.cache.get(h)
		if !ok {
			if msgs = s.readHour(h); msgs == nil {
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
func (s *ldStore) streamFile(h int64, f func(*LdMsg) bool) bool { return s.streamFileRaw(h, nil, f) }

// ldRawSev is the severity of a stored line read straight from its bytes, without parsing it (-1: not found).  The fields are
// written in LdMsg's order, so `,"s":` is first seen at the severity key: the fields before it hold no unescaped quote.
func ldRawSev(line []byte) int {
	i := bytes.Index(line, []byte(`,"s":`))
	if i < 0 || i+5 >= len(line) {
		return -1
	}
	c := line[i+5]
	if c < '0' || c > '7' || (i+6 < len(line) && line[i+6] >= '0' && line[i+6] <= '9') {
		return -1
	}
	return int(c - '0')
}

// rawOK says from a stored line's bytes whether it can match (false: skip it without parsing); a line it cannot read is parsed.
func (f *ldFilter) rawOK(line []byte) bool {
	if f.minSev >= 0 {
		if sev := ldRawSev(line); sev >= 0 && !f.sevOK(sev) {
			return false
		}
	}
	return f.rawWordsOK(line)
}

// rawUseful says whether rawOK can rule any line out (a severity, or words it can look for in the bytes).
func (f *ldFilter) rawUseful() bool { return f.minSev >= 0 || f.rawWords() }

// rawWords works out raw: for each OR group, the pieces of its words and field values (split at spaces and *) that a matching
// message's host, program, text or structured data must hold, and so the stored line too.  Only pieces of plain ASCII that JSON
// writes as they are (no " \ < > & or control characters) are used, and none that may be in a sender's name (the text a word is
// looked for in includes it, the line does not).  When a group has no such piece any line can match, and none is ruled out.
func (f *ldFilter) rawWords() bool {
	if f.rawUse != 0 {
		return f.rawUse > 0
	}
	f.rawUse = -1
	if len(f.groups) == 0 {
		return false
	}
	raw := make([][]string, len(f.groups))
	for gi, g := range f.groups {
		for _, t := range g {
			if t.neg {
				continue
			}
			v, field := t.text, false
			if t.field != nil {
				if t.field.op != "=" || !t.field.lit {
					continue
				}
				v, field = t.field.val, true
			}
			for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == '*' }) {
				if !ldRawSafe(p) || (!field && len(f.srcWords) > 0 && f.mayBeSenderName(p)) {
					continue
				}
				raw[gi] = append(raw[gi], p)
				f.rawField = f.rawField || field
			}
		}
		if len(raw[gi]) == 0 {
			return false
		}
	}
	f.raw, f.rawUse = raw, 1
	return true
}

// ldRawSafe: is p written in a JSON string as it is?
func ldRawSafe(p string) bool {
	for i := 0; i < len(p); i++ {
		if c := p[i]; c <= ' ' || c >= 0x7f || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
			return false
		}
	}
	return p != ""
}

// rawWordsOK: can the stored line match the words, as far as its bytes tell?  A line with an escape (\u) or a byte outside ASCII
// (whose lower case may differ) is always parsed.
func (f *ldFilter) rawWordsOK(line []byte) bool {
	if !f.rawWords() {
		return true
	}
	if bytes.Contains(line, []byte(`\u`)) {
		return true
	}
	low := f.lower[:0]
	for _, c := range line {
		if c >= 0x80 {
			return true
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		low = append(low, c)
	}
	f.lower = low
	if f.rawField && ldFieldsMadeUp(unsafe.String(unsafe.SliceData(low), len(low))) {
		return true
	}
	for _, g := range f.raw {
		all := true
		for _, p := range g {
			if !bytes.Contains(low, unsafe.Slice(unsafe.StringData(p), len(p))) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// scanHour is scanFile for a search or a count: an hour that is not in the cache, when the filter can rule lines out from their
// bytes (a severity, as for the Errors and Warnings tiles, or words: rawOK), is read straight from its file and only the lines that can match are parsed;
// it is not put in the cache then, so a long range does not push the newest hours out.
func (s *ldStore) scanHour(h int64, flt *ldFilter, f func(*LdMsg) bool) {
	if !flt.rawUseful() {
		s.scanFile(h, f)
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
	s.streamFileRaw(h, flt.rawOK, f)
}

// streamFileRaw is streamFile with a test on each line's bytes first (nil: every line is parsed).  f's message is the same
// variable for every line: f must copy what it keeps.
func (s *ldStore) streamFileRaw(h int64, pre func([]byte) bool, f func(*LdMsg) bool) bool {
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
	var m LdMsg // one for every line: f gets it only until it returns
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
				m = LdMsg{}
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

// ── search ──────────────────────────────────────────────────────────────────

// LdQuery is a search.  From and To are milliseconds (To 0: now); Limit counts messages.
type LdQuery struct {
	From   int64  `json:"from"`
	To     int64  `json:"to"`
	Q      string `json:"q,omitempty"`
	Host   string `json:"host,omitempty"`
	Prog   string `json:"prog,omitempty"`
	MinSev int    `json:"min_severity"` // -1 any
	// SevExact: only messages of exactly MinSev (the web page's Warnings tile), not that severity or worse.
	SevExact bool `json:"sev_exact,omitempty"`
	// SevLess: MinSev or less severe (info and debug for info), not that severity or worse (drop rules, ld_drop.go).
	SevLess bool   `json:"sev_less,omitempty"`
	Src     string `json:"src,omitempty"`
	// SrcName picks senders by name (the configured name, the reverse name or the host name; * wildcards) instead of by address: every node
	// turns it into the addresses it knows (srcSet) when it runs the query.
	SrcName string `json:"src_name,omitempty"`
	srcSet  map[string]bool
	// hostSrc is, for a host filter, the addresses of the senders whose name matches it: the Logs page names a message by its sender, so
	// host=fw1-mgmt finds that sender's messages even when the message calls the host something else.  Set by the node that runs the query.
	hostSrc map[string]bool
	// srcWords: for a search with words, each sender's names (senderWords): a word also matches the name of the message's sender.
	srcWords map[string]string
	Limit    int  `json:"limit"`
	Oldest   bool `json:"oldest_first,omitempty"` // the oldest matches (default: the newest)
	// Field: stats count the values of this field (ld_fields.go, or host, program, sender, severity) of the matching messages, in ByField.
	Field string `json:"field,omitempty"`
	// Measure and MField: per value of Field, stats also count the different values of MField ("distinct") or add up its numbers
	// ("sum"), in FieldAgg (ld_agg.go).
	Measure string `json:"measure,omitempty"`
	MField  string `json:"measure_field,omitempty"`
	// Values: with Field and a step, stats count these values of Field per bucket (ValSer): the values card's lines over time.
	Values []string `json:"values,omitempty"`
	// Fields: each message found is listed with its fields.
	Fields bool `json:"fields,omitempty"`
}

const (
	ldMaxLimit     = 5000
	ldDefaultLimit = 200
	ldListDefault  = 1000 // the Logs page: messages listed unless Configure says another number
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
	if q.SevExact && q.SevLess {
		return errors.New("a severity is exact, or this or less severe, not both")
	}
	if q.To != 0 && q.To < q.From {
		return errors.New("to is before from")
	}
	if len(q.Q) > 2000 || len(q.Host) > 255 || len(q.Prog) > 255 {
		return errors.New("query too long")
	}
	if q.Field != "" {
		q.Field = strings.ToLower(q.Field)
		if ft, ok := ldParseFieldTerm(q.Field + "=x"); !ok || ft.key != q.Field {
			return errors.New("field: letters, digits, _ . and - (starting with a letter or _)")
		}
	}
	return nil
}

func (q *LdQuery) filter() *ldFilter {
	expr := parseMatch(q.Q)
	return &ldFilter{expr: expr, groups: ldCompile(expr), host: strings.ToLower(strings.TrimSpace(q.Host)), prog: strings.ToLower(strings.TrimSpace(q.Prog)), minSev: q.MinSev, sevExact: q.SevExact, sevLess: q.SevLess, src: q.Src, srcSet: q.srcSet, hostSrc: q.hostSrc, srcWords: q.srcWords}
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
	var todo []int64 // the hours that can hold a match, in the order they are wanted
	for _, h := range hours {
		if f.hourOK(meta[h]) && s.wixOK(h, meta[h], f) {
			todo = append(todo, h)
		}
	}
	// newest first, the hours are read a few at a time (1, 2, 4... up to ldScanWorkers): a common word is found in the first
	// hour, a rare one needs them all
	type hourRes struct {
		hm      []LdMsg
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
					r.hm, r.scanned, r.partial = s.searchHour(ctx, q.filter(), q, to, h, meta[h].Count, 0) // a filter is one goroutine's
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
func (s *ldStore) searchHour(ctx context.Context, f *ldFilter, q LdQuery, to, h int64, count uint64, stop int) (hm []LdMsg, scanned uint64, partial bool) {
	keep := f.hitsUseful() && s.closedHour(h)
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
	var all []LdMsg // with keep: every match of the hour, whatever its time
	whole := true
	s.scanHour(h, f, func(m *LdMsg) bool {
		scanned++
		if scanned&0x3fff == 0 && ctx.Err() != nil {
			partial, whole = true, false
			return false
		}
		in := m.T >= q.From && m.T <= to
		if (!in && !keep) || !f.match(m) {
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

func (s *ldStore) Stats(ctx context.Context, q LdQuery, step int64) LdStatsAnswer {
	f := q.filter()
	r := LdStatsAnswer{Node: s.node, ByHost: map[string]uint64{}, ByProg: map[string]uint64{}, Step: step, Series: map[int64]uint64{}, BySrc: map[string]uint64{}}
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
	// count adds n messages like m, at bucket b
	count := func(m *LdMsg, n uint64, b int64) {
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
			fs := ldFields(m)
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
				if want[v] {
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
	// whole closed hours that a filter with words has not counted before: counted several at a time first, then taken like the
	// rest from what is kept (ld_hits.go)
	keepHour := func(h int64) bool {
		return f.hitsUseful() && s.closedHour(h) && q.Field == "" && (step == 0 || step%3600 == 0) && h*3600*1000 >= q.From && (h+1)*3600*1000-1 <= to
	}
	var need []int64
	for _, h := range hours {
		if keepHour(h) && f.hourOK(meta[h]) && s.wixOK(h, meta[h], f) && s.hitsGet(f, h, true, meta[h].Count) == nil {
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
		if !f.hourOK(meta[h]) || !s.wixOK(h, meta[h], f) {
			continue
		}
		// a whole closed hour, and nothing asked that a rollup cannot answer: add up its rollup instead of reading it
		if s.closedHour(h) && q.Field == "" && sumHourOK(f, step, h, q.From, to) {
			if sum := s.hourSum(h, meta[h].Count); sum != nil {
				var b int64
				if step > 0 {
					b = h * 3600 / step * step
				}
				var m LdMsg
				for i := range sum.rows {
					x := &sum.rows[i]
					m.Host, m.Prog, m.Src, m.Sev = sum.names[x.host], sum.names[x.prog], sum.names[x.src], x.sev
					if f.match(&m) {
						count(&m, x.n, b)
					}
				}
				r.Scanned += sum.count
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
				var m LdMsg
				for i := range x.sum.rows {
					row := &x.sum.rows[i]
					m.Host, m.Prog, m.Src, m.Sev = x.sum.names[row.host], x.sum.names[row.prog], x.sum.names[row.src], row.sev
					count(&m, row.n, b)
				}
				continue
			}
		}
		var mk ldSumMaker
		s.scanHour(h, f, func(m *LdMsg) bool {
			r.Scanned++
			if r.Scanned&0x3fff == 0 && ctx.Err() != nil {
				r.Partial = true
				return false
			}
			if m.T < q.From || m.T > to || !f.match(m) {
				return true
			}
			var b int64
			if step > 0 {
				b = (m.T / 1000) / step * step
			}
			count(m, 1, b)
			if keep {
				mk.add(m)
			}
			return true
		})
		if r.Partial {
			break
		}
		if keep {
			if sum := mk.done(meta[h].Count); sum != nil {
				s.hitsPut(f, h, true, &ldHits{count: meta[h].Count, sum: sum})
			}
		}
	}
	if r.FieldAgg != nil {
		ldAggTrim(r.FieldAgg, q.Measure)
	}
	return r
}

// countHours counts the matches of whole closed hours by row, several hours at a time, and keeps the counts (ld_hits.go); it
// gives how many messages it read.
func (s *ldStore) countHours(ctx context.Context, q LdQuery, hours []int64, meta map[int64]*ldHourMeta) (scanned uint64, partial bool) {
	var mu sync.Mutex
	next := 0
	var wg sync.WaitGroup
	for w := 0; w < min(ldScanWorkers(), len(hours)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := q.filter() // a filter is one goroutine's
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
				s.scanHour(h, f, func(m *LdMsg) bool {
					n++
					if n&0x3fff == 0 && ctx.Err() != nil {
						stopped = true
						return false
					}
					if f.match(m) {
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
