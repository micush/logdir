package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

// The log collector in the cluster.
//
// Every node keeps what it received, so a search is a question to every node: the node that is asked fans it out (this node
// directly, the others over the signed peer channel of clusternet.go, /cluster/log/query), merges the answers by time and cuts the
// list.  A node that does not answer is named in the answer (node_errors), and what it holds is missing from it.  A message a sender
// sent to two nodes is on both; the merge keeps one of the copies.

type ldQueryMsg struct {
	Op    string    `json:"op"` // search, tail, stats, senders, status, shipstatus, shiptest or shipreset
	Query LdQuery   `json:"query"`
	Step  int64     `json:"step,omitempty"`
	Ship  *ldShipOp `json:"ship,omitempty"`
}

type ldQueryReply struct {
	Answer  *LdAnswer      `json:"answer,omitempty"`
	Stats   *LdStatsAnswer `json:"stats,omitempty"`
	Senders []LdSenderInfo `json:"senders,omitempty"`
	Status  *LdNodeStatus  `json:"status,omitempty"`
	Ship    []LdShipStatus `json:"ship,omitempty"`
	ShipRes *LdShipResult  `json:"ship_result,omitempty"`
}

// ldPeerTestDelay makes this node answer other nodes' log questions late (tests only: a node far away).
var ldPeerTestDelay time.Duration

func (c *Cluster) registerLogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /cluster/log/query", c.peerAuth(c.handleLogQuery))
}

