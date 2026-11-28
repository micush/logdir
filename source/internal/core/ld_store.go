package core

import (
	"bytes"
	"errors"
	"strings"
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

const ldMetaNames = 2000

type LdHourMeta struct {
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

func (h *LdHourMeta) Add(m *LdMsg) {
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
type LdFilter struct {
	Expr     matchExpr `json:"-"`
	Groups   []LdGroup `json:"-"` // expr compiled: text and field terms (ld_expr.go)
	Host     string    `json:"-"` // lower case; an exact host, or a leading/trailing * wildcard
	prog     string
	minSev   int  // -1: any
	sevExact bool // only this severity, not this severe or worse
	sevLess  bool // this severity or less severe
	src      string
	SrcWords map[string]string `json:"-"` // each sender's names: a word of the search matches them too
	buf      []byte            // scratch for the lower-cased text of the message being matched (a filter is used by one goroutine)
	HostSrc  map[string]bool   `json:"-"` // senders whose name matches the host filter: their messages match it too
	srcSet   map[string]bool   // senders picked by name: nil is no such filter, an empty set matches nothing
	wixUse   int8              // the word index can rule hours out: 0 not worked out yet, 1 yes, -1 no
	raw      [][]string        // per OR group, pieces a stored line must hold (lower case) to match it (rawWordsOK)
	rawUse   int8              // the words can rule lines out from their bytes: 0 not worked out yet, 1 yes, -1 no
	rawField bool              // some of raw are field values (which a few formats make up: ldFieldsMadeUp)
	lower    []byte            // scratch: the lower-cased line
	hkey     string            // key, once worked out (ld_hits.go)
}

// sevOK: does a message of this severity pass the filter's severity?
func (f *LdFilter) sevOK(sev int) bool {
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

func (f *LdFilter) hostOK(h string) bool { return f.Host == "" || LdGlob(f.Host, strings.ToLower(h)) }
func (f *LdFilter) progOK(p string) bool { return f.prog == "" || LdGlob(f.prog, strings.ToLower(p)) }

// ldGlob matches s against a pattern with * in it (anywhere), case already folded.
func LdGlob(p, s string) bool {
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
func AppendLower(dst []byte, s string) []byte {
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

func (f *LdFilter) Match(m *LdMsg) bool {
	if !f.sevOK(int(m.Sev)) {
		return false
	}
	if f.src != "" && m.Src != f.src {
		return false
	}
	if f.srcSet != nil && !f.srcSet[m.Src] {
		return false
	}
	if !(f.hostOK(m.Host) || f.HostSrc[m.Src]) || !f.progOK(m.Prog) {
		return false
	}
	if len(f.Expr) == 0 {
		return true
	}
	if f.Groups == nil {
		f.Groups = ldCompile(f.Expr)
	}
	// the text and the fields are made when a term needs them, once per message
	var text string
	var kv []LdKV
	haveText, haveKV := false, false
	for _, g := range f.Groups {
		all := true
		for i := range g {
			t := &g[i]
			var found bool
			if t.Field != nil {
				if t.Field.lit && !haveKV {
					// the value must be in the text (or the structured data) to be a field: look for it there first, which is far
					// cheaper than reading the fields (unless the text has \u escapes, which JSON may use for any character)
					if !haveText {
						text, haveText = f.matchText(m), true
					}
					if !findTerm(text, t.Field.val) && !strings.Contains(text, "\\u") && !ldFieldsMadeUp(text) && (m.SD == "" || !findTerm(strings.ToLower(m.SD), t.Field.val)) {
						if t.neg {
							continue
						}
						all = false
						break
					}
				}
				if !haveKV {
					kv, haveKV = LdFields(m), true
				}
				found = t.Field.matches(kv)
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
func (f *LdFilter) matchText(m *LdMsg) string {
	buf := AppendLower(f.buf[:0], m.Host)
	buf = append(buf, ' ')
	buf = AppendLower(buf, m.Prog)
	buf = append(buf, ' ')
	buf = AppendLower(buf, m.Msg)
	if w := f.SrcWords[m.Src]; w != "" {
		buf = append(append(buf, ' '), w...)
	}
	f.buf = buf
	return unsafe.String(unsafe.SliceData(buf), len(buf))
}

// hourOK says whether an hour could hold a message the filter wants (false: skip the hour).
func (f *LdFilter) HourOK(h *LdHourMeta) bool {
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
		if f.Host != "" && !strings.Contains(f.Host, "*") && len(f.HostSrc) == 0 {
			if _, ok := h.Hosts[f.Host]; !ok {
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
func (f *LdFilter) RawOK(line []byte) bool {
	if f.minSev >= 0 {
		if sev := ldRawSev(line); sev >= 0 && !f.sevOK(sev) {
			return false
		}
	}
	return f.rawWordsOK(line)
}

// rawUseful says whether rawOK can rule any line out (a severity, or words it can look for in the bytes).
func (f *LdFilter) RawUseful() bool { return f.minSev >= 0 || f.rawWords() }

// rawWords works out raw: for each OR group, the pieces of its words and field values (split at spaces and *) that a matching
// message's host, program, text or structured data must hold, and so the stored line too.  Only pieces of plain ASCII that JSON
// writes as they are (no " \ < > & or control characters) are used, and none that may be in a sender's name (the text a word is
// looked for in includes it, the line does not).  When a group has no such piece any line can match, and none is ruled out.
func (f *LdFilter) rawWords() bool {
	if f.rawUse != 0 {
		return f.rawUse > 0
	}
	f.rawUse = -1
	if len(f.Groups) == 0 {
		return false
	}
	raw := make([][]string, len(f.Groups))
	for gi, g := range f.Groups {
		for _, t := range g {
			if t.neg {
				continue
			}
			v, field := t.text, false
			if t.Field != nil {
				if t.Field.op != "=" || !t.Field.lit {
					continue
				}
				v, field = t.Field.val, true
			}
			for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == '*' }) {
				if !ldRawSafe(p) || (!field && len(f.SrcWords) > 0 && f.mayBeSenderName(p)) {
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
func (f *LdFilter) rawWordsOK(line []byte) bool {
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
	SrcName string          `json:"src_name,omitempty"`
	SrcSet  map[string]bool `json:"-"`
	// hostSrc is, for a host filter, the addresses of the senders whose name matches it: the Logs page names a message by its sender, so
	// host=fw1-mgmt finds that sender's messages even when the message calls the host something else.  Set by the node that runs the query.
	HostSrc map[string]bool `json:"-"`
	// srcWords: for a search with words, each sender's names (senderWords): a word also matches the name of the message's sender.
	SrcWords map[string]string `json:"-"`
	Limit    int               `json:"limit"`
	Oldest   bool              `json:"oldest_first,omitempty"` // the oldest matches (default: the newest)
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

const LdMaxLimit = 5000

const ldDefaultLimit = 200

func (q *LdQuery) Validate() error {
	if q.Limit <= 0 {
		q.Limit = ldDefaultLimit
	}
	if q.Limit > LdMaxLimit {
		q.Limit = LdMaxLimit
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

func (q *LdQuery) Filter() *LdFilter {
	expr := parseMatch(q.Q)
	return &LdFilter{Expr: expr, Groups: ldCompile(expr), Host: strings.ToLower(strings.TrimSpace(q.Host)), prog: strings.ToLower(strings.TrimSpace(q.Prog)), minSev: q.MinSev, sevExact: q.SevExact, sevLess: q.SevLess, src: q.Src, srcSet: q.SrcSet, HostSrc: q.HostSrc, SrcWords: q.SrcWords}
}
