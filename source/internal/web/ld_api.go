package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"logdir/internal/core"
	"logdir/internal/logs"
)

func (w *WebServer) logGate(rw http.ResponseWriter) bool {
	if logs.LdSvc == nil {
		core.JsonError(rw, http.StatusServiceUnavailable, "this node has no log collector")
		return false
	}
	return true
}

func (w *WebServer) handleLogSearch(rw http.ResponseWriter, r *http.Request, s *Session) {
	if !w.logGate(rw) {
		return
	}
	q, err := logs.LdSvc.ParseLdQuery(r.URL.Query())
	if err != nil {
		core.JsonError(rw, http.StatusBadRequest, err.Error())
		return
	}
	if r.URL.Query().Get("limit") == "" { // the Logs page does not ask: the list is as long as Configure says
		q.Limit = logs.LdSvc.ListLimit()
	}
	ctx, cancel := context.WithTimeout(r.Context(), logs.LdReportTimeout(q))
	defer cancel()
	follow := logs.LdSvc.Config().Follow
	if r.URL.Query().Get("stream") == "1" {
		logs.StreamParts(rw, follow, func(part func(any, bool)) {
			logs.LdSvc.SearchEach(ctx, q, func(res logs.LdResult, done bool) { part(res, done) })
		})
		return
	}
	core.WriteJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": logs.LdSvc.Search(ctx, q), "follow": follow})
}

func (w *WebServer) handleLogTail(rw http.ResponseWriter, r *http.Request, s *Session) {
	if !w.logGate(rw) {
		return
	}
	v := r.URL.Query()
	q := core.LdQuery{Q: strings.TrimSpace(v.Get("q")), Host: strings.TrimSpace(v.Get("host")), Prog: strings.TrimSpace(v.Get("program")), MinSev: -1}
	if x := strings.TrimSpace(v.Get("severity")); x != "" {
		if strings.HasSuffix(x, "=") {
			x, q.SevExact = strings.TrimSpace(strings.TrimSuffix(x, "=")), true
		} else if strings.HasSuffix(x, "-") {
			x, q.SevLess = strings.TrimSpace(strings.TrimSuffix(x, "-")), true
		}
		if q.MinSev = core.ParseSevName(x); q.MinSev < 0 {
			core.JsonError(rw, http.StatusBadRequest, "severity: not a severity")
			return
		}
	}
	q.Limit, _ = strconv.Atoi(v.Get("limit"))
	if q.Limit <= 0 {
		q.Limit = 100
	}
	q.Fields = v.Get("fields") == "1" || v.Get("fields") == "true"
	if after := v.Get("after"); after != "" {
		t, err := logs.ParseLdTime(after)
		if err != nil {
			core.JsonError(rw, http.StatusBadRequest, err.Error())
			return
		}
		q.From = t
	} else {
		q.From = time.Now().Add(-time.Hour).UnixMilli()
	}
	if err := q.Validate(); err != nil {
		core.JsonError(rw, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	core.WriteJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": logs.LdSvc.Tail(ctx, q)})
}

func (w *WebServer) handleLogStats(rw http.ResponseWriter, r *http.Request, s *Session) {
	if !w.logGate(rw) {
		return
	}
	q, err := logs.LdSvc.ParseLdQuery(r.URL.Query())
	if err != nil {
		core.JsonError(rw, http.StatusBadRequest, err.Error())
		return
	}
	step := int64(0)
	if x := r.URL.Query().Get("step"); x != "" {
		d, err := core.ParseSince(x)
		if err != nil || d < time.Minute {
			core.JsonError(rw, http.StatusBadRequest, "step: 1m or more")
			return
		}
		step = int64(d / time.Second)
	} else if r.URL.Query().Get("series") == "1" {
		step = logs.LdSeriesStep(q.To - q.From)
	}
	if step > 0 && (q.To-q.From)/1000/step > 2000 {
		core.JsonError(rw, http.StatusBadRequest, "step: too small for this range (at most 2000 points)")
		return
	}
	top, _ := strconv.Atoi(r.URL.Query().Get("top"))
	ctx, cancel := context.WithTimeout(r.Context(), logs.LdReportTimeout(q))
	defer cancel()
	if r.URL.Query().Get("stream") == "1" {
		logs.StreamParts(rw, nil, func(part func(any, bool)) {
			logs.LdSvc.StatsEach(ctx, q, step, top, func(res logs.LdStatsResult, done bool) { part(res, done) })
		})
		return
	}
	core.WriteJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": logs.LdSvc.Stats(ctx, q, step, top)})
}

// GET /api/logs/shipping: every destination on every node.
func (w *WebServer) handleLogShipping(rw http.ResponseWriter, r *http.Request, s *Session) {
	if !w.logGate(rw) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	st, errs, n := logs.LdSvc.AllShipStatus(ctx)
	core.WriteJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"destinations": st, "node_errors": errs, "nodes": n}})
}

// POST /api/logs/shipping/test {name, node} or {dest} (a destination from the form, not saved yet: from this node), and
// POST /api/logs/shipping/reset {name, node, from}.
func (w *WebServer) handleLogShipOp(kind string) func(http.ResponseWriter, *http.Request, *Session) {
	return func(rw http.ResponseWriter, r *http.Request, s *Session) {
		if !w.logGate(rw) {
			return
		}
		var op logs.LdShipOp
		if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20)).Decode(&op); err != nil {
			core.JsonError(rw, http.StatusBadRequest, "bad request")
			return
		}
		if op.Dest == nil && strings.TrimSpace(op.Name) == "" {
			core.JsonError(rw, http.StatusBadRequest, "name: which destination")
			return
		}
		if kind == "shipreset" {
			if op.Dest != nil {
				core.JsonError(rw, http.StatusBadRequest, "a reset needs the name of a saved destination")
				return
			}
			if _, err := core.LdShipStart(op.From, time.Now()); err != nil {
				core.JsonError(rw, http.StatusBadRequest, err.Error())
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		res, errs := logs.LdSvc.ShipAll(ctx, kind, op)
		what := map[string]string{"shiptest": "test message", "shipreset": "cursor moved to " + core.FirstNonEmpty(op.From, "now")}[kind]
		name := op.Name
		if op.Dest != nil {
			name = op.Dest.Name + " (not saved)"
		}
		core.Infof("logs: shipping %s: %s, by %s", name, what, s.User)
		core.WriteJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"results": res, "node_errors": errs}})
	}
}

func (w *WebServer) handleLogSenders(rw http.ResponseWriter, r *http.Request, s *Session) {
	if !w.logGate(rw) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, errs := logs.LdSvc.AllSenders(ctx)
	if out == nil {
		out = []logs.LdSenderInfo{}
	}
	core.WriteJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"senders": out, "node_errors": errs}})
}

func (w *WebServer) handleLogStatus(rw http.ResponseWriter, r *http.Request, s *Session) {
	if !w.logGate(rw) {
		return
	}
	if r.URL.Query().Get("local") == "1" { // this node only (the Settings page reads the folder in use)
		core.WriteJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"nodes": []logs.LdNodeStatus{logs.LdSvc.Status()}}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	st, errs := logs.LdSvc.AllStatus(ctx)
	core.WriteJSON(rw, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"nodes": st, "node_errors": errs}})
}
