package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The log API: /api/logs/{search,tail,stats,senders,status}, and the MCP tools on top of them (logs_search, logs_tail, logs_stats,
// logs_senders, logs_status).  Times in a request are a range back from now (15m, 1h, 6h, 1d, 7d, 30d, all) or from and to as
// unix seconds, unix milliseconds or RFC 3339.

// parseLdTime reads a time: unix seconds, unix milliseconds or RFC 3339.  The result is milliseconds.
func parseLdTime(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		if n < 1e11 {
			return n * 1000, nil
		}
		return n, nil
	}
	for _, l := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(l, s); err == nil {
			return t.UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("%q is not a time: use unix seconds or 2026-11-20T10:00:00Z", s)
}

// parseLdQuery reads the arguments of a search.
var ldNameRE = regexp.MustCompile(`^[A-Za-z0-9._*-]{1,200}$`)

func (s *ldService) parseLdQuery(v url.Values) (LdQuery, error) {
	q := LdQuery{Q: strings.TrimSpace(v.Get("q")), Host: strings.TrimSpace(v.Get("host")), Prog: strings.TrimSpace(v.Get("program")), MinSev: -1}
	if q.Prog == "" {
		q.Prog = strings.TrimSpace(v.Get("prog"))
	}
	if x := strings.TrimSpace(v.Get("severity")); x != "" {
		if strings.HasSuffix(x, "=") { // "warning=": only warnings, not warning or worse
			x, q.SevExact = strings.TrimSpace(strings.TrimSuffix(x, "=")), true
		} else if strings.HasSuffix(x, "-") { // "info-": info or less severe (info and debug)
			x, q.SevLess = strings.TrimSpace(strings.TrimSuffix(x, "-")), true
		}
		if q.MinSev = parseSevName(x); q.MinSev < 0 {
			return q, fmt.Errorf("severity: %q is not one of emerg, alert, crit, err, warning, notice, info, debug (or 0 to 7)", x)
		}
	}
	if x := v.Get("source"); x != "" {
		if a, err := parseAddrLoose(x); err == nil {
			q.Src = a
		} else if ldNameRE.MatchString(x) {
			q.SrcName = strings.ToLower(x) // a name or a pattern: sw1, sw*.example.com
		} else {
			return q, fmt.Errorf("source: %q is neither an address nor a name (letters, digits, . - _ and * wildcards)", x)
		}
	}
	q.Limit, _ = strconv.Atoi(v.Get("limit"))
	q.Field = strings.TrimSpace(v.Get("field"))
	if m := v.Get("measure"); m != "" {
		kind, mf, err := parseLdMeasure(m)
		if err != nil {
			return q, err
		}
		if kind != "" && q.Field == "" {
			return q, errors.New("measure: counts per value of a field: give field too")
		}
		q.Measure, q.MField = kind, mf
	}
	for _, x := range v["values"] { // one value each (values=a&values=b): a value may hold a comma
		if q.Field == "" {
			return q, errors.New("values: values of a field: give field too")
		}
		if x != "" && len(q.Values) < ldValuesMax && len(x) <= 1024 {
			q.Values = append(q.Values, x)
		}
	}
	q.Fields = v.Get("fields") == "1" || v.Get("fields") == "true"
	q.Oldest = v.Get("oldest_first") == "1" || v.Get("oldest_first") == "true"
	now := time.Now()
	nowMs := now.UnixMilli()
	switch {
	case v.Get("from") != "" || v.Get("to") != "":
		var err error
		q.To = nowMs
		if v.Get("to") != "" {
			if q.To, err = parseLdTime(v.Get("to")); err != nil {
				return q, err
			}
		}
		if v.Get("from") == "" {
			return q, fmt.Errorf("from: needed with to")
		}
		if q.From, err = parseLdTime(v.Get("from")); err != nil {
			return q, err
		}
		if q.From >= q.To {
			return q, fmt.Errorf("from is not before to")
		}
	case strings.EqualFold(strings.TrimSpace(v.Get("range")), "all"):
		q.To = nowMs
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		q.From = s.oldestAll(ctx)
		cancel()
		if q.From == 0 {
			q.From = nowMs - 3600*1000
		}
	default:
		d, err := parseSince(firstNonEmpty(v.Get("range"), "1h"))
		if err != nil || d == 0 {
			return q, fmt.Errorf("range: use 15m, 1h, 6h, 1d, 7d, 30d, all, or from and to")
		}
		q.To, q.From = nowMs, nowMs-d.Milliseconds()
	}
	if err := q.Validate(); err != nil {
		return q, err
	}
	return q, nil
}

func parseAddrLoose(s string) (string, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("%q is not an address", s)
	}
	return a.Unmap().String(), nil
}

