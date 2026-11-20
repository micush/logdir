package main

import (
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// LdMsg is one syslog message as it is kept.  T is when this node received it (milliseconds since 1970): clocks of senders are
// not trusted, and receive time is what every node can sort by.
type LdMsg struct {
	T    int64  `json:"t"`
	Host string `json:"h,omitempty"`  // the host the message says it comes from (the sender's address when it names none)
	Prog string `json:"p,omitempty"`  // program / tag / app-name
	PID  string `json:"i,omitempty"`  // process id
	Fac  int8   `json:"f"`            // facility 0..23
	Sev  int8   `json:"s"`            // severity 0 (emergency) .. 7 (debug)
	Msg  string `json:"m"`            // the text
	Src  string `json:"a,omitempty"`  // the address it came from
	Via  string `json:"v,omitempty"`  // udp, tcp or tls
	Node string `json:"n,omitempty"`  // the node that received it
	MID  string `json:"d,omitempty"`  // RFC 5424 MSGID
	SD   string `json:"sd,omitempty"` // RFC 5424 structured data, as sent
	Sent int64  `json:"o,omitempty"`  // the sender's own timestamp when it could be read (milliseconds)
}

var sevNames = [8]string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}
var facNames = [24]string{"kern", "user", "mail", "daemon", "auth", "syslog", "lpr", "news", "uucp", "cron", "authpriv", "ftp",
	"ntp", "security", "console", "solaris-cron", "local0", "local1", "local2", "local3", "local4", "local5", "local6", "local7"}

func sevName(s int8) string {
	if s < 0 || int(s) >= len(sevNames) {
		return "?"
	}
	return sevNames[s]
}

func facName(f int8) string {
	if f < 0 || int(f) >= len(facNames) {
		return "?"
	}
	return facNames[f]
}

// parseSevName reads "err", "error", "3", "warn" ...; -1 when it is none.
func parseSevName(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	if n, err := strconv.Atoi(s); err == nil && n >= 0 && n <= 7 {
		return n
	}
	switch s {
	case "emergency", "panic":
		return 0
	case "error":
		return 3
	case "warn":
		return 4
	}
	for i, n := range sevNames {
		if s == n {
			return i
		}
	}
	return -1
}

var months3 = map[string]time.Month{"Jan": 1, "Feb": 2, "Mar": 3, "Apr": 4, "May": 5, "Jun": 6, "Jul": 7, "Aug": 8, "Sep": 9, "Oct": 10, "Nov": 11, "Dec": 12}

// parseSyslog turns a datagram or a framed line into a message: RFC 5424, RFC 3164 (BSD) and what devices send in its place
// (no priority, ISO timestamps, Cisco sequence numbers and "*Mar  1 18:48:50.483 UTC:" stamps, PAN-OS CSV bodies).  It never fails:
// text it cannot read is kept whole, as the message.
func parseSyslog(b []byte, src netip.Addr, via string, now time.Time, maxLine int) LdMsg {
	m := LdMsg{T: now.UnixMilli(), Fac: 1, Sev: 5, Src: src.String(), Via: via}
	s := strings.TrimRight(string(b), "\r\n\x00 ")
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if maxLine > 0 && len(s) > maxLine {
		s = strings.ToValidUTF8(s[:maxLine], "")
	}
	if len(s) > 2 && s[0] == '<' {
		if i := strings.IndexByte(s, '>'); i > 1 && i <= 4 {
			if pri, err := strconv.Atoi(s[1:i]); err == nil && pri >= 0 && pri < 192 {
				m.Fac, m.Sev = int8(pri/8), int8(pri%8)
				s = s[i+1:]
			}
		}
	}
	if len(s) > 2 && s[0] >= '1' && s[0] <= '9' && s[1] == ' ' && parse5424(&m, s[2:], now) {
		return m
	}
	parse3164(&m, s, now)
	return m
}

