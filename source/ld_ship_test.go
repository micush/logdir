package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func intp(v int) *int { return &v }

func TestShipValidate(t *testing.T) {
	good := []LogDestCfg{
		{Name: "a", Type: "syslog", Server: "10.0.0.1"},
		{Name: "b", Type: "syslog", Protocol: "tls", Server: "log.example.com:6514", Start: "24h", MaxBehindHours: 72},
		{Name: "c", Type: "syslog", Protocol: "udp", Server: "10.0.0.1", SpoofSource: true},
		{Name: "d", Type: "json_tcp", Server: "[2001:db8::1]:5000", Protocol: "tls"},
		{Name: "e", Type: "splunk_hec", URL: "https://splunk:8088", Token: "x"},
		{Name: "f", Type: "elastic", URL: "https://es:9200", Index: "logs-logdir-default"},
		{Name: "g", Type: "loki", URL: "http://loki:3100", MinSev: intp(4), SevExact: true, Senders: []string{"fw*", "10.0.0.0/8", "10.1.1.1"}, Start: "oldest"},
	}
	if err := validateDestinations(good); err != nil {
		t.Fatal(err)
	}
	bad := []LogDestCfg{
		{Name: "", Type: "syslog", Server: "x"},
		{Name: "a b", Type: "syslog", Server: "x"},
		{Name: "a", Type: "ftp", Server: "x"},
		{Name: "a", Type: "syslog"},
		{Name: "a", Type: "syslog", Server: "x", Protocol: "sctp"},
		{Name: "a", Type: "syslog", Server: "x:99999"},
		{Name: "a", Type: "syslog", Server: "x", SpoofSource: true},
		{Name: "a", Type: "json_tcp", Server: "x", Protocol: "udp"},
		{Name: "a", Type: "splunk_hec", URL: "https://s"},
		{Name: "a", Type: "elastic", URL: "ftp://s"},
		{Name: "a", Type: "loki", URL: "https://s", Server: "x"},
		{Name: "a", Type: "syslog", Server: "x", Token: "t"},
		{Name: "a", Type: "syslog", Server: "x", MinSev: intp(9)},
		{Name: "a", Type: "syslog", Server: "x", SevExact: true},
		{Name: "a", Type: "syslog", Server: "x", Start: "yesterday"},
		{Name: "a", Type: "syslog", Server: "x", Senders: []string{"bad name!"}},
		{Name: "a", Type: "syslog", Server: "x", CAPEM: "not a cert"},
		{Name: "a", Type: "elastic", URL: "https://s", Index: "a b"},
		{Name: "a", Type: "syslog", Server: "x", RateLimit: -1},
	}
	for i, d := range bad {
		if err := validateDestinations([]LogDestCfg{d}); err == nil {
			t.Errorf("bad %d accepted: %+v", i, d)
		}
	}
	if err := validateDestinations([]LogDestCfg{{Name: "A", Type: "syslog", Server: "x"}, {Name: "a", Type: "syslog", Server: "y"}}); err == nil {
		t.Error("a name used twice was accepted")
	}
	// the settings check runs them; a configuration without destinations is unchanged
	c := &LogsConfig{Destinations: []LogDestCfg{{Name: "x", Type: "nope"}}}
	if c.Validate() == nil {
		t.Error("LogsConfig.Validate does not check the destinations")
	}
	d := c.clone()
	d.Destinations[0].Name = "y"
	if c.Destinations[0].Name != "x" {
		t.Error("clone shares the destinations")
	}
}

