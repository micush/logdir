package logs

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"logdir/internal/core"
)

// what ldDecodeLine reads must be what encoding/json reads, and lines in the writer's own form must not need encoding/json
func checkLine(t *testing.T, line []byte, mustFast bool) {
	t.Helper()
	var fast, slow core.LdMsg
	ok := ldDecodeLine(line, &fast, map[string]string{})
	err := json.Unmarshal(line, &slow)
	if ok && err != nil {
		t.Fatalf("%q: read here, but encoding/json says %v", line, err)
	}
	if ok && !reflect.DeepEqual(fast, slow) {
		t.Fatalf("%q:\n here %+v\n json %+v", line, fast, slow)
	}
	if mustFast && !ok {
		t.Fatalf("%q: written by json.Marshal but not read here", line)
	}
}

func lineSamples() []core.LdMsg {
	return []core.LdMsg{
		{},
		{T: 1795448657978, Host: "fw0", Prog: "fortigate", Fac: 1, Sev: 6, Msg: `date=x devname="fw0" action="accept"`, Src: "127.0.0.1", Via: "udp", Node: "vm"},
		{T: -5, PID: "123", Fac: 23, Sev: 0, Msg: "tab\there\nnew \\ back / slash <b>&amp; \u2028 \x00 \x1f", MID: "ID47", SD: `[x@1 a="b\"c"]`, Sent: 1, Raw: "<13>raw"},
		{Msg: "ünïcödé 日本語 emoji 😀 Kelvin \u212a", Host: strings.Repeat("h", 100)},
		{Msg: string([]byte{0xff, 0xfe, 'a'})}, // invalid UTF-8: Marshal writes \ufffd
		{Fac: -128, Sev: 127, T: 9223372036854775807, Sent: -9223372036854775808},
	}
}

func TestDecodeLine(t *testing.T) {
	for _, m := range lineSamples() {
		b, _ := json.Marshal(&m)
		checkLine(t, b, true)
		checkLine(t, append(b, '\n'), true)
		checkLine(t, append(b, '\r', '\n'), true)
	}
	// not the writer's form: either read the same or left to encoding/json
	for _, l := range []string{
		`{"t":1, "h":"x"}`, `{"t":1.0}`, `{"t":01}`, `{"t":-}`, `{"t":1e3}`, `{"t":null}`, `{"h":null}`, `{"T":1}`, `{"t":1,"t":2}`,
		`{"x":1}`, `{"f":200}`, `{"s":-129}`, `{"t":99999999999999999999}`, `{"m":"\ud83d\ude00"}`, `{"m":"\ud83d"}`, `{"m":"\ude00x"}`,
		`{"m":"\uZZZZ"}`, `{"m":"\x"}`, `{"m":"a` + "\x01" + `"}`, `{"m":"a`, `{"m":"a"`, `{"m":"a",}`, `{"m":"a"}x`, `{"m":"\u00e9\u00E9"}`,
		`{"m":"` + string([]byte{0xc3}) + `"}`, `{"m":"` + string([]byte{0xc3, 0xa9}) + `\n"}`, `{}`, `{`, `}`, ``, `[]`, `{"h":"a\"b"}`,
		`{"sd":"x","d":"y","o":0,"r":"z","i":"1","v":"tls","n":"n","a":"::1"}`, `{"m":1}`, `{"t":"1"}`, `{"t":-0}`,
	} {
		checkLine(t, []byte(l), false)
	}
}

func FuzzDecodeLine(f *testing.F) {
	for _, m := range lineSamples() {
		b, _ := json.Marshal(&m)
		f.Add(b)
	}
	f.Add([]byte(`{"m":"\ud83d\ude00","t":-0}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		checkLine(t, b, false)
		var m core.LdMsg
		if json.Unmarshal(b, &m) == nil { // what the writer would write from it is read here
			out, _ := json.Marshal(&m)
			checkLine(t, out, true)
		}
	})
}

// a stored line longer than the line reader's buffer is read whole, from the hour being written and from a compressed one
func TestStreamLongLine(t *testing.T) {
	s, now := wordStore(t, t.TempDir(), 3, 50)
	big := "start " + strings.Repeat("abcdefghij", 250_000) + " finish"
	for _, at := range []time.Time{now.Add(-150 * time.Minute), now.Add(-time.Minute)} {
		s.Add(core.LdMsg{T: at.UnixMilli(), Host: "big", Prog: "x", Sev: 6, Src: "10.1.1.1", Msg: big})
	}
	s.Flush()
	var got []int
	for h := now.Add(-3*time.Hour).Unix() / 3600; h <= now.Unix()/3600; h++ {
		n := 0
		if !s.streamFileRaw(h, nil, func(m *core.LdMsg) bool {
			if m.Host == "big" {
				if m.Msg != big {
					t.Errorf("hour %d: the long message came back as %d bytes", h, len(m.Msg))
				}
				got = append(got, len(m.Msg))
			}
			n++
			return true
		}) {
			t.Errorf("hour %d: no file", h)
		}
		if n == 0 {
			t.Errorf("hour %d: nothing read", h)
		}
	}
	if len(got) != 2 {
		t.Fatalf("%d long messages read, want 2", len(got))
	}
	s.cache.set(0, 1)
	q := core.LdQuery{From: now.Add(-4 * time.Hour).UnixMilli(), MinSev: -1, Q: "start abcdefghij", Limit: 10}
	if a := s.Search(context.Background(), q); len(a.Msgs) != 2 {
		t.Errorf("search for it: %d", len(a.Msgs))
	}
}
