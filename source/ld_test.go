package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

var ldNow = time.Date(2026, 11, 20, 10, 30, 0, 0, time.UTC)
var ldSrc = netip.MustParseAddr("10.1.1.5")

func TestParseSyslogForms(t *testing.T) {
	cases := []struct {
		in                   string
		host, prog, pid, msg string
		fac, sev             int8
	}{
		{`<34>1 2026-11-20T10:29:59Z mymachine su 1234 ID47 - 'su root' failed on /dev/pts/8`, "mymachine", "su", "1234", "'su root' failed on /dev/pts/8", 4, 2},
		{`<165>1 2026-11-20T10:29:59.003Z host.example.com evntslog - ID47 [exampleSDID@32473 iut="3" eventSource="Application"] An application event`, "host.example.com", "evntslog", "", "An application event", 20, 5},
		{`<34>Nov 20 10:29:58 mymachine su[77]: 'su root' failed`, "mymachine", "su", "77", "'su root' failed", 4, 2},
		{`<13>Nov  2 10:29:58 sw1 sshd: Accepted publickey for bob`, "sw1", "sshd", "", "Accepted publickey for bob", 1, 5},
		{`<189>000123: *Nov 20 10:29:58.483 UTC: %LINK-3-UPDOWN: Interface Gi0/1, changed state to down`, "10.1.1.5", "%LINK-3-UPDOWN", "", "Interface Gi0/1, changed state to down", 23, 5},
		{`<14>Nov 20 10:29:58 fw1 1,2026/11/20 10:29:58,0123456,TRAFFIC,end`, "fw1", "", "", "1,2026/11/20 10:29:58,0123456,TRAFFIC,end", 1, 6},
		{`<14>2026-11-20T10:29:58+00:00 sw2 kernel: eth0 up`, "sw2", "kernel", "", "eth0 up", 1, 6},
		{`just some text without anything`, "10.1.1.5", "", "", "just some text without anything", 1, 5},
		{"<14>", "10.1.1.5", "", "", "", 1, 6},
	}
	for _, c := range cases {
		m := parseSyslog([]byte(c.in), ldSrc, "udp", ldNow, 8192)
		if m.Host != c.host || m.Prog != c.prog || m.PID != c.pid || m.Msg != c.msg || m.Fac != c.fac || m.Sev != c.sev {
			t.Errorf("%q:\n got host=%q prog=%q pid=%q msg=%q fac=%d sev=%d\nwant host=%q prog=%q pid=%q msg=%q fac=%d sev=%d",
				c.in, m.Host, m.Prog, m.PID, m.Msg, m.Fac, m.Sev, c.host, c.prog, c.pid, c.msg, c.fac, c.sev)
		}
		if m.T != ldNow.UnixMilli() {
			t.Errorf("%q: T is the receive time", c.in)
		}
	}
	m := parseSyslog([]byte(`<165>1 2026-11-20T10:29:59.003Z h a - ID47 [x@1 a="b"] hi`), ldSrc, "tcp", ldNow, 8192)
	if m.SD != `[x@1 a="b"]` || m.MID != "ID47" || m.Sent == 0 {
		t.Errorf("5424 extras: %+v", m)
	}
	// a long message is cut, bad UTF-8 is repaired, NULs and a trailing newline go
	long := parseSyslog([]byte("<14>"+strings.Repeat("é", 1000)+"\x00\n"), ldSrc, "udp", ldNow, 300)
	if len(long.Msg) > 300 || strings.ContainsRune(long.Msg, 0) {
		t.Errorf("long: %d", len(long.Msg))
	}
}

func TestParseYearBoundary(t *testing.T) {
	now := time.Date(2027, 1, 1, 0, 5, 0, 0, time.UTC)
	m := parseSyslog([]byte(`<13>Dec 31 23:59:58 h p: x`), ldSrc, "udp", now, 8192)
	if got := time.UnixMilli(m.Sent).UTC().Year(); got != 2026 {
		t.Errorf("a December stamp on 1 January is last year's: %d", got)
	}
}

