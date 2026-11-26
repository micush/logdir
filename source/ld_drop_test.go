package main

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

func sevp(n int) *int { return &n }

func TestDropValidate(t *testing.T) {
	ok := []LogDropCfg{
		{Name: "keepalives", Query: "keepalive"},
		{Name: "dns", Query: "action=accept and dst_port=53"},
		{Name: "chatty switches", Host: "sw*", Severity: sevp(6), SevMatch: "less"},
		{Name: "ntp", Program: "ntpd", Senders: []string{"core*", "10.9.0.0/16", "10.1.1.1"}},
		{Name: "debug", Severity: sevp(7), SevMatch: "exact"},
		{Name: "not errors", Query: "not error", Disabled: true},
	}
	if err := validateDropRules(ok); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		rules []LogDropCfg
		want  string
	}{
		{[]LogDropCfg{{Name: "all"}}, "would drop everything"},
		{[]LogDropCfg{{Name: " "}}, "needs a name"},
		{[]LogDropCfg{{Name: "a", Query: "x"}, {Name: "A", Query: "y"}}, "used twice"},
		{[]LogDropCfg{{Name: "a", Severity: sevp(8)}}, "0 to 7"},
		{[]LogDropCfg{{Name: "a", Query: "x", SevMatch: "less"}}, "needs a severity"},
		{[]LogDropCfg{{Name: "a", Severity: sevp(3), SevMatch: "more"}}, "not worse, exact or less"},
		{[]LogDropCfg{{Name: "a", Senders: []string{"bad name!"}}}, "not a name"},
		{[]LogDropCfg{{Name: "a", Query: strings.Repeat("x", 2001)}}, "too long"},
		{make([]LogDropCfg, ldDropMax+1), "at most"},
	} {
		err := (&LogsConfig{DropRules: c.rules}).Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.rules[0], err, c.want)
		}
	}
	// a copy is a copy
	c := &LogsConfig{DropRules: ok}
	d := c.clone()
	*d.DropRules[2].Severity = 1
	d.DropRules[3].Senders[0] = "x"
	if *c.DropRules[2].Severity != 6 || c.DropRules[3].Senders[0] != "core*" {
		t.Error("clone shares the rules")
	}
}

func dropService(t *testing.T, rules []LogDropCfg) *ldService {
	t.Helper()
	st := newLdStore(t.TempDir(), "n1")
	st.Load()
	s := &ldService{store: st, dns: newLdDNS(), senders: map[netip.Addr]*ldSender{}, refusedAt: map[netip.Addr]time.Time{}, expDNS: map[string][]netip.Prefix{}}
	s.expNets = []ldSenderNet{{p: netip.MustParsePrefix("10.9.0.0/16"), name: "core1"}, {p: netip.MustParsePrefix("10.0.0.0/8"), name: "net10"}}
	s.testCfg.Store(&LogsConfig{Enabled: true, DropRules: rules})
	s.reconcileDrops()
	return s
}

func (s *ldService) send(src, line string) {
	s.handle(netip.MustParseAddr(src), netip.Addr{}, "udp", []byte(line), time.Now())
}

func keptMsgs(t *testing.T, s *ldService) []string {
	t.Helper()
	s.store.Flush()
	a := s.store.Search(context.Background(), LdQuery{From: time.Now().Add(-time.Hour).UnixMilli(), MinSev: -1, Limit: 1000})
	var out []string
	for _, m := range a.Msgs {
		out = append(out, m.Msg)
	}
	return out
}

