package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"logdir/internal/core"
)

// The sketch: within 5% (4 standard errors) from 200 to 200000 different values, the union of two overlapping sets after a merge,
// and it travels (sparse while few registers are set, dense after) and refuses what is not a sketch.
func TestLdAggSketch(t *testing.T) {
	for _, n := range []int{129, 200, 1000, 5000, 20000, 200000} {
		var a ldAgg
		for i := range n {
			a.add("10.0." + strconv.Itoa(i/256) + "." + strconv.Itoa(i%256))
		}
		if d := a.distinct(); math.Abs(float64(d)-float64(n))/float64(n) > 0.05 {
			t.Fatalf("%d different: estimated %d", n, d)
		}
		a.seal()
		r, err := hllDecode(a.HLL)
		if err != nil || hllEstimate(r) != a.distinct() {
			t.Fatalf("%d: round trip: %v, %d vs %d (%c)", n, err, hllEstimate(r), a.distinct(), a.HLL[0])
		}
	}
	var small ldAgg
	for i := range ldAggExact {
		small.add(strconv.Itoa(i))
		small.add(strconv.Itoa(i)) // twice: counted once
	}
	if small.distinct() != ldAggExact || small.reg != nil {
		t.Fatalf("up to %d values are counted exactly: %d, sketch %v", ldAggExact, small.distinct(), small.reg != nil)
	}
	// two nodes: ports 1-6000 and 4001-10000 seen by the same source: 10000 different
	var x, y ldAgg
	for p := 1; p <= 6000; p++ {
		x.add(strconv.Itoa(p))
	}
	for p := 4001; p <= 10000; p++ {
		y.add(strconv.Itoa(p))
	}
	x.seal()
	y.seal()
	var u ldAgg
	if err := u.merge(&x); err != nil {
		t.Fatal(err)
	}
	if err := u.merge(&y); err != nil {
		t.Fatal(err)
	}
	if d := u.distinct(); math.Abs(float64(d)-10000)/10000 > 0.05 {
		t.Fatalf("union: %d, want about 10000", d)
	}
	// exact sets from two nodes: still exact
	var e1, e2, eu ldAgg
	for _, v := range []string{"22", "80", "443"} {
		e1.add(v)
	}
	for _, v := range []string{"443", "8443"} {
		e2.add(v)
	}
	e1.seal()
	e2.seal()
	eu.merge(&e1)
	eu.merge(&e2)
	if eu.distinct() != 4 {
		t.Fatalf("exact union: %d", eu.distinct())
	}
	for _, bad := range []string{"", "!!", "eA", "cwAAQQ", "ZAAA"} {
		if _, err := hllDecode(bad); err == nil {
			t.Fatalf("%q decoded", bad)
		}
	}
	for _, c := range []struct{ in, kind, f string }{{"", "", ""}, {"count", "", ""}, {"distinct:dst_port", "distinct", "dst_port"}, {"sum: bytes", "sum", "bytes"}} {
		if k, f, err := core.ParseLdMeasure(c.in); err != nil || k != c.kind || f != c.f {
			t.Fatalf("measure %q: %q %q %v", c.in, k, f, err)
		}
	}
	for _, bad := range []string{"avg:bytes", "distinct", "distinct:", "sum:a b", "sum:a=b"} {
		if _, _, err := core.ParseLdMeasure(bad); err == nil {
			t.Fatalf("measure %q accepted", bad)
		}
	}
}

