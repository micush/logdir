package main

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// Fields: a message's body read as name=value pairs, when the search asks for them (field=value in a search, the Logs page's
// field list, the top values of a field).  Nothing is stored: the fields are read from the text each time, so every message
// already kept has them, and a new format here applies to old messages too.
//
// What is read, in this order (the first that fits; the structured data and the Cisco mnemonic are read as well):
//   - CEF (`CEF:0|vendor|product|version|signature|name|severity|ext`): the header as cef_*, the extension's key=value pairs
//   - PAN-OS CSV (TRAFFIC and THREAT logs: the fields by position; other types: serial, type and subtype)
//   - a JSON object (the message, or what follows the first "{"), flattened with dots (a.b.c), arrays left out
//   - key=value pairs, key="quoted value" or key='quoted' (FortiGate, Linux audit, Juniper, most appliances)
//   - RFC 5424 structured data: each parameter, by its name
//   - a Cisco mnemonic (%ASA-6-302013:, %LINK-3-UPDOWN:): cisco_facility, cisco_severity, cisco_mnemonic, and for the ASA's
//     deny messages (106001, 106006, 106007, 106015, 106023, 106100) the addresses, ports and interfaces.
//
// Names are lower case.  Common names get an alias too, so one search finds them across vendors: src_ip, dst_ip, src_port,
// dst_port, action, proto, user (src and dst become src_ip and dst_ip only when they hold an address, as iptables and CEF write them).  ldFieldsVersion changes whenever what is read changes (the word index records which one it
// was made with).

const (
	ldFieldsVersion = 1
	ldFieldsMax     = 80  // fields read from one message
	ldFieldKeyMax   = 64  // characters of a name
	ldFieldValMax   = 512 // bytes of a value
)

// ldFieldsMadeUp says whether a message's (lower-case) text may give fields whose values are not copied from it: the ASA's deny
// messages get action=deny from "denied".  Any reader that makes a value up must be named here, or a search for it would miss.
func ldFieldsMadeUp(text string) bool {
	return strings.Contains(text, "%asa-") || strings.Contains(text, "%ftd-") || strings.Contains(text, "%pix-")
}

// ldKV is one field: Key lower case, Val as the message has it.
type ldKV struct {
	Key string `json:"k"`
	Val string `json:"v"`
}

var ldFieldAlias = map[string]string{
	"srcip": "src_ip", "source_ip": "src_ip", "sourceip": "src_ip", "src_addr": "src_ip", "saddr": "src_ip", "source-address": "src_ip", "source_address": "src_ip", "client_ip": "src_ip",
	"dstip": "dst_ip", "destination_ip": "dst_ip", "destinationip": "dst_ip", "dst_addr": "dst_ip", "daddr": "dst_ip", "destination-address": "dst_ip", "destination_address": "dst_ip",
	"srcport": "src_port", "sport": "src_port", "spt": "src_port", "source_port": "src_port", "source-port": "src_port",
	"dstport": "dst_port", "dport": "dst_port", "dpt": "dst_port", "destination_port": "dst_port", "destination-port": "dst_port",
	"act": "action", "protocol": "proto", "suser": "user", "username": "user", "usrname": "user", "user_name": "user",
}

type ldFieldSet struct {
	kv  []ldKV
	buf [16]ldKV // the first fields, without an allocation
}

// has says whether key k is already there: a look along the list, which for the few fields of a message is quicker than a map
// made for every message (at most ldFieldsMax and their aliases).
func (fs *ldFieldSet) has(k string) bool {
	for i := range fs.kv {
		if fs.kv[i].Key == k {
			return true
		}
	}
	return false
}

func (fs *ldFieldSet) add(k, v string) {
	if len(fs.kv) >= ldFieldsMax || k == "" {
		return
	}
	k = strings.ToLower(k)
	if len(k) > ldFieldKeyMax {
		return
	}
	if len(v) > ldFieldValMax {
		v = v[:ldFieldValMax]
	}
	if fs.kv == nil {
		fs.kv = fs.buf[:0]
	}
	if fs.has(k) { // the first one counts (a JSON key and a key=value inside its text: the JSON's)
		return
	}
	fs.kv = append(fs.kv, ldKV{k, v})
	a := ldFieldAlias[k]
	if (k == "src" || k == "dst") && ldLooksIP(v) { // iptables SRC= DST=, CEF src= dst=: only when it is an address
		a = k + "_ip"
	}
	if a != "" && !fs.has(a) {
		fs.kv = append(fs.kv, ldKV{a, v})
	}
}