func TestDropRules(t *testing.T) {
	rules := []LogDropCfg{
		{Name: "keepalives", Query: "keepalive"},
		{Name: "dns", Query: "action=accept and dst_port=53"},
		{Name: "chatty switches", Host: "sw*", Severity: sevp(6), SevMatch: "less"},
		{Name: "core ntp", Program: "ntpd", Senders: []string{"core*"}},
		{Name: "debug", Severity: sevp(7), SevMatch: "exact"},
		{Name: "off", Query: "noise", Disabled: true},
		{Name: "named", Query: "core1 and flap"}, // the sender's name is a word of the search too
	}
	s := dropService(t, rules)
	in := []struct {
		src, line string
		kept      bool
	}{
		{"10.1.1.1", "<14>Nov 26 10:00:00 r1 bgpd: keepalive sent to 192.0.2.1", false},
		{"10.1.1.1", "<14>Nov 26 10:00:00 r1 bgpd: KEEPALIVE timer", false},
		{"10.1.1.1", "<14>Nov 26 10:00:00 r1 bgpd: neighbor down", true},
		{"10.1.1.2", "<14>Nov 26 10:00:00 fw1 fw: action=accept dst_port=53 src=10.1.1.9", false},
		{"10.1.1.2", "<14>Nov 26 10:00:00 fw1 fw: action=accept dst_port=443", true},
		{"10.1.1.2", "<14>Nov 26 10:00:00 fw1 fw: action=deny dst_port=53", true},
		{"10.1.1.3", "<14>Nov 26 10:00:00 sw1 lldp: info from a switch", false},
		{"10.1.1.3", "<15>Nov 26 10:00:00 sw1 lldp: debug from a switch", false},
		{"10.1.1.3", "<12>Nov 26 10:00:00 sw1 lldp: warning from a switch", true},
		{"10.1.1.3", "<14>Nov 26 10:00:00 rtr1 lldp: info from a router", true},
		{"10.9.0.5", "<14>Nov 26 10:00:00 c1 ntpd: synchronized", false},
		{"10.1.1.4", "<14>Nov 26 10:00:00 c1 ntpd: synchronized elsewhere", true},
		{"10.1.1.4", "<15>Nov 26 10:00:00 c1 app: debug anywhere", false},
		{"10.1.1.4", "<14>Nov 26 10:00:00 c1 app: noise but the rule is off", true},
		{"10.9.0.6", "<14>Nov 26 10:00:00 c2 ifmgr: link flap", false},
		{"10.1.1.4", "<14>Nov 26 10:00:00 c2 ifmgr: link flap", true},
	}
	var want []string
	for _, m := range in {
		s.send(m.src, m.line)
		if m.kept {
			want = append(want, m.line[strings.Index(m.line, ": ")+2:])
		}
	}
	got := keptMsgs(t, s)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("kept:\n %q\nwant:\n %q", got, want)
	}
	counts := map[string]uint64{}
	for _, c := range s.Status().DropRules {
		counts[c.Name] = c.Dropped
		if c.Dropped > 0 && c.Last == 0 {
			t.Errorf("%s: no time of the last one", c.Name)
		}
	}
	wantC := map[string]uint64{"keepalives": 2, "dns": 1, "chatty switches": 2, "core ntp": 1, "debug": 1, "off": 0, "named": 1}
	for k, v := range wantC {
		if counts[k] != v {
			t.Errorf("%s dropped %d, want %d (%v)", k, counts[k], v, counts)
		}
	}
	// the sender whose messages were all dropped was still heard
	if s.senders[netip.MustParseAddr("10.9.0.5")] == nil {
		t.Error("a sender with only dropped messages is not marked as heard")
	}
	// the new senders known now: compiled again (a new sender does that), the same messages are dropped the compiled way
	set := s.drops.Load()
	s.reconcileDrops()
	if s.drops.Load() == set || !s.drops.Load().heard["10.9.0.5"] {
		t.Fatal("not compiled again for the new senders")
	}
	n := len(keptMsgs(t, s))
	s.send("10.9.0.5", "<14>Nov 26 10:00:01 c1 ntpd: synchronized again")
	s.send("10.9.0.6", "<14>Nov 26 10:00:01 c2 ifmgr: link flap again")
	if got := keptMsgs(t, s); len(got) != n {
		t.Errorf("known senders: kept %q", got[n:])
	}
	for _, c := range s.Status().DropRules {
		counts[c.Name] = c.Dropped
	}
	if counts["core ntp"] != 2 || counts["named"] != 2 {
		t.Errorf("known senders: %v", counts)
	}
	// the rules changed: compiled again at once, counts kept by name
	before := counts["keepalives"]
	s.testCfg.Store(&LogsConfig{Enabled: true, DropRules: []LogDropCfg{{Name: "keepalives", Query: "neighbor"}}})
	s.reconcileDrops()
	s.send("10.1.1.1", "<14>Nov 26 10:00:00 r1 bgpd: neighbor up")
	s.send("10.1.1.1", "<14>Nov 26 10:00:00 r1 bgpd: keepalive kept now")
	got = keptMsgs(t, s)
	if got[len(got)-1] != "keepalive kept now" || strings.Contains(strings.Join(got, "|"), "neighbor up") {
		t.Errorf("after the change: %q", got)
	}
	if st := s.Status(); len(st.DropRules) != 1 || st.DropRules[0].Dropped != before+1 {
		t.Errorf("after the change: %+v", st.DropRules)
	}
	// none: everything kept
	s.testCfg.Store(&LogsConfig{Enabled: true})
	s.reconcileDrops()
	s.send("10.1.1.1", "<14>Nov 26 10:00:00 r1 bgpd: neighbor up again")
	if got = keptMsgs(t, s); got[len(got)-1] != "neighbor up again" {
		t.Errorf("no rules: %q", got)
	}
}

