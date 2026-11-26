package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
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

const (
	ldShipMax           = 32
	ldShipBatch         = 500     // messages in one send
	ldShipBatchBytes    = 4 << 20 // bytes in one send
	ldShipReadMax       = 20000   // lines read before what matched is sent
	ldShipDefaultBehind = 24      // hours
	ldShipPEN           = "32473" // the enterprise number of the logdir element (RFC 5612's example number; see the help)
	ldShipSpoofMax      = 1472    // a spoofed datagram is cut to fit one 1500-byte frame
	ldShipFromVersion   = 37      // the first version that knows settings.logs.destinations
)

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

func (d *LogDestCfg) protocol() string {
	if d.Protocol == "" {
		return "tcp"
	}
	return d.Protocol
}

func (d *LogDestCfg) httpType() bool {
	return d.Type == "splunk_hec" || d.Type == "elastic" || d.Type == "loki"
}

// address is the server's host:port.
func (d *LogDestCfg) address() (string, error) {
	s := strings.TrimSpace(d.Server)
	if s == "" {
		return "", errors.New("needs a server")
	}
	port := "514"
	if d.protocol() == "tls" {
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
func (d *LogDestCfg) endpoint() (string, error) {
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

func (d *LogDestCfg) tlsConfig(host string) (*tls.Config, error) {
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
func ldShipStart(s string, now time.Time) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "now":
		return now.UnixMilli(), nil
	case "oldest", "all":
		return 0, nil
	}
	d, err := parseSince(s)
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
		if p := d.protocol(); p != "udp" && p != "tcp" && p != "tls" {
			return fmt.Errorf("destination %s: protocol %q is not udp, tcp or tls", n, d.Protocol)
		}
	case "json_tcp":
		if p := d.protocol(); p != "tcp" && p != "tls" {
			return fmt.Errorf("destination %s: json_tcp is sent over tcp or tls", n)
		}
	case "splunk_hec", "elastic", "loki":
		if _, err := d.endpoint(); err != nil {
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
	if !d.httpType() {
		if _, err := d.address(); err != nil {
			return fmt.Errorf("destination %s: %v", n, err)
		}
		if d.URL != "" || d.Token != "" {
			return fmt.Errorf("destination %s: url and token are for splunk_hec, elastic and loki", n)
		}
	}
	if d.SpoofSource && (d.Type != "syslog" || d.protocol() != "udp") {
		return fmt.Errorf("destination %s: spoof_source works with syslog over udp only", n)
	}
	if strings.TrimSpace(d.CAPEM) != "" {
		if _, err := d.tlsConfig(""); err != nil {
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
	if _, err := ldShipStart(d.Start, time.Now()); err != nil {
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
	if !ldNameRE.MatchString(s) {
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

// logShippingNeedsNewNodes refuses a change to the destinations while a node of the cluster runs a version that does not know them: it
// would refuse the shared settings (an unknown key) or drop them.  Said now, with the node's name.
func logShippingNeedsNewNodes(cur, dc *DaemonConfig, peers []PeerView) error {
	var a, b []LogDestCfg
	if cur != nil && cur.Settings.Logs != nil {
		a = cur.Settings.Logs.Destinations
	}
	if dc != nil && dc.Settings.Logs != nil {
		b = dc.Settings.Logs.Destinations
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if len(b) == 0 || bytes.Equal(ja, jb) {
		return nil
	}
	for _, p := range peers {
		if p.Self || p.Version == "" {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(p.Version), "v")); err == nil && n < ldShipFromVersion {
			return fmt.Errorf("node %s runs version %d, which does not know log shipping: update it to version %d or later (Operate ▸ Upgrade), then add the destinations", firstNonEmpty(p.Hostname, p.Addr), n, ldShipFromVersion)
		}
	}
	return nil
}

// ── what is sent ─────────────────────────────────────────────────────────────

// ldShipItem is one message with what this node knows about its sender.
type ldShipItem struct {
	m    *LdMsg
	name string // the sender's name in Configure ▸ Senders
}

// LdShipJSON is a message as the JSON destinations get it.
type LdShipJSON struct {
	Time       string `json:"time"`                // when this node received it (RFC 3339, UTC, milliseconds)
	SentTime   string `json:"sent_time,omitempty"` // the sender's own time, when the message gave one
	Host       string `json:"host,omitempty"`
	Program    string `json:"program,omitempty"`
	PID        string `json:"pid,omitempty"`
	Facility   string `json:"facility"`
	Severity   string `json:"severity"`
	MsgID      string `json:"msgid,omitempty"`
	SD         string `json:"structured_data,omitempty"`
	Message    string `json:"message"`
	Source     string `json:"source,omitempty"`      // the address it came from
	SourceName string `json:"source_name,omitempty"` // that address's name in Configure ▸ Senders
	Via        string `json:"via,omitempty"`
	Node       string `json:"node,omitempty"`
	Raw        string `json:"raw,omitempty"`
}

func ldShipTime(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z") }

func ldShipRecord(it ldShipItem, raw bool) LdShipJSON {
	m := it.m
	r := LdShipJSON{Time: ldShipTime(m.T), Host: m.Host, Program: m.Prog, PID: m.PID, Facility: facName(m.Fac), Severity: sevName(m.Sev), MsgID: m.MID,
		SD: m.SD, Message: m.Msg, Source: m.Src, SourceName: it.name, Via: m.Via, Node: m.Node}
	if m.Sent > 0 {
		r.SentTime = ldShipTime(m.Sent)
	}
	if raw {
		r.Raw = m.Raw
	}
	return r
}

// ld5424Field is a header field: printable US-ASCII without spaces, at most n bytes, "-" when empty.
func ld5424Field(s string, n int) string {
	if s == "" {
		return "-"
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(b) < n; i++ {
		c := s[i]
		if c < 33 || c > 126 {
			c = '_'
		}
		b = append(b, c)
	}
	return string(b)
}

// ldSDValue escapes a PARAM-VALUE: \ " and ] get a backslash.
func ldSDValue(s string) string {
	if !strings.ContainsAny(s, `\"]`) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r == '\\' || r == '"' || r == ']' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ldValidSD says whether s is one or more SD-ELEMENTs (what a sender's structured data must be to be passed on as such).
func ldValidSD(s string) bool {
	if len(s) < 3 || s[0] != '[' {
		return false
	}
	in, esc := false, false
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case in && c == '\\':
			esc = true
		case c == '"':
			in = !in
		case !in && c == '[':
			if depth != 0 {
				return false
			}
			depth = 1
		case !in && c == ']':
			if depth != 1 {
				return false
			}
			depth = 0
		case !in && depth == 0:
			return false
		}
	}
	return depth == 0 && !in
}

// ld5424 is a message as RFC 5424, with its origin.
func ld5424(it ldShipItem, raw bool) []byte {
	m := it.m
	var b bytes.Buffer
	pri := int(m.Fac)*8 + int(m.Sev&7)
	if pri < 0 || pri > 191 {
		pri = 13
	}
	ts := m.T
	if m.Sent > 0 {
		ts = m.Sent
	}
	host := m.Host
	if host == "" {
		host = m.Src
	}
	fmt.Fprintf(&b, "<%d>1 %s %s %s %s %s ", pri, ldShipTime(ts), ld5424Field(host, 255), ld5424Field(m.Prog, 48), ld5424Field(m.PID, 128), ld5424Field(m.MID, 32))
	hasOrigin := strings.HasPrefix(m.SD, "[origin ") || strings.Contains(m.SD, "][origin ")
	sdOK := m.SD != "" && ldValidSD(m.SD)
	if m.Src != "" && !(sdOK && hasOrigin) { // a message may hold one origin element: the sender's own one stays
		fmt.Fprintf(&b, `[origin ip="%s"]`, ldSDValue(m.Src))
	}
	fmt.Fprintf(&b, `[logdir@%s rcvd="%s"`, ldShipPEN, ldShipTime(m.T))
	if m.Src != "" {
		fmt.Fprintf(&b, ` ip="%s"`, ldSDValue(m.Src))
	}
	if it.name != "" {
		fmt.Fprintf(&b, ` sender="%s"`, ldSDValue(it.name))
	}
	if m.Node != "" {
		fmt.Fprintf(&b, ` node="%s"`, ldSDValue(m.Node))
	}
	if m.Via != "" {
		fmt.Fprintf(&b, ` via="%s"`, ldSDValue(m.Via))
	}
	if m.Sent > 0 {
		fmt.Fprintf(&b, ` sent="%s"`, ldShipTime(m.Sent))
	}
	if raw && m.Raw != "" {
		fmt.Fprintf(&b, ` raw="%s"`, ldSDValue(m.Raw))
	}
	b.WriteByte(']')
	if sdOK {
		b.WriteString(m.SD)
	} else if m.SD != "" { // what the sender sent as structured data, when it is not well formed, goes in our element's place
		b.Truncate(b.Len() - 1)
		fmt.Fprintf(&b, ` sd="%s"]`, ldSDValue(m.SD))
	}
	if m.Msg != "" {
		b.WriteByte(' ')
		b.WriteString(strings.NewReplacer("\r", " ", "\n", " ").Replace(m.Msg))
	}
	return b.Bytes()
}

// ── the senders ─────────────────────────────────────────────────────────────

type ldShipSender interface {
	send(ctx context.Context, items []ldShipItem) error // all of them, or an error (then all are sent again)
	close()
}

func newShipSender(d LogDestCfg) (ldShipSender, error) {
	switch {
	case d.Type == "syslog" && d.protocol() == "udp" && d.SpoofSource:
		return newSpoofSender(d)
	case d.Type == "syslog" && d.protocol() == "udp":
		return &ldUDPSender{d: d}, nil
	case d.Type == "syslog" || d.Type == "json_tcp":
		return &ldStreamSender{d: d}, nil
	case d.httpType():
		ep, err := d.endpoint()
		if err != nil {
			return nil, err
		}
		u, _ := url.Parse(ep)
		tc, err := d.tlsConfig(u.Hostname())
		if err != nil {
			return nil, err
		}
		tr := &http.Transport{TLSClientConfig: tc, Proxy: nil, MaxIdleConnsPerHost: 2, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
			DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext}
		return &ldHTTPSender{d: d, ep: ep, cl: &http.Client{Transport: tr, Timeout: 60 * time.Second}}, nil
	}
	return nil, fmt.Errorf("type %q cannot be sent", d.Type)
}

// tcp and tls: syslog octet-counted (RFC 6587), json_tcp a line each.
type ldStreamSender struct {
	d    LogDestCfg
	conn net.Conn
}

func (s *ldStreamSender) dial(ctx context.Context) error {
	addr, err := s.d.address()
	if err != nil {
		return err
	}
	dl := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var c net.Conn
	if s.d.protocol() == "tls" {
		host, _, _ := net.SplitHostPort(addr)
		tc, err := s.d.tlsConfig(host)
		if err != nil {
			return err
		}
		td := &tls.Dialer{NetDialer: dl, Config: tc}
		c, err = td.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
	} else {
		if c, err = dl.DialContext(ctx, "tcp", addr); err != nil {
			return err
		}
	}
	s.conn = c
	return nil
}

func (s *ldStreamSender) send(ctx context.Context, items []ldShipItem) error {
	if s.conn == nil {
		if err := s.dial(ctx); err != nil {
			return err
		}
	}
	var b bytes.Buffer
	for _, it := range items {
		if s.d.Type == "json_tcp" {
			j, _ := json.Marshal(ldShipRecord(it, s.d.Raw))
			b.Write(j)
			b.WriteByte('\n')
		} else {
			msg := ld5424(it, s.d.Raw)
			b.WriteString(strconv.Itoa(len(msg)))
			b.WriteByte(' ')
			b.Write(msg)
		}
	}
	s.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := s.conn.Write(b.Bytes()); err != nil {
		s.close()
		return err
	}
	// a server that closed the connection is seen on the next write; a read that does not wait finds out now
	s.conn.SetReadDeadline(time.Now().Add(time.Millisecond))
	var one [1]byte
	if _, err := s.conn.Read(one[:]); err != nil && !isTimeout(err) {
		s.close()
		return fmt.Errorf("the server closed the connection: %v", err)
	}
	return nil
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func (s *ldStreamSender) close() {
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
}

// udp: one message to a datagram.
type ldUDPSender struct {
	d    LogDestCfg
	conn net.Conn
}

func (s *ldUDPSender) send(ctx context.Context, items []ldShipItem) error {
	if s.conn == nil {
		addr, err := s.d.address()
		if err != nil {
			return err
		}
		if s.conn, err = (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "udp", addr); err != nil {
			return err
		}
	}
	for _, it := range items {
		msg := ld5424(it, s.d.Raw)
		if len(msg) > 65000 {
			msg = msg[:65000]
		}
		if _, err := s.conn.Write(msg); err != nil && !strings.Contains(err.Error(), "connection refused") { // an ICMP answer to the last one
			s.close()
			return err
		}
	}
	return nil
}

func (s *ldUDPSender) close() {
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
}

// http: a batch to a POST.
type ldHTTPSender struct {
	d  LogDestCfg
	ep string
	cl *http.Client
}

func (s *ldHTTPSender) body(items []ldShipItem) ([]byte, string) {
	var b bytes.Buffer
	switch s.d.Type {
	case "splunk_hec":
		for _, it := range items {
			ev := map[string]any{"time": float64(it.m.T) / 1000, "host": firstNonEmpty(it.m.Host, it.m.Src), "source": "logdir:" + it.m.Node, "sourcetype": "syslog",
				"event": ldShipRecord(it, s.d.Raw)}
			if s.d.Index != "" {
				ev["index"] = s.d.Index
			}
			j, _ := json.Marshal(ev)
			b.Write(j)
			b.WriteByte('\n')
		}
		return b.Bytes(), "application/json"
	case "elastic":
		idx := firstNonEmpty(s.d.Index, "logdir")
		act, _ := json.Marshal(map[string]any{"create": map[string]string{"_index": idx}})
		for _, it := range items {
			doc := struct {
				TS string `json:"@timestamp"`
				LdShipJSON
			}{ldShipTime(it.m.T), ldShipRecord(it, s.d.Raw)}
			j, _ := json.Marshal(doc)
			b.Write(act)
			b.WriteByte('\n')
			b.Write(j)
			b.WriteByte('\n')
		}
		return b.Bytes(), "application/x-ndjson"
	}
	// loki: one stream for each set of labels, the line the JSON record
	type stream struct {
		Stream map[string]string `json:"stream"`
		Values [][2]string       `json:"values"`
	}
	by := map[string]*stream{}
	var order []string
	for _, it := range items {
		lb := map[string]string{"job": "logdir", "node": it.m.Node, "severity": sevName(it.m.Sev)}
		if it.m.Host != "" {
			lb["host"] = it.m.Host
		}
		if it.m.Src != "" {
			lb["source"] = it.m.Src
		}
		k := lb["node"] + "\x00" + lb["severity"] + "\x00" + lb["host"] + "\x00" + lb["source"]
		st := by[k]
		if st == nil {
			st = &stream{Stream: lb}
			by[k] = st
			order = append(order, k)
		}
		j, _ := json.Marshal(ldShipRecord(it, s.d.Raw))
		st.Values = append(st.Values, [2]string{strconv.FormatInt(it.m.T*1e6, 10), string(j)})
	}
	out := struct {
		Streams []*stream `json:"streams"`
	}{}
	for _, k := range order {
		out.Streams = append(out.Streams, by[k])
	}
	j, _ := json.Marshal(out)
	return j, "application/json"
}

func (s *ldHTTPSender) send(ctx context.Context, items []ldShipItem) error {
	body, ct := s.body(items)
	req, err := http.NewRequestWithContext(ctx, "POST", s.ep, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("User-Agent", "logdir/"+version())
	if t := strings.TrimSpace(s.d.Token); t != "" {
		switch s.d.Type {
		case "splunk_hec":
			req.Header.Set("Authorization", "Splunk "+t)
		case "elastic":
			req.Header.Set("Authorization", "ApiKey "+t)
		default:
			req.Header.Set("Authorization", "Bearer "+t)
		}
	}
	resp, err := s.cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: %s", resp.Status, ldShort(string(rb)))
	}
	if s.d.Type == "elastic" { // the bulk API answers 200 and says which documents failed
		var r struct {
			Errors bool `json:"errors"`
			Items  []map[string]struct {
				Status int `json:"status"`
				Error  any `json:"error"`
			} `json:"items"`
		}
		if json.Unmarshal(rb, &r) == nil && r.Errors {
			for _, it := range r.Items {
				for _, x := range it {
					if x.Error != nil {
						e, _ := json.Marshal(x.Error)
						return fmt.Errorf("elastic refused a document (%d): %s", x.Status, ldShort(string(e)))
					}
				}
			}
			return errors.New("elastic refused a document")
		}
	}
	return nil
}

func (s *ldHTTPSender) close() { s.cl.CloseIdleConnections() }

func ldShort(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// ── the cursor ──────────────────────────────────────────────────────────────

// ldShipCursor is how far a node has sent to a destination: Line lines of hour Hour are done, and nothing before From (ms) is sent.
type ldShipCursor struct {
	Hour    int64  `json:"hour"`
	Line    int64  `json:"line"`
	From    int64  `json:"from,omitempty"`
	Sent    uint64 `json:"sent"`
	Skipped uint64 `json:"skipped,omitempty"`
	Last    int64  `json:"last,omitempty"` // when the last batch was accepted (Unix seconds)
}

func (s *ldStore) shipDir() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, ".ship")
}

func (s *ldStore) loadCursor(name string) (ldShipCursor, bool) {
	var c ldShipCursor
	d := s.shipDir()
	if d == "" {
		return c, false
	}
	b, err := os.ReadFile(filepath.Join(d, name+".json"))
	if err != nil || json.Unmarshal(b, &c) != nil {
		return ldShipCursor{}, false
	}
	return c, true
}

func (s *ldStore) saveCursor(name string, c ldShipCursor) {
	d := s.shipDir()
	if d == "" {
		return
	}
	if os.MkdirAll(d, 0o750) != nil {
		return
	}
	b, _ := json.Marshal(&c)
	p := filepath.Join(d, name+".json")
	if os.WriteFile(p+".tmp", b, 0o640) == nil {
		os.Rename(p+".tmp", p)
	}
}

// ldHourReader reads one hour's lines from a position on, and can be read again after the end while the hour is being written.
type ldHourReader struct {
	h       int64
	line    int64
	f       *os.File
	br      *bufio.Reader
	pending []byte // a line not yet ended by a newline (the writer is part way through it)
}

func (s *ldStore) openHour(h, skip int64) (*ldHourReader, error) {
	var f *os.File
	var r io.Reader
	for _, gz := range []bool{false, true} {
		fh, err := os.Open(s.path(h, gz))
		if err != nil {
			continue
		}
		f, r = fh, fh
		if gz {
			zr, err := ldLzReader(fh)
			if err != nil {
				fh.Close()
				return nil, err
			}
			r = zr
		}
		break
	}
	if f == nil {
		return nil, os.ErrNotExist
	}
	hr := &ldHourReader{h: h, f: f, br: bufio.NewReaderSize(r, 1<<20)}
	for hr.line < skip {
		l, ok := hr.next()
		if !ok {
			break
		}
		_ = l
	}
	return hr, nil
}

// next is the next whole line (false: none yet).
func (r *ldHourReader) next() ([]byte, bool) {
	for {
		b, err := r.br.ReadBytes('\n')
		if len(r.pending) > 0 {
			b = append(r.pending, b...)
			r.pending = nil
		}
		if err != nil {
			if len(b) > 0 {
				r.pending = append([]byte(nil), b...)
			}
			return nil, false
		}
		r.line++
		return b, true
	}
}

func (r *ldHourReader) close() {
	if r != nil && r.f != nil {
		r.f.Close()
	}
}

// ── the worker ──────────────────────────────────────────────────────────────

// LdShipStatus is one destination on one node.
type LdShipStatus struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Target    string `json:"target"`
	Node      string `json:"node"`
	Addr      string `json:"addr,omitempty"` // the node's cluster address: two nodes can have the same host name
	State     string `json:"state"`          // ok, behind, error, disabled, starting
	Sent      uint64 `json:"sent"`
	PerMin    uint64 `json:"per_min"`
	Skipped   uint64 `json:"skipped"`
	BehindS   int64  `json:"behind_s"`
	LastSent  int64  `json:"last_sent,omitempty"` // unix seconds
	LastError string `json:"last_error,omitempty"`
	ErrorAt   int64  `json:"error_at,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
	Note      string `json:"note,omitempty"`
}

func (d *LogDestCfg) target() string {
	if d.httpType() {
		ep, err := d.endpoint()
		if err != nil {
			return d.URL
		}
		if u, err := url.Parse(ep); err == nil {
			u.RawQuery, u.User = "", nil
			return u.String()
		}
		return ep
	}
	a, err := d.address()
	if err != nil {
		a = d.Server
	}
	return d.protocol() + "://" + a
}

type ldShipWorker struct {
	svc    *ldService
	d      LogDestCfg
	key    string // the destination as JSON: a change starts the worker again
	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	cur      ldShipCursor
	lastT    int64 // the time of the last line read (ms)
	caughtUp bool
	lastSent time.Time
	lastErr  string
	errAt    time.Time
	started  bool
	note     string
	minute   []ldShipTick
	reset    *ldShipCursor // asked for by --logs-ship-reset; taken up by the loop
}

type ldShipTick struct {
	at time.Time
	n  uint64
}

func (w *ldShipWorker) status(now time.Time) LdShipStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := LdShipStatus{Name: w.d.Name, Type: w.d.Type, Target: w.d.target(), Node: selfHost(), Sent: w.cur.Sent, Skipped: w.cur.Skipped, LastError: w.lastErr, Note: w.note,
		Cursor: fmt.Sprintf("%s line %d", ldHourName(w.cur.Hour), w.cur.Line)}
	if w.d.Type == "syslog" {
		st.Type = "syslog/" + w.d.protocol()
	}
	for _, t := range w.minute {
		if now.Sub(t.at) < time.Minute {
			st.PerMin += t.n
		}
	}
	if !w.lastSent.IsZero() {
		st.LastSent = w.lastSent.Unix()
	} else if w.cur.Last > 0 { // from before a restart
		st.LastSent = w.cur.Last
	}
	if !w.errAt.IsZero() {
		st.ErrorAt = w.errAt.Unix()
	}
	if !w.caughtUp && w.lastT > 0 {
		st.BehindS = int64(now.Sub(time.UnixMilli(w.lastT)) / time.Second)
	}
	switch {
	case w.d.Disabled:
		st.State, st.BehindS = "disabled", 0
	case !w.started:
		st.State = "starting"
	case w.lastErr != "":
		st.State = "error"
	case st.BehindS > 120:
		st.State = "behind"
	default:
		st.State = "ok"
	}
	return st
}

func (w *ldShipWorker) noteErr(err error) {
	w.mu.Lock()
	if err == nil {
		w.lastErr = ""
	} else {
		w.lastErr, w.errAt = err.Error(), time.Now()
	}
	w.mu.Unlock()
}

// ldShipFilter is a destination's filter (or a drop rule's) as this node runs it (sender names looked up again every half minute).
type ldShipFilter struct {
	q      LdQuery // what f was made from (each goroutine that matches makes its own: a filter keeps scratch buffers)
	f      *ldFilter
	nets   []netip.Prefix
	addrs  map[string]bool
	names  []string
	at     time.Time
	none   bool // senders were named and none of them is known here
	hasSrc bool
}

func (w *ldShipWorker) filter(prev *ldShipFilter) *ldShipFilter {
	if prev != nil && time.Since(prev.at) < 30*time.Second {
		return prev
	}
	d := w.d
	return w.svc.selection(nil, d.Query, d.Host, d.Program, d.MinSev, d.SevExact, false, d.Senders)
}

// ldNames keeps the sender names looked up while several filters are compiled together (nil: none kept).
type ldNames struct {
	words  map[string]string
	byName map[string]map[string]bool
}

func (s *ldService) wordsOf(nc *ldNames) map[string]string {
	if nc == nil {
		return s.senderWords()
	}
	if nc.words == nil {
		nc.words = s.senderWords()
	}
	return nc.words
}

func (s *ldService) byNameOf(nc *ldNames, pat string) map[string]bool {
	if nc == nil {
		return s.sendersByName(pat)
	}
	if nc.byName == nil {
		nc.byName = map[string]map[string]bool{}
	}
	k := strings.ToLower(strings.TrimSpace(pat))
	if m, ok := nc.byName[k]; ok {
		return m
	}
	m := s.sendersByName(pat)
	nc.byName[k] = m
	return m
}

// selection compiles a filter of the Logs page with a list of senders (names are looked up now: this can take a moment).
func (s *ldService) selection(nc *ldNames, query, host, prog string, minSev *int, sevExact, sevLess bool, senders []string) *ldShipFilter {
	q := LdQuery{Q: query, Host: host, Prog: prog, MinSev: -1, SevExact: sevExact, SevLess: sevLess}
	if minSev != nil {
		q.MinSev = *minSev
	}
	if q.Q != "" {
		q.srcWords = s.wordsOf(nc)
	}
	if q.Host != "" {
		q.hostSrc = s.byNameOf(nc, q.Host)
	}
	sf := &ldShipFilter{at: time.Now(), addrs: map[string]bool{}}
	for _, x := range senders {
		x = strings.TrimSpace(x)
		if p, err := netip.ParsePrefix(x); err == nil {
			sf.nets = append(sf.nets, p.Masked())
		} else if a, err := netip.ParseAddr(x); err == nil {
			sf.addrs[a.Unmap().String()] = true
		} else if x != "" {
			for a := range s.byNameOf(nc, x) {
				sf.addrs[a] = true
			}
			sf.names = append(sf.names, strings.ToLower(x))
		}
		sf.hasSrc = true
	}
	sf.q, sf.f = q, q.filter()
	return sf
}

func (sf *ldShipFilter) match(m *LdMsg) bool { return sf.matchWith(sf.f, m) }

// matchWith is match with another copy of the filter (one per goroutine).
func (sf *ldShipFilter) matchWith(f *ldFilter, m *LdMsg) bool {
	if sf.hasSrc && !sf.addrs[m.Src] {
		ok := false
		if a, err := netip.ParseAddr(m.Src); err == nil {
			for _, p := range sf.nets {
				if p.Contains(a.Unmap()) {
					ok = true
					break
				}
			}
		}
		if !ok {
			return false
		}
	}
	return f.match(m)
}

// run sends until stopped.
func (w *ldShipWorker) run(ctx context.Context) {
	defer close(w.done)
	st := w.svc.store
	d := w.d
	if d.Disabled { // the cursor stays where it is; it is shown, and a reset changes the file
		c, _ := st.loadCursor(d.Name)
		w.mu.Lock()
		w.cur, w.started = c, true
		w.mu.Unlock()
		<-ctx.Done()
		return
	}
	cur, ok := st.loadCursor(d.Name)
	if !ok {
		from, _ := ldShipStart(d.Start, st.now())
		cur = ldShipCursor{Hour: from / 1000 / 3600, From: from}
		if from == 0 {
			cur.Hour = st.Oldest() / 1000 / 3600
		}
		st.saveCursor(d.Name, cur)
	}
	w.mu.Lock()
	w.cur, w.started = cur, true
	w.mu.Unlock()
	var snd ldShipSender
	defer func() {
		if snd != nil {
			snd.close()
		}
	}()
	var rd *ldHourReader
	defer func() { rd.close() }()
	var flt *ldShipFilter
	backoff := time.Second
	saved := time.Now()
	sleep := func(dur time.Duration) bool {
		t := time.NewTimer(dur)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			return true
		}
	}
	defer func() { st.saveCursor(d.Name, w.snapshot()) }()
	for ctx.Err() == nil {
		// a reset asked for from the GUI or the command line
		w.mu.Lock()
		if w.reset != nil {
			c := *w.reset
			c.Sent, c.Skipped, c.Last = w.cur.Sent, w.cur.Skipped, w.cur.Last
			w.cur, w.reset, w.caughtUp, w.lastT = c, nil, false, 0
			w.mu.Unlock()
			rd.close()
			rd = nil
			st.saveCursor(d.Name, c)
		} else {
			w.mu.Unlock()
		}
		cur := w.snapshot()
		now := st.now()
		nowH := now.Unix() / 3600
		// too far behind: skip to the limit, counting what is skipped
		maxB := int64(d.MaxBehindHours)
		if maxB == 0 {
			maxB = ldShipDefaultBehind
		}
		if lim := nowH - maxB; cur.Hour < lim {
			var n uint64
			st.mu.Lock()
			for h, m := range st.meta {
				if h >= cur.Hour && h < lim {
					n += m.Count
				}
			}
			st.mu.Unlock()
			if n >= uint64(cur.Line) {
				n -= uint64(cur.Line)
			}
			w.mu.Lock()
			w.cur.Hour, w.cur.Line, w.cur.Skipped = lim, 0, w.cur.Skipped+n
			w.note = fmt.Sprintf("skipped %d message(s): more than %d hour(s) behind", n, maxB)
			w.mu.Unlock()
			rd.close()
			rd = nil
			continue
		}
		if rd == nil || rd.h != cur.Hour || rd.line != cur.Line {
			rd.close()
			rd = nil
			r, err := st.openHour(cur.Hour, cur.Line)
			if err != nil {
				if st.closedHour(cur.Hour) { // no such hour (nothing was received, or it was removed): the next one
					w.advanceHour(cur.Hour, nowH)
					continue
				}
				w.mu.Lock()
				w.caughtUp = true
				w.mu.Unlock()
				if !sleep(time.Second) {
					return
				}
				continue
			}
			rd = r
			if rd.line < cur.Line { // the hour holds fewer lines than the cursor says: it was written again; from its end
				w.mu.Lock()
				w.cur.Line = rd.line
				w.mu.Unlock()
			}
		}
		flt = w.filter(flt)
		var items []ldShipItem
		var lines int64
		var lastT int64
		size := 0
		limit := ldShipBatch
		if d.RateLimit > 0 && d.RateLimit < limit {
			limit = d.RateLimit
		}
		eof := false
		for len(items) < limit && size < ldShipBatchBytes && lines < ldShipReadMax {
			l, ok := rd.next()
			if !ok {
				eof = true
				break
			}
			lines++
			if len(l) <= 1 || !flt.f.rawOK(l) {
				continue
			}
			var m LdMsg
			if json.Unmarshal(l, &m) != nil {
				continue
			}
			lastT = m.T
			if m.T < cur.From || !flt.match(&m) {
				continue
			}
			mm := m
			var name string
			if a, err := netip.ParseAddr(m.Src); err == nil {
				name = w.svc.senderName(a)
			}
			items = append(items, ldShipItem{m: &mm, name: name})
			size += len(l)
		}
		if len(items) > 0 {
			if snd == nil {
				s, err := newShipSender(d)
				if err != nil {
					w.noteErr(err)
					if !sleep(30 * time.Second) {
						return
					}
					continue
				}
				snd = s
			}
			t0 := time.Now()
			sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
			err := snd.send(sctx, items)
			cancel()
			if err != nil {
				w.noteErr(err)
				rd.close() // the same lines are read and sent again
				rd = nil
				if !sleep(backoff) {
					return
				}
				backoff = min(backoff*2, 30*time.Second)
				continue
			}
			backoff = time.Second
			w.mu.Lock()
			w.lastErr = ""
			w.cur.Sent += uint64(len(items))
			w.lastSent = time.Now()
			w.cur.Last = w.lastSent.Unix()
			w.minute = append(w.minute, ldShipTick{time.Now(), uint64(len(items))})
			for len(w.minute) > 0 && time.Since(w.minute[0].at) > time.Minute {
				w.minute = w.minute[1:]
			}
			w.mu.Unlock()
			if d.RateLimit > 0 { // a rate: this batch takes len/rate seconds
				want := time.Duration(float64(len(items)) / float64(d.RateLimit) * float64(time.Second))
				if el := time.Since(t0); el < want && !sleep(want-el) {
					w.commit(lines, lastT, eof)
					return
				}
			}
		}
		w.commit(lines, lastT, eof)
		if time.Since(saved) > time.Second {
			st.saveCursor(d.Name, w.snapshot())
			saved = time.Now()
		}
		if eof {
			if st.closedHour(cur.Hour) && len(rd.pending) == 0 {
				rd.close()
				rd = nil
				w.advanceHour(cur.Hour, nowH)
				continue
			}
			w.mu.Lock()
			w.caughtUp = true
			w.mu.Unlock()
			if !sleep(time.Second) {
				return
			}
		}
	}
}

func (w *ldShipWorker) snapshot() ldShipCursor {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cur
}

func (w *ldShipWorker) commit(lines, lastT int64, eof bool) {
	w.mu.Lock()
	w.cur.Line += lines
	if lastT > 0 {
		w.lastT = lastT
	}
	if !eof {
		w.caughtUp = false
	}
	w.mu.Unlock()
}

// advanceHour moves the cursor to the first hour after h that holds messages (or to the hour being written).
func (w *ldShipWorker) advanceHour(h, nowH int64) {
	st := w.svc.store
	next := nowH
	st.mu.Lock()
	for x := range st.meta {
		if x > h && x < next {
			next = x
		}
	}
	st.mu.Unlock()
	if next <= h {
		next = h + 1
	}
	w.mu.Lock()
	w.cur.Hour, w.cur.Line = next, 0
	w.mu.Unlock()
}

// ── the set of workers ──────────────────────────────────────────────────────

type ldShipper struct {
	mu      sync.Mutex
	workers map[string]*ldShipWorker
}

// reconcileShipping starts, stops and restarts the workers to match the settings (every few seconds).
func (s *ldService) reconcileShipping(ctx context.Context) {
	cfg := s.config()
	keep := s.storeRaw(cfg)
	s.keepRaw.Store(keep)
	want := map[string]LogDestCfg{}
	if cfg.on() && s.store.dir != "" && s.store.dirErr == "" {
		for _, d := range cfg.Destinations {
			want[d.Name] = d
		}
	}
	s.ship.mu.Lock()
	if s.ship.workers == nil {
		s.ship.workers = map[string]*ldShipWorker{}
	}
	var stop []*ldShipWorker
	for name, w := range s.ship.workers {
		d, ok := want[name]
		k, _ := json.Marshal(d)
		if !ok || string(k) != w.key {
			stop = append(stop, w)
			delete(s.ship.workers, name)
		}
	}
	for name, d := range want {
		if _, ok := s.ship.workers[name]; ok {
			continue
		}
		k, _ := json.Marshal(d)
		wctx, cancel := context.WithCancel(ctx)
		w := &ldShipWorker{svc: s, d: d, key: string(k), cancel: cancel, done: make(chan struct{})}
		s.ship.workers[name] = w
		go w.run(wctx)
	}
	s.ship.mu.Unlock()
	for _, w := range stop {
		w.cancel()
		<-w.done
	}
}

// storeRaw: whether the bytes a sender sent are kept with each message (some destination sends them).
func (s *ldService) storeRaw(cfg LogsConfig) bool {
	for _, d := range cfg.Destinations {
		if !d.Disabled && (d.Raw || d.SpoofSource) {
			return true
		}
	}
	return false
}

func (s *ldService) stopShipping() {
	s.ship.mu.Lock()
	ws := s.ship.workers
	s.ship.workers = nil
	s.ship.mu.Unlock()
	for _, w := range ws {
		w.cancel()
		<-w.done
	}
}

// ShipStatus is every destination on this node.
func (s *ldService) ShipStatus() []LdShipStatus {
	now := time.Now()
	s.ship.mu.Lock()
	out := make([]LdShipStatus, 0, len(s.ship.workers))
	for _, w := range s.ship.workers {
		out = append(out, w.status(now))
	}
	s.ship.mu.Unlock()
	addr := s.selfAddr()
	for i := range out {
		out[i].Addr = addr
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ldShipOp is a test or a reset, asked of every node (Node: only that one).
type ldShipOp struct {
	Name string      `json:"name"`
	Node string      `json:"node,omitempty"`
	From string      `json:"from,omitempty"` // reset: now, oldest or a time back
	Dest *LogDestCfg `json:"dest,omitempty"` // test: a destination not saved yet (this node only)
}

// LdShipResult is what one node did.
type LdShipResult struct {
	Node  string `json:"node"`
	Addr  string `json:"addr,omitempty"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Note  string `json:"note,omitempty"`
}

func (s *ldService) destination(name string) (LogDestCfg, bool) {
	for _, d := range s.config().Destinations {
		if strings.EqualFold(d.Name, name) {
			return d, true
		}
	}
	return LogDestCfg{}, false
}

// selfAddr is this node's cluster address ("" without a cluster).
func (s *ldService) selfAddr() string {
	if s.mg == nil || s.mg.cl == nil || !s.mg.cl.Enabled() {
		return ""
	}
	return s.mg.cl.node.Snapshot().SelfAddr
}

// isNode: does node ("" for every node) name this node, by host name or by cluster address?
func (s *ldService) isNode(node string) bool {
	return node == "" || strings.EqualFold(node, selfHost()) || (node == s.selfAddr() && node != "")
}

// ShipTest sends one test message to the destination from this node.
func (s *ldService) ShipTest(op ldShipOp) *LdShipResult {
	r := &LdShipResult{Node: selfHost(), Addr: s.selfAddr()}
	if !s.isNode(op.Node) {
		return nil
	}
	var d LogDestCfg
	if op.Dest != nil {
		d = *op.Dest
		if err := d.Validate(); err != nil {
			r.Error = err.Error()
			return r
		}
	} else {
		var ok bool
		if d, ok = s.destination(op.Name); !ok {
			r.Error = fmt.Sprintf("no destination %q", op.Name)
			return r
		}
	}
	snd, err := newShipSender(d)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer snd.close()
	now := time.Now()
	m := &LdMsg{T: now.UnixMilli(), Host: selfHost(), Prog: "logdir", Fac: 1, Sev: 6, Node: selfHost(), Via: "test",
		Msg: fmt.Sprintf("logdir shipping test from %s to %s", selfHost(), d.Name)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := snd.send(ctx, []ldShipItem{{m: m}}); err != nil {
		r.Error = err.Error()
		return r
	}
	r.OK = true
	if d.SpoofSource {
		r.Note = "a test message has no sender, so it is sent from this node's own address"
	}
	return r
}

// ShipReset moves this node's cursor of a destination.
func (s *ldService) ShipReset(op ldShipOp) *LdShipResult {
	r := &LdShipResult{Node: selfHost(), Addr: s.selfAddr()}
	if !s.isNode(op.Node) {
		return nil
	}
	from, err := ldShipStart(op.From, s.store.now())
	if err != nil {
		r.Error = err.Error()
		return r
	}
	c := ldShipCursor{Hour: from / 1000 / 3600, From: from}
	if from == 0 {
		c.Hour = s.store.Oldest() / 1000 / 3600
	}
	s.ship.mu.Lock()
	var w *ldShipWorker
	for name, x := range s.ship.workers {
		if strings.EqualFold(name, op.Name) {
			w = x
		}
	}
	s.ship.mu.Unlock()
	if w == nil || w.d.Disabled {
		if _, ok := s.destination(op.Name); !ok {
			r.Error = fmt.Sprintf("no destination %q", op.Name)
			return r
		}
		old, _ := s.store.loadCursor(op.Name) // not running here (disabled): the cursor file is changed
		c.Sent, c.Skipped, c.Last = old.Sent, old.Skipped, old.Last
		s.store.saveCursor(op.Name, c)
		if w != nil {
			w.mu.Lock()
			w.cur = c
			w.mu.Unlock()
		}
		r.OK = true
		return r
	}
	w.mu.Lock()
	w.reset = &c
	w.note = ""
	w.mu.Unlock()
	r.OK = true
	return r
}

// ldRawText is what a sender sent, as text that can be stored (the line ends cut, cut at max_line, invalid UTF-8 replaced).
func ldRawText(b []byte, maxLine int) string {
	s := strings.TrimRight(string(b), "\r\n\x00")
	if maxLine > 0 && len(s) > maxLine {
		s = s[:maxLine]
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

// AllShipStatus is every destination on every node.
func (s *ldService) AllShipStatus(ctx context.Context) ([]LdShipStatus, []NodeError, int) {
	reps, errs, n := s.fan(ctx, ldQueryMsg{Op: "shipstatus", Query: LdQuery{From: time.Now().UnixMilli()}})
	out := []LdShipStatus{}
	for _, r := range reps {
		out = append(out, r.Ship...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Node < out[j].Node
	})
	return out, errs, n
}

// ShipAll runs a test or a reset on every node (op.Node: that node only).  A destination not saved yet (op.Dest) is tried from this
// node only.
func (s *ldService) ShipAll(ctx context.Context, kind string, op ldShipOp) ([]LdShipResult, []NodeError) {
	if op.Dest != nil {
		op.Node = ""
		return []LdShipResult{*s.ShipTest(op)}, nil
	}
	reps, errs, _ := s.fan(ctx, ldQueryMsg{Op: kind, Query: LdQuery{From: time.Now().UnixMilli()}, Ship: &op})
	out := []LdShipResult{}
	for _, r := range reps {
		if r.ShipRes != nil {
			out = append(out, *r.ShipRes)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out, errs
}
