package core

import (
	"testing"
)

func intp(v int) *int { return &v }

func TestShipValidate(t *testing.T) {
	good := []LogDestCfg{
		{Name: "a", Type: "syslog", Server: "10.0.0.1"},
		{Name: "b", Type: "syslog", Protocol: "tls", Server: "log.example.com:6514", Start: "24h", MaxBehindHours: 72},
		{Name: "c", Type: "syslog", Protocol: "udp", Server: "10.0.0.1", SpoofSource: true},
		{Name: "d", Type: "json_tcp", Server: "[2001:db8::1]:5000", Protocol: "tls"},
		{Name: "e", Type: "splunk_hec", URL: "https://splunk:8088", Token: "x"},
		{Name: "f", Type: "elastic", URL: "https://es:9200", Index: "logs-logdir-default"},
		{Name: "g", Type: "loki", URL: "http://loki:3100", MinSev: intp(4), SevExact: true, Senders: []string{"fw*", "10.0.0.0/8", "10.1.1.1"}, Start: "oldest"},
	}
	if err := validateDestinations(good); err != nil {
		t.Fatal(err)
	}
	bad := []LogDestCfg{
		{Name: "", Type: "syslog", Server: "x"},
		{Name: "a b", Type: "syslog", Server: "x"},
		{Name: "a", Type: "ftp", Server: "x"},
		{Name: "a", Type: "syslog"},
		{Name: "a", Type: "syslog", Server: "x", Protocol: "sctp"},
		{Name: "a", Type: "syslog", Server: "x:99999"},
		{Name: "a", Type: "syslog", Server: "x", SpoofSource: true},
		{Name: "a", Type: "json_tcp", Server: "x", Protocol: "udp"},
		{Name: "a", Type: "splunk_hec", URL: "https://s"},
		{Name: "a", Type: "elastic", URL: "ftp://s"},
		{Name: "a", Type: "loki", URL: "https://s", Server: "x"},
		{Name: "a", Type: "syslog", Server: "x", Token: "t"},
		{Name: "a", Type: "syslog", Server: "x", MinSev: intp(9)},
		{Name: "a", Type: "syslog", Server: "x", SevExact: true},
		{Name: "a", Type: "syslog", Server: "x", Start: "yesterday"},
		{Name: "a", Type: "syslog", Server: "x", Senders: []string{"bad name!"}},
		{Name: "a", Type: "syslog", Server: "x", CAPEM: "not a cert"},
		{Name: "a", Type: "elastic", URL: "https://s", Index: "a b"},
		{Name: "a", Type: "syslog", Server: "x", RateLimit: -1},
	}
	for i, d := range bad {
		if err := validateDestinations([]LogDestCfg{d}); err == nil {
			t.Errorf("bad %d accepted: %+v", i, d)
		}
	}
	if err := validateDestinations([]LogDestCfg{{Name: "A", Type: "syslog", Server: "x"}, {Name: "a", Type: "syslog", Server: "y"}}); err == nil {
		t.Error("a name used twice was accepted")
	}
	// the settings check runs them; a configuration without destinations is unchanged
	c := &LogsConfig{Destinations: []LogDestCfg{{Name: "x", Type: "nope"}}}
	if c.Validate() == nil {
		t.Error("LogsConfig.Validate does not check the destinations")
	}
	d := c.clone()
	d.Destinations[0].Name = "y"
	if c.Destinations[0].Name != "x" {
		t.Error("clone shares the destinations")
	}
}