func TestShip5424(t *testing.T) {
	m := &LdMsg{T: 1764100000123, Sent: 1764100000000, Host: "fw 1", Prog: "sshd", PID: "42", Fac: 4, Sev: 3, Src: "10.1.1.4", Node: "ld2", Via: "udp",
		MID: "ID47", SD: `[exampleSDID@32473 iut="3"]`, Msg: "failed\npassword", Raw: `<35>Nov 25 sshd[42]: failed "x"]`}
	got := string(ld5424(ldShipItem{m: m, name: "fw1"}, true))
	want := `<35>1 2025-11-25T19:46:40.000Z fw_1 sshd 42 ID47 [origin ip="10.1.1.4"][logdir@32473 rcvd="2025-11-25T19:46:40.123Z" ip="10.1.1.4" sender="fw1" node="ld2" via="udp" sent="2025-11-25T19:46:40.000Z" raw="<35>Nov 25 sshd[42\]: failed \"x\"\]"][exampleSDID@32473 iut="3"] failed password`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// the sender's own origin element stays the only one
	m2 := &LdMsg{T: 1764100000123, Src: "10.1.1.4", SD: `[origin ip="192.0.2.1"]`, Msg: "x"}
	if g := string(ld5424(ldShipItem{m: m2}, false)); strings.Count(g, "[origin ") != 1 || !strings.Contains(g, `[origin ip="192.0.2.1"]`) || !strings.Contains(g, `ip="10.1.1.4"`) {
		t.Fatalf("own origin: %s", g)
	}
	// structured data that is not well formed goes inside logdir's element
	m3 := &LdMsg{T: 1764100000123, Src: "10.1.1.4", SD: `[broken`, Msg: "x"}
	if g := string(ld5424(ldShipItem{m: m3}, false)); !strings.Contains(g, ` sd="[broken"]`) || ldValidSD(`[broken`) {
		t.Fatalf("bad sd: %s", g)
	}
	// no host: the address; nothing: dashes
	m4 := &LdMsg{T: 1764100000123, Src: "10.1.1.4"}
	if g := string(ld5424(ldShipItem{m: m4}, false)); !strings.HasPrefix(g, `<0>1 2025-11-25T19:46:40.123Z 10.1.1.4 - - - [origin`) {
		t.Fatalf("empty: %s", g)
	}
	for _, sd := range []string{`[a b="c"]`, `[a b="c\"]"][d]`} {
		if !ldValidSD(sd) {
			t.Errorf("%s: not valid", sd)
		}
	}
	for _, sd := range []string{`x[a]`, `[a][`, `[a b="c]`, `[a] [b]`} {
		if ldValidSD(sd) {
			t.Errorf("%s: valid", sd)
		}
	}
}

// shipService is a collector with a store under dir holding hours closed hours of n messages each and open messages in the hour
// being written (times relative to now), and the given destinations.
func shipService(t *testing.T, dir string, hours, n, open int, dests ...LogDestCfg) *ldService {
	t.Helper()
	st := newLdStore(dir, "n1")
	st.Load()
	now := time.Now()
	nowH := now.Unix() / 3600
	k := 0
	add := func(at time.Time) {
		st.Add(LdMsg{T: at.UnixMilli(), Host: fmt.Sprintf("h%d", k%3), Prog: "app", Fac: 1, Sev: int8(k % 8), Src: fmt.Sprintf("10.1.%d.%d", k%2, k%5), Via: "udp",
			Msg: fmt.Sprintf("message %d", k)})
		k++
	}
	for h := nowH - int64(hours); h < nowH; h++ {
		for i := 0; i < n; i++ {
			add(time.Unix(h*3600, 0).Add(time.Duration(i) * time.Second))
		}
	}
	start := time.Unix(nowH*3600, 0)
	for i := 0; i < open; i++ {
		at := start.Add(time.Duration(i) * time.Millisecond)
		if at.After(now) {
			at = now
		}
		add(at)
	}
	st.Flush()
	st.Maintain()
	s := &ldService{store: st, dns: newLdDNS(), senders: map[netip.Addr]*ldSender{}, refusedAt: map[netip.Addr]time.Time{}, expDNS: map[string][]netip.Prefix{}}
	s.testCfg.Store(&LogsConfig{Enabled: true, Destinations: dests})
	return s
}

func (s *ldService) addNow(msg string) {
	s.store.Add(LdMsg{T: time.Now().UnixMilli(), Host: "late", Prog: "app", Fac: 1, Sev: 6, Src: "10.1.0.9", Via: "tcp", Msg: msg})
	s.store.Flush()
}

// shipSink is a TCP syslog server that reads octet-counted frames.
type shipSink struct {
	ln   net.Listener
	mu   sync.Mutex
	msgs []string
}

func newShipSink(t *testing.T, addr string) *shipSink {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	k := &shipSink{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					ls, err := br.ReadString(' ')
					if err != nil {
						return
					}
					n, err := strconv.Atoi(strings.TrimSpace(ls))
					if err != nil {
						return
					}
					b := make([]byte, n)
					if _, err := io.ReadFull(br, b); err != nil {
						return
					}
					k.mu.Lock()
					k.msgs = append(k.msgs, string(b))
					k.mu.Unlock()
				}
			}(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return k
}

func (k *shipSink) got() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.msgs...)
}

