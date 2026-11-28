package core

import (
	"encoding/json"
	"testing"
)

func TestRawSev(t *testing.T) {
	for _, m := range []LdMsg{{Sev: 3, Msg: "x"}, {Sev: 0, Host: `a,"s":7`, Prog: `p,"s":6`, PID: `,"s":5`, Msg: `,"s":4`}, {Sev: 7, Msg: ""}} {
		b, _ := json.Marshal(&m)
		if got := ldRawSev(b); got != int(m.Sev) {
			t.Errorf("%s: severity %d, want %d", b, got, m.Sev)
		}
	}
	for _, line := range []string{`{"t":1}`, `{"t":1,"s":12}`, `{"t":1,"s":`, `{"t":1,"s":x}`} {
		if got := ldRawSev([]byte(line)); got != -1 {
			t.Errorf("%s: severity %d, want -1", line, got)
		}
	}
}
