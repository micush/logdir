package main

import (
	"context"
	"encoding/json"
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
	Op    string  `json:"op"` // search, tail, stats, senders or status
	Query LdQuery `json:"query"`
	Step  int64   `json:"step,omitempty"`
}

type ldQueryReply struct {
	Answer  *LdAnswer      `json:"answer,omitempty"`
	Stats   *LdStatsAnswer `json:"stats,omitempty"`
	Senders []LdSenderInfo `json:"senders,omitempty"`
	Status  *LdNodeStatus  `json:"status,omitempty"`
}

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
	peers := s.otherPeers()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range peers {
		wg.Add(1)
		go func(p ClusterPeer) {
			defer wg.Done()
			to := ldRoundTimeout(m.Query.From, m.Query.To)
			cctx, cancel := context.WithTimeout(ctx, to)
			defer cancel()
			var r ldQueryReply
			err := s.mg.cl.call(cctx, p, "POST", "/cluster/log/query", m, &r, to)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, NodeError{p.Addr, err.Error()})
				return
			}
			reps = append(reps, r)
		}(p)
	}
	lctx, cancel := context.WithTimeout(ctx, ldRoundTimeout(m.Query.From, m.Query.To))
	lr, _ := s.local(lctx, m)
	cancel()
	wg.Wait()
	reps = append([]ldQueryReply{lr}, reps...)
	sort.Slice(errs, func(i, j int) bool { return errs[i].Node < errs[j].Node })
	return reps, errs, len(peers) + 1
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
	if q.To == 0 {
		q.To = time.Now().UnixMilli()
	}
	reps, errs, n := s.fan(ctx, ldQueryMsg{Op: "search", Query: q})
	var parts []LdAnswer
	for _, r := range reps {
		if r.Answer != nil {
			parts = append(parts, *r.Answer)
		}
	}
	return s.result(q, parts, errs, n)
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
		res.Messages = append(res.Messages, ldView(m))
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
	Partial    bool                `json:"partial,omitempty"`
	Note       string              `json:"note,omitempty"`
}

type LdCount struct {
	Name  string `json:"name"`
	Count uint64 `json:"count"`
}

type LdPoint struct {
	T     int64  `json:"t"` // seconds
	Count uint64 `json:"count"`
}

func topCounts(m map[string]uint64, n int) []LdCount {
	out := make([]LdCount, 0, len(m))
	for k, v := range m {
		out = append(out, LdCount{k, v})
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
	if q.To == 0 {
		q.To = time.Now().UnixMilli()
	}
	reps, errs, n := s.fan(ctx, ldQueryMsg{Op: "stats", Query: q, Step: step})
	res := LdStatsResult{From: q.From, To: q.To, Nodes: n, NodeErrors: errs, BySeverity: map[string]uint64{}, Step: step}
	hosts, progs, series := map[string]uint64{}, map[string]uint64{}, map[int64]uint64{}
	srcs, srcSer, sevSer := map[string]uint64{}, map[string]map[int64]uint64{}, [3]map[int64]uint64{{}, {}, {}}
	for _, r := range reps {
		if r.Stats == nil {
			continue
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
	} else if len(reps) > 1 {
		res.Note = "a message a sender sent to two nodes is counted on both"
	}
	return res
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