func shipWait(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestShipSyslogTCP(t *testing.T) {
	sink := newShipSink(t, "127.0.0.1:0")
	dir := t.TempDir()
	d := LogDestCfg{Name: "arch", Type: "syslog", Server: sink.ln.Addr().String(), Start: "oldest"}
	s := shipService(t, dir, 3, 50, 20, d)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.reconcileShipping(ctx)
	shipWait(t, "170 messages", 10*time.Second, func() bool { return len(sink.got()) >= 170 })
	got := sink.got()
	for i, m := range got[:170] {
		if !strings.Contains(m, fmt.Sprintf(" message %d", i)) || !strings.Contains(m, `[origin ip="10.1.`) || !strings.Contains(m, `[logdir@32473 rcvd="`) {
			t.Fatalf("message %d: %s", i, m)
		}
	}
	// a new message is sent as it arrives
	s.addNow("fresh one")
	shipWait(t, "the new message", 5*time.Second, func() bool { g := sink.got(); return len(g) == 171 && strings.HasSuffix(g[170], "fresh one") })
	st := s.ShipStatus()
	if len(st) != 1 || st[0].State != "ok" || st[0].Sent != 171 || st[0].Target != "tcp://"+sink.ln.Addr().String() {
		t.Fatalf("status %+v", st)
	}
	// stopped and started again: carries on where it was, nothing twice
	s.stopShipping()
	c, ok := s.store.loadCursor("arch")
	if !ok || c.Sent != 171 {
		t.Fatalf("cursor %+v %v", c, ok)
	}
	s.addNow("after restart")
	s.reconcileShipping(ctx)
	shipWait(t, "the message after the restart", 5*time.Second, func() bool { return len(sink.got()) == 172 })
	time.Sleep(300 * time.Millisecond)
	if g := sink.got(); len(g) != 172 || !strings.HasSuffix(g[171], "after restart") {
		t.Fatalf("after restart: %d messages, last %q", len(g), g[len(g)-1])
	}
	// a reset to the oldest sends everything again
	res, errs := s.ShipAll(ctx, "shipreset", ldShipOp{Name: "arch", From: "oldest"})
	if len(res) != 1 || !res[0].OK || len(errs) != 0 {
		t.Fatalf("reset %+v %v", res, errs)
	}
	shipWait(t, "everything again", 10*time.Second, func() bool { return len(sink.got()) == 172*2 })
	// a test message
	res, _ = s.ShipAll(ctx, "shiptest", ldShipOp{Name: "arch"})
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("test %+v", res)
	}
	shipWait(t, "the test message", 5*time.Second, func() bool { g := sink.got(); return strings.Contains(g[len(g)-1], "logdir shipping test from") })
	// a destination taken out stops
	s.testCfg.Store(&LogsConfig{Enabled: true})
	s.reconcileShipping(ctx)
	if len(s.ShipStatus()) != 0 {
		t.Fatal("a removed destination still runs")
	}
}

// A server that is down: the state says so, and once it is up everything arrives.
func TestShipRetries(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	d := LogDestCfg{Name: "late", Type: "syslog", Server: addr, Start: "oldest"}
	s := shipService(t, t.TempDir(), 1, 30, 0, d)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.reconcileShipping(ctx)
	shipWait(t, "the error state", 5*time.Second, func() bool {
		st := s.ShipStatus()
		return len(st) == 1 && st[0].State == "error" && st[0].LastError != ""
	})
	sink := newShipSink(t, addr)
	shipWait(t, "30 messages", 15*time.Second, func() bool { return len(sink.got()) == 30 })
	shipWait(t, "the ok state", 5*time.Second, func() bool { st := s.ShipStatus(); return st[0].State == "ok" && st[0].LastError == "" })
	s.stopShipping()
}

