package core

import (
	"fmt"
	"strings"
)

const ldDropMax = 100

type LogDropCfg struct {
	Name     string   `json:"name"`
	Query    string   `json:"query,omitempty"` // the Logs page's search language: words, fields, and, or, not
	Host     string   `json:"host,omitempty"`
	Program  string   `json:"program,omitempty"`
	Senders  []string `json:"senders,omitempty"`   // names (* wildcards), addresses or networks
	Severity *int     `json:"severity,omitempty"`  // 0 (emergency) to 7 (debug)
	SevMatch string   `json:"sev_match,omitempty"` // with severity: "" or "worse" (this or more severe), "exact", "less" (this or less severe)
	Disabled bool     `json:"disabled,omitempty"`
}

func (d *LogDropCfg) Validate() error {
	n := strings.TrimSpace(d.Name)
	if n == "" || len(n) > 64 {
		return fmt.Errorf("a drop rule needs a name of 1 to 64 characters")
	}
	if len(d.Query) > 2000 || len(d.Host) > 255 || len(d.Program) > 255 {
		return fmt.Errorf("drop rule %s: filter too long", n)
	}
	if strings.TrimSpace(d.Query) == "" && strings.TrimSpace(d.Host) == "" && strings.TrimSpace(d.Program) == "" && len(d.Senders) == 0 && d.Severity == nil {
		return fmt.Errorf("drop rule %s: needs words, a host, a program, a sender or a severity (it would drop everything)", n)
	}
	if q := strings.TrimSpace(d.Query); q != "" {
		if err := (&LdQuery{Q: q, MinSev: -1}).Validate(); err != nil {
			return fmt.Errorf("drop rule %s: %v", n, err)
		}
	}
	if d.Severity != nil && (*d.Severity < 0 || *d.Severity > 7) {
		return fmt.Errorf("drop rule %s: severity must be 0 to 7", n)
	}
	switch d.SevMatch {
	case "", "worse", "exact", "less":
	default:
		return fmt.Errorf("drop rule %s: sev_match %q is not worse, exact or less", n, d.SevMatch)
	}
	if d.SevMatch != "" && d.Severity == nil {
		return fmt.Errorf("drop rule %s: sev_match needs a severity", n)
	}
	if len(d.Senders) > 200 {
		return fmt.Errorf("drop rule %s: at most 200 senders", n)
	}
	for _, s := range d.Senders {
		if err := ldCheckSenderWord(s); err != nil {
			return fmt.Errorf("drop rule %s: %v", n, err)
		}
	}
	return nil
}

func validateDropRules(ds []LogDropCfg) error {
	if len(ds) > ldDropMax {
		return fmt.Errorf("logs: at most %d drop rules", ldDropMax)
	}
	seen := map[string]bool{}
	for i := range ds {
		if err := ds[i].Validate(); err != nil {
			return fmt.Errorf("logs: %v", err)
		}
		k := strings.ToLower(strings.TrimSpace(ds[i].Name))
		if seen[k] {
			return fmt.Errorf("logs: drop rule name %q is used twice", ds[i].Name)
		}
		seen[k] = true
	}
	return nil
}

func cloneDrops(ds []LogDropCfg) []LogDropCfg {
	if ds == nil {
		return nil
	}
	out := make([]LogDropCfg, len(ds))
	for i, d := range ds {
		out[i] = d
		out[i].Senders = append([]string(nil), d.Senders...)
		if d.Severity != nil {
			v := *d.Severity
			out[i].Severity = &v
		}
	}
	return out
}
