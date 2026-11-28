package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"logdir/internal/core"
	"logdir/internal/logs"
)

// PUT /api/logs/saved {"save": {…}} adds a saved search or replaces the one of that name (case ignored; "rename" names the one it
// replaces when the name changes), {"delete": "name"} removes one.  It answers with the list as saved.  Read-only users cannot (as every
// write).
func (w *WebServer) handleLogSaved(rw http.ResponseWriter, r *http.Request, s *Session) {
	var req struct {
		Save   *core.LogSavedCfg `json:"save"`
		Rename string            `json:"rename"`
		Delete string            `json:"delete"`
	}
	if !core.ReadJSON(rw, r, 64<<10, &req) {
		return
	}
	if (req.Save == nil) == (req.Delete == "") {
		core.JsonError(rw, http.StatusBadRequest, "say what to do: save or delete")
		return
	}
	if req.Save != nil {
		a := req.Save
		a.Name = strings.TrimSpace(a.Name)
		a.By, a.At = s.User, time.Now().Unix()
		if err := a.Validate(); err != nil {
			core.JsonError(rw, http.StatusUnprocessableEntity, strings.TrimPrefix(err.Error(), "logs: "))
			return
		}
	}
	// read, change, save; again when somebody else saved in between
	for try := 0; ; try++ {
		cur, _, err := w.Mg.LiveConfig()
		if err != nil {
			core.JsonError(rw, http.StatusInternalServerError, err.Error())
			return
		}
		base := core.ConfigBase(cur)
		b, err := json.Marshal(cur)
		if err != nil {
			core.JsonError(rw, http.StatusInternalServerError, err.Error())
			return
		}
		dc := core.NewDaemonConfig()
		if err := json.Unmarshal(b, dc); err != nil {
			core.JsonError(rw, http.StatusInternalServerError, err.Error())
			return
		}
		if dc.Settings.Logs == nil {
			dc.Settings.Logs = &core.LogsConfig{}
		}
		list, note, err := logs.ChangeSaved(dc.Settings.Logs.Saved, req.Save, req.Rename, req.Delete)
		if err != nil {
			core.JsonError(rw, http.StatusUnprocessableEntity, err.Error())
			return
		}
		dc.Settings.Logs.Saved = list
		if err := dc.Validate(); err != nil {
			core.JsonError(rw, http.StatusUnprocessableEntity, err.Error())
			return
		}
		err = w.Mg.PutConfigBase(dc, s.User, note, base)
		if errors.Is(err, core.ErrConfigChanged) && try < 3 {
			continue
		}
		if err != nil {
			core.JsonError(rw, http.StatusInternalServerError, err.Error())
			return
		}
		core.Infof("web: %q: %s (from %s)", s.User, note, core.ClientIP(r))
		core.WriteJSON(rw, http.StatusOK, map[string]any{"ok": true, "saved": list})
		return
	}
}

// GET /api/logs/saved: the saved searches (read-only users too).
func (w *WebServer) handleLogSavedList(rw http.ResponseWriter, r *http.Request, s *Session) {
	list := []core.LogSavedCfg{}
	if cur, _, err := w.Mg.LiveConfig(); err == nil && cur.Settings.Logs != nil && cur.Settings.Logs.Saved != nil {
		list = cur.Settings.Logs.Saved
	}
	core.WriteJSON(rw, http.StatusOK, map[string]any{"ok": true, "saved": list})
}