func (w *WebServer) logGate(rw http.ResponseWriter) bool {
	if ldSvc == nil {
		jsonError(rw, http.StatusServiceUnavailable, "this node has no log collector")
		return false
	}
	return true
}

func ldReportTimeout(q LdQuery) time.Duration {
	d := ldRoundTimeout(q.From, q.To) + 10*time.Second
	return d
}

func (w *WebServer) handleLogSearch(rw http.ResponseWriter, r *http.Request, s *session) {
	if !w.logGate(rw) {
		return
	}
	q, err := ldSvc.parseLdQuery(r.URL.Query())
	if err != nil {
		jsonError(rw, http.StatusBadRequest, err.Error())
		return
	}
	if r.URL.Query().Get("limit") == "" { // the Logs page does not ask: the list is as long as Configure says
		q.Limit = ldSvc.listLimit()
	}
	ctx, cancel := context.WithTimeout(r.Context(), ldReportTimeout(q))
	defer cancel()
	follow := ldSvc.config().Follow
	if r.URL.Query().Get("stream") == "1" {
		streamParts(rw, follow, func(part func(any, bool)) {
			ldSvc.SearchEach(ctx, q, func(res LdResult, done bool) { part(res, done) })
		})
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": ldSvc.Search(ctx, q), "follow": follow})
}

// streamParts answers with one JSON line per part of a cluster-wide answer (application/x-ndjson): {"ok":true,"data":…,"done":…},
// each line the whole answer so far, the last with "done":true.  Nodes that answer within a few milliseconds of each other go out as one
// line, and a slow reader never holds up the nodes' answers: only the newest part is waiting to be written.
func streamParts(rw http.ResponseWriter, follow any, run func(part func(v any, done bool))) {
	h := rw.Header()
	h.Set("Content-Type", "application/x-ndjson")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	rw.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(rw)
	var mu sync.Mutex
	var latest any
	var latestDone, have bool
	sig := make(chan struct{}, 1)
	fin := make(chan struct{})
	go func() {
		defer close(fin)
		run(func(v any, done bool) {
			mu.Lock()
			latest, latestDone, have = v, done, true
			mu.Unlock()
			select {
			case sig <- struct{}{}:
			default:
			}
		})
	}()
	enc := json.NewEncoder(rw)
	write := func() (done bool) {
		mu.Lock()
		v, d, ok := latest, latestDone, have
		have = false
		mu.Unlock()
		if !ok {
			return false
		}
		line := map[string]any{"ok": true, "data": v, "done": d}
		if follow != nil {
			line["follow"] = follow
		}
		if enc.Encode(line) == nil {
			rc.Flush()
		}
		return d
	}
	for {
		select {
		case <-sig:
			time.Sleep(ldStreamGather)
			if write() {
				<-fin
				return
			}
		case <-fin:
			write()
			return
		}
	}
}

// ldValuesMax is how many values of a field one count draws over time (values=).
const ldValuesMax = 10

// ldStreamGather: after a node answers, how long a streamed answer waits for others before it writes a line.
const ldStreamGather = 20 * time.Millisecond

func (w *WebServer) handleLogTail(rw http.ResponseWriter, r *http.Request, s *session) {
	if !w.logGate(rw) {
		return
	}
	v := r.URL.Query()
	q := LdQuery{Q: strings.TrimSpace(v.Get("q")), Host: strings.TrimSpace(v.Get("host")), Prog: strings.TrimSpace(v.Get("program")), MinSev: -1}
	if x := strings.TrimSpace(v.Get("severity")); x != "" {
		if strings.HasSuffix(x, "=") {
			x, q.SevExact = strings.TrimSpace(strings.TrimSuffix(x, "=")), true
		} else if strings.HasSuffix(x, "-") {
			x, q.SevLess = strings.TrimSpace(strings.TrimSuffix(x, "-")), true
		}
		if q.MinSev = parseSevName(x); q.MinSev < 0 {
			jsonError(rw, http.StatusBadRequest, "severity: not a severity")
			return
		}
	}
	q.Limit, _ = strconv.Atoi(v.Get("limit"))
	if q.Limit <= 0 {
		q.Limit = 100
	}
	q.Fields = v.Get("fields") == "1" || v.Get("fields") == "true"
	if after := v.Get("after"); after != "" {
		t, err := parseLdTime(after)
		if err != nil {
			jsonError(rw, http.StatusBadRequest, err.Error())
			return
		}
		q.From = t
	} else {
		q.From = time.Now().Add(-time.Hour).UnixMilli()
	}
	if err := q.Validate(); err != nil {
		jsonError(rw, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": ldSvc.Tail(ctx, q)})
}

func (w *WebServer) handleLogStats(rw http.ResponseWriter, r *http.Request, s *session) {
	if !w.logGate(rw) {
		return
	}
	q, err := ldSvc.parseLdQuery(r.URL.Query())
	if err != nil {
		jsonError(rw, http.StatusBadRequest, err.Error())
		return
	}
	step := int64(0)
	if x := r.URL.Query().Get("step"); x != "" {
		d, err := parseSince(x)
		if err != nil || d < time.Minute {
			jsonError(rw, http.StatusBadRequest, "step: 1m or more")
			return
		}
		step = int64(d / time.Second)
	} else if r.URL.Query().Get("series") == "1" {
		step = ldSeriesStep(q.To - q.From)
	}
	if step > 0 && (q.To-q.From)/1000/step > 2000 {
		jsonError(rw, http.StatusBadRequest, "step: too small for this range (at most 2000 points)")
		return
	}
	top, _ := strconv.Atoi(r.URL.Query().Get("top"))
	ctx, cancel := context.WithTimeout(r.Context(), ldReportTimeout(q))
	defer cancel()
	if r.URL.Query().Get("stream") == "1" {
		streamParts(rw, nil, func(part func(any, bool)) {
			ldSvc.StatsEach(ctx, q, step, top, func(res LdStatsResult, done bool) { part(res, done) })
		})
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": ldSvc.Stats(ctx, q, step, top)})
}

// ldSeriesStep is the bucket of the Logs chart over span milliseconds: about 60 bars, whole minutes; from 30 minutes up a whole number
// of hours (1, 2, 3, 4, 6, 8, 12 or 24, then whole days), so each bar holds whole hours and the closed ones come from their rollups.
func ldSeriesStep(span int64) int64 {
	step := span / 1000 / 60
	if step < 1800 {
		if step < 60 {
			step = 60
		}
		return step / 60 * 60
	}
	for _, hrs := range []int64{1, 2, 3, 4, 6, 8, 12, 24} {
		if step <= hrs*3600 {
			return hrs * 3600
		}
	}
	return (step + 86399) / 86400 * 86400
}

// GET /api/logs/shipping: every destination on every node.
func (w *WebServer) handleLogShipping(rw http.ResponseWriter, r *http.Request, s *session) {
	if !w.logGate(rw) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	st, errs, n := ldSvc.AllShipStatus(ctx)
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"destinations": st, "node_errors": errs, "nodes": n}})
}

// POST /api/logs/shipping/test {name, node} or {dest} (a destination from the form, not saved yet: from this node), and
// POST /api/logs/shipping/reset {name, node, from}.
func (w *WebServer) handleLogShipOp(kind string) func(http.ResponseWriter, *http.Request, *session) {
	return func(rw http.ResponseWriter, r *http.Request, s *session) {
		if !w.logGate(rw) {
			return
		}
		var op ldShipOp
		if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(&op); err != nil {
			jsonError(rw, http.StatusBadRequest, "bad request")
			return
		}
		if op.Dest == nil && strings.TrimSpace(op.Name) == "" {
			jsonError(rw, http.StatusBadRequest, "name: which destination")
			return
		}
		if kind == "shipreset" {
			if op.Dest != nil {
				jsonError(rw, http.StatusBadRequest, "a reset needs the name of a saved destination")
				return
			}
			if _, err := ldShipStart(op.From, time.Now()); err != nil {
				jsonError(rw, http.StatusBadRequest, err.Error())
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		res, errs := ldSvc.ShipAll(ctx, kind, op)
		what := map[string]string{"shiptest": "test message", "shipreset": "cursor moved to " + firstNonEmpty(op.From, "now")}[kind]
		name := op.Name
		if op.Dest != nil {
			name = op.Dest.Name + " (not saved)"
		}
		infof("logs: shipping %s: %s, by %s", name, what, s.user)
		writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"results": res, "node_errors": errs}})
	}
}

