package main

import (
	"testing"
)

// Over the web API: saved by one call at a time, kept in the configuration with who saved it, a search the query parser refuses is
// refused, and the list reads back.
func TestSavedSearchesWeb(t *testing.T) {
	e := newWebEnv(t)
	e.login("alice", "pw")
	put := func(body any) resp { return e.do("PUT", "/api/logs/saved", body, withAuth(e, true)) }
	if r := put(map[string]any{"save": map[string]any{"name": "port scans", "q": "action=deny", "range": "1d", "field": "src_ip", "measure": "distinct:dst_port"}}); r.code != 200 {
		t.Fatalf("save: %d %s", r.code, r.raw)
	}
	if r := put(map[string]any{"save": map[string]any{"name": "ssh", "q": "sshd", "severity": "warning-"}}); r.code != 200 {
		t.Fatalf("save 2: %d %s", r.code, r.raw)
	}
	if r := put(map[string]any{"save": map[string]any{"name": "bad", "severity": "loud"}}); r.code != 422 {
		t.Fatalf("a bad severity: %d %s", r.code, r.raw)
	}
	if r := put(map[string]any{}); r.code != 400 {
		t.Fatalf("nothing to do: %d", r.code)
	}
	dc, _, _ := e.mg.LiveConfig()
	if dc.Settings.Logs == nil || len(dc.Settings.Logs.Saved) != 2 || dc.Settings.Logs.Saved[0].By != "alice" || dc.Settings.Logs.Saved[0].At == 0 {
		t.Fatalf("in the configuration: %+v", dc.Settings.Logs)
	}
	r := e.do("GET", "/api/logs/saved", nil, withAuth(e, false))
	if r.code != 200 || len(r.body["saved"].([]any)) != 2 {
		t.Fatalf("list: %d %s", r.code, r.raw)
	}
	if r := put(map[string]any{"delete": "SSH"}); r.code != 200 || len(r.body["saved"].([]any)) != 1 {
		t.Fatalf("delete: %d %s", r.code, r.raw)
	}
	// without the CSRF token: refused
	if r := e.do("PUT", "/api/logs/saved", map[string]any{"delete": "port scans"}, withAuth(e, false)); r.code != 403 {
		t.Fatalf("without CSRF: %d", r.code)
	}
}
