package core

import (
	"strconv"
	"strings"
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
	Raw  string `json:"r,omitempty"`  // the bytes the sender sent, kept only while a shipping destination sends them (ld_ship.go)
}

var SevNames = [8]string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}

// parseSevName reads "err", "error", "3", "warn" ...; -1 when it is none.
func ParseSevName(s string) int {
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
	for i, n := range SevNames {
		if s == n {
			return i
		}
	}
	return -1
}

// text is what a search looks in: the host, the program and the message.
func (m *LdMsg) text() string { return m.Host + " " + m.Prog + " " + m.Msg }
