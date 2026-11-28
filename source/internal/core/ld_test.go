package core

import (
	"testing"
)

func TestLogsConfigValidate(t *testing.T) {
	ok := &LogsConfig{Enabled: true, Senders: []LogSenderCfg{{Name: "sw1", Address: "10.1.1.0/24"}, {Name: "fw", Address: "fw.example.com"}}, Alerts: []LogAlertCfg{{Name: "auth", Query: "failed password", Count: 5, WindowMin: 10}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*LogsConfig{
		"port":      {UDPPort: 70000},
		"name":      {Senders: []LogSenderCfg{{Name: "a b", Address: "10.0.0.1"}}},
		"address":   {Senders: []LogSenderCfg{{Name: "a", Address: "not an address"}}},
		"dupe":      {Senders: []LogSenderCfg{{Name: "a", Address: "10.0.0.1"}, {Name: "A", Address: "10.0.0.2"}}},
		"alert":     {Alerts: []LogAlertCfg{{Name: "x"}}},
		"window":    {Alerts: []LogAlertCfg{{Name: "x", Query: "y", WindowMin: 99999}}},
		"retention": {RetentionDays: -1},
	} {
		if c.Validate() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	p, tc, tl := (&LogsConfig{UDPPort: -1, TLSPort: 7000}).Ports()
	if p != 0 || tc != 514 || tl != 7000 {
		t.Errorf("ports %d %d %d", p, tc, tl)
	}
}

func TestHostFilterAlsoMatchesTheSendersName(t *testing.T) {
	q := LdQuery{Host: "fw1-mgmt", MinSev: -1, HostSrc: map[string]bool{"192.168.200.10": true}}
	f := q.Filter()
	if !f.Match(&LdMsg{Host: "cush", Src: "192.168.200.10"}) {
		t.Fatal("a message from the sender called fw1-mgmt did not match host=fw1-mgmt")
	}
	if !f.Match(&LdMsg{Host: "fw1-mgmt", Src: "10.0.0.9"}) {
		t.Fatal("a message that names the host itself did not match")
	}
	if f.Match(&LdMsg{Host: "cush", Src: "10.0.0.9"}) {
		t.Fatal("another sender's message matched")
	}
	if !f.HourOK(&LdHourMeta{Count: 1, Hosts: map[string]struct{}{"cush": {}}, Progs: map[string]struct{}{}}) {
		t.Fatal("an hour that has no message naming the host was skipped, though the sender's messages match")
	}
}

func TestWordsAlsoMatchTheSendersName(t *testing.T) {
	q := LdQuery{Q: "fw1-mgmt and not drop", MinSev: -1, SrcWords: map[string]string{"192.168.200.10": "cush fw1-mgmt.cush.local fw1-mgmt"}}
	f := q.Filter()
	if !f.Match(&LdMsg{Host: "cush", Src: "192.168.200.10", Msg: `devname="fw1" action="deny"`}) {
		t.Fatal("a word did not match the name of the message's sender")
	}
	if f.Match(&LdMsg{Host: "cush", Src: "192.168.200.10", Msg: "drop"}) {
		t.Fatal("a not-word was ignored")
	}
	if f.Match(&LdMsg{Host: "cush", Src: "10.0.0.9", Msg: "x"}) {
		t.Fatal("another sender's message matched")
	}
}
