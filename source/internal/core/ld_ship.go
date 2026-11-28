package core

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Log shipping (Configure ▸ Shipping, settings.logs.destinations): every node sends what it received on to other systems, read from its
// own hour store, so a destination that is down misses nothing: each node keeps, per destination, how far it has sent (a cursor:
// the hour and the line in it) and carries on from there.  Delivery is at least once: a batch that fails is sent again.
//
// What a message carries is kept: the sender's address, its own time, host, program, PID, facility, severity, MSGID and structured
// data, and logdir's own (when it was received, by which node, over what, the sender's name in Configure ▸ Senders).
//
//   - syslog: RFC 5424 over udp, tcp or tls (octet-counted, RFC 6587), with the sender's address in the standard origin element
//     ([origin ip=...], RFC 5424 7.2) and logdir's in [logdir@32473 ...].  raw adds the bytes the sender sent, as raw="...".
//     spoof_source (udp only, IPv4) sends each message from the sender's own address, as the sender sent it (its own bytes when
//     they were kept), so the destination cannot tell logdir was in between.
//   - json_tcp: one JSON object to a line over tcp or tls.
//   - splunk_hec, elastic, loki: the same JSON object, posted in batches over HTTPS.
//
// The setting is shared by the cluster; the cursors are each node's own (in the log folder, .ship/<name>.json).

const ldShipMax = 32

// LogDestCfg is one destination.
type LogDestCfg struct {
	Name     string `json:"name"`
	Type     string `json:"type"`               // syslog, json_tcp, splunk_hec, elastic, loki
	Protocol string `json:"protocol,omitempty"` // syslog: tcp (default), udp or tls; json_tcp: tcp or tls
	Server   string `json:"server,omitempty"`   // syslog, json_tcp: host or host:port (514 for syslog over udp and tcp, 6514 over tls)
	URL      string `json:"url,omitempty"`      // splunk_hec, elastic, loki
	Token    string `json:"token,omitempty"`    // splunk_hec: the HEC token; elastic: an API key; loki: a bearer token
	Index    string `json:"index,omitempty"`    // elastic: the index or data stream (logdir); splunk_hec: the index (the token's default)
	CAPEM    string `json:"ca_pem,omitempty"`   // tls and https: the certificate authority to trust (PEM; empty: the system's)
	// what is sent: the Logs page's filters (all of them must match; none: everything)
	Query    string   `json:"query,omitempty"`
	Host     string   `json:"host,omitempty"`
	Program  string   `json:"program,omitempty"`
	MinSev   *int     `json:"min_severity,omitempty"`
	SevExact bool     `json:"sev_exact,omitempty"`
	Senders  []string `json:"senders,omitempty"` // names (Configure ▸ Senders, DNS, host; * wildcards), addresses or networks
	// where a new destination starts: now (default), a time back (1h, 24h, 7d) or oldest
	Start          string `json:"start,omitempty"`
	MaxBehindHours int    `json:"max_behind_hours,omitempty"` // further behind than this (default 24), the oldest are skipped
	Raw            bool   `json:"raw,omitempty"`              // also send the bytes the sender sent (kept from now on)
	SpoofSource    bool   `json:"spoof_source,omitempty"`     // syslog over udp: send from the sender's address
	RateLimit      int    `json:"rate_limit,omitempty"`       // messages a second (0: no limit)
	Disabled       bool   `json:"disabled,omitempty"`
}

var ldShipNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (d *LogDestCfg) ProtocolOrDefault() string {
	if d.Protocol == "" {
		return "tcp"
	}
	return d.Protocol
}

func (d *LogDestCfg) HttpType() bool {
	return d.Type == "splunk_hec" || d.Type == "elastic" || d.Type == "loki"
}

// address is the server's host:port.
func (d *LogDestCfg) Address() (string, error) {
	s := strings.TrimSpace(d.Server)
	if s == "" {
		return "", errors.New("needs a server")
	}
	port := "514"
	if d.ProtocolOrDefault() == "tls" {
		port = "6514"
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("server %q: %q is not a port", s, p)
		}
		if h == "" {
			return "", fmt.Errorf("server %q has no host", s)
		}
		return s, nil
	}
	if a, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
		return net.JoinHostPort(a.String(), port), nil
	}
	if !hostNameRe.MatchString(s) {
		return "", fmt.Errorf("server %q is not a host name or an address", s)
	}
	return net.JoinHostPort(s, port), nil
}

// endpoint is the URL a batch is posted to.
func (d *LogDestCfg) Endpoint() (string, error) {
	u, err := url.Parse(strings.TrimSpace(d.URL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("url %q: use https://host:port", d.URL)
	}
	p := strings.TrimRight(u.Path, "/")
	switch d.Type {
	case "splunk_hec":
		if !strings.HasSuffix(p, "/services/collector/event") {
			p += "/services/collector/event"
		}
	case "elastic":
		if !strings.HasSuffix(p, "/_bulk") {
			p += "/_bulk"
		}
	case "loki":
		if !strings.HasSuffix(p, "/loki/api/v1/push") {
			p += "/loki/api/v1/push"
		}
	}
	u.Path = p
	return u.String(), nil
}

func (d *LogDestCfg) TlsConfig(host string) (*tls.Config, error) {
	c := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if strings.TrimSpace(d.CAPEM) != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(d.CAPEM)) {
			return nil, errors.New("ca_pem holds no certificate")
		}
		c.RootCAs = pool
	}
	return c, nil
}

