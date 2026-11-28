package logs

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"syscall"
	"time"

	"logdir/internal/core"
)

// spoof_source: syslog over udp sent from the sender's own address, so the destination sees the message as if logdir were not there.
// It needs a raw socket (root or CAP_NET_RAW) and works for IPv4 only; a message from an IPv6 sender, or to an IPv6 server, is sent
// the ordinary way (from this node, with its origin in the structured data).  What is sent is the sender's own bytes when they were
// kept, else the message as RFC 5424.  A datagram is cut to fit a 1500-byte frame (the raw socket does not fragment).  Packets with
// another host's source address are often dropped on the way (reverse-path filters, firewalls): the help says so.

const ldSpoofSrcPort = 514

type ldSpoofSender struct {
	d       core.LogDestCfg
	fd      int
	dst     netip.AddrPort
	resolve time.Time
	plain   *ldUDPSender // senders that are not IPv4, and a server that is not
}

func newSpoofSender(d core.LogDestCfg) (ldShipSender, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_RAW)
	if err != nil {
		return nil, fmt.Errorf("spoof_source needs a raw socket (run as root or with CAP_NET_RAW): %v", err)
	}
	return &ldSpoofSender{d: d, fd: fd, plain: &ldUDPSender{d: d}}, nil
}

func (s *ldSpoofSender) target(ctx context.Context) (netip.AddrPort, error) {
	if s.dst.IsValid() && time.Since(s.resolve) < 5*time.Minute {
		return s.dst, nil
	}
	addr, err := s.d.Address()
	if err != nil {
		return netip.AddrPort{}, err
	}
	host, port, _ := net.SplitHostPort(addr)
	p, _ := net.LookupPort("udp", port)
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return netip.AddrPort{}, err
	}
	for _, ip := range ips {
		if ip.Unmap().Is4() {
			s.dst, s.resolve = netip.AddrPortFrom(ip.Unmap(), uint16(p)), time.Now()
			return s.dst, nil
		}
	}
	return netip.AddrPortFrom(ips[0], uint16(p)), nil
}

func (s *ldSpoofSender) send(ctx context.Context, items []ldShipItem) error {
	dst, err := s.target(ctx)
	if err != nil {
		return err
	}
	var rest []ldShipItem
	for _, it := range items {
		src, err := netip.ParseAddr(it.m.Src)
		if err != nil || !src.Unmap().Is4() || !dst.Addr().Is4() {
			rest = append(rest, it)
			continue
		}
		payload := []byte(it.m.Raw)
		if len(payload) == 0 {
			payload = ld5424(it, false)
		}
		if len(payload) > ldShipSpoofMax {
			payload = payload[:ldShipSpoofMax]
		}
		pkt := ldSpoofPacket(src.Unmap(), dst.Addr(), ldSpoofSrcPort, dst.Port(), payload)
		sa := &syscall.SockaddrInet4{Addr: dst.Addr().As4()}
		if err := syscall.Sendto(s.fd, pkt, 0, sa); err != nil {
			return fmt.Errorf("spoofed send: %v", err)
		}
	}
	if len(rest) > 0 {
		return s.plain.send(ctx, rest)
	}
	return nil
}

func (s *ldSpoofSender) close() {
	if s.fd > 0 {
		syscall.Close(s.fd)
		s.fd = -1
	}
	s.plain.close()
}

// ldSpoofPacket is an IPv4 header, a UDP header and the payload, with both checksums.
func ldSpoofPacket(src, dst netip.Addr, sport, dport uint16, payload []byte) []byte {
	ulen := 8 + len(payload)
	pkt := make([]byte, 20+ulen)
	ip := pkt[:20]
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:], uint16(len(pkt)))
	binary.BigEndian.PutUint16(ip[4:], uint16(rand.IntN(65536)))
	ip[6] = 0x40 // don't fragment
	ip[8] = 64
	ip[9] = syscall.IPPROTO_UDP
	s4, d4 := src.As4(), dst.As4()
	copy(ip[12:16], s4[:])
	copy(ip[16:20], d4[:])
	binary.BigEndian.PutUint16(ip[10:], ldChecksum(ip, 0))
	u := pkt[20:]
	binary.BigEndian.PutUint16(u[0:], sport)
	binary.BigEndian.PutUint16(u[2:], dport)
	binary.BigEndian.PutUint16(u[4:], uint16(ulen))
	copy(u[8:], payload)
	// the pseudo header: source, destination, protocol, UDP length
	var ph uint32
	ph += uint32(s4[0])<<8 | uint32(s4[1])
	ph += uint32(s4[2])<<8 | uint32(s4[3])
	ph += uint32(d4[0])<<8 | uint32(d4[1])
	ph += uint32(d4[2])<<8 | uint32(d4[3])
	ph += syscall.IPPROTO_UDP + uint32(ulen)
	c := ldChecksum(u, ph)
	if c == 0 {
		c = 0xffff
	}
	binary.BigEndian.PutUint16(u[6:], c)
	return pkt
}

func ldChecksum(b []byte, sum uint32) uint16 {
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}
