package core

import (
	"strings"
	"testing"
)

func sevp(n int) *int { return &n }

func TestDropValidate(t *testing.T) {
	ok := []LogDropCfg{
		{Name: "keepalives", Query: "keepalive"},
		{Name: "dns", Query: "action=accept and dst_port=53"},
		{Name: "chatty switches", Host: "sw*", Severity: sevp(6), SevMatch: "less"},
		{Name: "ntp", Program: "ntpd", Senders: []string{"core*", "10.9.0.0/16", "10.1.1.1"}},
		{Name: "debug", Severity: sevp(7), SevMatch: "exact"},
		{Name: "not errors", Query: "not error", Disabled: true},
	}
	if err := validateDropRules(ok); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		rules []LogDropCfg
		want  string
	}{
		{[]LogDropCfg{{Name: "all"}}, "would drop everything"},
		{[]LogDropCfg{{Name: " "}}, "needs a name"},
		{[]LogDropCfg{{Name: "a", Query: "x"}, {Name: "A", Query: "y"}}, "used twice"},
		{[]LogDropCfg{{Name: "a", Severity: sevp(8)}}, "0 to 7"},
		{[]LogDropCfg{{Name: "a", Query: "x", SevMatch: "less"}}, "needs a severity"},
		{[]LogDropCfg{{Name: "a", Severity: sevp(3), SevMatch: "more"}}, "not worse, exact or less"},
		{[]LogDropCfg{{Name: "a", Senders: []string{"bad name!"}}}, "not a name"},
		{[]LogDropCfg{{Name: "a", Query: strings.Repeat("x", 2001)}}, "too long"},
		{make([]LogDropCfg, ldDropMax+1), "at most"},
	} {
		err := (&LogsConfig{DropRules: c.rules}).Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.rules[0], err, c.want)
		}
	}
	// a copy is a copy
	c := &LogsConfig{DropRules: ok}
	d := c.clone()
	*d.DropRules[2].Severity = 1
	d.DropRules[3].Senders[0] = "x"
	if *c.DropRules[2].Severity != 6 || c.DropRules[3].Senders[0] != "core*" {
		t.Error("clone shares the rules")
	}
}