// parse5424 reads TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA [MSG]; false when it is not that.
func parse5424(m *LdMsg, s string, now time.Time) bool {
	f := make([]string, 0, 5)
	rest := s
	for len(f) < 5 {
		i := strings.IndexByte(rest, ' ')
		if i <= 0 {
			return false
		}
		f = append(f, rest[:i])
		rest = rest[i+1:]
	}
	ts := time.Time{}
	if f[0] != "-" {
		t, err := time.Parse(time.RFC3339Nano, f[0])
		if err != nil {
			return false
		}
		ts = t
	}
	sd := ""
	switch {
	case strings.HasPrefix(rest, "-"):
		rest = strings.TrimPrefix(strings.TrimPrefix(rest, "-"), " ")
	case strings.HasPrefix(rest, "["):
		i, esc := 0, false
		depth := 0
		end := -1
		for i = 0; i < len(rest); i++ {
			c := rest[i]
			switch {
			case esc:
				esc = false
			case c == '\\' && depth > 0:
				esc = true
			case c == '[':
				depth++
			case c == ']':
				depth--
				if depth == 0 && (i+1 >= len(rest) || rest[i+1] != '[') {
					end = i + 1
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return false
		}
		sd, rest = rest[:end], strings.TrimPrefix(rest[end:], " ")
	default:
		return false
	}
	nilDash := func(v string) string {
		if v == "-" {
			return ""
		}
		return v
	}
	m.Host, m.Prog, m.PID, m.MID, m.SD = nilDash(f[1]), nilDash(f[2]), nilDash(f[3]), nilDash(f[4]), sd
	if !ts.IsZero() {
		m.Sent = ts.UnixMilli()
	}
	m.Msg = strings.TrimPrefix(rest, "\xef\xbb\xbf") // a UTF-8 byte order mark
	if m.Host == "" {
		m.Host = m.Src
	}
	return true
}

// parse3164 reads [timestamp] [host] [tag[pid]:] message, as far as the text allows.
func parse3164(m *LdMsg, s string, now time.Time) {
	s = strings.TrimLeft(s, " ")
	// Cisco: "000123: " sequence numbers, and "*" or "." before a stamp that may be unsynchronised
	if i := strings.Index(s, ": "); i > 0 && i <= 10 && allDigits(s[:i]) {
		s = s[i+2:]
	}
	s = strings.TrimLeft(s, "*.")
	rest := s
	if t, r, ok := stamp3164(s, now); ok {
		m.Sent, rest = t.UnixMilli(), strings.TrimPrefix(r, " ")
		rest = strings.TrimPrefix(rest, ": ") // Cisco puts a colon after the stamp
		rest = strings.TrimLeft(rest, " ")
	}
	host := ""
	if i := strings.IndexByte(rest, ' '); i > 0 {
		w := rest[:i]
		after := rest[i+1:]
		// a host name has no colon (an IPv6 address has, but never ends the word with one), a tag ends with one
		if !strings.HasSuffix(w, ":") && !strings.ContainsAny(w, "[]") && (looksLikeHost(w) && m.Sent != 0 || isAddr(w)) {
			host, rest = w, after
		}
	}
	m.Host = host
	if i := strings.Index(rest, ": "); i > 0 && i <= 64 && !strings.ContainsAny(rest[:i], " \t") {
		tag := rest[:i]
		if j := strings.IndexByte(tag, '['); j > 0 && strings.HasSuffix(tag, "]") {
			m.Prog, m.PID = tag[:j], tag[j+1:len(tag)-1]
		} else {
			m.Prog = tag
		}
		rest = rest[i+2:]
	} else if strings.HasPrefix(rest, "%") { // Cisco "%LINK-3-UPDOWN: ..."
		if i := strings.Index(rest, ": "); i > 0 && i <= 64 {
			m.Prog, rest = rest[:i], rest[i+2:]
		}
	}
	m.Msg = rest
	if m.Host == "" {
		m.Host = m.Src
	}
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isAddr(s string) bool { _, err := netip.ParseAddr(s); return err == nil }

func looksLikeHost(w string) bool {
	if w == "" || len(w) > 255 {
		return false
	}
	for i := 0; i < len(w); i++ {
		c := w[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// stamp3164 reads "Nov 20 10:00:00", "Nov  2 10:00:00.123", "2026-11-20T10:00:00Z" or "2026-11-20 10:00:00" at the start of s.
func stamp3164(s string, now time.Time) (time.Time, string, bool) {
	if len(s) >= 15 && s[3] == ' ' {
		if mo, ok := months3[s[:3]]; ok && s[6] == ' ' && s[9] == ':' && s[12] == ':' {
			day, e1 := strconv.Atoi(strings.TrimSpace(s[4:6]))
			hh, e2 := strconv.Atoi(s[7:9])
			mi, e3 := strconv.Atoi(s[10:12])
			ss, e4 := strconv.Atoi(s[13:15])
			if e1 == nil && e2 == nil && e3 == nil && e4 == nil {
				rest := s[15:]
				ns := 0
				if strings.HasPrefix(rest, ".") { // fractions, then maybe a zone name (Cisco)
					j := 1
					for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
						j++
					}
					if f, err := strconv.Atoi((rest[1:j] + "000000000")[:9]); err == nil {
						ns = f
					}
					rest = rest[j:]
				}
				if r2 := strings.TrimLeft(rest, " "); r2 != rest || strings.HasPrefix(rest, ":") {
					if i := strings.IndexByte(r2, ':'); i > 0 && i <= 5 && allUpper(r2[:i]) { // " UTC:"
						rest = r2[i:]
					}
				}
				y := now.Year()
				t := time.Date(y, mo, day, hh, mi, ss, ns, now.Location())
				if t.After(now.Add(36 * time.Hour)) {
					t = t.AddDate(-1, 0, 0)
				}
				return t, rest, true
			}
		}
	}
	if len(s) >= 19 && s[4] == '-' && s[7] == '-' && (s[10] == 'T' || s[10] == ' ') {
		i := strings.IndexByte(s, ' ')
		if s[10] == ' ' {
			if j := strings.IndexByte(s[11:], ' '); j >= 0 {
				i = 11 + j
			} else {
				i = len(s)
			}
		}
		if i < 0 {
			i = len(s)
		}
		w := strings.Replace(s[:i], " ", "T", 1)
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
			if t, err := time.ParseInLocation(layout, w, now.Location()); err == nil {
				return t, s[i:], true
			}
		}
	}
	return time.Time{}, s, false
}

func allUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return s != ""
}

// text is what a search looks in: the host, the program and the message.
func (m *LdMsg) text() string { return m.Host + " " + m.Prog + " " + m.Msg }