// Counting by value over two nodes: the message counts, the different ports per source (exact while few, the sketch for the scanner),
// the sum of bytes, the order by the measure, the lines over time, and host as a field.
func TestStatsMeasure(t *testing.T) {
	now := time.Now().Truncate(time.Minute).Add(-30 * time.Minute)
	mk := func(node string, lines func(add func(string, time.Time))) LdStatsAnswer {
		st := NewLdStore(t.TempDir(), node)
		st.Load()
		lines(func(text string, at time.Time) {
			st.Add(parseSyslog([]byte(text), netip.MustParseAddr("192.0.2.1"), "udp", at, 8192))
		})
		q := core.LdQuery{From: now.Add(-time.Hour).UnixMilli(), To: now.Add(time.Hour).UnixMilli(), MinSev: -1, Field: "src_ip", Measure: "distinct", MField: "dst_port", Values: []string{"10.0.0.9", "10.0.0.1"}}
		if err := q.Validate(); err != nil {
			t.Fatal(err)
		}
		a := st.Stats(context.Background(), q, 600)
		// as it travels between nodes
		b, _ := json.Marshal(a)
		var back LdStatsAnswer
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		return back
	}
	fw := func(src string, port, bytes int) string {
		return fmt.Sprintf("<134>fw1 filterlog: action=deny src_ip=%s dst_port=%d bytes=%d", src, port, bytes)
	}
	// node 1: 10.0.0.9 tries ports 1-3000, 10.0.0.1 ports 22 and 443 (5 times each); node 2: 10.0.0.9 ports 2001-5000, 10.0.0.1 port 22
	a1 := mk("n1", func(add func(string, time.Time)) {
		for p := 1; p <= 3000; p++ {
			add(fw("10.0.0.9", p, 60), now.Add(time.Duration(p)*time.Millisecond))
		}
		for i := range 5 {
			add(fw("10.0.0.1", 22, 1000), now.Add(11*time.Minute+time.Duration(i)*time.Second))
			add(fw("10.0.0.1", 443, 1000), now.Add(11*time.Minute+time.Duration(i)*time.Second))
		}
	})
	a2 := mk("n2", func(add func(string, time.Time)) {
		for p := 2001; p <= 5000; p++ {
			add(fw("10.0.0.9", p, 60), now.Add(time.Duration(p)*time.Millisecond))
		}
		add(fw("10.0.0.1", 22, 500), now.Add(21*time.Minute))
	})
	if !a1.Measured || len(a1.FieldAgg) != 2 || a1.FieldAgg["10.0.0.9"].HLL == "" || len(a1.FieldAgg["10.0.0.1"].Vals) != 2 {
		t.Fatalf("node 1 answer: %+v", a1.FieldAgg)
	}
	q := core.LdQuery{From: now.Add(-time.Hour).UnixMilli(), To: now.Add(time.Hour).UnixMilli(), Field: "src_ip", Measure: "distinct", MField: "dst_port", Values: []string{"10.0.0.9", "10.0.0.1"}}
	st := ldFanState{nodes: 2, reps: []ldQueryReply{{Stats: &a1}, {Stats: &a2}}, done: true}
	res := statsResult(q, 600, 10, st)
	if res.Measure != "distinct:dst_port" || len(res.TopValues) != 2 {
		t.Fatalf("result: %+v", res)
	}
	scan, ssh := res.TopValues[0], res.TopValues[1]
	if scan.Name != "10.0.0.9" || scan.Count != 6000 || math.Abs(float64(*scan.Distinct)-5000)/5000 > 0.05 {
		t.Fatalf("the scanner: %s %d messages, %d ports (want about 5000)", scan.Name, scan.Count, *scan.Distinct)
	}
	if ssh.Name != "10.0.0.1" || ssh.Count != 11 || *ssh.Distinct != 2 {
		t.Fatalf("10.0.0.1: %+v %d", ssh, *ssh.Distinct)
	}
	// lines over time: 10-minute buckets; the scanner's 6000 in the first, 10.0.0.1's 10 in the second and 1 in the third
	var nine, one []uint64 = res.ValSeries["10.0.0.9"], res.ValSeries["10.0.0.1"]
	if len(nine) != len(res.Series) || len(one) != len(res.Series) {
		t.Fatalf("series lengths: %d %d %d", len(nine), len(one), len(res.Series))
	}
	var sum9, sum1 uint64
	nz := 0
	for i := range one {
		sum9 += nine[i]
		sum1 += one[i]
		if one[i] > 0 {
			nz++
		}
	}
	if sum9 != 6000 || sum1 != 11 || nz != 2 {
		t.Fatalf("value series: %v / %v", nine, one)
	}

	// sum of bytes, by host (a field the message does not have: the message's host)
	sumOf := func(node string, lines []string) LdStatsAnswer {
		st := NewLdStore(t.TempDir(), node)
		st.Load()
		for i, l := range lines {
			st.Add(parseSyslog([]byte(l), netip.MustParseAddr("192.0.2.1"), "udp", now.Add(time.Duration(i)*time.Second), 8192))
		}
		q := core.LdQuery{From: now.Add(-time.Hour).UnixMilli(), To: now.Add(time.Hour).UnixMilli(), MinSev: -1, Field: "host", Measure: "sum", MField: "bytes"}
		q.Validate()
		return st.Stats(context.Background(), q, 0)
	}
	b1 := sumOf("n1", []string{"<134>Nov 26 10:00:00 fwA x: bytes=100", "<134>Nov 26 10:00:00 fwA x: bytes=250.5", "<134>Nov 26 10:00:00 fwB x: bytes=7", "<134>Nov 26 10:00:00 fwB x: no bytes here"})
	b2 := sumOf("n2", []string{"<134>Nov 26 10:00:00 fwB x: bytes=1000", "<134>Nov 26 10:00:00 fwA x: bytes=abc"})
	q = core.LdQuery{Field: "host", Measure: "sum", MField: "bytes"}
	res = statsResult(q, 0, 10, ldFanState{nodes: 2, reps: []ldQueryReply{{Stats: &b1}, {Stats: &b2}}, done: true})
	if len(res.TopValues) != 2 || res.TopValues[0].Name != "fwB" || *res.TopValues[0].Sum != 1007 || res.TopValues[0].Count != 3 || res.TopValues[0].Numeric != 2 ||
		res.TopValues[1].Name != "fwA" || *res.TopValues[1].Sum != 350.5 || res.TopValues[1].Count != 3 || res.TopValues[1].Numeric != 2 {
		b, _ := json.Marshal(res.TopValues)
		t.Fatalf("sum of bytes by host: %s", b)
	}
	// a node that does not know measures (older): counted, and said
	old := b2
	old.FieldAgg, old.Measured = nil, false
	res = statsResult(q, 0, 10, ldFanState{nodes: 2, reps: []ldQueryReply{{Stats: &b1}, {Stats: &old}}, done: true})
	if res.Note == "" || res.TopValues[0].Name != "fwA" || *res.TopValues[0].Sum != 350.5 || *res.TopValues[1].Sum != 7 {
		t.Fatalf("older node: note %q, %+v", res.Note, res.TopValues)
	}
}

