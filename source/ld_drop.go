package main

// Drop rules (settings.logs.drop_rules): messages that match a rule are not kept.  A rule is the Logs page's filter (words, fields,
// host=, program=, senders) and a severity; a message that matches any enabled rule is dropped after it is parsed and before it is
// written, so it is not stored, searched, counted, shipped or seen by the pattern alerts.  Its sender is still marked as heard
// (Monitor ▸ Senders would otherwise show a sender whose messages are all dropped as silent).  Each node counts what each rule
// dropped since it started (Configure ▸ Senders ▸ Drop rules shows the counts of every node).
//
// The rules are compiled in the background every 3 seconds when they change, and every 30 seconds for sender names (which need
// DNS); matching a message never waits for that.  A compiled filter keeps scratch buffers, so each goroutine that handles messages
// takes its own copy from a pool.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	ldDropMax         = 100 // rules
	ldDropFromVersion = 40  // the first version that knows settings.logs.drop_rules
)

type LogDropCfg struct {
	Name     string   `json:"name"`
	Query    string   `json:"query,omitempty"` // the Logs page's search language: words, fields, and, or, not
	Host     string   `json:"host,omitempty"`
	Program  string   `json:"program,omitempty"`
	Senders  []string `json:"senders,omitempty"`   // names (* wildcards), addresses or networks
	Severity *int     `json:"severity,omitempty"`  // 0 (emergency) to 7 (debug)
	SevMatch string   `json:"sev_match,omitempty"` // with severity: "" or "worse" (this or more severe), "exact", "less" (this or less severe)
	Disabled bool     `json:"disabled,omitempty"`
}

func (d *LogDropCfg) Validate() error {
	n := strings.TrimSpace(d.Name)
	if n == "" || len(n) > 64 {
		return fmt.Errorf("a drop rule needs a name of 1 to 64 characters")
	}
	if len(d.Query) > 2000 || len(d.Host) > 255 || len(d.Program) > 255 {
		return fmt.Errorf("drop rule %s: filter too long", n)
	}
	if strings.TrimSpace(d.Query) == "" && strings.TrimSpace(d.Host) == "" && strings.TrimSpace(d.Program) == "" && len(d.Senders) == 0 && d.Severity == nil {
		return fmt.Errorf("drop rule %s: needs words, a host, a program, a sender or a severity (it would drop everything)", n)
	}
	if q := strings.TrimSpace(d.Query); q != "" {
		if err := (&LdQuery{Q: q, MinSev: -1}).Validate(); err != nil {
			return fmt.Errorf("drop rule %s: %v", n, err)
		}
	}
	if d.Severity != nil && (*d.Severity < 0 || *d.Severity > 7) {
		return fmt.Errorf("drop rule %s: severity must be 0 to 7", n)
	}
	switch d.SevMatch {
	case "", "worse", "exact", "less":
	default:
		return fmt.Errorf("drop rule %s: sev_match %q is not worse, exact or less", n, d.SevMatch)
	}
	if d.SevMatch != "" && d.Severity == nil {
		return fmt.Errorf("drop rule %s: sev_match needs a severity", n)
	}
	if len(d.Senders) > 200 {
		return fmt.Errorf("drop rule %s: at most 200 senders", n)
	}
	for _, s := range d.Senders {
		if err := ldCheckSenderWord(s); err != nil {
			return fmt.Errorf("drop rule %s: %v", n, err)
		}
	}
	return nil
}

func validateDropRules(ds []LogDropCfg) error {
	if len(ds) > ldDropMax {
		return fmt.Errorf("logs: at most %d drop rules", ldDropMax)
	}
	seen := map[string]bool{}
	for i := range ds {
		if err := ds[i].Validate(); err != nil {
			return fmt.Errorf("logs: %v", err)
		}
		k := strings.ToLower(strings.TrimSpace(ds[i].Name))
		if seen[k] {
			return fmt.Errorf("logs: drop rule name %q is used twice", ds[i].Name)
		}
		seen[k] = true
	}
	return nil
}

func cloneDrops(ds []LogDropCfg) []LogDropCfg {
	if ds == nil {
		return nil
	}
	out := make([]LogDropCfg, len(ds))
	for i, d := range ds {
		out[i] = d
		out[i].Senders = append([]string(nil), d.Senders...)
		if d.Severity != nil {
			v := *d.Severity
			out[i].Severity = &v
		}
	}
	return out
}

// logDropNeedsNewNodes refuses a change to the drop rules while a node of the cluster runs a version that does not know them: it
// would keep what the rules drop everywhere else, without a word.
func logDropNeedsNewNodes(cur, dc *DaemonConfig, peers []PeerView) error {
	var a, b []LogDropCfg
	if cur != nil && cur.Settings.Logs != nil {
		a = cur.Settings.Logs.DropRules
	}
	if dc != nil && dc.Settings.Logs != nil {
		b = dc.Settings.Logs.DropRules
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if len(b) == 0 || bytes.Equal(ja, jb) {
		return nil
	}
	for _, p := range peers {
		if p.Self || p.Version == "" {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(p.Version), "v")); err == nil && n < ldDropFromVersion {
			return fmt.Errorf("node %s runs version %d, which does not know drop rules: update it to version %d or later (Operate ▸ Upgrade), then add the rules", firstNonEmpty(p.Hostname, p.Addr), n, ldDropFromVersion)
		}
	}
	return nil
}

