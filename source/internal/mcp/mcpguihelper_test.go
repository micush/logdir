package mcp

import (
	"testing"
)

// A request that changes anything but a device is refused over MCP, at every route.
func TestMCPWriteGuard(t *testing.T) {
	for _, c := range []struct {
		m, p string
		ok   bool
	}{
		{"GET", "/api/config", true}, {"PUT", "/api/config", false}, {"POST", "/api/canvas/edit", false},
		{"POST", "/api/proxy?node=x&path=/api/config", false},
		{"POST", "/api/nodepause", false}, {"POST", "/api/schedule", false},
	} {
		if err := mcpWriteGuard(c.m, c.p); (err == nil) != c.ok {
			t.Errorf("%s %s: %v", c.m, c.p, err)
		}
	}
}