// ldFields reads a message's fields.
func ldFields(m *LdMsg) []ldKV {
	var fs ldFieldSet
	msg := m.Msg
	ldCiscoFields(&fs, m.Prog, msg)
	switch {
	case ldCEFFields(&fs, msg):
	case ldPANFields(&fs, msg):
	case ldJSONFields(&fs, msg):
	default:
		ldKVFields(&fs, msg)
	}
	if m.SD != "" {
		ldSDFields(&fs, m.SD)
	}
	return fs.kv
}

// ── key=value ────────────────────────────────────────────────────────────────

func ldKeyByte(c byte) bool {
	return c == '_' || c == '.' || c == '-' || c == ':' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

// ldKVFields reads key=value pairs: a key starts a word (or follows a , ; ( [ { |), a value runs to the next space, or is
// quoted ("…" or '…', with \" inside).
func ldKVFields(fs *ldFieldSet, s string) {
	for i := 0; i < len(s); {
		// the start of a key
		if i > 0 && !strings.ContainsRune(" \t,;([{|", rune(s[i-1])) {
			i++
			continue
		}
		j := i
		for j < len(s) && ldKeyByte(s[j]) {
			j++
		}
		if j == i || j >= len(s) || s[j] != '=' || !ldKeyStart(s[i]) {
			i = max(j, i+1)
			continue
		}
		key := ldTrimRight(s[i:j], ":.-")
		j++ // past =
		var val string
		if j < len(s) && (s[j] == '"' || s[j] == '\'') {
			q := s[j]
			k := j + 1
			var b strings.Builder
			for k < len(s) && s[k] != q {
				if s[k] == '\\' && k+1 < len(s) && (s[k+1] == q || s[k+1] == '\\') {
					k++
				}
				b.WriteByte(s[k])
				k++
			}
			val, j = b.String(), min(k+1, len(s))
		} else {
			k := j
			for k < len(s) && s[k] != ' ' && s[k] != '\t' {
				// a , or ; followed by another key=: the pair ends there (a=1,b=2)
				if (s[k] == ',' || s[k] == ';') && ldKeyAt(s, k+1) {
					break
				}
				k++
			}
			val, j = ldTrimRight(s[j:k], ",;"), k
		}
		if key != "" {
			fs.add(key, val)
		}
		i = j
	}
}

// ldTrimRight is strings.TrimRight for a few ASCII bytes, without the set that call builds each time (it ran for every key of
// every message counted).
func ldTrimRight(s, cut string) string {
	for len(s) > 0 && strings.IndexByte(cut, s[len(s)-1]) >= 0 {
		s = s[:len(s)-1]
	}
	return s
}

func ldKeyStart(c byte) bool { return c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') }

// ldKeyAt: does a key= start at s[i]?
func ldKeyAt(s string, i int) bool {
	if i >= len(s) || !ldKeyStart(s[i]) {
		return false
	}
	j := i
	for j < len(s) && ldKeyByte(s[j]) {
		j++
	}
	return j < len(s) && s[j] == '='
}

// ── JSON ─────────────────────────────────────────────────────────────────────

func ldJSONFields(fs *ldFieldSet, s string) bool {
	i := strings.IndexByte(s, '{')
	if i < 0 || i > 64 || !strings.HasSuffix(strings.TrimSpace(s), "}") {
		return false
	}
	d := json.NewDecoder(strings.NewReader(s[i:]))
	d.UseNumber()
	var v map[string]any
	if d.Decode(&v) != nil {
		return false
	}
	ldFlatten(fs, "", v, 0)
	return true
}

func ldFlatten(fs *ldFieldSet, prefix string, v map[string]any, depth int) {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		name := k
		if prefix != "" {
			name = prefix + "." + k
		}
		switch x := v[k].(type) {
		case string:
			fs.add(name, x)
		case json.Number:
			fs.add(name, x.String())
		case bool:
			fs.add(name, strconv.FormatBool(x))
		case map[string]any:
			if depth < 3 {
				ldFlatten(fs, name, x, depth+1)
			}
		}
	}
}

// ── CEF ──────────────────────────────────────────────────────────────────────

var ldCEFHead = []string{"cef_version", "cef_vendor", "cef_product", "cef_device_version", "cef_signature", "cef_name", "cef_severity"}

func ldCEFFields(fs *ldFieldSet, s string) bool {
	i := strings.Index(s, "CEF:")
	if i < 0 || i > 64 {
		return false
	}
	s = s[i+4:]
	// seven header fields separated by |, where \| and \\ are escapes
	var parts []string
	var b strings.Builder
	j := 0
	for ; j < len(s) && len(parts) < 7; j++ {
		c := s[j]
		switch {
		case c == '\\' && j+1 < len(s) && (s[j+1] == '|' || s[j+1] == '\\'):
			j++
			b.WriteByte(s[j])
		case c == '|':
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	if len(parts) < 7 {
		return false
	}
	for k, p := range parts {
		fs.add(ldCEFHead[k], p)
	}
	ldCEFExt(fs, s[j:])
	return true
}

// ldCEFExt reads a CEF extension: key=value where a value may hold spaces and runs to the next " key="; \= \\ \n are escapes.
func ldCEFExt(fs *ldFieldSet, s string) {
	type kpos struct{ ks, ke int } // the key's start and the = after it
	var keys []kpos
	for i := 0; i < len(s); i++ {
		if (i == 0 || s[i-1] == ' ') && ldKeyStart(s[i]) {
			j := i
			for j < len(s) && (ldKeyByte(s[j]) && s[j] != ':') {
				j++
			}
			if j < len(s) && s[j] == '=' && (j == 0 || s[j-1] != '\\') {
				keys = append(keys, kpos{i, j})
				i = j
			}
		}
	}
	for n, k := range keys {
		end := len(s)
		if n+1 < len(keys) {
			end = keys[n+1].ks
		}
		v := strings.TrimRight(s[k.ke+1:end], " ")
		v = strings.NewReplacer(`\=`, "=", `\\`, `\`, `\n`, "\n", `\r`, "\r").Replace(v)
		fs.add(s[k.ks:k.ke], v)
	}
}

// ── PAN-OS CSV ───────────────────────────────────────────────────────────────

// the fields of TRAFFIC and THREAT logs, by position (PAN-OS syslog field descriptions)
var ldPANCommon = map[int]string{2: "serial", 3: "type", 4: "subtype", 6: "generated_time", 7: "src_ip", 8: "dst_ip", 9: "nat_src_ip", 10: "nat_dst_ip",
	11: "rule", 12: "src_user", 13: "dst_user", 14: "app", 15: "vsys", 16: "src_zone", 17: "dst_zone", 18: "in_if", 19: "out_if",
	20: "log_action", 22: "session_id", 23: "repeat_count", 24: "src_port", 25: "dst_port", 26: "nat_src_port", 27: "nat_dst_port",
	28: "flags", 29: "proto", 30: "action"}
var ldPANTraffic = map[int]string{31: "bytes", 32: "bytes_sent", 33: "bytes_received", 34: "packets"}
var ldPANThreat = map[int]string{31: "url", 32: "threat", 33: "category", 34: "threat_severity"}

func ldPANFields(fs *ldFieldSet, s string) bool {
	// a PAN-OS log starts with a number and the receive time: 1,2026/11/26 10:00:00,0123456789,TRAFFIC,…
	i := strings.IndexByte(s, ',')
	if i < 1 || i > 12 || len(s) < i+21 || s[i+5] != '/' || s[i+8] != '/' {
		return false
	}
	f := ldCSV(s, 120)
	if len(f) < 6 {
		return false
	}
	typ := strings.ToUpper(f[3])
	switch typ {
	case "TRAFFIC", "THREAT":
		if len(f) < 31 {
			return false
		}
	case "SYSTEM", "CONFIG", "HIPMATCH", "GLOBALPROTECT", "USERID", "DECRYPTION", "AUTHENTICATION", "TUNNEL", "CORRELATION", "GTP", "SCTP", "IPTAG":
		fs.add("receive_time", f[1])
		fs.add("serial", f[2])
		fs.add("type", f[3])
		fs.add("subtype", f[4])
		return true
	default:
		return false
	}
	fs.add("receive_time", f[1])
	for k := 2; k <= 30; k++ {
		if n := ldPANCommon[k]; n != "" {
			fs.add(n, f[k])
		}
	}
	more := ldPANTraffic
	if typ == "THREAT" {
		more = ldPANThreat
	}
	for k := 31; k <= 34 && k < len(f); k++ {
		fs.add(more[k], f[k])
	}
	return true
}

// ldCSV splits a line of comma-separated values, "quoted" ones with "" for a quote, up to max fields.
func ldCSV(s string, max int) []string {
	var out []string
	for len(out) < max {
		if len(s) > 0 && s[0] == '"' {
			var b strings.Builder
			k := 1
			for k < len(s) {
				if s[k] == '"' {
					if k+1 < len(s) && s[k+1] == '"' {
						b.WriteByte('"')
						k += 2
						continue
					}
					break
				}
				b.WriteByte(s[k])
				k++
			}
			out = append(out, b.String())
			s = s[min(k+1, len(s)):]
			if len(s) == 0 || s[0] != ',' {
				return out
			}
			s = s[1:]
			continue
		}
		j := strings.IndexByte(s, ',')
		if j < 0 {
			return append(out, s)
		}
		out = append(out, s[:j])
		s = s[j+1:]
	}
	return out
}

// ── RFC 5424 structured data ─────────────────────────────────────────────────

// ldSDFields reads [id name="value" …][…]: each parameter by its name (the element's id is left out).
func ldSDFields(fs *ldFieldSet, sd string) {
	for i := 0; i < len(sd); {
		if sd[i] != '[' {
			return
		}
		j := i + 1
		for j < len(sd) && sd[j] != ' ' && sd[j] != ']' {
			j++
		}
		for j < len(sd) && sd[j] == ' ' {
			k := j + 1
			for k < len(sd) && sd[k] != '=' && sd[k] != ']' {
				k++
			}
			if k+1 >= len(sd) || sd[k] != '=' || sd[k+1] != '"' {
				return
			}
			name := sd[j+1 : k]
			var b strings.Builder
			k += 2
			for k < len(sd) && sd[k] != '"' {
				if sd[k] == '\\' && k+1 < len(sd) {
					k++
				}
				b.WriteByte(sd[k])
				k++
			}
			fs.add(name, b.String())
			j = k + 1
		}
		if j >= len(sd) || sd[j] != ']' {
			return
		}
		i = j + 1
	}
}

// ── Cisco ────────────────────────────────────────────────────────────────────

// ldCiscoFields reads %FACILITY-SEVERITY-MNEMONIC: in the program or the first 120 bytes of the text, and the ASA's deny messages.
func ldCiscoFields(fs *ldFieldSet, prog, msg string) {
	fac, sev, mn, ok := ldCiscoMnemonic(prog)
	if !ok {
		head := msg
		if len(head) > 120 {
			head = head[:120]
		}
		for i := strings.IndexByte(head, '%'); i >= 0 && !ok; {
			fac, sev, mn, ok = ldCiscoMnemonic(head[i:])
			n := strings.IndexByte(head[i+1:], '%')
			if n < 0 {
				break
			}
			i += n + 1
		}
	}
	if !ok {
		return
	}
	fs.add("cisco_facility", fac)
	fs.add("cisco_severity", sev)
	fs.add("cisco_mnemonic", mn)
	if strings.EqualFold(fac, "ASA") || strings.EqualFold(fac, "FTD") || strings.EqualFold(fac, "PIX") {
		ldASADeny(fs, mn, msg)
	}
}

// ldCiscoMnemonic reads %FAC-SEV-MNEMONIC (the : after it is optional).
func ldCiscoMnemonic(s string) (fac, sev, mn string, ok bool) {
	s = strings.TrimPrefix(s, "%")
	a := strings.IndexByte(s, '-')
	if a < 1 || a+3 > len(s) || s[a+1] < '0' || s[a+1] > '7' || s[a+2] != '-' {
		return
	}
	for _, c := range []byte(s[:a]) {
		if !(c == '_' || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')) {
			return
		}
	}
	e := a + 3
	for e < len(s) && (s[e] == '_' || ('A' <= s[e] && s[e] <= 'Z') || ('0' <= s[e] && s[e] <= '9')) {
		e++
	}
	if e == a+3 || (e < len(s) && s[e] != ':' && s[e] != ' ') {
		return
	}
	return s[:a], s[a+1 : a+2], s[a+3 : e], true
}

// ldASADeny reads the addresses of the ASA's deny messages, "src IF:ADDR/PORT dst IF:ADDR/PORT" (106023, 106100) or
// "from ADDR/PORT to ADDR/PORT … on interface IF" (106001, 106006, 106007, 106015).
func ldASADeny(fs *ldFieldSet, mn, msg string) {
	switch mn {
	case "106023", "106100", "106001", "106006", "106007", "106015":
	default:
		return
	}
	w := strings.Fields(msg)
	fs.add("action", "deny")
	for i := 0; i+1 < len(w); i++ {
		switch strings.ToLower(w[i]) {
		case "deny", "denied":
			if p := strings.ToLower(w[i+1]); p == "tcp" || p == "udp" || p == "icmp" || strings.HasPrefix(p, "protocol") {
				fs.add("proto", p)
			}
		case "tcp", "udp", "icmp":
			if i+1 < len(w) && (strings.ToLower(w[i+1]) == "src" || strings.HasPrefix(strings.ToLower(w[i+1]), "access-list")) {
				fs.add("proto", strings.ToLower(w[i]))
			}
		case "src", "from":
			ldASAEnd(fs, "src", w[i+1])
		case "dst", "to":
			ldASAEnd(fs, "dst", w[i+1])
		case "interface":
			fs.add("interface", strings.Trim(w[i+1], ",."))
		}
	}
}

// ldASAEnd reads IF:ADDR/PORT, ADDR/PORT or IF:ADDR(…) into side_ip, side_port and side_if.
func ldASAEnd(fs *ldFieldSet, side, t string) {
	t = strings.TrimRight(t, ",.")
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = t[:i]
	}
	if i := strings.LastIndexByte(t, ':'); i >= 0 && strings.Count(t, ":") == 1 {
		fs.add(side+"_if", t[:i])
		t = t[i+1:]
	}
	port := ""
	if i := strings.LastIndexByte(t, '/'); i >= 0 {
		t, port = t[:i], t[i+1:]
	}
	if !ldLooksIP(t) {
		return
	}
	fs.add(side+"_ip", t)
	if port != "" {
		if _, err := strconv.Atoi(port); err == nil {
			fs.add(side+"_port", port)
		}
	}
}

func ldLooksIP(s string) bool {
	if s == "" {
		return false
	}
	dots := 0
	for _, c := range []byte(s) {
		switch {
		case c == '.':
			dots++
		case c == ':' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F'):
		default:
			return false
		}
	}
	return dots == 3 || strings.Contains(s, ":")
}

// ── matching ─────────────────────────────────────────────────────────────────

// ldFieldTerm is field=value (with * wildcards) or field>n, field<n, field>=n, field<=n in a search.
type ldFieldTerm struct {
	key string
	op  string // = > < >= <=
	val string // lower case
	num float64
	// lit: an = value with nothing a format writes differently (no " ' \ = | / ] and plain ASCII), so a message whose text (or
	// structured data) does not hold it cannot have it as a field (see ldFieldsMadeUp): the message is not read for fields at all.
	lit bool
}

// ldParseFieldTerm reads a search word as a field term: a name (letters, digits, _ . -; not starting with a digit), an operator and a
// value; > < >= <= need a number.
func ldParseFieldTerm(w string) (ldFieldTerm, bool) {
	if w == "" || !ldKeyStart(w[0]) {
		return ldFieldTerm{}, false
	}
	i := 0
	for i < len(w) && ldKeyByte(w[i]) && w[i] != ':' {
		i++
	}
	if i == 0 || i >= len(w) || i > ldFieldKeyMax {
		return ldFieldTerm{}, false
	}
	key, rest := w[:i], w[i:]
	for _, op := range []string{">=", "<=", "=", ">", "<"} {
		if strings.HasPrefix(rest, op) {
			v := rest[len(op):]
			if v == "" {
				return ldFieldTerm{}, false
			}
			t := ldFieldTerm{key: key, op: op, val: v}
			if op == "=" {
				t.lit = !strings.ContainsAny(v, "\"'\\=|/]")
				for i := 0; i < len(v); i++ {
					if v[i] >= 0x80 {
						t.lit = false
					}
				}
			}
			if op != "=" {
				n, err := strconv.ParseFloat(v, 64)
				if err != nil {
					return ldFieldTerm{}, false
				}
				t.num = n
			}
			return t, true
		}
	}
	return ldFieldTerm{}, false
}

func (t *ldFieldTerm) matches(kv []ldKV) bool {
	for _, f := range kv {
		if f.Key != t.key {
			continue
		}
		if t.op == "=" {
			if ldGlob(t.val, strings.ToLower(f.Val)) {
				return true
			}
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(f.Val), 64)
		if err != nil {
			continue
		}
		switch t.op {
		case ">":
			if n > t.num {
				return true
			}
		case "<":
			if n < t.num {
				return true
			}
		case ">=":
			if n >= t.num {
				return true
			}
		case "<=":
			if n <= t.num {
				return true
			}
		}
	}
	return false
}
