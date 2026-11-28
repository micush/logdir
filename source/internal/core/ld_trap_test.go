package core

import (
	"testing"
)

// systemd-resolved answers a reverse lookup of the host's default gateway with "_gateway": not a name for the Logs page.
func TestRealNames(t *testing.T) {
	got := realNames([]string{"_gateway", "_outbound.", "switch2.cush.local.", "_localdnsstub", "_LocalDNSProxy"})
	if len(got) != 1 || got[0] != "switch2.cush.local." {
		t.Fatalf("%v", got)
	}
	if realNames([]string{"_gateway"}) != nil {
		t.Fatal("_gateway kept")
	}
}
