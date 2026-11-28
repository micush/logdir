package core

import (
	"fmt"
	"strings"
)

const (
	MaxLogSaved     = 200
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
	if x := strings.TrimRight(a.Severity, "=-"); a.Severity != "" && (ParseSevName(x) < 0 || len(a.Severity)-len(x) > 1) {
		return fmt.Errorf("logs: saved search %s: severity %q is not one of emerg … debug, with = (only) or - (or less severe)", n, a.Severity)
	}
	if a.Source != "" {
		if _, err := ParseAddrLoose(a.Source); err != nil && !LdNameRE.MatchString(a.Source) {
			return fmt.Errorf("logs: saved search %s: source %q is neither an address nor a name", n, a.Source)
		}
	}
	if a.Measure != "" || a.OverTime {
		if a.Field == "" {
			return fmt.Errorf("logs: saved search %s: counting by value needs a field", n)
		}
	}
	if _, _, err := ParseLdMeasure(a.Measure); err != nil {
		return fmt.Errorf("logs: saved search %s: %v", n, err)
	}
	return nil
}

func ValidateSaved(list []LogSavedCfg) error {
	if len(list) > MaxLogSaved {
		return fmt.Errorf("logs: at most %d saved searches", MaxLogSaved)
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