// Filters: severity, senders (a network), the start time; disabled keeps the cursor.
func TestShipFilter(t *testing.T) {
	sink := newShipSink(t, "127.0.0.1:0")
	d := LogDestCfg{Name: "errs", Type: "syslog", Server: sink.ln.Addr().String(), Start: "oldest", MinSev: intp(3), Senders: []string{"10.1.1.0/24"}}
	s := shipService(t, t.TempDir(), 2, 80, 0, d)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.reconcileShipping(ctx)
	// k%8 <= 3 and k%2 == 1: k%8 in {1, 3}: 2 of every 8
	shipWait(t, "40 messages", 10*time.Second, func() bool { return len(sink.got()) == 40 })
	for _, m := range sink.got() {
		if !strings.Contains(m, `ip="10.1.1.`) {
			t.Fatalf("not from 10.1.1.0/24: %s", m)
		}
		pri, _ := strconv.Atoi(m[1:strings.IndexByte(m, '>')])
		if pri%8 > 3 {
			t.Fatalf("severity %d: %s", pri%8, m)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(sink.got()); n != 40 {
		t.Fatalf("%d messages", n)
	}
	// start "now" sends nothing that was there before
	d2 := LogDestCfg{Name: "new", Type: "syslog", Server: sink.ln.Addr().String()}
	s.testCfg.Store(&LogsConfig{Enabled: true, Destinations: []LogDestCfg{d, d2}})
	s.reconcileShipping(ctx)
	time.Sleep(500 * time.Millisecond)
	if n := len(sink.got()); n != 40 {
		t.Fatalf("start now sent %d old messages", n-40)
	}
	s.addNow("only this")
	shipWait(t, "the new message", 5*time.Second, func() bool { return len(sink.got()) == 41 })
	// disabled: nothing sent, the cursor kept and shown
	d2.Disabled = true
	s.testCfg.Store(&LogsConfig{Enabled: true, Destinations: []LogDestCfg{d, d2}})
	s.reconcileShipping(ctx)
	s.addNow("while off")
	time.Sleep(1500 * time.Millisecond)
	var st LdShipStatus
	for _, x := range s.ShipStatus() {
		if x.Name == "new" {
			st = x
		}
	}
	if st.State != "disabled" || st.Sent != 1 {
		t.Fatalf("disabled: %+v", st)
	}
	if n := len(sink.got()); n != 41 {
		t.Fatalf("a disabled destination sent: %d", n)
	}
	d2.Disabled = false
	s.testCfg.Store(&LogsConfig{Enabled: true, Destinations: []LogDestCfg{d, d2}})
	s.reconcileShipping(ctx)
	shipWait(t, "what came while it was off", 5*time.Second, func() bool { g := sink.got(); return len(g) == 42 && strings.HasSuffix(g[41], "while off") })
	s.stopShipping()
}

// Too far behind: the oldest are skipped and counted.
func TestShipMaxBehind(t *testing.T) {
	sink := newShipSink(t, "127.0.0.1:0")
	d := LogDestCfg{Name: "short", Type: "syslog", Server: sink.ln.Addr().String(), Start: "oldest", MaxBehindHours: 2}
	s := shipService(t, t.TempDir(), 5, 10, 0, d)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.reconcileShipping(ctx)
	shipWait(t, "the last two hours", 10*time.Second, func() bool { return len(sink.got()) == 20 })
	st := s.ShipStatus()[0]
	if st.Skipped != 30 || !strings.Contains(st.Note, "skipped 30") {
		t.Fatalf("status %+v", st)
	}
	s.stopShipping()
}

func TestShipUDPAndJSONTCP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	var mu sync.Mutex
	var dgrams []string
	go func() {
		b := make([]byte, 70000)
		for {
			n, _, err := pc.ReadFrom(b)
			if err != nil {
				return
			}
			mu.Lock()
			dgrams = append(dgrams, string(b[:n]))
			mu.Unlock()
		}
	}()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	var lines []string
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		sc := bufio.NewScanner(c)
		for sc.Scan() {
			mu.Lock()
			lines = append(lines, sc.Text())
			mu.Unlock()
		}
	}()
	s := shipService(t, t.TempDir(), 1, 25, 0,
		LogDestCfg{Name: "u", Type: "syslog", Protocol: "udp", Server: pc.LocalAddr().String(), Start: "oldest"},
		LogDestCfg{Name: "j", Type: "json_tcp", Server: ln.Addr().String(), Start: "oldest"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.reconcileShipping(ctx)
	shipWait(t, "25 datagrams and 25 lines", 10*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return len(dgrams) == 25 && len(lines) == 25 })
	mu.Lock()
	defer mu.Unlock()
	if !strings.HasPrefix(dgrams[0], "<8>1 ") || !strings.Contains(dgrams[0], `[origin ip="10.1.0.0"]`) {
		t.Fatalf("datagram %s", dgrams[0])
	}
	var j LdShipJSON
	if err := json.Unmarshal([]byte(lines[3]), &j); err != nil || j.Message != "message 3" || j.Source != "10.1.1.3" || j.Severity != "err" || j.Facility != "user" || j.Node != "n1" || j.Via != "udp" {
		t.Fatalf("json %s (%v)", lines[3], err)
	}
	s.stopShipping()
}

func TestShipHTTP(t *testing.T) {
	type hit struct {
		path, auth, ct, body string
	}
	var mu sync.Mutex
	var hits []hit
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, hit{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(b)})
		f := fail
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "_bulk") {
			if f {
				w.Write([]byte(`{"errors":true,"items":[{"create":{"status":400,"error":{"type":"mapper_parsing_exception"}}}]}`))
				return
			}
			w.Write([]byte(`{"errors":false,"items":[]}`))
			return
		}
		w.Write([]byte(`{"text":"Success","code":0}`))
	}))
	defer srv.Close()
	s := shipService(t, t.TempDir(), 1, 12, 0,
		LogDestCfg{Name: "spl", Type: "splunk_hec", URL: srv.URL, Token: "tok1", Index: "net", Start: "oldest"},
		LogDestCfg{Name: "es", Type: "elastic", URL: srv.URL + "/", Token: "k2", Start: "oldest"},
		LogDestCfg{Name: "lk", Type: "loki", URL: srv.URL, Token: "b3", Start: "oldest"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.reconcileShipping(ctx)
	shipWait(t, "the three posts", 10*time.Second, func() bool {
		st := s.ShipStatus()
		return len(st) == 3 && st[0].Sent == 12 && st[1].Sent == 12 && st[2].Sent == 12
	})
	mu.Lock()
	by := map[string]hit{}
	for _, h := range hits {
		by[h.path] = h
	}
	mu.Unlock()
	spl := by["/services/collector/event"]
	if spl.auth != "Splunk tok1" || strings.Count(spl.body, "\n") != 12 || !strings.Contains(spl.body, `"index":"net"`) || !strings.Contains(spl.body, `"source":"10.1.0.0"`) {
		t.Fatalf("splunk %+v", spl)
	}
	es := by["/_bulk"]
	if es.auth != "ApiKey k2" || es.ct != "application/x-ndjson" || strings.Count(es.body, `{"create":{"_index":"logdir"}}`) != 12 || !strings.Contains(es.body, `"@timestamp":"`) {
		t.Fatalf("elastic %+v", es)
	}
	lk := by["/loki/api/v1/push"]
	var push struct {
		Streams []struct {
			Stream map[string]string `json:"stream"`
			Values [][2]string       `json:"values"`
		} `json:"streams"`
	}
	if lk.auth != "Bearer b3" || json.Unmarshal([]byte(lk.body), &push) != nil || len(push.Streams) == 0 || push.Streams[0].Stream["job"] != "logdir" {
		t.Fatalf("loki %+v", lk)
	}
	n := 0
	for _, st := range push.Streams {
		n += len(st.Values)
		if _, err := strconv.ParseInt(st.Values[0][0], 10, 64); err != nil || st.Stream["source"] == "" {
			t.Fatalf("loki stream %+v", st)
		}
	}
	if n != 12 {
		t.Fatalf("loki: %d values", n)
	}
	// elastic refusing a document is an error, and the batch is sent again
	mu.Lock()
	fail = true
	mu.Unlock()
	s.addNow("refused")
	shipWait(t, "the elastic error", 5*time.Second, func() bool {
		for _, st := range s.ShipStatus() {
			if st.Name == "es" && st.State == "error" && strings.Contains(st.LastError, "mapper_parsing_exception") {
				return true
			}
		}
		return false
	})
	mu.Lock()
	fail = false
	mu.Unlock()
	shipWait(t, "elastic again", 10*time.Second, func() bool {
		for _, st := range s.ShipStatus() {
			if st.Name == "es" {
				return st.State == "ok" && st.Sent == 13
			}
		}
		return false
	})
	s.stopShipping()
	// a test of a destination that is not saved
	res, _ := s.ShipAll(ctx, "shiptest", ldShipOp{Dest: &LogDestCfg{Name: "try", Type: "splunk_hec", URL: srv.URL, Token: "zz"}})
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("unsaved test %+v", res)
	}
	res, _ = s.ShipAll(ctx, "shiptest", ldShipOp{Dest: &LogDestCfg{Name: "try", Type: "splunk_hec", URL: srv.URL}})
	if len(res) != 1 || res[0].OK || !strings.Contains(res[0].Error, "token") {
		t.Fatalf("invalid unsaved test %+v", res)
	}
}

