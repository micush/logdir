package main

// Saved searches of the Logs page: a name for what the page shows (the filter, the range, the severity, and the values card with what
// it counts), kept in settings.logs.saved so every user and every node sees the same list.  The page saves and deletes one at a time
// (PUT /api/logs/saved), so two people saving at once do not overwrite each other.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	maxLogSaved     = 200
	maxLogSavedName = 64
)

// LogSavedCfg is one saved search: the Logs page's state, as its query string has it.
type LogSavedCfg struct {
	Name     string `json:"name"`
	Q        string `json:"q,omitempty"`
	Range    string `json:"range,omitempty"`    // 15m, 1h, 6h, 1d, 7d, 30d or all (default 1h)
	Severity string `json:"severity,omitempty"` // as the API: warning, warning= (only), info- (or less severe)
	Host     string `json:"host,omitempty"`
	Program  string `json:"program,omitempty"`
	Source   string `json:"source,omitempty"`
	Field    string `json:"field,omitempty"`     // the values card: the field counted
	Measure  string `json:"measure,omitempty"`   // and what it counts besides messages: distinct:F or sum:F
	OverTime bool   `json:"over_time,omitempty"` // and its values drawn over time
	By       string `json:"by,omitempty"`        // who saved it
	At       int64  `json:"at,omitempty"`        // when (Unix seconds)
}

var ldSavedRanges = map[string]bool{"15m": true, "1h": true, "6h": true, "1d": true, "7d": true, "30d": true, "all": true}

// Validate checks a saved search as the API would its query string (a search that cannot run is refused when it is saved).
func (a LogSavedCfg) Validate() error {
	n := strings.TrimSpace(a.Name)
	if n == "" || len(n) > maxLogSavedName || n != a.Name {
		return fmt.Errorf("logs: a saved search needs a name of 1 to %d characters, without spaces at either end", maxLogSavedName)
	}
	if len(a.Q) > 2000 || len(a.Host) > 255 || len(a.Program) > 255 || len(a.Source) > 255 || len(a.Field) > 128 || len(a.Measure) > 140 || len(a.Severity) > 16 || len(a.By) > 128 {
		return fmt.Errorf("logs: saved search %s: too long", n)
	}
	if a.Range != "" && !ldSavedRanges[a.Range] {
		return fmt.Errorf("logs: saved search %s: range %q is not one of 15m, 1h, 6h, 1d, 7d, 30d, all", n, a.Range)
	}
	if x := strings.TrimRight(a.Severity, "=-"); a.Severity != "" && (parseSevName(x) < 0 || len(a.Severity)-len(x) > 1) {
		return fmt.Errorf("logs: saved search %s: severity %q is not one of emerg … debug, with = (only) or - (or less severe)", n, a.Severity)
	}
	if a.Source != "" {
		if _, err := parseAddrLoose(a.Source); err != nil && !ldNameRE.MatchString(a.Source) {
			return fmt.Errorf("logs: saved search %s: source %q is neither an address nor a name", n, a.Source)
		}
	}
	if a.Measure != "" || a.OverTime {
		if a.Field == "" {
			return fmt.Errorf("logs: saved search %s: counting by value needs a field", n)
		}
	}
	if _, _, err := parseLdMeasure(a.Measure); err != nil {
		return fmt.Errorf("logs: saved search %s: %v", n, err)
	}
	return nil
}

func validateSaved(list []LogSavedCfg) error {
	if len(list) > maxLogSaved {
		return fmt.Errorf("logs: at most %d saved searches", maxLogSaved)
	}
	seen := map[string]bool{}
	for _, a := range list {
		if err := a.Validate(); err != nil {
			return err
		}
		if k := strings.ToLower(a.Name); seen[k] {
			return fmt.Errorf("logs: saved search name %q is used twice", a.Name)
		} else {
			seen[k] = true
		}
	}
	return nil
}