func (c *Cluster) handleLogQuery(rw http.ResponseWriter, r *http.Request, caller ClusterPeer, body []byte) {
	var m ldQueryMsg
	if json.Unmarshal(body, &m) != nil || ldSvc == nil {
		jsonError(rw, http.StatusBadRequest, "bad request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), ldRoundTimeout(m.Query.From, m.Query.To))
	defer cancel()
	if d := ldPeerTestDelay; d > 0 { // tests: a far node
		time.Sleep(d)
	}
	rep, ok := ldSvc.local(ctx, m)
	if !ok {
		jsonError(rw, http.StatusBadRequest, "bad op or query")
		return
	}
	b, _ := json.Marshal(rep)
	rw.Header().Set("Content-Type", "application/json")
	rw.Write(b)
}

// local answers a question from this node's own data.
func (s *ldService) local(ctx context.Context, m ldQueryMsg) (ldQueryReply, bool) {
	var r ldQueryReply
	switch m.Op {
	case "search", "tail", "stats":
		if err := m.Query.Validate(); err != nil {
			return r, false
		}
		if m.Query.Q != "" {
			m.Query.srcWords = s.senderWords()
		}
		if m.Query.Host != "" {
			m.Query.hostSrc = s.sendersByName(m.Query.Host)
		}
		if m.Query.SrcName != "" {
			m.Query.srcSet = s.sendersByName(m.Query.SrcName)
			if m.Query.srcSet == nil {
				m.Query.srcSet = map[string]bool{}
			}
		}
	}
	switch m.Op {
	case "search":
		a := s.store.Search(ctx, m.Query)
		r.Answer = &a
	case "tail":
		a := s.store.Tail(m.Query)
		r.Answer = &a
	case "stats":
		st := s.store.Stats(ctx, m.Query, m.Step)
		r.Stats = &st
	case "senders":
		r.Senders = s.Senders()
	case "status":
		st := s.Status()
		r.Status = &st
	case "shipstatus":
		r.Ship = s.ShipStatus()
	case "shiptest", "shipreset":
		if m.Ship == nil || m.Ship.Dest != nil { // a destination not saved is tried from the node that was asked only
			return r, false
		}
		if m.Op == "shiptest" {
			r.ShipRes = s.ShipTest(*m.Ship)
		} else {
			r.ShipRes = s.ShipReset(*m.Ship)
		}
	default:
		return r, false
	}
	return r, true
}

// ldRoundTimeout is how long a node gets to answer: a short range is quick, a long one reads many hours.
func ldRoundTimeout(from, to int64) time.Duration {
	if to == 0 {
		to = time.Now().UnixMilli()
	}
	span := (to - from) / 1000
	switch {
	case span <= 6*3600:
		return 10 * time.Second
	case span <= 3*86400:
		return 30 * time.Second
	}
	return 90 * time.Second
}

// NodeError says which node could not answer.
type NodeError struct {
	Node  string `json:"node"`
	Error string `json:"error"`
}

func (s *ldService) otherPeers() []ClusterPeer {
	if s.mg == nil || s.mg.cl == nil || !s.mg.cl.Enabled() {
		return nil
	}
	return s.mg.cl.node.Snapshot().Peers
}

// fan asks every node (this one directly) and returns the answers.  An unreachable node is reported, not fatal.
func (s *ldService) fan(ctx context.Context, m ldQueryMsg) (reps []ldQueryReply, errs []NodeError, nodes int) {
	st := s.fanEach(ctx, m, nil)
	return st.reps, st.errs, st.nodes
}

// NodeTime is how long a node took to answer, as the node that asked saw it (the link's round trips included).
type NodeTime struct {
	Node string `json:"node"`
	Ms   int64  `json:"ms"`
}

// ldFanState is what has come back so far: the answers, the nodes that failed, the time each took, and the nodes still out.
type ldFanState struct {
	reps    []ldQueryReply
	errs    []NodeError
	times   []NodeTime
	pending []string
	nodes   int
	done    bool
}

func (st ldFanState) copy() ldFanState {
	st.reps = append([]ldQueryReply(nil), st.reps...)
	st.errs = append([]NodeError(nil), st.errs...)
	st.times = append([]NodeTime(nil), st.times...)
	st.pending = append([]string(nil), st.pending...)
	sort.Slice(st.errs, func(i, j int) bool { return st.errs[i].Node < st.errs[j].Node })
	sort.Strings(st.pending)
	return st
}

// fanName names this node in node_ms and pending: its cluster address, as the other nodes are named (its host name alone).
func (s *ldService) fanName() string {
	if a := s.selfAddr(); a != "" {
		return a
	}
	return selfHost()
}

// fanEach asks every node at once, this one too, and calls part (when not nil) with a copy of what has come back each time a
// node answers or fails, one call at a time; the last call has done set.  It returns the final state.
func (s *ldService) fanEach(ctx context.Context, m ldQueryMsg, part func(ldFanState)) ldFanState {
	peers := s.otherPeers()
	self := s.fanName()
	var mu sync.Mutex
	var wg sync.WaitGroup
	st := ldFanState{nodes: len(peers) + 1}
	st.pending = append(st.pending, self)
	for _, p := range peers {
		st.pending = append(st.pending, p.Addr)
	}
	to := ldRoundTimeout(m.Query.From, m.Query.To)
	got := func(node string, start time.Time, r *ldQueryReply, err error) {
		mu.Lock()
		defer mu.Unlock()
		for i, n := range st.pending {
			if n == node {
				st.pending = append(st.pending[:i], st.pending[i+1:]...)
				break
			}
		}
		st.times = append(st.times, NodeTime{node, time.Since(start).Milliseconds()})
		switch {
		case err != nil:
			st.errs = append(st.errs, NodeError{node, err.Error()})
		case node == self && len(peers) > 0:
			st.reps = append([]ldQueryReply{*r}, st.reps...) // this node first, as before
		default:
			st.reps = append(st.reps, *r)
		}
		st.done = len(st.pending) == 0
		if part != nil {
			part(st.copy())
		}
	}
	start := time.Now()
	for _, p := range peers {
		wg.Add(1)
		go func(p ClusterPeer) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, to)
			defer cancel()
			var r ldQueryReply
			err := s.mg.cl.call(cctx, p, "POST", "/cluster/log/query", m, &r, to)
			got(p.Addr, start, &r, err)
		}(p)
	}
	lctx, cancel := context.WithTimeout(ctx, to)
	lr, _ := s.local(lctx, m)
	cancel()
	got(self, start, &lr, nil)
	wg.Wait()
	return st.copy()
}

// LdResult is the cluster-wide answer to a search.
type LdResult struct {
	From       int64       `json:"from"`
	To         int64       `json:"to"`
	Nodes      int         `json:"nodes"`
	NodeErrors []NodeError `json:"node_errors,omitempty"`
	Messages   []LdView    `json:"messages"`
	Matched    int         `json:"returned"`
	Truncated  bool        `json:"truncated,omitempty"` // there are more matches than the limit (older ones, or newer when oldest_first)
	Partial    bool        `json:"partial,omitempty"`   // a node ran out of time
	Duplicates int         `json:"duplicates_dropped,omitempty"`
	Note       string      `json:"note,omitempty"`
	Pending    []string    `json:"pending,omitempty"` // nodes not heard from yet: only in a streamed answer before its last part
	NodeMs     []NodeTime  `json:"node_ms,omitempty"` // how long each node took (more than one node)
}

