package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// the line check (rawOK) never rules out a line that matches: every word, phrase, wildcard and field term of a set of awkward
// messages, with and without a sender name that holds some of those words
func TestRawWordsNeverMiss(t *testing.T) {
	msgs := []LdMsg{
		{Host: "FW1", Prog: "fortigate", Src: "10.0.0.1", Msg: `date=x devname="fw1" srcip=192.168.1.7 dstport=443 action="accept" Session=17000001`},
		{Host: "core-sw", Prog: "%LINK-3-UPDOWN", Src: "10.0.0.2", Msg: `Interface Gi0/1, changed state to down <b>&amp;</b> tab	here`},
		{Host: "asa", Src: "10.0.0.3", Msg: `%ASA-2-106001: Inbound TCP connection denied from 198.51.100.7/4444 to 10.0.0.5/80 flags SYN on interface outside`},
		{Host: "app", Prog: "api", Src: "10.0.0.4", Msg: `{"user":"\u0062ob","path":"a\/b","n":1.50,"ok":true,"q":"say \"hi\""}`},
		{Host: "web", Prog: "nginx", Src: "10.0.0.5", Msg: "ünïcödé KELVIN \u212a straße Iİ \u212aelvin", SD: `[meta@1 who="Bo\]b" mode="FAST"]`},
		{Host: "pa", Src: "10.0.0.6", Msg: `1,2026/11/26 10:00:00,1,TRAFFIC,end,1,x,1.1.1.1,2.2.2.2,0.0.0.0,0.0.0.0,"r""1",,,app,vsys1,a,b,c,d,e,,1,1,5,6,0,0,0x0,tcp,allow`},
		{Host: "cef", Src: "10.0.0.7", Msg: `CEF:0|Vend|Prod|1|sig|Name here|5|act=a\=b src=10.9.9.9 msg=x\|y suser=Admin`},
		{Host: "lx", Prog: "sshd", PID: "77", Src: "10.0.0.8", Msg: "Failed password for root from 203.0.113.9 port 22 ssh2"},
	}
	names := map[string]string{"10.0.0.1": "edge firewall", "10.0.0.8": "bastion sshd-host"}
	var words []string
	for _, m := range msgs {
		ws := strings.Fields(strings.ToLower(m.Host + " " + m.Prog + " " + m.Msg))
		for i, w := range ws {
			words = append(words, w, "not "+w, w[:len(w)/2+1]+"*"+w[len(w)-1:], strings.ToUpper(w))
			if i+1 < len(ws) {
				words = append(words, w+" "+ws[i+1], `"`+w+" "+ws[i+1]+`"`)
			}
		}
		for _, kv := range LdFields(&m) {
			v := strings.ToLower(kv.Val)
			if v != "" && !strings.ContainsAny(v, " \t\n") {
				words = append(words, kv.Key+"="+v, kv.Key+"="+v[:len(v)/2]+"*", "nothere or "+kv.Key+"="+v)
			}
		}
	}
	words = append(words, "edge", "firewall fw1", "sshd-host", "bastion", "fw1 fortigate", "fw1 fortigate date=x", "k", "ss", "kelvin k")
	ruledOut := 0
	for _, srcWords := range []map[string]string{nil, names} {
		for _, w := range words {
			q := LdQuery{Q: w, MinSev: -1}
			q.SrcWords = srcWords
			f := q.Filter()
			for i := range msgs {
				m := msgs[i]
				line, _ := json.Marshal(&m)
				if f.RawOK(line) {
					continue
				}
				ruledOut++
				if f.Match(&m) {
					t.Errorf("%q (names %v) ruled out %s, which matches", w, srcWords != nil, line)
				}
			}
		}
	}
	if ruledOut < 1000 {
		t.Errorf("only %d lines ruled out", ruledOut)
	}
}
