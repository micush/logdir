package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"logdir/internal/core"
	"logdir/internal/logs"
)

// mcpEnv is a web server whose MCP access list is acl; post sends one JSON-RPC message and returns the HTTP status and body.
func mcpEnv(t *testing.T, acl ...string) (*webEnv, func(msg string, hdr ...string) (int, map[string]any)) {
	t.Helper()
	e := newWebEnv(t)
	e.mg.webH = e.ws.Handler()
	sup := e.ws.Status.Sup()
	sup.Mu.Lock()
	sup.Dc.Settings.MCPACL = acl
	sup.Mu.Unlock()
	return e, func(msg string, hdr ...string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", e.ts.URL+"/mcp", strings.NewReader(msg))
		req.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return res.StatusCode, m
	}
}

func mcpCall(name, args string) string {
	return `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

// toolResult is the text of a tools/call answer and whether it is an error.
func toolResult(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", m)
	}
	c := r["content"].([]any)[0].(map[string]any)
	return c["text"].(string), r["isError"] == true
}

func toolNames(t *testing.T, m map[string]any) []string {
	var out []string
	for _, x := range m["result"].(map[string]any)["tools"].([]any) {
		out = append(out, x.(map[string]any)["name"].(string))
	}
	return out
}

func TestMCPNobodyUntilListed(t *testing.T) {
	_, post := mcpEnv(t)
	if code, _ := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`); code != 403 {
		t.Fatalf("empty list: %d", code)
	}
	_, post = mcpEnv(t, "192.0.2.1 rw", "10.0.0.0/8")
	if code, _ := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`); code != 403 {
		t.Fatalf("not listed: %d", code)
	}
}

func TestMCPReadOnly(t *testing.T) {
	_, post := mcpEnv(t, "127.0.0.0/8")
	code, m := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	if code != 200 || m["result"].(map[string]any)["protocolVersion"] != "2025-03-26" {
		t.Fatalf("%d %v", code, m)
	}
	if code, _ := post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
		t.Fatalf("notification: %d", code)
	}
	_, m = post(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	names := strings.Join(toolNames(t, m), " ")
	if !strings.Contains(names, "logs_search") || strings.Contains(names, "set_node_paused") || strings.Contains(names, "schedule_set") || strings.Contains(names, "api_request") || strings.Contains(names, "edit_config") {
		t.Fatalf("read-only tools: %s", names)
	}
	if txt, isErr := toolResult(t, func() map[string]any { _, m := post(mcpCall("list_nodes", `{}`)); return m }()); isErr || !strings.Contains(txt, `"this_node":true`) {
		t.Fatalf("list_nodes: %s", txt)
	}
	requireGateTools(t, post)
	// a write is refused, whatever the client sends
	_, m = post(mcpCall("set_node_paused", `{"node":"this","paused":true}`))
	if m["error"] == nil {
		t.Fatalf("a tool that changes logdir is gone, yet it answered: %v", m)
	}
	_, m = post(mcpCall("no_such_tool", `{}`))
	if m["error"] == nil {
		t.Fatalf("unknown tool: %v", m)
	}
}

func TestMCPRefusesBrowsersAndGET(t *testing.T) {
	e, post := mcpEnv(t, "127.0.0.1 rw")
	if code, _ := post(`{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "https://evil.example"); code != 403 {
		t.Fatalf("foreign origin: %d", code)
	}
	res, err := http.Get(e.ts.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 405 {
		t.Fatalf("GET: %d", res.StatusCode)
	}
	// a batch
	res, err = http.Post(e.ts.URL+"/mcp", "application/json", bytes.NewBufferString(`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"}]`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(string(b), "[") || strings.Count(string(b), `"id"`) != 1 {
		t.Fatalf("batch: %d %s", res.StatusCode, b)
	}
}

func TestMCPResultLimitSetting(t *testing.T) {
	e, _ := mcpEnv(t, "127.0.0.1")
	sup := e.ws.Status.Sup()
	if sup.McpResultLimit() != 1<<20 {
		t.Fatalf("default %d", sup.McpResultLimit())
	}
	sup.Mu.Lock()
	sup.Dc.Settings.MCPMaxResultMB = 4
	sup.Mu.Unlock()
	if sup.McpResultLimit() != 4<<20 {
		t.Fatalf("4 MB: %d", sup.McpResultLimit())
	}
	for _, n := range []int{-1, 9} {
		dc := core.NewDaemonConfig()
		dc.Settings.MCPMaxResultMB = n
		if err := dc.Validate(); err == nil {
			t.Errorf("accepted %d MB", n)
		}
	}
	dc := core.NewDaemonConfig()
	dc.Settings.MCPMaxResultMB = 1
	if err := dc.Validate(); err != nil || dc.Settings.MCPMaxResultMB != 0 {
		t.Errorf("1 MB is the default and is not written: %v %d", err, dc.Settings.MCPMaxResultMB)
	}
}

// mcpClusterGone are the tools an MCP caller no longer has: anything about logdir's own cluster, configuration, alerts, schedules,
// updates or host, and the raw route of the GUI's API.  What is left is the devices, the device list and status, the cluster
// status (list_nodes, get_topology) and the network tools.
var mcpClusterGone = []string{"get_gateways", "get_alerts", "get_lldp", "get_host_stats", "get_schedule", "get_updates", "get_node_status", "get_config", "list_api", "api_get", "get_log"}

// requireGateTools checks that the tool list has the device and status tools and none of mcpClusterGone, that each of those
// answers "unknown tool", and that a node cannot be picked.
func requireGateTools(t *testing.T, post func(string, ...string) (int, map[string]any), hdr ...string) {
	t.Helper()
	_, m := post(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, hdr...)
	have := toolNames(t, m)
	for _, want := range []string{"list_nodes", "get_topology", "logs_search", "logs_stats", "logs_senders", "logs_status", "logs_shipping"} {
		if !core.ContainsStr(have, want) {
			t.Errorf("%s is not offered: %v", want, have)
		}
	}
	for _, gone := range mcpClusterGone {
		if core.ContainsStr(have, gone) {
			t.Errorf("%s is offered", gone)
		}
		if _, m := post(mcpCall(gone, `{}`), hdr...); m["error"] == nil {
			t.Errorf("%s answered: %v", gone, m)
		}
	}
	for _, name := range have {
		if strings.HasPrefix(name, "logs_") || name == "list_nodes" || name == "get_topology" {
			continue
		}
		t.Errorf("unexpected tool %s", name)
	}
	for _, name := range []string{"list_nodes", "get_topology", "logs_status"} {
		_, m := post(mcpCall(name, `{"node":"other","target":"all"}`), hdr...)
		if txt, isErr := toolResult(t, m); !isErr || !strings.Contains(txt, "no node argument") {
			t.Errorf("%s with a node: %v %s", name, isErr, txt)
		}
	}
}

// A node that is not in a cluster is the primary of a cluster of one: list_nodes must say so, show its pause as
// get_topology does, and give no empty cluster address.
func TestMCPListNodesStandalone(t *testing.T) {
	e, post := mcpEnv(t, "127.0.0.0/8")
	nodes := func() []map[string]any {
		_, m := post(mcpCall("list_nodes", `{}`))
		txt, isErr := toolResult(t, m)
		var out []map[string]any
		if isErr || json.Unmarshal([]byte(txt), &out) != nil || len(out) != 1 {
			t.Fatalf("list_nodes: %v %s", isErr, txt)
		}
		return out
	}
	n := nodes()[0]
	if n["primary"] != true || n["role"] != "primary" || n["this_node"] != true || n["paused"] != false || n["version"] != core.Version() {
		t.Fatalf("standalone node: %v", n)
	}
	if _, has := n["addr"]; has {
		t.Fatalf("a node with no cluster has no cluster address: %v", n)
	}
	sup := e.ws.Status.Sup()
	sup.Mu.Lock()
	sup.Dc.NodePaused = true
	sup.Mu.Unlock()
	if n = nodes()[0]; n["paused"] != true || n["primary"] != true {
		t.Fatalf("paused standalone node: %v", n)
	}
	_, m := post(mcpCall("get_topology", `{}`))
	if txt, _ := toolResult(t, m); !strings.Contains(txt, `"node_paused":true`) || !strings.Contains(txt, `"role":"primary"`) {
		t.Fatalf("get_topology disagrees: %s", txt)
	}
}

// The log tools through the endpoint, on real messages: every tool answers, filters work, fields come back, and the
// privacy layer hides public addresses (v4 and v6), the domain of an e-mail address and public AS numbers, in the message
// and in its fields, while private addresses stay.
func TestMCPLogTools(t *testing.T) {
	old := logs.LdSvc
	defer func() { logs.LdSvc = old }()
	logs.LdSvc = logs.NewLdService(nil, nil, "", nil)
	logs.LdSvc.TestCfg.Store(&core.LogsConfig{Enabled: true})
	now := time.Now()
	logs.LdSvc.Store = newTestStore(t, &now)
	at := now.Add(-5 * time.Minute)
	logs.LdSvc.Store.Add(core.LdMsg{T: at.UnixMilli(), Host: "sw1", Prog: "sshd", Sev: 3, Fac: 4, Msg: "Failed password for jo.doe@example.com from 8.8.8.8 port 2222", Src: "10.1.1.5", Via: "udp"})
	logs.LdSvc.Store.Add(core.LdMsg{T: at.Add(time.Second).UnixMilli(), Host: "fw1", Prog: "TRAFFIC", Sev: 6, Fac: 1,
		Msg: "action=deny src=2001:4860:4860::8888 src_ip=10.0.0.7 dst_ip=1.1.1.1 dst_port=443 peer AS 15169", Src: "10.1.1.6", Via: "tcp"})
	_, post := mcpEnv(t, "127.0.0.0/8")
	call := func(name, args string) string {
		t.Helper()
		_, m := post(mcpCall(name, args))
		txt, isErr := toolResult(t, m)
		if isErr {
			t.Fatalf("%s %s: %s", name, args, txt)
		}
		for _, leak := range []string{"8.8.8.8", "1.1.1.1", "2001:4860", "example.com", "15169"} {
			if strings.Contains(txt, leak) {
				t.Errorf("%s shows %s: %s", name, leak, txt)
			}
		}
		return txt
	}
	if txt := call("logs_search", `{"range":"1h"}`); !strings.Contains(txt, `"returned":2`) || !strings.Contains(txt, "jo.doe from x.x.x.x") ||
		!strings.Contains(txt, "src_ip=10.0.0.7") || !strings.Contains(txt, "AS xx169") || strings.Index(txt, "sw1") > strings.Index(txt, "fw1") {
		t.Errorf("logs_search: %s", txt)
	}
	if txt := call("logs_search", `{"severity":"err"}`); !strings.Contains(txt, `"returned":1`) || !strings.Contains(txt, "sshd") {
		t.Errorf("logs_search severity: %s", txt)
	}
	if txt := call("logs_search", `{"q":"src_ip=10.0.0.7","fields":"true"}`); !strings.Contains(txt, `"returned":1`) ||
		!strings.Contains(txt, `{"k":"src_ip","v":"10.0.0.7"}`) || !strings.Contains(txt, `{"k":"dst_ip","v":"x.x.x.x"}`) {
		t.Errorf("logs_search fields: %s", txt)
	}
	if txt := call("logs_tail", `{"host":"fw1"}`); !strings.Contains(txt, `"returned":1`) || !strings.Contains(txt, "TRAFFIC") {
		t.Errorf("logs_tail: %s", txt)
	}
	if txt := call("logs_stats", `{"range":"1h","field":"dst_port"}`); !strings.Contains(txt, `"by_severity":{"err":1,"info":1}`) || !strings.Contains(txt, "443") {
		t.Errorf("logs_stats: %s", txt)
	}
	if txt := call("logs_status", `{}`); !strings.Contains(txt, `"stored":2`) {
		t.Errorf("logs_status: %s", txt)
	}
	call("logs_senders", `{}`)
	call("logs_shipping", `{}`)
	_, m := post(mcpCall("logs_search", `{"severity":"bogus"}`))
	if txt, isErr := toolResult(t, m); !isErr || !strings.Contains(txt, "not one of") {
		t.Errorf("bad severity: %v %s", isErr, txt)
	}
}