// The API's parameters.
func TestStatsMeasureParams(t *testing.T) {
	s := &LdService{BySender: map[netip.Addr]*LdSender{}}
	s.TestCfg.Store(&core.LogsConfig{})
	q, err := s.ParseLdQuery(map[string][]string{"range": {"1h"}, "field": {"src_ip"}, "measure": {"distinct:dst_port"}, "values": {"a,b", "c"}})
	if err != nil || q.Measure != "distinct" || q.MField != "dst_port" || len(q.Values) != 2 || q.Values[0] != "a,b" {
		t.Fatalf("%+v %v", q, err)
	}
	for _, bad := range []map[string][]string{{"range": {"1h"}, "measure": {"sum:bytes"}}, {"range": {"1h"}, "values": {"x"}}, {"range": {"1h"}, "field": {"a"}, "measure": {"max:bytes"}}} {
		if _, err := s.ParseLdQuery(bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}

// Saved searches: the rules, and saving, replacing, renaming and deleting one at a time.
func TestSavedSearches(t *testing.T) {
	ok := core.LogSavedCfg{Name: "port scans", Q: "action=deny", Range: "1d", Field: "src_ip", Measure: "distinct:dst_port", OverTime: true}
	if err := core.ValidateSaved([]core.LogSavedCfg{ok}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]core.LogSavedCfg{
		{{Name: ""}}, {{Name: " x"}}, {{Name: "x", Range: "2d"}}, {{Name: "x", Measure: "sum:bytes"}}, {{Name: "x", OverTime: true}},
		{{Name: "x", Field: "a", Measure: "avg:b"}}, {{Name: "Dup"}, {Name: "dup"}},
	} {
		if err := core.ValidateSaved(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	list, _, err := ChangeSaved(nil, &ok, "", "")
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	two := core.LogSavedCfg{Name: "denies", Q: "deny"}
	list, _, _ = ChangeSaved(list, &two, "", "")
	ch := ok
	ch.Name, ch.Range = "PORT SCANS", "7d" // same name, case ignored: replaced
	list, _, _ = ChangeSaved(list, &ch, "", "")
	if len(list) != 2 || list[0].Range != "7d" || list[0].Name != "PORT SCANS" {
		t.Fatalf("replace: %+v", list)
	}
	rn := two
	rn.Name = "all denies"
	list, _, _ = ChangeSaved(list, &rn, "denies", "")
	if len(list) != 2 || list[1].Name != "all denies" {
		t.Fatalf("rename: %+v", list)
	}
	if _, _, err := ChangeSaved(list, &rn, "port scans", ""); err == nil {
		t.Fatal("renamed onto a name in use")
	}
	list, _, _ = ChangeSaved(list, nil, "", "ALL DENIES")
	if len(list) != 1 {
		t.Fatalf("delete: %+v", list)
	}
	if _, _, err := ChangeSaved(list, nil, "", "nope"); err == nil {
		t.Fatal("deleted what is not there")
	}
}