// ── matching ─────────────────────────────────────────────────────────────────

// ldDropRule is one enabled rule as this node runs it.
type ldDropRule struct {
	name  string
	sel   *ldShipFilter // the filter (and its senders); sel.f is never used for matching: each goroutine has its own copy
	count *atomic.Uint64
}

// ldDropSet is the rules compiled at one moment.
type ldDropSet struct {
	sig   string // the rules as configured: compiled again when they change
	at    time.Time
	heard map[string]bool // the senders known when compiled: names of the others are looked up as their messages come (dropFresh)
	n     int             // how many senders were known: a new one compiles the rules again
	rules []ldDropRule
	pool  sync.Pool // []*ldFilter, one per rule
}

// ldDropStat is what a node says about each rule.
type LdDropCount struct {
	Name    string `json:"name"`
	Dropped uint64 `json:"dropped"`        // since this node started
	Last    int64  `json:"last,omitempty"` // ms of the last message it dropped
}

// ldDropCounter is a rule's count on this node; kept by name, so a change to the rules does not start it again.
type ldDropCounter struct {
	n    atomic.Uint64
	last atomic.Int64
}

func (s *ldService) dropCounter(name string) *ldDropCounter {
	v, _ := s.dropCounts.LoadOrStore(strings.ToLower(name), &ldDropCounter{})
	return v.(*ldDropCounter)
}

// reconcileDrops compiles the rules again when they changed, or every 30 seconds (sender names).
func (s *ldService) reconcileDrops() {
	cfg := s.config()
	sigB, _ := json.Marshal(cfg.DropRules)
	sig := string(sigB)
	s.mu.Lock()
	heard := make(map[string]bool, len(s.senders))
	for a := range s.senders {
		heard[a.String()] = true
	}
	s.mu.Unlock()
	cur := s.drops.Load()
	if cur != nil && cur.sig == sig && time.Since(cur.at) < 30*time.Second && cur.n == len(heard) {
		return
	}
	set := &ldDropSet{sig: sig, at: time.Now(), heard: heard, n: len(heard)}
	nc := &ldNames{} // looked up once for all the rules
	for i := range cfg.DropRules {
		d := &cfg.DropRules[i]
		if d.Disabled {
			continue
		}
		r := ldDropRule{name: strings.TrimSpace(d.Name)}
		r.sel = s.selection(nc, d.Query, d.Host, d.Program, d.Severity, d.SevMatch == "exact", d.SevMatch == "less", d.Senders)
		r.count = &s.dropCounter(r.name).n
		set.rules = append(set.rules, r)
	}
	set.pool.New = func() any {
		fs := make([]*ldFilter, len(set.rules))
		for i := range set.rules {
			fs[i] = set.rules[i].sel.q.filter()
		}
		return fs
	}
	s.drops.Store(set)
}

// dropRule is the name of the first enabled rule that drops m ("": keep it), and counts it.
func (s *ldService) dropRule(m *LdMsg) string {
	set := s.drops.Load()
	if set == nil || len(set.rules) == 0 {
		return ""
	}
	fs := set.pool.Get().([]*ldFilter)
	defer set.pool.Put(fs)
	fresh := "" // a sender not known when the rules were compiled: its name in Configure ▸ Senders, looked up now
	if !set.heard[m.Src] {
		if a, err := netip.ParseAddr(m.Src); err == nil {
			fresh = strings.ToLower(s.senderName(a))
		}
	}
	for i := range set.rules {
		r := &set.rules[i]
		if !r.matchMsg(fs[i], m, fresh) {
			continue
		}
		r.count.Add(1)
		c := s.dropCounter(r.name)
		c.last.Store(m.T)
		ldStats.ruleDropped.Add(1)
		return r.name
	}
	return ""
}

// matchMsg: does the rule match m?  fresh is the name of a sender the compiled filter does not know yet: it is used for the rule's
// senders, its host= and its words, as the filter would once compiled again.
func (r *ldDropRule) matchMsg(f *ldFilter, m *LdMsg, fresh string) bool {
	if fresh == "" {
		return r.sel.matchWith(f, m)
	}
	sel := r.sel
	if sel.hasSrc {
		named := false
		for _, p := range sel.names {
			if ldGlob(p, fresh) {
				named = true
				break
			}
		}
		if named {
			sel = &ldShipFilter{} // its senders are this one: only the rest of the filter is left
		}
	}
	words, hostSrc := f.srcWords, f.hostSrc
	if len(f.expr) > 0 {
		f.srcWords = map[string]string{m.Src: fresh}
	}
	if f.host != "" && ldGlob(f.host, fresh) {
		f.hostSrc = map[string]bool{m.Src: true}
	}
	ok := sel.matchWith(f, m)
	f.srcWords, f.hostSrc = words, hostSrc
	return ok
}

// dropCounts is each configured rule's count on this node, in the order of the rules.
func (s *ldService) dropCountsNow() []LdDropCount {
	cfg := s.config()
	out := make([]LdDropCount, 0, len(cfg.DropRules))
	for _, d := range cfg.DropRules {
		c := s.dropCounter(strings.TrimSpace(d.Name))
		out = append(out, LdDropCount{Name: strings.TrimSpace(d.Name), Dropped: c.n.Load(), Last: c.last.Load()})
	}
	return out
}
