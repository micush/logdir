package main

import (
	"fmt"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
)

// settings.logs: the syslog collector.  Shared by every node (the senders, the ports, the retention and the pattern alerts); where
// the collector listens besides the gateways (log_listen) and where the segments are kept (log_dir) are per node.

const (
	defaultSyslogUDP   = 514
	defaultSyslogTCP   = 514
	defaultSyslogTLS   = 6514
	defaultLogMaxLine  = 8192
	defaultLogDirPct   = 90
	maxLogAlertWindow  = 24 * 60
	maxLogAlertsConfig = 100
)

type LogsConfig struct {
	Enabled bool `json:"enabled"`
	// Ports: 0 = the default (514 for udp and tcp, 6514 for tls); -1 = not listened on.
	UDPPort int `json:"udp_port,omitempty"`
	TCPPort int `json:"tcp_port,omitempty"`
	TLSPort int `json:"tls_port,omitempty"`
	// Senders are the hosts accepted (everything else is dropped before a byte is parsed): a name and an address.  The address is
	// an IPv4 or IPv6 address, a network (10.9.0.0/16) or a host name that DNS resolves.
	Senders []LogSenderCfg `json:"senders,omitempty"`
	// DirMaxPercent: the log folder may use this percentage (0 = the default, 90) of the volume it is on.  At the cap the oldest
	// segments go first.  Each node applies it to its own volume.
	DirMaxPercent int `json:"dir_max_percent,omitempty"`
	// MemMaxPercent: the closed hours kept in memory for fast searches may use this percentage (0 = the default, 70) of the machine's
	// memory (the container's limit when smaller).  At the cap the oldest hours go first and are read from their files again.
	MemMaxPercent int `json:"mem_max_percent,omitempty"`
	// RetentionDays: segments older than this are deleted (0 = keep until the disk cap removes them).
	RetentionDays int `json:"retention_days,omitempty"`
	// ListLimit is how many of the newest matching messages the Logs page lists (0 = the default, 1000; 100 to 5000).
	ListLimit int `json:"list_limit,omitempty"`
	// Follow makes the Logs page follow the newest messages as they arrive, every couple of seconds, instead of reloading every 15.
	Follow bool `json:"follow,omitempty"`
	// MaxLine is the longest message kept (bytes; the rest is cut off).  0 = 8192.
	MaxLine int `json:"max_line,omitempty"`
	// Alerts are the pattern alerts: when a search matches at least Count messages in the last WindowMin minutes (on all nodes
	// together), an alert opens; it closes when the count falls under.
	Alerts []LogAlertCfg `json:"alerts,omitempty"`
	// Saved are the Logs page's saved searches (ld_saved.go), shared by every user.
	Saved []LogSavedCfg `json:"saved,omitempty"`
	// Destinations are where each node sends on what it received (Configure ▸ Shipping, ld_ship.go).
	Destinations []LogDestCfg `json:"destinations,omitempty"`
	// DropRules: messages that match one are not kept (ld_drop.go).
	DropRules []LogDropCfg `json:"drop_rules,omitempty"`
}

type LogSenderCfg struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Disabled bool   `json:"disabled,omitempty"` // kept in the list, but its messages are refused
}

type LogAlertCfg struct {
	Name      string `json:"name"`
	Query     string `json:"query"`                  // the search language of the Logs page: words, and, or, not / !word
	Host      string `json:"host,omitempty"`         // only this sender host
	MinSev    *int   `json:"min_severity,omitempty"` // 0 (emergency) to 7 (debug): this severe or worse; absent = any
	Count     int    `json:"count"`                  // matches that open the alert (default 1)
	WindowMin int    `json:"window_min"`             // minutes (default 5)
	Severity  string `json:"severity,omitempty"`     // of the alert: warning (default) or critical
	Disabled  bool   `json:"disabled,omitempty"`
}

func (c *LogsConfig) on() bool { return c != nil && (c.Enabled || len(c.Senders) > 0) }

func (c *LogsConfig) clone() *LogsConfig {
	if c == nil {
		return nil
	}
	d := *c
	d.Senders = append([]LogSenderCfg(nil), c.Senders...)
	d.Alerts = append([]LogAlertCfg(nil), c.Alerts...)
	d.Saved = append([]LogSavedCfg(nil), c.Saved...)
	d.Destinations = cloneDests(c.Destinations)
	d.DropRules = cloneDrops(c.DropRules)
	return &d
}

func (c *LogsConfig) ports() (udp, tcp, tls int) {
	pick := func(v, def int) int {
		if v == 0 {
			return def
		}
		if v < 0 {
			return 0
		}
		return v
	}
	if c == nil {
		return defaultSyslogUDP, defaultSyslogTCP, defaultSyslogTLS
	}
	return pick(c.UDPPort, defaultSyslogUDP), pick(c.TCPPort, defaultSyslogTCP), pick(c.TLSPort, defaultSyslogTLS)
}

