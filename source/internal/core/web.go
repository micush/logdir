package core

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"
)

const FailWindow = 10 * time.Minute

const LockDuration = 5 * time.Minute

type FailRec struct {
	Count    int       `json:"-"`
	Inflight int       `json:"-"` // login attempts that passed the lockout check and have not finished yet
	First    time.Time `json:"-"`
	Locked   time.Time `json:"-"`
}

// ── helpers ──────────────────────────────────────────────────────────────────

func WriteJSON(rw http.ResponseWriter, code int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	json.NewEncoder(rw).Encode(v)
}

func JsonError(rw http.ResponseWriter, code int, msg string) {
	WriteJSON(rw, code, map[string]any{"ok": false, "error": msg})
}

func ReadJSON(rw http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, limit))
	if err != nil {
		JsonError(rw, http.StatusRequestEntityTooLarge, "request body too large")
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		JsonError(rw, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func ClientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}
