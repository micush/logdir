package main

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"
)

// reverseNames is the names an address reverse-looks-up to.  Go's resolver goes through the C library (nsswitch, systemd-resolved, mDNS
// modules) when it can, and that path gives no answer on some hosts where `host` has one (a ".local" zone, say).  So when it finds
// nothing the pure-Go resolver is tried, and then a plain PTR query sent to the nameservers of /etc/resolv.conf, as `host` does.
// The error is a *net.DNSError with IsNotFound when a server said there is no such record.
func reverseNames(ctx context.Context, addr string) ([]string, error) {
	names, err := net.DefaultResolver.LookupAddr(ctx, addr)
	if len(names) > 0 {
		return names, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	pg := &net.Resolver{PreferGo: true}
	if n, e := pg.LookupAddr(ctx, addr); len(n) > 0 {
		return n, nil
	} else if err == nil {
		err = e
	}
	if ctx.Err() != nil {
		return nil, err
	}
	n, e := rawPTR(ctx, addr, resolvConfServers("/etc/resolv.conf"))
	if len(n) > 0 {
		return n, nil
	}
	if e != nil {
		var de *net.DNSError
		if errors.As(e, &de) && de.IsNotFound {
			return nil, e
		}
	}
	return nil, err
}

// resolvConfServers is the nameserver addresses (host:53) of a resolv.conf, at most three.
func resolvConfServers(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		if len(f) >= 2 && f[0] == "nameserver" {
			if a, err := netip.ParseAddr(f[1]); err == nil {
				out = append(out, net.JoinHostPort(a.String(), "53"))
			}
		}
		if len(out) == 3 {
			break
		}
	}
	return out
}

// ptrName is the name a PTR query for the address asks about (4.3.2.1.in-addr.arpa. or the nibbles under ip6.arpa.).
func ptrName(a netip.Addr) string {
	a = a.Unmap()
	if a.Is4() {
		b := a.As4()
		return rdnsItoa(int(b[3])) + "." + rdnsItoa(int(b[2])) + "." + rdnsItoa(int(b[1])) + "." + rdnsItoa(int(b[0])) + ".in-addr.arpa."
	}
	const hex = "0123456789abcdef"
	b := a.As16()
	var sb strings.Builder
	for i := 15; i >= 0; i-- {
		sb.WriteByte(hex[b[i]&15])
		sb.WriteByte('.')
		sb.WriteByte(hex[b[i]>>4])
		sb.WriteByte('.')
	}
	return sb.String() + "ip6.arpa."
}

func rdnsItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

// rawPTR sends a plain PTR query (no EDNS, as `host` does) to each server in turn: UDP, then TCP when the answer is cut.
func rawPTR(ctx context.Context, addr string, servers []string) ([]string, error) {
	a, err := netip.ParseAddr(addr)
	if err != nil || len(servers) == 0 {
		return nil, errors.New("no nameserver to ask")
	}
	q := buildPTRQuery(ptrName(a))
	var last error = errors.New("no nameserver answered")
	for _, srv := range servers {
		if ctx.Err() != nil {
			break
		}
		resp, err := rdnsExchange(ctx, "udp", srv, q)
		if err == nil && len(resp) >= 4 && resp[2]&0x02 != 0 { // truncated
			resp, err = rdnsExchange(ctx, "tcp", srv, q)
		}
		if err != nil {
			last = err
			continue
		}
		names, rcode, perr := parsePTRAnswer(resp, q[0:2])
		if perr != nil {
			last = perr
			continue
		}
		if len(names) > 0 {
			return names, nil
		}
		if rcode == 0 || rcode == 3 { // no data, or no such name: an answer
			return nil, &net.DNSError{Err: "no such host", Name: addr, Server: srv, IsNotFound: true}
		}
		last = errors.New("server failure")
	}
	return nil, last
}

func buildPTRQuery(name string) []byte {
	q := []byte{0x4c, 0x44, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0} // id "LD", recursion desired, one question
	for _, l := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		q = append(q, byte(len(l)))
		q = append(q, l...)
	}
	return append(q, 0, 0, 12, 0, 1) // root, type PTR, class IN
}

func rdnsExchange(ctx context.Context, network, srv string, q []byte) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(cctx, network, srv)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if dl, ok := cctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	if network == "tcp" {
		m := append([]byte{byte(len(q) >> 8), byte(len(q))}, q...)
		if _, err := c.Write(m); err != nil {
			return nil, err
		}
		var l [2]byte
		if _, err := readFull(c, l[:]); err != nil {
			return nil, err
		}
		buf := make([]byte, binary.BigEndian.Uint16(l[:]))
		_, err := readFull(c, buf)
		return buf, err
	}
	if _, err := c.Write(q); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := c.Read(buf)
	return buf[:n], err
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		k, err := c.Read(b[n:])
		n += k
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// parsePTRAnswer reads the PTR names out of a response to a query with the given id; it never panics on malformed bytes.
func parsePTRAnswer(m []byte, id []byte) (names []string, rcode int, err error) {
	bad := errors.New("malformed DNS answer")
	if len(m) < 12 || m[0] != id[0] || m[1] != id[1] || m[2]&0x80 == 0 {
		return nil, 0, bad
	}
	rcode = int(m[3] & 15)
	qd, an := int(binary.BigEndian.Uint16(m[4:])), int(binary.BigEndian.Uint16(m[6:]))
	off := 12
	for i := 0; i < qd; i++ {
		_, o, ok := readDNSName(m, off)
		if !ok || o+4 > len(m) {
			return nil, 0, bad
		}
		off = o + 4
	}
	for i := 0; i < an; i++ {
		_, o, ok := readDNSName(m, off)
		if !ok || o+10 > len(m) {
			return nil, 0, bad
		}
		typ := binary.BigEndian.Uint16(m[o:])
		rdlen := int(binary.BigEndian.Uint16(m[o+8:]))
		o += 10
		if o+rdlen > len(m) {
			return nil, 0, bad
		}
		if typ == 12 {
			if n, _, ok := readDNSName(m, o); ok && n != "" {
				names = append(names, n)
			}
		}
		off = o + rdlen
	}
	return names, rcode, nil
}

// readDNSName reads a (possibly compressed) name at off; it returns the name without the trailing dot and the offset after it.
func readDNSName(m []byte, off int) (string, int, bool) {
	var parts []string
	end, jumps := -1, 0
	for {
		if off >= len(m) {
			return "", 0, false
		}
		l := int(m[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			return strings.Join(parts, "."), end, true
		case l&0xc0 == 0xc0:
			if off+1 >= len(m) || jumps > 10 {
				return "", 0, false
			}
			if end < 0 {
				end = off + 2
			}
			off = (l&0x3f)<<8 | int(m[off+1])
			jumps++
		case l&0xc0 != 0:
			return "", 0, false
		default:
			if off+1+l > len(m) {
				return "", 0, false
			}
			parts = append(parts, string(m[off+1:off+1+l]))
			off += 1 + l
		}
	}
}