// LdView is a message as the API and the MCP tools show it.
type LdView struct {
	Time     string `json:"time"` // RFC 3339, UTC, milliseconds
	T        int64  `json:"t"`    // milliseconds since 1970
	Host     string `json:"host,omitempty"`
	Program  string `json:"program,omitempty"`
	PID      string `json:"pid,omitempty"`
	Facility string `json:"facility"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source,omitempty"`
	// SourceName is the name the sender's address reverse-looks-up to, when it has one (looked up in the background and kept for 30 minutes).
	SourceName string `json:"source_name,omitempty"`
	Via        string `json:"via,omitempty"`
	Node       string `json:"node,omitempty"`
	Fields     []ldKV `json:"fields,omitempty"` // what the body reads as (ld_fields.go), when the search asks
	MsgID      string `json:"msgid,omitempty"`
	SD         string `json:"structured_data,omitempty"`
}

func ldView(m LdMsg) LdView {
	return LdView{Time: time.UnixMilli(m.T).UTC().Format("2006-01-02T15:04:05.000Z"), T: m.T, Host: m.Host, Program: m.Prog, PID: m.PID,
		Facility: facName(m.Fac), Severity: sevName(m.Sev), Message: m.Msg, Source: m.Src, Via: m.Via, Node: m.Node, MsgID: m.MID, SD: m.SD}
}

const ldDupWindow = 2000 // ms: the same message from the same sender on two nodes within this is one message

// mergeLdAnswers puts the nodes' messages in time order, drops the copies a sender's two sends left on two nodes, and cuts the list to
// the limit (the newest, or the oldest when oldest asks).
func mergeLdAnswers(parts []LdAnswer, q LdQuery) (msgs []LdMsg, truncated, partial bool, dups int) {
	type seen struct {
		t    int64
		node string
	}
	var all []LdMsg
	for _, p := range parts {
		all = append(all, p.Msgs...)
		truncated = truncated || p.Truncated
		partial = partial || p.Partial
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].T != all[j].T {
			return all[i].T < all[j].T
		}
		return all[i].Node < all[j].Node
	})
	by := map[string][]seen{}
	out := all[:0:0]
	for _, m := range all {
		key := m.Src + "\x00" + m.Host + "\x00" + m.Prog + "\x00" + m.Msg
		dup := false
		for _, x := range by[key] {
			if x.node != m.Node && m.T-x.t <= ldDupWindow {
				dup = true
				break
			}
		}
		if dup {
			dups++
			continue
		}
		by[key] = append(by[key], seen{m.T, m.Node})
		out = append(out, m)
	}
	if len(out) > q.Limit {
		truncated = true
		if q.Oldest {
			out = out[:q.Limit]
		} else {
			out = out[len(out)-q.Limit:]
		}
	}
	return out, truncated, partial, dups
}

// Search is the cluster-wide search.
func (s *ldService) Search(ctx context.Context, q LdQuery) LdResult {
	return s.SearchEach(ctx, q, nil)
}

// SearchEach is Search, also calling part (when not nil) with the answer so far each time a node answers; the last call holds every
// node.  Calls are made one at a time.
func (s *ldService) SearchEach(ctx context.Context, q LdQuery, part func(LdResult, bool)) LdResult {
	if q.To == 0 {
		q.To = time.Now().UnixMilli()
	}
	var cb func(ldFanState)
	if part != nil {
		cb = func(st ldFanState) { part(s.searchResult(q, st), st.done) }
	}
	return s.searchResult(q, s.fanEach(ctx, ldQueryMsg{Op: "search", Query: q}, cb))
}

func (s *ldService) searchResult(q LdQuery, st ldFanState) LdResult {
	var parts []LdAnswer
	for _, r := range st.reps {
		if r.Answer != nil {
			parts = append(parts, *r.Answer)
		}
	}
	res := s.result(q, parts, st.errs, st.nodes)
	res.Pending = st.pending
	if st.nodes > 1 {
		res.NodeMs = st.times
	}
	return res
}