func (w *WebServer) handleLogSenders(rw http.ResponseWriter, r *http.Request, s *session) {
	if !w.logGate(rw) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, errs := ldSvc.AllSenders(ctx)
	if out == nil {
		out = []LdSenderInfo{}
	}
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"senders": out, "node_errors": errs}})
}

func (w *WebServer) handleLogStatus(rw http.ResponseWriter, r *http.Request, s *session) {
	if !w.logGate(rw) {
		return
	}
	if r.URL.Query().Get("local") == "1" { // this node only (the Settings page reads the folder in use)
		writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"nodes": []LdNodeStatus{ldSvc.Status()}}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	st, errs := ldSvc.AllStatus(ctx)
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"nodes": st, "node_errors": errs}})
}

// ── MCP tools ────────────────────────────────────────────────────────────────────────────────────────────────────

var logSearchProps = []mcpProp{
	{name: "q", typ: "string", desc: "Words to find in the host, program and message, case ignored: `link down`, `denied and 10.1.1.5`, `login or sudo`, `error and not timeout` (also !timeout). Plain words next to each other are one phrase. " +
		"A word name=value is a field of the message body (key=value pairs, JSON, CEF, PAN-OS CSV, Cisco %FAC-SEV-MNEMONIC, structured data): `action=deny`, `dst_port=443`, `user=ro*`, " +
		"`bytes>1000000` (> < >= <= need a number), `not action=allow`. Common names work across vendors: src_ip, dst_ip, src_port, dst_port, action, proto, user. fields=true shows what a message has."},
	{name: "range", typ: "string", desc: "How far back: 15m, 1h (default), 6h, 1d, 7d, 30d, or all (as far back as any node holds). Or give from and to (unix seconds or RFC 3339)."},
	{name: "from", typ: "string", desc: "Start of the range (instead of range)."},
	{name: "to", typ: "string", desc: "End of the range, default now."},
	{name: "host", typ: "string", desc: "Only messages that say they come from this host (exact, or with * wildcards: sw*, *.example.com)."},
	{name: "program", typ: "string", desc: "Only this program, tag or application name (sshd, %LINK-3-UPDOWN, TRAFFIC; * wildcards work)."},
	{name: "severity", typ: "string", desc: "This severe or worse: emerg, alert, crit, err, warning, notice, info, debug (or 0 to 7). `err` gives emerg, alert, crit and err.", enum: []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}},
	{name: "source", typ: "string", desc: "Only messages received from this sender: its address, or its name (the name on Configure ▸ Senders, the name its address reverse-looks-up to, or the host name its messages give; * wildcards: sw*.example.com)."},
	{name: "limit", typ: "integer", desc: "How many messages, default 200, at most 5000."},
	{name: "oldest_first", typ: "string", desc: "true: the oldest matches of the range (default: the newest).", enum: []string{"true", "false"}},
	{name: "fields", typ: "string", desc: "true: list each message's fields (name and value) with it.", enum: []string{"true", "false"}},
}

