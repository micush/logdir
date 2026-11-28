package logs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"logdir/internal/core"
)

// The log API: /api/logs/{search,tail,stats,senders,status}, and the MCP tools on top of them (logs_search, logs_tail, logs_stats,
// logs_senders, logs_status).  Times in a request are a range back from now (15m, 1h, 6h, 1d, 7d, 30d, all) or from and to as
// unix seconds, unix milliseconds or RFC 3339.

// parseLdTime reads a time: unix seconds, unix milliseconds or RFC 3339.  The result is milliseconds.
func ParseLdTime(s string) (int64, error) {
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

func (s *LdService) ParseLdQuery(v url.Values) (core.LdQuery, error) {
	q := core.LdQuery{Q: strings.TrimSpace(v.Get("q")), Host: strings.TrimSpace(v.Get("host")), Prog: strings.TrimSpace(v.Get("program")), MinSev: -1}
	if q.Prog == "" {
		q.Prog = strings.TrimSpace(v.Get("prog"))
	}
	if x := strings.TrimSpace(v.Get("severity")); x != "" {
		if strings.HasSuffix(x, "=") { // "warning=": only warnings, not warning or worse
			x, q.SevExact = strings.TrimSpace(strings.TrimSuffix(x, "=")), true
		} else if strings.HasSuffix(x, "-") { // "info-": info or less severe (info and debug)
			x, q.SevLess = strings.TrimSpace(strings.TrimSuffix(x, "-")), true
		}
		if q.MinSev = core.ParseSevName(x); q.MinSev < 0 {
			return q, fmt.Errorf("severity: %q is not one of emerg, alert, crit, err, warning, notice, info, debug (or 0 to 7)", x)
		}
	}
	if x := v.Get("source"); x != "" {
		if a, err := core.ParseAddrLoose(x); err == nil {
			q.Src = a
		} else if core.LdNameRE.MatchString(x) {
			q.SrcName = strings.ToLower(x) // a name or a pattern: sw1, sw*.example.com
		} else {
			return q, fmt.Errorf("source: %q is neither an address nor a name (letters, digits, . - _ and * wildcards)", x)
		}
	}
	q.Limit, _ = strconv.Atoi(v.Get("limit"))
	q.Field = strings.TrimSpace(v.Get("field"))
	if m := v.Get("measure"); m != "" {
		kind, mf, err := core.ParseLdMeasure(m)
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
			if q.To, err = ParseLdTime(v.Get("to")); err != nil {
				return q, err
			}
		}
		if v.Get("from") == "" {
			return q, fmt.Errorf("from: needed with to")
		}
		if q.From, err = ParseLdTime(v.Get("from")); err != nil {
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
		d, err := core.ParseSince(core.FirstNonEmpty(v.Get("range"), "1h"))
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

func LdReportTimeout(q core.LdQuery) time.Duration {
	d := ldRoundTimeout(q.From, q.To) + 10*time.Second
	return d
}

// streamParts answers with one JSON line per part of a cluster-wide answer (application/x-ndjson): {"ok":true,"data":…,"done":…},
// each line the whole answer so far, the last with "done":true.  Nodes that answer within a few milliseconds of each other go out as one
// line, and a slow reader never holds up the nodes' answers: only the newest part is waiting to be written.
func StreamParts(rw http.ResponseWriter, follow any, run func(part func(v any, done bool))) {
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

// ldSeriesStep is the bucket of the Logs chart over span milliseconds: about 60 bars, whole minutes; from 12 minutes up (a span of
// 12 hours or more) a whole number of hours (1, 2, 3, 4, 6, 8, 12 or 24, then whole days), so each bar holds whole hours and the
// closed ones come from their rollups: a day is 24 bars of an hour, not 60 of 24 minutes that would read every message.
func LdSeriesStep(span int64) int64 {
	step := span / 1000 / 60
	if step < 720 {
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

// ── MCP tools ────────────────────────────────────────────────────────────────────────────────────────────────────

var LogSearchProps = []core.McpProp{
	{Name: "q", Typ: "string", Desc: "Words to find in the host, program and message, case ignored: `link down`, `denied and 10.1.1.5`, `login or sudo`, `error and not timeout` (also !timeout). Plain words next to each other are one phrase. " +
		"A word name=value is a field of the message body (key=value pairs, JSON, CEF, PAN-OS CSV, Cisco %FAC-SEV-MNEMONIC, structured data): `action=deny`, `dst_port=443`, `user=ro*`, " +
		"`bytes>1000000` (> < >= <= need a number), `not action=allow`. Common names work across vendors: src_ip, dst_ip, src_port, dst_port, action, proto, user. fields=true shows what a message has."},
	{Name: "range", Typ: "string", Desc: "How far back: 15m, 1h (default), 6h, 1d, 7d, 30d, or all (as far back as any node holds). Or give from and to (unix seconds or RFC 3339)."},
	{Name: "from", Typ: "string", Desc: "Start of the range (instead of range)."},
	{Name: "to", Typ: "string", Desc: "End of the range, default now."},
	{Name: "host", Typ: "string", Desc: "Only messages that say they come from this host (exact, or with * wildcards: sw*, *.example.com)."},
	{Name: "program", Typ: "string", Desc: "Only this program, tag or application name (sshd, %LINK-3-UPDOWN, TRAFFIC; * wildcards work)."},
	{Name: "severity", Typ: "string", Desc: "This severe or worse: emerg, alert, crit, err, warning, notice, info, debug (or 0 to 7). `err` gives emerg, alert, crit and err.", Enum: []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}},
	{Name: "source", Typ: "string", Desc: "Only messages received from this sender: its address, or its name (the name on Configure ▸ Senders, the name its address reverse-looks-up to, or the host name its messages give; * wildcards: sw*.example.com)."},
	{Name: "limit", Typ: "integer", Desc: "How many messages, default 200, at most 5000."},
	{Name: "oldest_first", Typ: "string", Desc: "true: the oldest matches of the range (default: the newest).", Enum: []string{"true", "false"}},
	{Name: "fields", Typ: "string", Desc: "true: list each message's fields (name and value) with it.", Enum: []string{"true", "false"}},
}
