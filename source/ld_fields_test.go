package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func fieldMap(m LdMsg) map[string]string {
	out := map[string]string{}
	for _, f := range ldFields(&m) {
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

func TestFieldsFormats(t *testing.T) {
	cases := []struct {
		name string
		m    LdMsg
		want map[string]string
		not  []string
	}{
		{"fortigate", LdMsg{Msg: `date=2026-11-26 time=10:00:01 devname="FG 100F" logid="0000000013" type="traffic" srcip=10.1.1.5 srcport=51515 dstip=8.8.8.8 dstport=53 proto=17 action="deny" policyid=0 msg="it said \"no\""`},
			map[string]string{"devname": "FG 100F", "srcip": "10.1.1.5", "src_ip": "10.1.1.5", "dst_ip": "8.8.8.8", "src_port": "51515", "dst_port": "53", "action": "deny", "policyid": "0", "msg": `it said "no"`}, nil},
		{"iptables", LdMsg{Prog: "kernel", Msg: `[UFW BLOCK] IN=eth0 OUT= MAC=00:11 SRC=203.0.113.9 DST=192.0.2.1 LEN=60 PROTO=TCP SPT=40000 DPT=22 WINDOW=64240`},
			map[string]string{"src": "203.0.113.9", "src_ip": "203.0.113.9", "dst_ip": "192.0.2.1", "proto": "TCP", "src_port": "40000", "dst_port": "22", "out": ""}, nil},
		{"src not an address", LdMsg{Msg: `copy src=/tmp/a dst=/tmp/b`}, map[string]string{"src": "/tmp/a"}, []string{"src_ip", "dst_ip"}},
		{"audit", LdMsg{Msg: `type=SYSCALL msg=audit(1700000000.123:456): arch=c000003e syscall=59 success=yes exe="/usr/bin/ls" auid=1000`},
			map[string]string{"type": "SYSCALL", "msg": "audit(1700000000.123:456):", "syscall": "59", "exe": "/usr/bin/ls", "auid": "1000"}, nil},
		{"comma pairs", LdMsg{Msg: `a=1,b=2;c=x y`}, map[string]string{"a": "1", "b": "2", "c": "x"}, nil},
		{"json", LdMsg{Prog: "app", Msg: `{"level":"error","user":{"name":"bob","id":7},"ok":false,"tags":["a"],"lat":1.5e3}`},
			map[string]string{"level": "error", "user.name": "bob", "user.id": "7", "ok": "false", "lat": "1.5e3"}, []string{"tags"}},
		{"json after a prefix", LdMsg{Msg: `audit: {"action":"login","src_ip":"10.0.0.1"}`}, map[string]string{"action": "login", "src_ip": "10.0.0.1"}, nil},
		{"cef", LdMsg{Msg: `CEF:0|Security|threatmanager|1.0|100|worm successfully stopped|10|src=10.0.0.1 dst=2.1.2.2 spt=1232 msg=Detected a threat. No action needed act=blocked a\=b`},
			map[string]string{"cef_vendor": "Security", "cef_name": "worm successfully stopped", "cef_severity": "10", "src_ip": "10.0.0.1", "dst_ip": "2.1.2.2", "src_port": "1232", "msg": "Detected a threat. No action needed", "act": "blocked a=b", "action": "blocked a=b"}, nil},
		{"cef escaped pipe", LdMsg{Msg: `CEF:0|V|P\|X|1|sig|name|5|x=1`}, map[string]string{"cef_product": "P|X", "x": "1"}, nil},
		{"panos traffic", LdMsg{Msg: `1,2026/11/26 10:00:00,012345678901,TRAFFIC,end,2561,2026/11/26 10:00:00,10.1.1.5,8.8.8.8,0.0.0.0,0.0.0.0,allow-dns,,,dns,vsys1,trust,untrust,ethernet1/2,ethernet1/1,default,,12345,1,51515,53,0,0,0x19,udp,allow,520,120,400,4`},
			map[string]string{"type": "TRAFFIC", "subtype": "end", "src_ip": "10.1.1.5", "dst_ip": "8.8.8.8", "rule": "allow-dns", "app": "dns", "src_zone": "trust", "dst_zone": "untrust", "src_port": "51515", "dst_port": "53", "proto": "udp", "action": "allow", "bytes": "520", "packets": "4", "serial": "012345678901"}, nil},
		{"panos threat quoted", LdMsg{Msg: `1,2026/11/26 10:00:00,012345678901,THREAT,url,2561,2026/11/26 10:00:00,10.1.1.5,1.2.3.4,0.0.0.0,0.0.0.0,web,,,web-browsing,vsys1,trust,untrust,e1/2,e1/1,default,,9,1,50000,80,0,0,0x0,tcp,alert,"example.com/a,b ""q""",(9999),news,informational`},
			map[string]string{"type": "THREAT", "url": `example.com/a,b "q"`, "threat": "(9999)", "category": "news", "threat_severity": "informational", "action": "alert"}, nil},
		{"panos system", LdMsg{Msg: `1,2026/11/26 10:00:00,012345678901,SYSTEM,general,0,2026/11/26 10:00:00,,general,,0,0,general,informational,"x"`},
			map[string]string{"type": "SYSTEM", "subtype": "general", "serial": "012345678901"}, []string{"src_ip"}},
		{"cisco ios", LdMsg{Msg: `*Nov 26 10:00:00.000: %LINK-3-UPDOWN: Interface Gi0/1, changed state to down`},
			map[string]string{"cisco_facility": "LINK", "cisco_severity": "3", "cisco_mnemonic": "UPDOWN"}, nil},
		{"asa 106023", LdMsg{Prog: "%ASA-4-106023", Msg: `Deny tcp src outside:198.51.100.7/4444 dst inside:10.0.0.5/22 by access-group "OUT" [0x0, 0x0]`},
			map[string]string{"cisco_mnemonic": "106023", "action": "deny", "proto": "tcp", "src_ip": "198.51.100.7", "src_port": "4444", "src_if": "outside", "dst_ip": "10.0.0.5", "dst_port": "22", "dst_if": "inside"}, nil},
		{"asa 106001", LdMsg{Msg: `%ASA-2-106001: Inbound TCP connection denied from 198.51.100.7/4444 to 10.0.0.5/80 flags SYN on interface outside`},
			map[string]string{"cisco_mnemonic": "106001", "action": "deny", "src_ip": "198.51.100.7", "dst_ip": "10.0.0.5", "dst_port": "80", "interface": "outside"}, nil},
		{"asa other", LdMsg{Msg: `%ASA-6-302013: Built inbound TCP connection 1 for outside:1.2.3.4/443 (1.2.3.4/443) to inside:10.0.0.5/5555 (10.0.0.5/5555)`},
			map[string]string{"cisco_mnemonic": "302013", "cisco_severity": "6"}, []string{"src_ip", "action"}},
		{"structured data", LdMsg{Msg: "hello", SD: `[exampleSDID@32473 iut="3" eventSource="Application" eventID="10\"11"][origin ip="10.1.1.1"]`},
			map[string]string{"iut": "3", "eventsource": "Application", "eventid": `10"11`, "ip": "10.1.1.1"}, nil},
		{"plain text", LdMsg{Msg: `Accepted password for root from 10.1.1.1 port 22 ssh2`}, map[string]string{}, []string{"root", "from"}},
		{"url query is a value", LdMsg{Msg: `GET url=http://x/?a=1&b=2 status=200`}, map[string]string{"url": "http://x/?a=1&b=2", "status": "200"}, []string{"a", "b"}},
	}
	for _, c := range cases {
		got := fieldMap(c.m)
		wantFields(t, c.name, got, c.want)
		for _, k := range c.not {
			if _, ok := got[k]; ok {
				t.Errorf("%s: has %s = %q", c.name, k, got[k])
			}
		}
	}
}

// fields never panic and stay bounded on any text
func TestFieldsHostile(t *testing.T) {
	inputs := []string{"", "=", "==", "a=", `a="`, `a='x`, "CEF:", "CEF:0|a|b", "CEF:0|||||||", "{", "{]", `{"a":`, "1,", "1,2026/11/26 1",
		"%", "%A-", "%A-9-X:", "%A-1-:", strings.Repeat("k=v ", 1000), strings.Repeat("{", 100000) + "}", "[a b=\"", "[a b=c]",
		"1,2026/11/26 10:00:00,x,TRAFFIC," + strings.Repeat(",", 200), "\xff\xfe=\x00", "CEF:0|a|b|c|d|e|f|" + strings.Repeat("x=", 3000)}
	for _, in := range inputs {
		for _, sd := range []string{"", in} {
			kv := ldFields(&LdMsg{Msg: in, Prog: in, SD: sd})
			if len(kv) > ldFieldsMax+ldFieldsMax {
				t.Errorf("%d fields from %.40q", len(kv), in)
			}
			for _, f := range kv {
				if len(f.Val) > ldFieldValMax || len(f.Key) > ldFieldKeyMax {
					t.Errorf("unbounded field %.40q from %.40q", f.Key, in)
				}
			}
		}
	}
}

func FuzzFields(f *testing.F) {
	for _, s := range []string{`a=1 b="x y"`, `{"a":{"b":1}}`, "CEF:0|a|b|c|d|e|f|x=1", "%ASA-4-106023: Deny tcp src a:1.2.3.4/1 dst b:5.6.7.8/2",
		"1,2026/11/26 10:00:00,1,TRAFFIC,end,1,x,1.1.1.1,2.2.2.2"} {
		f.Add(s, "")
	}
	f.Fuzz(func(t *testing.T, msg, sd string) {
		ldFields(&LdMsg{Msg: msg, SD: sd, Prog: msg})
		for _, w := range strings.Fields(msg) {
			if ft, ok := ldParseFieldTerm(strings.ToLower(w)); ok {
				ft.matches(ldFields(&LdMsg{Msg: msg}))
			}
		}
	})
}

func TestFieldTermParse(t *testing.T) {
	for _, c := range []struct {
		w      string
		ok     bool
		key    string
		op     string
		val    string
		number float64
	}{
		{"action=deny", true, "action", "=", "deny", 0}, {"dst_port=443", true, "dst_port", "=", "443", 0}, {"bytes>1000", true, "bytes", ">", "1000", 1000},
		{"bytes>=1.5", true, "bytes", ">=", "1.5", 1.5}, {"n<-3", true, "n", "<", "-3", -3}, {"user.name=b*b", true, "user.name", "=", "b*b", 0},
		{"a>b", false, "", "", "", 0}, {"=x", false, "", "", "", 0}, {"a=", false, "", "", "", 0}, {"1a=2", false, "", "", "", 0},
		{"http://x/?a=1", false, "", "", "", 0}, {"<html>", false, "", "", "", 0}, {"plain", false, "", "", "", 0},
	} {
		ft, ok := ldParseFieldTerm(c.w)
		if ok != c.ok || (ok && (ft.key != c.key || ft.op != c.op || ft.val != c.val || ft.num != c.number)) {
			t.Errorf("%q: %v %+v", c.w, ok, ft)
		}
	}
}

func fieldStore(t testing.TB) (*ldStore, time.Time) {
	now := ldNow
	s := newLdStore(t.TempDir(), "n1")
	s.now = func() time.Time { return now }
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
			s.Add(LdMsg{T: base.Add(time.Duration(h)*time.Hour + time.Duration(i)*time.Minute).UnixMilli(), Host: "fw1", Prog: "fw", Sev: 6, Msg: m, Src: "10.0.0.1"})
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
		a := s.Search(context.Background(), LdQuery{From: from, MinSev: -1, Q: c.q, Limit: 1000})
		if len(a.Msgs) != 3*c.want {
			t.Errorf("%q: %d messages, want %d", c.q, len(a.Msgs), 3*c.want)
		}
		st := scanStats(s, LdQuery{From: from, MinSev: -1, Q: c.q}, 0)
		_ = st
	}
}

func TestFieldTopValues(t *testing.T) {
	s, now := fieldStore(t)
	r := s.Stats(context.Background(), LdQuery{From: now.Add(-4 * time.Hour).UnixMilli(), MinSev: -1, Field: "dst_port"}, 3600)
	want := map[string]uint64{"53": 3, "443": 6, "22": 3}
	if fmt.Sprint(r.ByField) != fmt.Sprint(want) {
		t.Errorf("by field %v, want %v", r.ByField, want)
	}
	if r.Total != 18 {
		t.Errorf("total %d", r.Total)
	}
	// with a filter: only the matching messages are counted
	r = s.Stats(context.Background(), LdQuery{From: now.Add(-4 * time.Hour).UnixMilli(), MinSev: -1, Q: "action=deny", Field: "action"}, 0)
	if fmt.Sprint(r.ByField) != fmt.Sprint(map[string]uint64{"deny": 6}) {
		t.Errorf("by field %v", r.ByField)
	}
}

// the shortcut that looks for a field's value in the text before reading the fields gives what reading every message's fields gives
func TestFieldLiteralShortcut(t *testing.T) {
	msgs := []LdMsg{
		{Msg: `%ASA-2-106001: Inbound TCP connection denied from 198.51.100.7/4444 to 10.0.0.5/80 flags SYN on interface outside`},
		{Prog: "%ASA-4-106023", Msg: `Deny tcp src outside:1.2.3.4/1 dst inside:5.6.7.8/2 by access-group "X"`},
		{Msg: `{"user":"\u0062ob","x":"a\/b"}`},
		{Msg: `name='it\'s' q="say \"hi\"" k=a\\b`},
		{Msg: `CEF:0|V|P|1|s|n|5|act=a\=b msg=x\|y`},
		{Msg: "plain", SD: `[x@1 who="bo\]b" mode="FAST"]`},
		{Msg: `1,2026/11/26 10:00:00,1,TRAFFIC,end,1,x,1.1.1.1,2.2.2.2,0.0.0.0,0.0.0.0,"r""1",,,app,vsys1,a,b,c,d,e,,1,1,5,6,0,0,0x0,tcp,allow`},
		{Msg: `SRC=10.0.0.1 DST=10.0.0.2 PROTO=TCP`},
	}
	var words []string
	for i := range msgs {
		for _, f := range ldFields(&msgs[i]) {
			v := strings.ToLower(f.Val)
			if v == "" || strings.ContainsAny(v, " \t\n") {
				continue
			}
			words = append(words, f.Key+"="+v, "not "+f.Key+"="+v, f.Key+"="+v[:len(v)/2]+"*")
		}
	}
	for _, w := range words {
		f := (&LdQuery{Q: w, MinSev: -1}).filter()
		for i := range msgs {
			m := msgs[i]
			want := false
			for _, g := range f.groups { // the long way: every message's fields
				all := true
				for _, tm := range g {
					found := tm.field != nil && tm.field.matches(ldFields(&m))
					if tm.field == nil {
						found = findTerm(strings.ToLower(m.Host+" "+m.Prog+" "+m.Msg), tm.text)
					}
					if found == tm.neg {
						all = false
					}
				}
				want = want || all
			}
			if got := f.match(&m); got != want {
				t.Errorf("%q on %.50q: %v, reading the fields says %v", w, m.Msg, got, want)
			}
		}
	}
	if len(words) < 60 {
		t.Errorf("only %d words tried", len(words))
	}
}