// Tail is the newest messages after q.From, cluster-wide.
func (s *ldService) Tail(ctx context.Context, q LdQuery) LdResult {
	q.Oldest = false
	reps, errs, n := s.fan(ctx, ldQueryMsg{Op: "tail", Query: q})
	var parts []LdAnswer
	for _, r := range reps {
		if r.Answer != nil {
			parts = append(parts, *r.Answer)
		}
	}
	return s.result(q, parts, errs, n)
}

func (s *ldService) result(q LdQuery, parts []LdAnswer, errs []NodeError, n int) LdResult {
	msgs, trunc, partial, dups := mergeLdAnswers(parts, q)
	res := LdResult{From: q.From, To: q.To, Nodes: n, NodeErrors: errs, Truncated: trunc, Partial: partial, Duplicates: dups, Messages: make([]LdView, 0, len(msgs))}
	for _, m := range msgs {
		v := ldView(m)
		if q.Fields {
			v.Fields = ldFields(&m)
		}
		res.Messages = append(res.Messages, v)
	}
	res.Matched = len(res.Messages)
	s.nameSources(res.Messages)
	switch {
	case len(errs) > 0:
		res.Note = "some nodes did not answer: what they hold is missing from this list"
	case partial:
		res.Note = "a node ran out of time reading the range: narrow the range or the search"
	case trunc && q.Oldest:
		res.Note = "more messages match than the limit: these are the oldest; narrow the range to see the rest"
	case trunc:
		res.Note = "more messages match than the limit: these are the newest; narrow the range or the search to see older ones"
	case len(msgs) == 0:
		res.Note = "no message matches (see logs_status: is anything sending here?)"
	}
	return res
}

// LdStatsResult is the cluster-wide counts.
type LdStatsResult struct {
	From       int64               `json:"from"`
	To         int64               `json:"to"`
	Nodes      int                 `json:"nodes"`
	NodeErrors []NodeError         `json:"node_errors,omitempty"`
	Total      uint64              `json:"total"`
	BySeverity map[string]uint64   `json:"by_severity"`
	TopHosts   []LdCount           `json:"top_hosts,omitempty"`
	TopProgs   []LdCount           `json:"top_programs,omitempty"`
	Step       int64               `json:"step_s,omitempty"`
	Series     []LdPoint           `json:"series,omitempty"`
	Sources    int                 `json:"sources,omitempty"`         // how many different senders the messages came from
	TopSources []LdCount           `json:"top_sources,omitempty"`     // the busiest senders (by address)
	SrcSeries  map[string][]uint64 `json:"source_series,omitempty"`   // the busiest senders' counts per bucket, one entry per point of Series
	SevSeries  map[string][]uint64 `json:"severity_series,omitempty"` // "info", "warning" and "error" counts per bucket, as SrcSeries
	Heard      []uint64            `json:"heard,omitempty"`           // how many different senders were heard in each bucket (the busiest ldSrcSeriesMax per node)
	Field      string              `json:"field,omitempty"`           // the field whose values TopValues counts
	TopValues  []LdCount           `json:"top_values,omitempty"`      // its most frequent values among the matching messages
	Values     int                 `json:"values,omitempty"`          // how many different values it has (up to 5000 per node are counted)
	Measure    string              `json:"measure,omitempty"`         // what top_values also count: distinct:F or sum:F; they are ordered by it
	ValSeries  map[string][]uint64 `json:"value_series,omitempty"`    // per value asked for (values=), its counts per bucket, one entry per point of Series
	Partial    bool                `json:"partial,omitempty"`
	Note       string              `json:"note,omitempty"`
	Pending    []string            `json:"pending,omitempty"` // as LdResult
	NodeMs     []NodeTime          `json:"node_ms,omitempty"`
}

type LdCount struct {
	Name  string `json:"name"`
	Count uint64 `json:"count"`
	// with a measure (top_values only): how many different values of the measured field, or the sum of its numbers and how many
	// messages had a number there
	Distinct *uint64  `json:"distinct,omitempty"`
	Sum      *float64 `json:"sum,omitempty"`
	Numeric  uint64   `json:"numeric,omitempty"`
}

