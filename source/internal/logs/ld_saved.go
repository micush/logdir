package logs

import (
	"fmt"
	"strings"

	"logdir/internal/core"
)

// changeSaved is the list after saving a (replacing the one named rename, or a, case ignored) or deleting del.
func ChangeSaved(list []core.LogSavedCfg, a *core.LogSavedCfg, rename, del string) ([]core.LogSavedCfg, string, error) {
	out := append([]core.LogSavedCfg(nil), list...)
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
	if len(out) >= core.MaxLogSaved {
		return nil, "", fmt.Errorf("at most %d saved searches: delete one first", core.MaxLogSaved)
	}
	return append(out, *a), "logs: saved search " + a.Name + " saved", nil
}