func TestFraming(t *testing.T) {
	in := "<14>line one\n\n<14>line two\r\n13 <14>octets ok<14>x\n" + "<14>" + strings.Repeat("a", 500) + "\n<14>after\n"
	br := bufio.NewReaderSize(strings.NewReader(in), 64)
	var got []string
	for {
		b, err := ldReadFramed(br, 100)
		if len(b) > 0 {
			got = append(got, string(b))
		}
		if err != nil {
			break
		}
	}
	want := []string{"<14>line one", "<14>line two", "<14>octets ok", "<14>x", "<14>" + strings.Repeat("a", 96), "<14>after"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func newTestStore(t *testing.T, now *time.Time) *ldStore {
	s := newLdStore(t.TempDir(), "n1")
	s.now = func() time.Time { return *now }
	s.Load()
	return s
}

func addAt(s *ldStore, at time.Time, host, prog string, sev int8, msg string) {
	s.Add(LdMsg{T: at.UnixMilli(), Host: host, Prog: prog, Sev: sev, Fac: 1, Msg: msg, Src: "10.1.1.5", Via: "udp"})
}

func TestStoreSearchFiltersAndOrder(t *testing.T) {
	now := ldNow
	s := newTestStore(t, &now)
	for i := 0; i < 50; i++ {
		addAt(s, now.Add(time.Duration(i-50)*time.Minute), fmt.Sprintf("sw%d", i%3), "sshd", int8(i%8), fmt.Sprintf("message %d from user", i))
	}
	ctx := context.Background()
	q := LdQuery{From: now.Add(-24 * time.Hour).UnixMilli(), MinSev: -1, Limit: 10}
	a := s.Search(ctx, q)
	if len(a.Msgs) != 10 || !a.Truncated || a.Msgs[9].Msg != "message 49 from user" || a.Msgs[0].Msg != "message 40 from user" {
		t.Fatalf("newest ten, oldest first: %v trunc=%v", msgsOf(a), a.Truncated)
	}
	q.Oldest = true
	if a = s.Search(ctx, q); a.Msgs[0].Msg != "message 0 from user" || len(a.Msgs) != 10 {
		t.Fatalf("oldest ten: %v", msgsOf(a))
	}
	q = LdQuery{From: now.Add(-24 * time.Hour).UnixMilli(), MinSev: 3, Host: "sw1", Limit: 100}
	for _, m := range s.Search(ctx, q).Msgs {
		if m.Host != "sw1" || m.Sev > 3 {
			t.Fatalf("filter let through %+v", m)
		}
	}
	q = LdQuery{From: now.Add(-24 * time.Hour).UnixMilli(), MinSev: -1, Q: "message 4 and !40", Limit: 100}
	if got := msgsOf(s.Search(ctx, q)); len(got) == 0 || strings.Contains(strings.Join(got, "|"), "40") {
		t.Fatalf("expression: %v", got)
	}
	q = LdQuery{From: now.Add(-24 * time.Hour).UnixMilli(), MinSev: -1, Host: "SW*", Prog: "ssh*", Limit: 1000}
	if n := len(s.Search(ctx, q).Msgs); n != 50 {
		t.Errorf("wildcards: %d", n)
	}
	// a range holds only its own messages
	q = LdQuery{From: now.Add(-10 * time.Minute).UnixMilli(), To: now.Add(-5 * time.Minute).UnixMilli(), MinSev: -1, Limit: 100}
	if n := len(s.Search(ctx, q).Msgs); n != 6 {
		t.Errorf("range: %d", n)
	}
	// the tail
	tl := s.Tail(LdQuery{From: now.Add(-3 * time.Minute).UnixMilli(), MinSev: -1, Limit: 100})
	if len(tl.Msgs) != 2 || tl.Msgs[1].Msg != "message 49 from user" {
		t.Errorf("tail: %v", msgsOf(tl))
	}
}

func msgsOf(a LdAnswer) []string {
	var o []string
	for _, m := range a.Msgs {
		o = append(o, m.Msg)
	}
	return o
}

func TestStoreSurvivesRestartAndCompresses(t *testing.T) {
	now := ldNow
	dir := t.TempDir()
	s := newLdStore(dir, "n1")
	s.now = func() time.Time { return now }
	s.Load()
	for h := 0; h < 5; h++ {
		for i := 0; i < 20; i++ {
			addAt(s, now.Add(-time.Duration(h)*time.Hour-time.Duration(i)*time.Minute), "sw1", "kernel", 6, fmt.Sprintf("h%d i%d", h, i))
		}
	}
	addAt(s, now.Add(-time.Hour*4), "rare", "oddprog", 2, "needle")
	s.Maintain() // the closed hours are compressed
	s.Close()    // what Stop does before the update's re-exec
	s2 := newLdStore(dir, "n1")
	s2.now = func() time.Time { return now }
	s2.Load()
	hrs, n := s2.Count()
	if n != 101 || hrs < 5 {
		t.Fatalf("after a restart: %d hours, %d messages", hrs, n)
	}
	gz := 0
	for h := range s2.meta {
		if _, err := osStat(s2.path(h, true)); err == nil {
			gz++
		}
	}
	if gz < 3 {
		t.Errorf("only %d hours compressed", gz)
	}
	a := s2.Search(context.Background(), LdQuery{From: now.Add(-48 * time.Hour).UnixMilli(), MinSev: 2, Host: "rare", Limit: 10})
	if len(a.Msgs) != 1 || a.Msgs[0].Msg != "needle" || a.Scanned > 25 {
		t.Errorf("index: %v scanned %d (hours without the host are skipped)", msgsOf(a), a.Scanned)
	}
	// the open hour continues in the same file after a restart
	addAt(s2, now, "sw1", "kernel", 6, "after restart")
	s2.Close()
	s3 := newLdStore(dir, "n1")
	s3.now = func() time.Time { return now }
	s3.Load()
	if _, n := s3.Count(); n != 102 {
		t.Errorf("second restart: %d", n)
	}
}

func TestStoreRetentionAndCap(t *testing.T) {
	now := ldNow
	s := newTestStore(t, &now)
	for d := 9; d >= 0; d-- {
		addAt(s, now.Add(-time.Duration(d)*24*time.Hour), "h", "p", 6, "x")
	}
	s.Close()
	s.retDays = 3
	s.Maintain()
	_, n := s.Count()
	if n > 4 || n < 3 {
		t.Errorf("retention of 3 days left %d", n)
	}
	s.retDays = 0
	s.capPct = 1
	s.volSize = func(string) (uint64, error) { return 100, nil } // a volume so small that everything is over the cap
	s.Maintain()
	if _, n := s.Count(); n > 1 {
		t.Errorf("the cap left %d", n)
	}
}

func TestMergeDropsTheCopyOnTheOtherNode(t *testing.T) {
	a := LdAnswer{Node: "n1", Msgs: []LdMsg{{T: 1000, Node: "n1", Src: "s", Host: "h", Msg: "same"}, {T: 5000, Node: "n1", Src: "s", Host: "h", Msg: "again"}, {T: 5100, Node: "n1", Src: "s", Host: "h", Msg: "again"}}}
	b := LdAnswer{Node: "n2", Msgs: []LdMsg{{T: 1200, Node: "n2", Src: "s", Host: "h", Msg: "same"}, {T: 9000, Node: "n2", Src: "s", Host: "h", Msg: "other"}}}
	out, trunc, _, dups := mergeLdAnswers([]LdAnswer{a, b}, LdQuery{Limit: 100})
	if dups != 1 || len(out) != 4 || trunc {
		t.Fatalf("dups=%d out=%+v", dups, out)
	}
	for i := 1; i < len(out); i++ {
		if out[i].T < out[i-1].T {
			t.Fatal("not in time order")
		}
	}
	// two equal lines on one node are two lines
	if out[1].Msg != "again" || out[2].Msg != "again" {
		t.Errorf("a repeat on one node was dropped: %+v", out)
	}
	out, trunc, _, _ = mergeLdAnswers([]LdAnswer{a, b}, LdQuery{Limit: 2})
	if !trunc || len(out) != 2 || out[1].Msg != "other" {
		t.Errorf("the newest two: %+v", out)
	}
}

func TestLogsConfigValidate(t *testing.T) {
	ok := &LogsConfig{Enabled: true, Senders: []LogSenderCfg{{Name: "sw1", Address: "10.1.1.0/24"}, {Name: "fw", Address: "fw.example.com"}}, Alerts: []LogAlertCfg{{Name: "auth", Query: "failed password", Count: 5, WindowMin: 10}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*LogsConfig{
		"port":      {UDPPort: 70000},
		"name":      {Senders: []LogSenderCfg{{Name: "a b", Address: "10.0.0.1"}}},
		"address":   {Senders: []LogSenderCfg{{Name: "a", Address: "not an address"}}},
		"dupe":      {Senders: []LogSenderCfg{{Name: "a", Address: "10.0.0.1"}, {Name: "A", Address: "10.0.0.2"}}},
		"alert":     {Alerts: []LogAlertCfg{{Name: "x"}}},
		"window":    {Alerts: []LogAlertCfg{{Name: "x", Query: "y", WindowMin: 99999}}},
		"retention": {RetentionDays: -1},
	} {
		if c.Validate() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	p, tc, tl := (&LogsConfig{UDPPort: -1, TLSPort: 7000}).ports()
	if p != 0 || tc != 514 || tl != 7000 {
		t.Errorf("ports %d %d %d", p, tc, tl)
	}
}

// The collector end to end on this host: who may send, and what is stored.
func TestCollectorAllowsOnlySenders(t *testing.T) {
	s := &ldService{store: newLdStore("", "n1"), senders: map[netip.Addr]*ldSender{}, refusedAt: map[netip.Addr]time.Time{}, expDNS: map[string][]netip.Prefix{}}
	s.expNets = []ldSenderNet{{netip.MustParsePrefix("10.1.1.0/24"), "lab"}, {netip.MustParsePrefix("10.1.1.5/32"), "sw1"}}
	s.handle(ldSrc, netip.Addr{}, "udp", []byte("<14>Nov 20 10:29:58 sw1 sshd[1]: hello"), time.Now())
	s.handle(netip.MustParseAddr("192.0.2.9"), netip.Addr{}, "udp", []byte("<14>spoof"), time.Now())
	s.handle(ldSrc, netip.MustParseAddr("10.9.9.1"), "tcp", []byte("<14>second"), time.Now())
	if n := len(s.store.ring); n != 2 {
		t.Fatalf("stored %d, want 2 (the refused one is not)", n)
	}
	if got := s.senderName(ldSrc); got != "sw1" {
		t.Errorf("the narrowest entry names the sender: %q", got)
	}
	info := s.Senders()
	if len(info) != 1 || info[0].Msgs != 2 || info[0].Host != "sw1" || info[0].Name != "sw1" || len(info[0].To) != 1 {
		t.Errorf("senders: %+v", info)
	}
}

func osStat(p string) (os.FileInfo, error) { return os.Stat(p) }

// TCP and TLS on loopback: both framings, a refused sender, and the connection ending cleanly.
func TestCollectorTCPAndTLS(t *testing.T) {
	cert, err := tlsTestCert()
	if err != nil {
		t.Fatal(err)
	}
	for _, proto := range []string{"tcp", "tls"} {
		s := &ldService{store: newLdStore("", "n1"), senders: map[netip.Addr]*ldSender{}, refusedAt: map[netip.Addr]time.Time{}, expDNS: map[string][]netip.Prefix{}, conns: map[net.Conn]struct{}{}}
		s.cancel = func() {}
		s.expNets = []ldSenderNet{{netip.MustParsePrefix("127.0.0.0/8"), "lo"}}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		if proto == "tls" {
			ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
		}
		go s.accept(ln, proto)
		var c net.Conn
		if proto == "tls" {
			c, err = tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true})
		} else {
			c, err = net.Dial("tcp", ln.Addr().String())
		}
		if err != nil {
			t.Fatal(err)
		}
		const pre = "<14>Nov 20 10:29:58 h p: "
		fmt.Fprint(c, pre+"one\n")
		fmt.Fprintf(c, "%d %s", len(pre+"two spaced"), pre+"two spaced")
		fmt.Fprint(c, pre+"three\n")
		c.Close()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			s.store.mu.Lock()
			n := len(s.store.ring)
			s.store.mu.Unlock()
			if n >= 3 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		s.store.mu.Lock()
		var got []string
		for _, m := range s.store.ring {
			got = append(got, m.Via+":"+m.Msg)
		}
		s.store.mu.Unlock()
		want := []string{proto + ":one", proto + ":two spaced", proto + ":three"}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: got %q want %q", proto, got, want)
		}
		ln.Close()
	}
}

func tlsTestCert() (tls.Certificate, error) {
	c, k, err := generateSelfSigned()
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(c, k)
}

func TestPatternAlertSamples(t *testing.T) {
	now := time.Now()
	s := newLdStore("", "n1")
	s.Add(LdMsg{T: now.Add(-30 * time.Minute).UnixMilli(), Host: "fw1", Prog: "sshd", Sev: 4, Msg: "Failed password for old"})
	for i := 5; i >= 0; i-- {
		s.Add(LdMsg{T: now.Add(-time.Duration(i) * time.Minute).UnixMilli(), Host: "fw1", Prog: "sshd", Sev: 4, Msg: "Failed password for root"})
	}
	sev := 4
	cfg := LogsConfig{Alerts: []LogAlertCfg{
		{Name: "brute force", Query: "failed password", Count: 5, WindowMin: 10},
		{Name: "quiet", Query: "kernel panic", Count: 1, WindowMin: 10},
		{Name: "severe", Host: "fw*", MinSev: &sev, Count: 7, WindowMin: 60},
		{Name: "off", Query: "failed", Disabled: true},
	}}
	got := map[string]bool{}
	for _, a := range ldAlertSamplesFor(cfg, s) {
		if a.Rule != "log_pattern" {
			t.Fatalf("rule %q", a.Rule)
		}
		got[a.Subject] = a.Bad
	}
	if len(got) != 3 || !got["brute force"] || got["quiet"] || !got["severe"] {
		t.Errorf("samples: %v", got)
	}
}

func TestLdLzRoundTripAndDamage(t *testing.T) {
	var plain bytes.Buffer
	for i := 0; plain.Len() < 3*ldLzBlock+777; i++ {
		fmt.Fprintf(&plain, `{"t":%d,"h":"sw%d","p":"sshd","m":"Failed password for invalid user admin from 203.0.113.%d"}`+"\n", i, i%7, i%250)
	}
	var packed bytes.Buffer
	if err := ldLzWrite(&packed, bytes.NewReader(plain.Bytes())); err != nil {
		t.Fatal(err)
	}
	if packed.Len() >= plain.Len()/2 {
		t.Errorf("poor ratio: %d of %d", packed.Len(), plain.Len())
	}
	back := func(b []byte) []byte {
		r, err := ldLzReader(bytes.NewReader(b))
		if err != nil {
			return nil
		}
		out, _ := io.ReadAll(r)
		return out
	}
	if !bytes.Equal(back(packed.Bytes()), plain.Bytes()) {
		t.Fatal("round trip differs")
	}
	// a torn end gives the frames before it, never a panic; flipped bytes end the stream at the bad frame
	cut := packed.Bytes()[:packed.Len()-100]
	if got := back(cut); len(got) == 0 || !bytes.HasPrefix(plain.Bytes(), got) {
		t.Errorf("torn file: got %d bytes", len(got))
	}
	bad := append([]byte(nil), packed.Bytes()...)
	for i := 40; i < len(bad); i += 997 {
		bad[i] ^= 0x5a
	}
	if got := back(bad); !bytes.HasPrefix(plain.Bytes(), got) {
		t.Error("damaged file returned bytes that were never written")
	}
	rnd := make([]byte, 5000)
	for i := range rnd {
		rnd[i] = byte(i * 131)
	}
	back(append([]byte(ldLzMagic), rnd...))
}

// Reverse names: 127.0.0.1 reverse-looks-up to localhost (the hosts file), 10.1.1.5 to nothing here.
func TestReverseNamesOnSendersAndSearch(t *testing.T) {
	loop, other := netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.1.1.5")
	now := time.Now()
	s := &ldService{store: newTestStore(t, &now), dns: newLdDNS(), senders: map[netip.Addr]*ldSender{}, refusedAt: map[netip.Addr]time.Time{}, expDNS: map[string][]netip.Prefix{}}
	s.expNets = []ldSenderNet{{netip.MustParsePrefix("10.1.1.0/24"), "lab"}, {netip.MustParsePrefix("127.0.0.0/8"), "me"}}
	s.handle(loop, netip.Addr{}, "udp", []byte("<14>Nov 20 10:29:58 hostA sshd[1]: from loopback"), now)
	s.handle(other, netip.Addr{}, "udp", []byte("<14>Nov 20 10:29:58 hostB sshd[1]: from lab"), now)

	// the first Senders call starts the lookups; once they are in, the next has the names
	s.Senders()
	s.dns.names([]netip.Addr{loop, other}, 2*time.Second)
	got := map[string]LdSenderInfo{}
	for _, i := range s.Senders() {
		got[i.Addr] = i
	}
	if got["127.0.0.1"].RDNS != "localhost" || got["127.0.0.1"].Rev != "localhost" || got["10.1.1.5"].RDNS != "" {
		t.Errorf("senders: %+v", got)
	}

	// a search by name: the configured name, the reverse name, the host the messages give, with wildcards; no match is nothing
	for pat, want := range map[string]int{"localhost": 1, "LOC*": 1, "me": 1, "lab": 1, "hostb": 1, "host*": 2, "*": 2, "nobody*": 0} {
		q := LdQuery{From: now.Add(-time.Hour).UnixMilli(), MinSev: -1, SrcName: pat}
		a, ok := s.local(context.Background(), ldQueryMsg{Op: "search", Query: q})
		if !ok || a.Answer == nil || len(a.Answer.Msgs) != want {
			t.Errorf("source=%s: %v %+v", pat, ok, a.Answer)
		}
	}
	// the sender's name comes with each message of a result
	a, _ := s.local(context.Background(), ldQueryMsg{Op: "search", Query: LdQuery{From: now.Add(-time.Hour).UnixMilli(), MinSev: -1}})
	res := s.result(LdQuery{From: now.Add(-time.Hour).UnixMilli(), MinSev: -1, Limit: 10}, []LdAnswer{*a.Answer}, nil, 1)
	names := map[string]string{}
	for _, m := range res.Messages {
		names[m.Source] = m.SourceName
	}
	if names["127.0.0.1"] != "localhost" || names["10.1.1.5"] != "" {
		t.Errorf("source names: %v", names)
	}
	// the URL form: a name is accepted, a bad one refused
	if q, err := s.parseLdQuery(url.Values{"source": {"sw*.example.com"}}); err != nil || q.SrcName != "sw*.example.com" || q.Src != "" {
		t.Errorf("parse name: %+v %v", q, err)
	}
	if _, err := s.parseLdQuery(url.Values{"source": {"a b;c"}}); err == nil {
		t.Error("a bad source was accepted")
	}
}

// "warning=" asks for warnings only (the web page's Warnings tile), where "warning" is warnings or worse.
func TestSearchSeverityExact(t *testing.T) {
	now := ldNow
	s := newTestStore(t, &now)
	for i := 0; i < 32; i++ {
		addAt(s, now.Add(time.Duration(i-40)*time.Minute), "sw1", "sshd", int8(i%8), fmt.Sprintf("message %d", i))
	}
	svc := &ldService{}
	q, err := svc.parseLdQuery(url.Values{"severity": {"warning="}})
	if err != nil || !q.SevExact || q.MinSev != 4 {
		t.Fatalf("warning= parsed as %+v, %v", q, err)
	}
	if q, err = svc.parseLdQuery(url.Values{"severity": {"warning"}}); err != nil || q.SevExact || q.MinSev != 4 {
		t.Fatalf("warning parsed as %+v, %v", q, err)
	}
	q.From, q.Limit = now.Add(-24*time.Hour).UnixMilli(), 100
	worse := len(s.Search(context.Background(), q).Msgs)
	q.SevExact = true
	exact := s.Search(context.Background(), q).Msgs
	if len(exact) != 4 || worse != 20 {
		t.Fatalf("exact warnings %d (want 4), warning or worse %d (want 20)", len(exact), worse)
	}
	for _, m := range exact {
		if m.Sev != 4 {
			t.Fatalf("exact let through %+v", m)
		}
	}
}

func TestListLimit(t *testing.T) {
	s := &ldService{}
	if got := s.listLimit(); got != 1000 {
		t.Fatalf("default list limit = %d, want 1000", got)
	}
	for _, n := range []int{0, 100, 1000, 5000} {
		if c := (LogsConfig{ListLimit: n}); c.Validate() != nil {
			t.Fatalf("list_limit %d was refused", n)
		}
	}
	for _, n := range []int{-1, 99, 5001} {
		if c := (LogsConfig{ListLimit: n}); c.Validate() == nil {
			t.Fatalf("list_limit %d was accepted", n)
		}
	}
}