type LdPoint struct {
	T     int64  `json:"t"` // seconds
	Count uint64 `json:"count"`
}

func topCounts(m map[string]uint64, n int) []LdCount {
	out := make([]LdCount, 0, len(m))
	for k, v := range m {
		out = append(out, LdCount{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Stats counts what matches over the cluster; step 0 gives no series.
func (s *ldService) Stats(ctx context.Context, q LdQuery, step int64, top int) LdStatsResult {
	return s.StatsEach(ctx, q, step, top, nil)
}

// StatsEach is Stats, calling part with the counts so far as SearchEach does.
func (s *ldService) StatsEach(ctx context.Context, q LdQuery, step int64, top int, part func(LdStatsResult, bool)) LdStatsResult {
	if q.To == 0 {
		q.To = time.Now().UnixMilli()
	}
	var cb func(ldFanState)
	if part != nil {
		cb = func(st ldFanState) { part(statsResult(q, step, top, st), st.done) }
	}
	return statsResult(q, step, top, s.fanEach(ctx, ldQueryMsg{Op: "stats", Query: q, Step: step}, cb))
}

func statsResult(q LdQuery, step int64, top int, st ldFanState) LdStatsResult {
	reps, errs, n := st.reps, st.errs, st.nodes
	res := LdStatsResult{From: q.From, To: q.To, Nodes: n, NodeErrors: errs, BySeverity: map[string]uint64{}, Step: step, Pending: st.pending}
	if n > 1 {
		res.NodeMs = st.times
	}
	hosts, progs, series := map[string]uint64{}, map[string]uint64{}, map[int64]uint64{}
	srcs, srcSer, sevSer := map[string]uint64{}, map[string]map[int64]uint64{}, [3]map[int64]uint64{{}, {}, {}}
	vals := map[string]uint64{}
	aggs, valSer := map[string]*ldAgg{}, map[string]map[int64]uint64{}
	unmeasured := 0
	for _, r := range reps {
		if r.Stats == nil {
			continue
		}
		if q.Measure != "" && !r.Stats.Measured {
			unmeasured++ // a node older than v42
		}
		for k, o := range r.Stats.FieldAgg {
			a := aggs[k]
			if a == nil {
				a = &ldAgg{}
				aggs[k] = a
			}
			if err := a.merge(o); err != nil {
				res.Partial = true
			}
		}
		for k, m := range r.Stats.ValSer {
			if valSer[k] == nil {
				valSer[k] = map[int64]uint64{}
			}
			for t, v := range m {
				valSer[k][t] += v
			}
		}
		res.Total += r.Stats.Total
		res.Partial = res.Partial || r.Stats.Partial
		for i, c := range r.Stats.BySev {
			if c > 0 {
				res.BySeverity[sevNames[i]] += c
			}
		}
		for k, v := range r.Stats.ByHost {
			hosts[k] += v
		}
		for k, v := range r.Stats.ByProg {
			progs[k] += v
		}
		for k, v := range r.Stats.Series {
			series[k] += v
		}
		for k, v := range r.Stats.BySrc {
			srcs[k] += v
		}
		for k, v := range r.Stats.ByField {
			vals[k] += v
		}
		for k, m := range r.Stats.SrcSer {
			if srcSer[k] == nil {
				srcSer[k] = map[int64]uint64{}
			}
			for t, v := range m {
				srcSer[k][t] += v
			}
		}
		for i, m := range r.Stats.SevSer {
			for t, v := range m {
				sevSer[i][t] += v
			}
		}
	}
	res.Sources = len(srcs)
	res.TopSources = topCounts(srcs, 50)
	if top <= 0 {
		top = 20
	}
	res.TopHosts, res.TopProgs = topCounts(hosts, top), topCounts(progs, top)
	if q.Field != "" {
		res.Field, res.Values = q.Field, len(vals)
		if q.Measure == "" {
			res.TopValues = topCounts(vals, top)
		} else {
			res.Measure = q.Measure + ":" + q.MField
			res.TopValues = topMeasured(vals, aggs, q.Measure, top)
		}
	}
	if step > 0 {
		var ts []int64
		for t := q.From / 1000 / step * step; t <= q.To/1000; t += step {
			res.Series = append(res.Series, LdPoint{t, series[t]})
			ts = append(ts, t)
		}
		res.SrcSeries = map[string][]uint64{}
		for _, c := range res.TopSources {
			if m := srcSer[c.Name]; m != nil {
				col := make([]uint64, len(ts))
				for i, t := range ts {
					col[i] = m[t]
				}
				res.SrcSeries[c.Name] = col
			}
		}
		res.SevSeries = map[string][]uint64{}
		for i, name := range []string{"info", "warning", "error"} {
			col := make([]uint64, len(ts))
			for j, t := range ts {
				col[j] = sevSer[i][t]
			}
			res.SevSeries[name] = col
		}
		if len(q.Values) > 0 {
			res.ValSeries = map[string][]uint64{}
			for _, v := range q.Values {
				col := make([]uint64, len(ts))
				for i, t := range ts {
					col[i] = valSer[v][t]
				}
				res.ValSeries[v] = col
			}
		}
		res.Heard = make([]uint64, len(ts))
		for j, t := range ts {
			for _, m := range srcSer {
				if m[t] > 0 {
					res.Heard[j]++
				}
			}
		}
	}
	if len(errs) > 0 {
		res.Note = "some nodes did not answer: what they hold is missing from the counts"
	} else if unmeasured > 0 {
		res.Note = fmt.Sprintf("%d node(s) older than v42 counted messages only: their share of %s is missing", unmeasured, res.Measure)
	} else if len(reps) > 1 {
		res.Note = "a message a sender sent to two nodes is counted on both"
	}
	return res
}

// topMeasured is the n values with the most different values of the measured field (or the largest sums), with their message counts.
func topMeasured(vals map[string]uint64, aggs map[string]*ldAgg, kind string, n int) []LdCount {
	out := make([]LdCount, 0, len(vals))
	key := map[string]float64{}
	for k, c := range vals {
		x := LdCount{Name: k, Count: c}
		var d uint64
		var sum float64
		if a := aggs[k]; a != nil {
			d, sum, x.Numeric = a.distinct(), a.Sum, a.Nums
		}
		if kind == "distinct" {
			x.Distinct = &d
			key[k] = float64(d)
		} else {
			x.Sum = &sum
			key[k] = sum
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := key[out[i].Name], key[out[j].Name]
		if a != b {
			return a > b
		}
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (s *ldService) AllSenders(ctx context.Context) ([]LdSenderInfo, []NodeError) {
	reps, errs, _ := s.fan(ctx, ldQueryMsg{Op: "senders"})
	var out []LdSenderInfo
	for _, r := range reps {
		out = append(out, r.Senders...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Addr != out[j].Addr {
			return out[i].Addr < out[j].Addr
		}
		return out[i].Node < out[j].Node
	})
	return out, errs
}

func (s *ldService) AllStatus(ctx context.Context) ([]LdNodeStatus, []NodeError) {
	reps, errs, _ := s.fan(ctx, ldQueryMsg{Op: "status"})
	var out []LdNodeStatus
	for _, r := range reps {
		if r.Status != nil {
			out = append(out, *r.Status)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out, errs
}

// oldestAll is the time (ms) of the oldest message on any node.
func (s *ldService) oldestAll(ctx context.Context) int64 {
	o := s.store.Oldest()
	if len(s.otherPeers()) == 0 {
		return o
	}
	sts, _ := s.AllStatus(ctx)
	for _, st := range sts {
		if st.Oldest > 0 && (o == 0 || st.Oldest < o) {
			o = st.Oldest
		}
	}
	return o
}

var _ = strings.TrimSpace

// nameSources puts the reverse name of each message's sender into the view (a lookup is never waited on for more than a moment).
func (s *ldService) nameSources(v []LdView) {
	if s == nil || s.dns == nil {
		return
	}
	var addrs []netip.Addr
	for _, m := range v {
		if a, err := netip.ParseAddr(m.Source); err == nil {
			addrs = append(addrs, a.Unmap())
		}
	}
	if len(addrs) == 0 {
		return
	}
	names := s.dns.names(addrs, 1500*time.Millisecond)
	for i := range v {
		if a, err := netip.ParseAddr(v[i].Source); err == nil {
			v[i].SourceName = names[a.Unmap()]
		}
	}
}