// startFrom reads where a cursor starts: now, a duration back, oldest (0).  The result is milliseconds.
func LdShipStart(s string, now time.Time) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "now":
		return now.UnixMilli(), nil
	case "oldest", "all":
		return 0, nil
	}
	d, err := ParseSince(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("start %q: use now, oldest or a time back such as 1h, 24h or 7d", s)
	}
	return now.Add(-d).UnixMilli(), nil
}

func (d *LogDestCfg) Validate() error {
	if !ldShipNameRe.MatchString(d.Name) {
		return fmt.Errorf("destination name %q: 1 to 64 letters, digits, . _ or - (starting with a letter or digit)", d.Name)
	}
	n := d.Name
	switch d.Type {
	case "syslog":
		if p := d.ProtocolOrDefault(); p != "udp" && p != "tcp" && p != "tls" {
			return fmt.Errorf("destination %s: protocol %q is not udp, tcp or tls", n, d.Protocol)
		}
	case "json_tcp":
		if p := d.ProtocolOrDefault(); p != "tcp" && p != "tls" {
			return fmt.Errorf("destination %s: json_tcp is sent over tcp or tls", n)
		}
	case "splunk_hec", "elastic", "loki":
		if _, err := d.Endpoint(); err != nil {
			return fmt.Errorf("destination %s: %v", n, err)
		}
		if d.Type == "splunk_hec" && strings.TrimSpace(d.Token) == "" {
			return fmt.Errorf("destination %s: splunk_hec needs the HEC token", n)
		}
		if d.Protocol != "" || d.Server != "" {
			return fmt.Errorf("destination %s: %s is sent to a url, not a server and protocol", n, d.Type)
		}
	default:
		return fmt.Errorf("destination %s: type %q is not syslog, json_tcp, splunk_hec, elastic or loki", n, d.Type)
	}
	if !d.HttpType() {
		if _, err := d.Address(); err != nil {
			return fmt.Errorf("destination %s: %v", n, err)
		}
		if d.URL != "" || d.Token != "" {
			return fmt.Errorf("destination %s: url and token are for splunk_hec, elastic and loki", n)
		}
	}
	if d.SpoofSource && (d.Type != "syslog" || d.ProtocolOrDefault() != "udp") {
		return fmt.Errorf("destination %s: spoof_source works with syslog over udp only", n)
	}
	if strings.TrimSpace(d.CAPEM) != "" {
		if _, err := d.TlsConfig(""); err != nil {
			return fmt.Errorf("destination %s: %v", n, err)
		}
	}
	if len(d.Index) > 200 || strings.ContainsAny(d.Index, " \"\\/,#*?<>|") {
		return fmt.Errorf("destination %s: index %q is not an index name", n, d.Index)
	}
	if len(d.Query) > 2000 || len(d.Host) > 255 || len(d.Program) > 255 {
		return fmt.Errorf("destination %s: filter too long", n)
	}
	if d.MinSev != nil && (*d.MinSev < 0 || *d.MinSev > 7) {
		return fmt.Errorf("destination %s: min_severity must be 0 to 7", n)
	}
	if d.SevExact && d.MinSev == nil {
		return fmt.Errorf("destination %s: sev_exact needs min_severity", n)
	}
	if len(d.Senders) > 200 {
		return fmt.Errorf("destination %s: at most 200 senders", n)
	}
	for _, s := range d.Senders {
		if err := ldCheckSenderWord(s); err != nil {
			return fmt.Errorf("destination %s: %v", n, err)
		}
	}
	if _, err := LdShipStart(d.Start, time.Now()); err != nil {
		return fmt.Errorf("destination %s: %v", n, err)
	}
	if d.MaxBehindHours < 0 || d.MaxBehindHours > 24*366 {
		return fmt.Errorf("destination %s: max_behind_hours must be 0 to %d", n, 24*366)
	}
	if d.RateLimit < 0 || d.RateLimit > 1000000 {
		return fmt.Errorf("destination %s: rate_limit must be 0 to 1000000", n)
	}
	return nil
}

// ldCheckSenderWord accepts what a filter may name a sender by: a name (* wildcards), an address or a network.
func ldCheckSenderWord(s string) error {
	s = strings.TrimSpace(s)
	if _, err := netip.ParsePrefix(s); err == nil {
		return nil
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return nil
	}
	if !LdNameRE.MatchString(s) {
		return fmt.Errorf("sender %q is not a name, an address or a network", s)
	}
	return nil
}

func validateDestinations(ds []LogDestCfg) error {
	if len(ds) > ldShipMax {
		return fmt.Errorf("logs: at most %d destinations", ldShipMax)
	}
	seen := map[string]bool{}
	for i := range ds {
		if err := ds[i].Validate(); err != nil {
			return fmt.Errorf("logs: %v", err)
		}
		k := strings.ToLower(ds[i].Name)
		if seen[k] {
			return fmt.Errorf("logs: destination name %q is used twice", ds[i].Name)
		}
		seen[k] = true
	}
	return nil
}

func cloneDests(ds []LogDestCfg) []LogDestCfg {
	if ds == nil {
		return nil
	}
	out := make([]LogDestCfg, len(ds))
	for i, d := range ds {
		out[i] = d
		out[i].Senders = append([]string(nil), d.Senders...)
		if d.MinSev != nil {
			v := *d.MinSev
			out[i].MinSev = &v
		}
	}
	return out
}

func (d *LogDestCfg) Target() string {
	if d.HttpType() {
		ep, err := d.Endpoint()
		if err != nil {
			return d.URL
		}
		if u, err := url.Parse(ep); err == nil {
			u.RawQuery, u.User = "", nil
			return u.String()
		}
		return ep
	}
	a, err := d.Address()
	if err != nil {
		a = d.Server
	}
	return d.ProtocolOrDefault() + "://" + a
}