// many goroutines handle messages at once (UDP and every TCP connection): each has its own copy of a filter
func TestDropConcurrent(t *testing.T) {
	s := dropService(t, []LogDropCfg{{Name: "odd", Query: "odd and tokz*q"}, {Name: "f", Query: "n>=900"}})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				kind := map[bool]string{true: "odd", false: "even"}[i%2 == 1]
				s.send(fmt.Sprintf("10.1.2.%d", g+1), fmt.Sprintf("<14>Nov 26 10:00:00 h%d app: %s tokzaxq n=%d", g, kind, i*4))
			}
		}(g)
		if g == 4 { // a change of rules while messages arrive
			s.reconcileDrops()
		}
	}
	wg.Wait()
	// kept: the even ones under 900: i even and i*4 < 900 -> i in 0,2..224: 113 per goroutine
	if got := len(keptMsgs(t, s)); got != 8*113 {
		t.Errorf("%d kept, want %d", got, 8*113)
	}
	var n uint64
	for _, c := range s.Status().DropRules {
		n += c.Dropped
	}
	if n != 8*(250-113) {
		t.Errorf("%d dropped, want %d", n, 8*(250-113))
	}
}

func TestDropNeedsNewNodes(t *testing.T) {
	cur := &DaemonConfig{}
	dc := &DaemonConfig{}
	dc.Settings.Logs = &LogsConfig{DropRules: []LogDropCfg{{Name: "a", Query: "x"}}}
	old := []PeerView{{Self: true}, {Hostname: "ld9", Version: "39"}}
	if err := logDropNeedsNewNodes(cur, dc, old); err == nil || !strings.Contains(err.Error(), "ld9") {
		t.Fatalf("a node on 39: %v", err)
	}
	if err := logDropNeedsNewNodes(cur, dc, []PeerView{{Self: true}, {Hostname: "ld9", Version: "40"}}); err != nil {
		t.Fatal(err)
	}
	if err := logDropNeedsNewNodes(dc, dc, old); err != nil { // nothing changed
		t.Fatal(err)
	}
	if err := logDropNeedsNewNodes(dc, cur, old); err != nil { // removing them is always allowed
		t.Fatal(err)
	}
}

// "this or less severe" (severity=info-) gives what a count of every message gives: through the hour index, the line bytes, the
// rollups, what is kept for filters with words and the cache
func TestSevLessSearch(t *testing.T) {
	now := ldNow
	st := newLdStore(t.TempDir(), "n1")
	st.now = func() time.Time { return now }
	st.Load()
	type msg struct{ sev, hour int }
	var all []msg
	for h := 0; h < 8; h++ {
		for i := 0; i < 120; i++ {
			sev := (i * 7 / 3) % 8
			if h%3 == 1 && sev < 5 { // some hours hold only the less severe ones (the hour index rules them out for worse)
				sev = 5 + sev%3
			}
			at := now.Add(-time.Duration(8-h)*time.Hour + time.Duration(i)*time.Second)
			st.Add(LdMsg{T: at.UnixMilli(), Host: "h", Prog: "p", Src: "10.0.0.1", Sev: int8(sev), Msg: fmt.Sprintf("word%d tokz", i%3)})
			all = append(all, msg{sev, h})
		}
	}
	st.Flush()
	st.Maintain()
	from := now.Add(-9 * time.Hour).UnixMilli()
	for sev := 0; sev <= 7; sev++ {
		for _, mode := range []string{"worse", "exact", "less"} {
			for _, q := range []string{"", "tokz", "word1"} {
				want := 0
				for i, m := range all {
					ok := (mode == "worse" && m.sev <= sev) || (mode == "exact" && m.sev == sev) || (mode == "less" && m.sev >= sev)
					if ok && (q == "" || q == "tokz" || i%120%3 == 1) {
						want++
					}
				}
				lq := LdQuery{From: from, MinSev: sev, SevExact: mode == "exact", SevLess: mode == "less", Q: q, Limit: 5000}
				for round := 0; round < 2; round++ { // the second time: from the cache, the rollups and what was kept
					if n := len(st.Search(context.Background(), lq).Msgs); n != want {
						t.Errorf("search sev %d %s %q round %d: %d, want %d", sev, mode, q, round, n, want)
					}
					if n := st.Stats(context.Background(), lq, 3600).Total; n != uint64(want) {
						t.Errorf("stats sev %d %s %q round %d: %d, want %d", sev, mode, q, round, n, want)
					}
				}
				st.cache.set(0, 1)
				st.cache.set(0, 64)
			}
		}
	}
	if err := (&LdQuery{MinSev: 3, SevExact: true, SevLess: true}).Validate(); err == nil {
		t.Error("exact and less together accepted")
	}
}