func (c *LogsConfig) maxLine() int {
	if c == nil || c.MaxLine <= 0 {
		return defaultLogMaxLine
	}
	return c.MaxLine
}

var hostNameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,251}[A-Za-z0-9])?$`)

// checkSenderAddr accepts an address, a network or a host name.
func checkSenderAddr(a string) error {
	if _, err := netip.ParsePrefix(a); err == nil {
		return nil
	}
	if _, err := netip.ParseAddr(a); err == nil {
		return nil
	}
	if hostNameRe.MatchString(a) {
		return nil
	}
	return fmt.Errorf("%q is not an address, a network or a host name", a)
}

func (c *LogsConfig) Validate() error {
	if c == nil {
		return nil
	}
	for _, p := range []int{c.UDPPort, c.TCPPort, c.TLSPort} {
		if p < -1 || p > 65535 {
			return fmt.Errorf("logs: port %d is not a port (1 to 65535; -1 = off)", p)
		}
	}
	if c.DirMaxPercent < 0 || c.DirMaxPercent > 100 {
		return fmt.Errorf("logs: dir_max_percent %d must be 0 to 100", c.DirMaxPercent)
	}
	if c.MemMaxPercent < 0 || c.MemMaxPercent > 100 {
		return fmt.Errorf("logs: mem_max_percent %d must be 0 to 100", c.MemMaxPercent)
	}
	if c.RetentionDays < 0 || c.RetentionDays > 36500 {
		return fmt.Errorf("logs: retention_days %d must be 0 to 36500", c.RetentionDays)
	}
	if c.ListLimit != 0 && (c.ListLimit < 100 || c.ListLimit > ldMaxLimit) {
		return fmt.Errorf("logs: list_limit %d must be 100 to %d (0 = 1000)", c.ListLimit, ldMaxLimit)
	}
	if c.MaxLine < 0 || (c.MaxLine > 0 && (c.MaxLine < 256 || c.MaxLine > 1<<20)) {
		return fmt.Errorf("logs: max_line %d must be 256 to 1048576", c.MaxLine)
	}
	seenN, seenA := map[string]bool{}, map[string]bool{}
	for _, s := range c.Senders {
		n := strings.TrimSpace(s.Name)
		if n == "" || len(n) > 64 || strings.ContainsAny(n, " \t,=()!&|") {
			return fmt.Errorf("logs: sender name %q must be 1 to 64 characters without spaces or , = ( ) ! & |", s.Name)
		}
		if err := checkSenderAddr(strings.TrimSpace(s.Address)); err != nil {
			return fmt.Errorf("logs: sender %s: %v", n, err)
		}
		if seenN[strings.ToLower(n)] {
			return fmt.Errorf("logs: sender name %q is used twice", n)
		}
		if a := strings.ToLower(strings.TrimSpace(s.Address)); seenA[a] {
			return fmt.Errorf("logs: sender address %q is listed twice", s.Address)
		} else {
			seenA[a] = true
		}
		seenN[strings.ToLower(n)] = true
	}
	if len(c.Alerts) > maxLogAlertsConfig {
		return fmt.Errorf("logs: at most %d pattern alerts", maxLogAlertsConfig)
	}
	seenN = map[string]bool{}
	for _, a := range c.Alerts {
		n := strings.TrimSpace(a.Name)
		if n == "" || len(n) > 64 {
			return fmt.Errorf("logs: an alert needs a name of 1 to 64 characters")
		}
		if seenN[strings.ToLower(n)] {
			return fmt.Errorf("logs: alert name %q is used twice", n)
		}
		seenN[strings.ToLower(n)] = true
		if strings.TrimSpace(a.Query) == "" && a.Host == "" && a.MinSev == nil {
			return fmt.Errorf("logs: alert %s needs a query, a host or a severity", n)
		}
		if a.Count < 0 || a.Count > 1000000 {
			return fmt.Errorf("logs: alert %s: count %d must be 1 to 1000000", n, a.Count)
		}
		if a.WindowMin < 0 || a.WindowMin > maxLogAlertWindow {
			return fmt.Errorf("logs: alert %s: window_min %d must be 1 to %d", n, a.WindowMin, maxLogAlertWindow)
		}
		if a.MinSev != nil && (*a.MinSev < 0 || *a.MinSev > 7) {
			return fmt.Errorf("logs: alert %s: min_severity must be 0 to 7", n)
		}
	}
	if err := validateDropRules(c.DropRules); err != nil {
		return err
	}
	if err := validateSaved(c.Saved); err != nil {
		return err
	}
	return validateDestinations(c.Destinations)
}

func validLogDir(d string) error {
	if d == "" {
		return nil
	}
	if strings.ContainsRune(d, 0) || !filepath.IsAbs(d) || filepath.Clean(d) == "/" {
		return fmt.Errorf("log_dir: %q must be an absolute path to a folder (not /)", d)
	}
	return nil
}
