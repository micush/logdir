package main

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
	ctx, cancel := context.WithTimeout(r.Context(), ldReportTimeout(q))
	defer cancel()
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": ldSvc.Search(ctx, q)})
}

func (w *WebServer) handleLogTail(rw http.ResponseWriter, r *http.Request, s *session) {
	if !w.logGate(rw) {
		return
	}
	v := r.URL.Query()
	q := LdQuery{Q: strings.TrimSpace(v.Get("q")), Host: strings.TrimSpace(v.Get("host")), Prog: strings.TrimSpace(v.Get("program")), MinSev: -1}
	if x := strings.TrimSpace(v.Get("severity")); x != "" {
		if q.MinSev = parseSevName(x); q.MinSev < 0 {
			jsonError(rw, http.StatusBadRequest, "severity: not a severity")
			return
		}
	}
	q.Limit, _ = strconv.Atoi(v.Get("limit"))
	if q.Limit <= 0 {
		q.Limit = 100
	}
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
		step = (q.To - q.From) / 1000 / 60
		if step < 60 {
			step = 60
		}
		step = step / 60 * 60
	}
	if step > 0 && (q.To-q.From)/1000/step > 2000 {
		jsonError(rw, http.StatusBadRequest, "step: too small for this range (at most 2000 points)")
		return
	}
	top, _ := strconv.Atoi(r.URL.Query().Get("top"))
	ctx, cancel := context.WithTimeout(r.Context(), ldReportTimeout(q))
	defer cancel()
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": ldSvc.Stats(ctx, q, step, top)})
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
	{name: "q", typ: "string", desc: "Words to find in the host, program and message, case ignored: `link down`, `denied and 10.1.1.5`, `login or sudo`, `error and not timeout` (also !timeout). Plain words next to each other are one phrase."},
	{name: "range", typ: "string", desc: "How far back: 15m, 1h (default), 6h, 1d, 7d, 30d, or all (as far back as any node holds). Or give from and to (unix seconds or RFC 3339)."},
	{name: "from", typ: "string", desc: "Start of the range (instead of range)."},
	{name: "to", typ: "string", desc: "End of the range, default now."},
	{name: "host", typ: "string", desc: "Only messages that say they come from this host (exact, or with * wildcards: sw*, *.example.com)."},
	{name: "program", typ: "string", desc: "Only this program, tag or application name (sshd, %LINK-3-UPDOWN, TRAFFIC; * wildcards work)."},
	{name: "severity", typ: "string", desc: "This severe or worse: emerg, alert, crit, err, warning, notice, info, debug (or 0 to 7). `err` gives emerg, alert, crit and err.", enum: []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}},
	{name: "source", typ: "string", desc: "Only messages received from this sender: its address, or its name (the name on Configure ▸ Senders, the name its address reverse-looks-up to, or the host name its messages give; * wildcards: sw*.example.com)."},
	{name: "limit", typ: "integer", desc: "How many messages, default 200, at most 5000."},
	{name: "oldest_first", typ: "string", desc: "true: the oldest matches of the range (default: the newest).", enum: []string{"true", "false"}},
}

func logQS(c *mcpCtx, extra map[string]string) string {
	v := url.Values{}
	for _, p := range logSearchProps {
		if s := c.str(p.name); s != "" {
			v.Set(p.name, s)
		}
	}
	for _, k := range []string{"step", "top", "after"} {
		if s := c.str(k); s != "" {
			v.Set(k, s)
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
				{name: "top", typ: "integer", desc: "How many hosts and programs, default 20."},
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
		{name: "logs_status", title: "Log collector status",
			desc:   "The state of the log collector on every node: what it listens on, messages received (by udp, tcp and tls), refused and dropped, what is stored and how much disk it uses, and the oldest message each node holds.",
			noNode: true,
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/status")
				return asAny(b), err
			}},
	}
}