// PUT /api/logs/saved {"save": {…}} adds a saved search or replaces the one of that name (case ignored; "rename" names the one it
// replaces when the name changes), {"delete": "name"} removes one.  It answers with the list as saved.  Read-only users cannot (as every
// write).
func (w *WebServer) handleLogSaved(rw http.ResponseWriter, r *http.Request, s *session) {
	var req struct {
		Save   *LogSavedCfg `json:"save"`
		Rename string       `json:"rename"`
		Delete string       `json:"delete"`
	}
	if !readJSON(rw, r, 64<<10, &req) {
		return
	}
	if (req.Save == nil) == (req.Delete == "") {
		jsonError(rw, http.StatusBadRequest, "say what to do: save or delete")
		return
	}
	if req.Save != nil {
		a := req.Save
		a.Name = strings.TrimSpace(a.Name)
		a.By, a.At = s.user, time.Now().Unix()
		if err := a.Validate(); err != nil {
			jsonError(rw, http.StatusUnprocessableEntity, strings.TrimPrefix(err.Error(), "logs: "))
			return
		}
	}
	// read, change, save; again when somebody else saved in between
	for try := 0; ; try++ {
		cur, _, err := w.mg.LiveConfig()
		if err != nil {
			jsonError(rw, http.StatusInternalServerError, err.Error())
			return
		}
		base := configBase(cur)
		b, err := json.Marshal(cur)
		if err != nil {
			jsonError(rw, http.StatusInternalServerError, err.Error())
			return
		}
		dc := newDaemonConfig()
		if err := json.Unmarshal(b, dc); err != nil {
			jsonError(rw, http.StatusInternalServerError, err.Error())
			return
		}
		if dc.Settings.Logs == nil {
			dc.Settings.Logs = &LogsConfig{}
		}
		list, note, err := changeSaved(dc.Settings.Logs.Saved, req.Save, req.Rename, req.Delete)
		if err != nil {
			jsonError(rw, http.StatusUnprocessableEntity, err.Error())
			return
		}
		dc.Settings.Logs.Saved = list
		if err := dc.Validate(); err != nil {
			jsonError(rw, http.StatusUnprocessableEntity, err.Error())
			return
		}
		err = w.mg.PutConfigBase(dc, s.user, note, base)
		if errors.Is(err, ErrConfigChanged) && try < 3 {
			continue
		}
		if err != nil {
			jsonError(rw, http.StatusInternalServerError, err.Error())
			return
		}
		infof("web: %q: %s (from %s)", s.user, note, clientIP(r))
		writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "saved": list})
		return
	}
}

// GET /api/logs/saved: the saved searches (read-only users too).
func (w *WebServer) handleLogSavedList(rw http.ResponseWriter, r *http.Request, s *session) {
	list := []LogSavedCfg{}
	if cur, _, err := w.mg.LiveConfig(); err == nil && cur.Settings.Logs != nil && cur.Settings.Logs.Saved != nil {
		list = cur.Settings.Logs.Saved
	}
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "saved": list})
}

// changeSaved is the list after saving a (replacing the one named rename, or a, case ignored) or deleting del.
func changeSaved(list []LogSavedCfg, a *LogSavedCfg, rename, del string) ([]LogSavedCfg, string, error) {
	out := append([]LogSavedCfg(nil), list...)
	find := func(n string) int {
		for i, x := range out {
			if strings.EqualFold(x.Name, n) {
				return i
			}
		}
		return -1
	}
	if a == nil {
		i := find(strings.TrimSpace(del))
		if i < 0 {
			return nil, "", fmt.Errorf("no saved search is named %q", del)
		}
		return append(out[:i], out[i+1:]...), "logs: saved search " + out[i].Name + " deleted", nil
	}
	old := find(a.Name)
	if rename != "" && !strings.EqualFold(rename, a.Name) {
		if old >= 0 {
			return nil, "", fmt.Errorf("a saved search is already named %q", out[old].Name)
		}
		old = find(rename)
	}
	if old >= 0 {
		out[old] = *a
		return out, "logs: saved search " + a.Name + " changed", nil
	}
	if len(out) >= maxLogSaved {
		return nil, "", fmt.Errorf("at most %d saved searches: delete one first", maxLogSaved)
	}
	return append(out, *a), "logs: saved search " + a.Name + " saved", nil
}
