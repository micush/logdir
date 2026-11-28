package main

import (
	"testing"
	"time"

	"logdir/internal/core"
)

// a call through the endpoint is counted: the caller, the tool, a refusal, and the page's answer
func TestMCPUsageEndToEnd(t *testing.T) {
	e, post := mcpEnv(t, "127.0.0.0/8")
	post(mcpCall("list_nodes", `{}`))
	post(mcpCall("logs_status", `{}`))
	r := e.ws.Usage.Report(time.Hour)
	tot := r["totals"].(core.McpUseSum)
	if tot.Calls != 2 || tot.Refused != 0 {
		t.Fatalf("totals %+v", tot)
	}
	tools := map[string]int64{}
	for _, s := range r["by_tool"].([]core.McpUseSum) {
		tools[s.Name] = s.Calls
	}
	if tools["list_nodes"] != 1 || tools["logs_status"] != 1 {
		t.Errorf("by tool %v", tools)
	}
	if k := r["by_key"].([]core.McpUseSum); len(k) != 1 || k[0].Name != "(address list)" {
		t.Errorf("by key %+v", k)
	}
	if s := r["by_source"].([]core.McpUseSum); len(s) != 1 || s[0].Name != "127.0.0.1" {
		t.Errorf("by source %+v", s)
	}
}
