package web

import (
	"testing"
)

// the Logs page asks the node picked in the top bar: its calls must be relayable
func TestProxyRelaysLogCalls(t *testing.T) {
	for _, p := range []string{"/api/logs/search?q=x&range=1h", "/api/logs/senders", "/api/logs/status"} {
		if err := ProxyAllowed("GET", p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}
