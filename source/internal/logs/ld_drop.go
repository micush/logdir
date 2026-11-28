package logs

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

	"logdir/internal/core"
)

const ldDropFromVersion = 40

// logDropNeedsNewNodes refuses a change to the drop rules while a node of the cluster runs a version that does not know them: it
// would keep what the rules drop everywhere else, without a word.
func LogDropNeedsNewNodes(cur, dc *core.DaemonConfig, peers []core.PeerView) error {
	var a, b []core.LogDropCfg
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
			return fmt.Errorf("node %s runs version %d, which does not know drop rules: update it to version %d or later (Operate ▸ Upgrade), then add the rules", core.FirstNonEmpty(p.Hostname, p.Addr), n, ldDropFromVersion)
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

func (s *LdService) dropCounter(name string) *ldDropCounter {
	v, _ := s.dropCounts.LoadOrStore(strings.ToLower(name), &ldDropCounter{})
	return v.(*ldDropCounter)
}

// reconcileDrops compiles the rules again when they changed, or every 30 seconds (sender names).
func (s *LdService) reconcileDrops() {
	cfg := s.Config()
	sigB, _ := json.Marshal(cfg.DropRules)
	sig := string(sigB)
	s.mu.Lock()
	heard := make(map[string]bool, len(s.BySender))
	for a := range s.BySender {
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
		fs := make([]*core.LdFilter, len(set.rules))
		for i := range set.rules {
			fs[i] = set.rules[i].sel.q.Filter()
		}
		return fs
	}
	s.drops.Store(set)
}

// dropRule is the name of the first enabled rule that drops m ("": keep it), and counts it.
func (s *LdService) dropRule(m *core.LdMsg) string {
	set := s.drops.Load()
	if set == nil || len(set.rules) == 0 {
		return ""
	}
	fs := set.pool.Get().([]*core.LdFilter)
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
func (r *ldDropRule) matchMsg(f *core.LdFilter, m *core.LdMsg, fresh string) bool {
	if fresh == "" {
		return r.sel.matchWith(f, m)
	}
	sel := r.sel
	if sel.hasSrc {
		named := false
		for _, p := range sel.names {
			if core.LdGlob(p, fresh) {
				named = true
				break
			}
		}
		if named {
			sel = &ldShipFilter{} // its senders are this one: only the rest of the filter is left
		}
	}
	words, hostSrc := f.SrcWords, f.HostSrc
	if len(f.Expr) > 0 {
		f.SrcWords = map[string]string{m.Src: fresh}
	}
	if f.Host != "" && core.LdGlob(f.Host, fresh) {
		f.HostSrc = map[string]bool{m.Src: true}
	}
	ok := sel.matchWith(f, m)
	f.SrcWords, f.HostSrc = words, hostSrc
	return ok
}

// dropCounts is each configured rule's count on this node, in the order of the rules.
func (s *LdService) dropCountsNow() []LdDropCount {
	cfg := s.Config()
	out := make([]LdDropCount, 0, len(cfg.DropRules))
	for _, d := range cfg.DropRules {
		c := s.dropCounter(strings.TrimSpace(d.Name))
		out = append(out, LdDropCount{Name: strings.TrimSpace(d.Name), Dropped: c.n.Load(), Last: c.last.Load()})
	}
	return out
}
