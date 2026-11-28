package logs

import (
	"strconv"
	"unicode/utf8"

	"logdir/internal/core"
)

// ldDecodeLine reads one stored line into m (false: not in the form json.Marshal writes; m is then undefined).  in keeps one copy
// of the short strings that repeat in every line (host, program, address, via, node) for the lines of one read; nil: none kept.
func ldDecodeLine(line []byte, m *core.LdMsg, in map[string]string) bool {
	n := len(line)
	for n > 0 && (line[n-1] == '\n' || line[n-1] == '\r') {
		n--
	}
	line = line[:n]
	if n < 2 || line[0] != '{' || line[n-1] != '}' {
		return false
	}
	*m = core.LdMsg{}
	if n == 2 {
		return true
	}
	var seen uint32
	var buf []byte
	i := 1
	for {
		// the key: plain letters, no escapes
		if line[i] != '"' {
			return false
		}
		j := i + 1
		for j < n && line[j] != '"' && line[j] != '\\' {
			j++
		}
		if j+1 >= n || line[j] != '"' || line[j+1] != ':' {
			return false
		}
		key := line[i+1 : j]
		i = j + 2
		var k uint
		var num *int64
		var str *string
		var t8 *int8
		intern := false
		switch string(key) {
		case "t":
			k, num = 0, &m.T
		case "h":
			k, str, intern = 1, &m.Host, true
		case "p":
			k, str, intern = 2, &m.Prog, true
		case "i":
			k, str = 3, &m.PID
		case "f":
			k, t8 = 4, &m.Fac
		case "s":
			k, t8 = 5, &m.Sev
		case "m":
			k, str = 6, &m.Msg
		case "a":
			k, str, intern = 7, &m.Src, true
		case "v":
			k, str, intern = 8, &m.Via, true
		case "n":
			k, str, intern = 9, &m.Node, true
		case "d":
			k, str = 10, &m.MID
		case "sd":
			k, str = 11, &m.SD
		case "o":
			k, num = 12, &m.Sent
		case "r":
			k, str = 13, &m.Raw
		default:
			return false
		}
		if seen&(1<<k) != 0 {
			return false
		}
		seen |= 1 << k
		if i >= n {
			return false
		}
		if str != nil {
			var s string
			var ok bool
			s, i, buf, ok = ldDecodeString(line, i, buf, intern, in)
			if !ok {
				return false
			}
			*str = s
		} else {
			// an integer: -?(0|[1-9][0-9]*), nothing more
			j := i
			if line[j] == '-' {
				j++
			}
			d := j
			for j < n && line[j] >= '0' && line[j] <= '9' {
				j++
			}
			if j == d || (line[d] == '0' && j > d+1) || j-i > 20 {
				return false
			}
			v, err := strconv.ParseInt(string(line[i:j]), 10, 64)
			if err != nil {
				return false
			}
			if t8 != nil {
				if v < -128 || v > 127 {
					return false
				}
				*t8 = int8(v)
			} else {
				*num = v
			}
			i = j
		}
		if i >= n {
			return false
		}
		if line[i] == '}' {
			return i == n-1
		}
		if line[i] != ',' || i+1 >= n {
			return false
		}
		i++
	}
}

// ldDecodeString reads the JSON string at line[i] (which must be '"'); it gives the string, the index after it and the buffer
// (kept for the next string).  Short repeated strings are kept once in in when intern is set.
func ldDecodeString(line []byte, i int, buf []byte, intern bool, in map[string]string) (string, int, []byte, bool) {
	n := len(line)
	if line[i] != '"' {
		return "", i, buf, false
	}
	i++
	start := i
	high := false
	for i < n && line[i] != '"' && line[i] != '\\' {
		c := line[i]
		if c < 0x20 {
			return "", i, buf, false
		}
		if c >= 0x80 {
			high = true
		}
		i++
	}
	if i >= n {
		return "", i, buf, false
	}
	if line[i] == '"' { // no escapes: the bytes as they are
		b := line[start:i]
		if high && !utf8.Valid(b) {
			return "", i, buf, false
		}
		i++
		if intern && in != nil && len(b) <= 64 {
			if s, ok := in[string(b)]; ok {
				return s, i, buf, true
			}
			s := string(b)
			if len(in) < 4096 {
				in[s] = s
			}
			return s, i, buf, true
		}
		return string(b), i, buf, true
	}
	// escapes: build it in buf
	buf = append(buf[:0], line[start:i]...)
	for {
		if i >= n {
			return "", i, buf, false
		}
		c := line[i]
		switch {
		case c == '"':
			if high && !utf8.Valid(buf) {
				return "", i, buf, false
			}
			return string(buf), i + 1, buf, true
		case c < 0x20:
			return "", i, buf, false
		case c != '\\':
			if c >= 0x80 {
				high = true
			}
			buf = append(buf, c)
			i++
			continue
		}
		if i+1 >= n {
			return "", i, buf, false
		}
		switch line[i+1] {
		case '"', '\\', '/':
			buf = append(buf, line[i+1])
		case 'b':
			buf = append(buf, '\b')
		case 'f':
			buf = append(buf, '\f')
		case 'n':
			buf = append(buf, '\n')
		case 'r':
			buf = append(buf, '\r')
		case 't':
			buf = append(buf, '\t')
		case 'u':
			r, ok := ldHex4(line, i+2)
			if !ok {
				return "", i, buf, false
			}
			i += 6
			if r >= 0xD800 && r < 0xE000 { // a surrogate: only a whole pair is read here
				if r >= 0xDC00 || i+6 > n || line[i] != '\\' || line[i+1] != 'u' {
					return "", i, buf, false
				}
				lo, ok := ldHex4(line, i+2)
				if !ok || lo < 0xDC00 || lo >= 0xE000 {
					return "", i, buf, false
				}
				r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
				i += 6
			}
			buf = utf8.AppendRune(buf, r)
			continue
		default:
			return "", i, buf, false
		}
		i += 2
	}
}

func ldHex4(b []byte, i int) (rune, bool) {
	if i+4 > len(b) {
		return 0, false
	}
	var r rune
	for _, c := range b[i : i+4] {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}
