package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// A search over two nodes, one of them far: what this node holds comes back first, the far node's part after, and the last part
// is the whole answer, the same as without streaming.
func TestStreamedSearchParts(t *testing.T) {
	failDelay = 0
	a, b := newTNode(t, "A"), newTNode(t, "B")
	a.join2(t, b)
	a.sync()
	st := newLdStore(t.TempDir(), "n1")
	st.Load()
	svc := &ldService{store: st, senders: map[netip.Addr]*ldSender{}, refusedAt: map[netip.Addr]time.Time{}, expDNS: map[string][]netip.Prefix{}, mg: a.mg}
	svc.testCfg.Store(&LogsConfig{})
	svc.expNets = []ldSenderNet{{netip.PrefixFrom(ldSrc, ldSrc.BitLen()), "sw1"}}
	oldSvc := ldSvc
	ldSvc = svc // B answers from the same store (one process): every message comes back twice
	t.Cleanup(func() { ldSvc = oldSvc; ldPeerTestDelay = 0; closePeerConns() })
	now := time.Now()
	for i := range 5 {
		svc.handle(ldSrc, netip.Addr{}, "udp", []byte("<14>sw1 bgpd: keepalive "+string(rune('a'+i))), now.Add(-time.Duration(i)*time.Second))
	}
	q := LdQuery{From: now.Add(-time.Hour).UnixMilli(), To: now.Add(time.Minute).UnixMilli(), Q: "keepalive", MinSev: -1, Limit: 100}
	if err := q.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(svc.otherPeers()) != 1 {
		t.Fatalf("peers: %+v", svc.otherPeers())
	}
	const far = 400 * time.Millisecond
	ldPeerTestDelay = far

	start := time.Now()
	type part struct {
		n       int
		pending []string
		done    bool
		at      time.Duration
	}
	var parts []part
	final := svc.SearchEach(context.Background(), q, func(r LdResult, done bool) {
		parts = append(parts, part{len(r.Messages), r.Pending, done, time.Since(start)})
	})
	if len(parts) != 2 || parts[0].n != 5 || len(parts[0].pending) != 1 || parts[0].pending[0] != b.addr || parts[0].done || parts[0].at >= far {
		t.Fatalf("first part: %+v", parts)
	}
	if !parts[1].done || parts[1].n != 10 || len(parts[1].pending) != 0 || parts[1].at < far {
		t.Fatalf("last part: %+v", parts)
	}
	if len(final.Messages) != 10 || len(final.Pending) != 0 || len(final.NodeMs) != 2 {
		t.Fatalf("final: %d messages, pending %v, node_ms %+v", len(final.Messages), final.Pending, final.NodeMs)
	}
	for _, nt := range final.NodeMs {
		if (nt.Node == b.addr) != (nt.Ms >= far.Milliseconds()) {
			t.Fatalf("node times: %+v", final.NodeMs)
		}
	}
	plain := svc.Search(context.Background(), q)
	if len(plain.Messages) != 10 || plain.Pending != nil {
		t.Fatalf("plain search: %d, pending %v", len(plain.Messages), plain.Pending)
	}

	// Over HTTP: the first line is written (flushed) before the far node answers.
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		streamParts(rw, nil, func(part func(any, bool)) {
			svc.StatsEach(r.Context(), q, 0, 5, func(res LdStatsResult, done bool) { part(res, done) })
		})
	}))
	defer srv.Close()
	start = time.Now()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content type %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	var lines []struct {
		Data LdStatsResult `json:"data"`
		Done bool          `json:"done"`
		at   time.Duration
	}
	for sc.Scan() {
		var l struct {
			Data LdStatsResult `json:"data"`
			Done bool          `json:"done"`
			at   time.Duration
		}
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		l.at = time.Since(start)
		lines = append(lines, l)
	}
	if len(lines) != 2 || lines[0].Data.Total != 5 || lines[0].Done || lines[0].at >= far || len(lines[0].Data.Pending) != 1 {
		t.Fatalf("first line: %+v", lines)
	}
	if lines[1].Data.Total != 10 || !lines[1].Done || lines[1].at < far || lines[1].Data.Pending != nil {
		t.Fatalf("last line: %+v", lines)
	}

	// A node that fails is reported in the part that is last, and the answer still ends.
	b.mg.cl.Stop()
	closePeerConns()
	ldPeerTestDelay = 0
	parts = nil
	final = svc.SearchEach(context.Background(), q, func(r LdResult, done bool) {
		parts = append(parts, part{len(r.Messages), r.Pending, done, 0})
	})
	if len(final.NodeErrors) != 1 || len(final.Messages) != 5 || !parts[len(parts)-1].done {
		t.Fatalf("a node down: errors %+v, %d messages, parts %+v", final.NodeErrors, len(final.Messages), parts)
	}
}
