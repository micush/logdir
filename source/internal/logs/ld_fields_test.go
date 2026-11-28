package logs

import (
	"context"
	"fmt"
	"testing"
	"time"

	"logdir/internal/core"
)

func fieldMap(m core.LdMsg) map[string]string {
	out := map[string]string{}
	for _, f := range core.LdFields(&m) {
		out[f.Key] = f.Val
	}
	return out
}

func wantFields(t *testing.T, name string, got map[string]string, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s = %q, want %q (all: %v)", name, k, got[k], v, got)
		}
	}
}

func fieldStore(t testing.TB) (*LdStore, time.Time) {
	now := ldNow
	s := NewLdStore(t.TempDir(), "n1")
	s.Now = func() time.Time { return now }
	s.Load()
	base := now.Add(-3 * time.Hour)
	msgs := []string{
		`srcip=10.1.1.5 dstip=8.8.8.8 dstport=53 action="deny" sentbyte=100`,
		`srcip=10.1.1.6 dstip=1.1.1.1 dstport=443 action="accept" sentbyte=5000`,
		`srcip=10.1.1.6 dstip=1.1.1.1 dstport=443 action="accept" sentbyte=70000`,
		`{"action":"deny","src_ip":"10.9.9.9","dst_port":22}`,
		`Accepted password for root from 10.1.1.1 port 22 ssh2`,
		`failed password for admin user=root from 10.1.1.2`,
	}
	for h := 0; h < 3; h++ {
		for i, m := range msgs {
			s.Add(core.LdMsg{T: base.Add(time.Duration(h)*time.Hour + time.Duration(i)*time.Minute).UnixMilli(), Host: "fw1", Prog: "fw", Sev: 6, Msg: m, Src: "10.0.0.1"})
		}
	}
	s.Flush()
	s.Maintain()
	return s, now
}

func TestFieldSearch(t *testing.T) {
	s, now := fieldStore(t)
	from := now.Add(-4 * time.Hour).UnixMilli()
	for _, c := range []struct {
		q    string
		want int // per hour
	}{
		{"action=deny", 2}, {"action=accept", 2}, {"action=acc*", 2}, {"not action=deny", 4}, {"!action=deny", 4},
		{"dst_port=443", 2}, {"dst_port=22", 1}, {"dst_port=53 or dst_port=22", 2}, {"sentbyte>1000", 2}, {"sentbyte>=5000", 2},
		{"sentbyte<1000", 1}, {"sentbyte>100000", 0}, {"src_ip=10.1.1.*", 3}, {"action=deny dst_port=53", 1},
		{"failed password user=root", 1}, {"failed password user=admin", 0}, {"password", 2}, {"ACTION=DENY", 2}, {"nope=1", 0},
		{"accept sentbyte>10000", 1}, {"action=deny and 10.9.9.9", 1},
	} {
		a := s.Search(context.Background(), core.LdQuery{From: from, MinSev: -1, Q: c.q, Limit: 1000})
		if len(a.Msgs) != 3*c.want {
			t.Errorf("%q: %d messages, want %d", c.q, len(a.Msgs), 3*c.want)
		}
		st := scanStats(s, core.LdQuery{From: from, MinSev: -1, Q: c.q}, 0)
		_ = st
	}
}

func TestFieldTopValues(t *testing.T) {
	s, now := fieldStore(t)
	r := s.Stats(context.Background(), core.LdQuery{From: now.Add(-4 * time.Hour).UnixMilli(), MinSev: -1, Field: "dst_port"}, 3600)
	want := map[string]uint64{"53": 3, "443": 6, "22": 3}
	if fmt.Sprint(r.ByField) != fmt.Sprint(want) {
		t.Errorf("by field %v, want %v", r.ByField, want)
	}
	if r.Total != 18 {
		t.Errorf("total %d", r.Total)
	}
	// with a filter: only the matching messages are counted
	r = s.Stats(context.Background(), core.LdQuery{From: now.Add(-4 * time.Hour).UnixMilli(), MinSev: -1, Q: "action=deny", Field: "action"}, 0)
	if fmt.Sprint(r.ByField) != fmt.Sprint(map[string]uint64{"deny": 6}) {
		t.Errorf("by field %v", r.ByField)
	}
}

// A field the message has under its own name wins over one an alias filled in first (src= gives src_ip only when the
// message has no src_ip), in either order, and the first real one still counts.
func TestFieldAliasDoesNotHideRealField(t *testing.T) {
	for _, msg := range []string{
		"action=deny src=2001:db8::1 src_ip=10.0.0.7 dst_port=443",
		"action=deny src_ip=10.0.0.7 src=2001:db8::1 dst_port=443",
	} {
		got := fieldMap(core.LdMsg{Prog: "TRAFFIC", Msg: msg})
		wantFields(t, msg, got, map[string]string{"src": "2001:db8::1", "src_ip": "10.0.0.7", "dst_port": "443", "action": "deny"})
		n := 0
		for _, f := range core.LdFields(&core.LdMsg{Prog: "TRAFFIC", Msg: msg}) {
			if f.Key == "src_ip" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: src_ip %d times", msg, n)
		}
	}
	// no src_ip in the message: the alias still fills it in
	wantFields(t, "alias", fieldMap(core.LdMsg{Msg: "SRC=10.1.1.1 DST=10.2.2.2"}), map[string]string{"src_ip": "10.1.1.1", "dst_ip": "10.2.2.2"})
	// two real ones: the first counts
	wantFields(t, "first", fieldMap(core.LdMsg{Msg: "src_ip=10.0.0.1 src_ip=10.0.0.2"}), map[string]string{"src_ip": "10.0.0.1"})
	// and a search by it finds the message
	now := ldNow
	s := newTestStore(t, &now)
	addAt(s, now.Add(-time.Minute), "fw1", "TRAFFIC", 6, "action=deny src=2001:db8::1 src_ip=10.0.0.7 dst_port=443")
	q := core.LdQuery{From: now.Add(-time.Hour).UnixMilli(), MinSev: -1, Q: "src_ip=10.0.0.7", Limit: 10}
	if a := s.Search(context.Background(), q); len(a.Msgs) != 1 {
		t.Fatalf("search src_ip=10.0.0.7: %v", msgsOf(a))
	}
}