func logQS(c *mcpCtx, extra map[string]string) string {
	v := url.Values{}
	for _, p := range logSearchProps {
		if s := c.str(p.name); s != "" {
			v.Set(p.name, s)
		}
	}
	for _, k := range []string{"step", "top", "after", "field", "measure"} {
		if s := c.str(k); s != "" {
			v.Set(k, s)
		}
	}
	for _, x := range strings.Split(c.str("values"), ",") {
		if x = strings.TrimSpace(x); x != "" {
			v.Add("values", x)
		}
	}
	for k, x := range extra {
		v.Set(k, x)
	}
	return v.Encode()
}

// logMCPTools are the log tools, added to the list in mcp.go's init.
func logMCPTools() []mcpTool {
	return []mcpTool{
		{name: "logs_search", title: "Search the logs",
			desc: "Search the syslog the cluster collects: every node keeps what it received, so the search asks every node and merges the answers by time (newest last). " +
				"Each message has time (UTC), host, program, pid, facility, severity, message, the sender address (source), how it arrived (udp, tcp, tls) and the node that received it. " +
				"Narrow with q (words, and/or/not), host, program, severity (this severe or worse), source and a range; the answer says when there are more matches than the limit (then it holds the newest) and node_errors names a node that did not answer. " +
				"Start with logs_stats to see which hosts and programs are noisy, then search them.",
			noNode: true, props: logSearchProps,
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/search?" + logQS(c, nil))
				return asAny(b), err
			}},
		{name: "logs_tail", title: "Newest log messages",
			desc:   "The newest messages that match (default the last 100), from every node, newest last. Give `after` (a unix time in milliseconds, a message's t) to get only what came since, to follow the log.",
			noNode: true,
			props: []mcpProp{
				{name: "q", typ: "string", desc: "Words to find (see logs_search)."},
				{name: "host", typ: "string", desc: "Only this host."},
				{name: "program", typ: "string", desc: "Only this program."},
				{name: "severity", typ: "string", desc: "This severe or worse.", enum: []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}},
				{name: "limit", typ: "integer", desc: "How many, default 100."},
				{name: "after", typ: "string", desc: "Only messages after this time (milliseconds since 1970, as t in an earlier answer)."},
			},
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/tail?" + logQS(c, nil))
				return asAny(b), err
			}},
		{name: "logs_stats", title: "Log counts",
			desc: "How many messages match, in total, by severity, by the busiest hosts and programs, and (with step) per interval: the quickest way to see what is noisy, when something started or whether a host went quiet. " +
				"Takes the same filters as logs_search. A message a sender sent to two nodes is counted on both.",
			noNode: true,
			props: append([]mcpProp{
				{name: "step", typ: "string", desc: "Width of a point in the series: 1m, 5m, 1h ... Absent: no series."},
				{name: "top", typ: "integer", desc: "How many hosts and programs (and values of field), default 20."},
				{name: "field", typ: "string", desc: "Also count the values of this field among the matching messages (top_values): dst_port, action, src_ip, rule, or host, program, sender, severity … Reads every matching message, so a long range takes longer."},
				{name: "measure", typ: "string", desc: "With field: per value, also count distinct:FIELD (how many different values of another field, e.g. distinct:dst_port per src_ip: a port scan; exact up to 128, about 1.6% off past that) or sum:FIELD (add up its numbers, e.g. sum:bytes). top_values are then ordered by it."},
				{name: "values", typ: "string", desc: "With field and step: these values of the field (comma-separated, up to 10) counted per bucket, in value_series."},
			}, logSearchProps[:8]...),
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/stats?" + logQS(c, nil))
				return asAny(b), err
			}},
		{name: "logs_senders", title: "Log senders",
			desc:   "The hosts sending syslog to the cluster: address, the name it was given on Configure ▸ Senders, the host name its messages give, how it sends (udp, tcp, tls), when first and last heard, messages and bytes, and which node receives them.",
			noNode: true,
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/senders")
				return asAny(b), err
			}},
		{name: "logs_shipping", title: "Log shipping status",
			desc: "Where each node sends the syslog on to (Configure ▸ Shipping): for every destination on every node, its state (ok, behind, error, disabled), messages sent " +
				"(in total and in the last minute), how far behind it is (seconds), messages skipped because it fell too far behind, when it last sent and its last error.",
			noNode: true,
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/shipping")
				return asAny(b), err
			}},
		{name: "logs_status", title: "Log collector status",
			desc:   "The state of the log collector on every node: what it listens on, messages received (by udp, tcp and tls), refused and dropped, how many each drop rule kept out (drop_rules: messages that match one are never stored, so a search cannot find them), what is stored and how much disk it uses, and the oldest message each node holds.",
			noNode: true,
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/status")
				return asAny(b), err
			}},
	}
}
