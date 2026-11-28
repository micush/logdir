package mcp

import (
	"strings"
	"testing"

	"logdir/internal/core"
)

func TestTrapConfig(t *testing.T) {
	ok := []core.LogsConfig{{}, {TrapPort: -1}, {TrapPort: 1162}, {UDPPort: -1, TrapPort: 514}, {TrapCommunities: []string{"public", "n0c-Tr4ps"}}}
	for _, c := range ok {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []core.LogsConfig{{TrapPort: 65536}, {TrapPort: -2}, {TrapPort: 514}, {UDPPort: 1162, TrapPort: 1162}, {TrapCommunities: []string{""}},
		{TrapCommunities: []string{"has space"}}, {TrapCommunities: []string{"a", "a"}}, {TrapCommunities: []string{strings.Repeat("x", 65)}}}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v: accepted", c)
		}
	}
	var none *core.LogsConfig
	if none.TrapPortOrDefault() != 162 || (&core.LogsConfig{TrapPort: -1}).TrapPortOrDefault() != 0 || (&core.LogsConfig{TrapPort: 1162}).TrapPortOrDefault() != 1162 {
		t.Error("trap port")
	}
	if !(&core.LogsConfig{}).TrapCommunityOK("anything") || (&core.LogsConfig{TrapCommunities: []string{"a"}}).TrapCommunityOK("b") || !(&core.LogsConfig{TrapCommunities: []string{"a"}}).TrapCommunityOK("a") {
		t.Error("communities")
	}
	if !mcpSecret("trap_communities") {
		t.Error("trap communities are not hidden from MCP callers")
	}
}