// The raw bytes: kept while a destination wants them, sent as raw="..." and in JSON.
func TestShipRaw(t *testing.T) {
	sink := newShipSink(t, "127.0.0.1:0")
	d := LogDestCfg{Name: "r", Type: "syslog", Server: sink.ln.Addr().String(), Raw: true}
	s := shipService(t, t.TempDir(), 0, 0, 0, d)
	s.expNets = []ldSenderNet{{p: netip.MustParsePrefix("10.0.0.0/8"), name: "net10"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.reconcileShipping(ctx)
	if !s.keepRaw.Load() {
		t.Fatal("raw bytes are not kept")
	}
	time.Sleep(200 * time.Millisecond)
	src := netip.MustParseAddr("10.2.3.4")
	s.handle(src, netip.Addr{}, "udp", []byte("<13>Nov 25 10:00:00 sw1 app: hello \"there\"\n"), time.Now())
	s.store.Flush()
	shipWait(t, "the message", 5*time.Second, func() bool { return len(sink.got()) == 1 })
	g := sink.got()[0]
	if !strings.Contains(g, `raw="<13>Nov 25 10:00:00 sw1 app: hello \"there\""`) || !strings.Contains(g, `sender="net10"`) {
		t.Fatalf("raw: %s", g)
	}
	// no destination wants them: not kept
	s.testCfg.Store(&LogsConfig{Enabled: true})
	s.reconcileShipping(ctx)
	if s.keepRaw.Load() {
		t.Fatal("raw bytes kept with no destination that sends them")
	}
}

func TestShipSpoofPacket(t *testing.T) {
	src, dst := netip.MustParseAddr("10.1.2.3"), netip.MustParseAddr("192.0.2.9")
	p := ldSpoofPacket(src, dst, 514, 5514, []byte("hello"))
	if len(p) != 33 || p[0] != 0x45 || binary.BigEndian.Uint16(p[2:]) != 33 || p[9] != 17 {
		t.Fatalf("header % x", p[:20])
	}
	if ldChecksum(p[:20], 0) != 0 {
		t.Fatal("IP checksum")
	}
	var ph uint32 = 0x0a01 + 0x0203 + 0xc000 + 0x0209 + 17 + 13
	if ldChecksum(p[20:], ph) != 0 {
		t.Fatal("UDP checksum")
	}
}

// Sent for real from a raw socket (as root), from another local address: the receiver sees the sender's address.
func TestShipSpoofSend(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	d := LogDestCfg{Name: "sp", Type: "syslog", Protocol: "udp", Server: pc.LocalAddr().String(), SpoofSource: true}
	snd, err := newShipSender(d)
	if err != nil {
		t.Skip(err)
	}
	defer snd.close()
	m := &LdMsg{T: time.Now().UnixMilli(), Src: "127.0.0.55", Raw: "<13>Nov 25 10:00:00 sw1 app: as sent", Msg: "as sent"}
	if err := snd.send(context.Background(), []ldShipItem{{m: m}}); err != nil {
		t.Fatal(err)
	}
	pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	b := make([]byte, 2000)
	n, from, err := pc.ReadFrom(b)
	if err != nil {
		t.Fatal(err)
	}
	if fa := from.(*net.UDPAddr); fa.IP.String() != "127.0.0.55" || fa.Port != 514 || string(b[:n]) != m.Raw {
		t.Fatalf("from %v: %q", from, b[:n])
	}
}

func TestShipNeedsNewNodes(t *testing.T) {
	cur := &DaemonConfig{}
	dc := &DaemonConfig{}
	dc.Settings.Logs = &LogsConfig{Destinations: []LogDestCfg{{Name: "a", Type: "syslog", Server: "x"}}}
	old := []PeerView{{Self: true, Version: "37"}, {Hostname: "ld9", Version: "36"}}
	if err := logShippingNeedsNewNodes(cur, dc, old); err == nil || !strings.Contains(err.Error(), "ld9") {
		t.Fatalf("an old node was not named: %v", err)
	}
	if err := logShippingNeedsNewNodes(cur, dc, []PeerView{{Self: true}, {Hostname: "ld9", Version: "37"}}); err != nil {
		t.Fatal(err)
	}
	if err := logShippingNeedsNewNodes(dc, dc, old); err != nil {
		t.Fatalf("an unchanged list was refused: %v", err)
	}
	if err := logShippingNeedsNewNodes(dc, cur, old); err != nil {
		t.Fatalf("removing every destination was refused: %v", err)
	}
}
