package logs

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"logdir/internal/cluster"
	"logdir/internal/core"
)

// The syslog collector of a node: UDP (514), TCP (514) and TLS (6514) listeners on the addresses of the gateways (the VIPs, the
// further addresses and the anycast addresses) and on the node's extra addresses, like the MCP listeners (mcplisten.go) and the
// flow collector this was made from: IP_FREEBIND, so a socket exists before the address does, and a set that follows the
// configuration.  A sender is pointed at a gateway address.  The cluster then does what it does for everything else behind a VIP:
// the active gateway answers for the address, the sender's messages reach that node, and when it fails they reach another.  Every
// node keeps what it received, so a search asks every node (ld_cluster.go).
//
// Who may send: the entries of settings.logs.senders (a name and an address, a network or a host name).  Everyone else is counted
// (refused) and dropped before a byte is parsed; a TCP or TLS connection from one is closed at once.
//
// TCP framing is read per message: an octet count ("123 <34>...", RFC 6587) when the message starts with digits and a space,
// otherwise one message per line.  TLS is the same inside the tunnel, with the certificate of the GUI.

const (
	ldMaxConns    = 2000 // TCP and TLS connections at once
	ldMaxOctets   = 1 << 20
	ldUDPReadBuf  = 8 << 20
	ldSenderLimit = 4096
)

type ldListenKey struct {
	addr  netip.Addr
	port  int
	proto string // udp, tcp, tls, trap (SNMP traps, over UDP)
}

func (k ldListenKey) String() string {
	return k.proto + " " + net.JoinHostPort(k.addr.String(), strconv.Itoa(k.port))
}

type LdSender struct {
	First, Last time.Time
	Msgs, Bytes uint64
	Via         string
	Host        string                   // the host name its latest message gave
	To          map[netip.Addr]time.Time // the local addresses its messages were sent to
}

// LdSenderInfo is one sender in the status.
type LdSenderInfo struct {
	Addr  string   `json:"addr"`
	Name  string   `json:"name,omitempty"`
	Host  string   `json:"host,omitempty"` // the host name the sender's messages give
	RDNS  string   `json:"rdns,omitempty"` // the name the address reverse-looks-up to (sw1.example.com); empty while unknown or when it has none
	Rev   string   `json:"rev,omitempty"`  // its host part (sw1): what Topology calls the sender
	Via   string   `json:"via,omitempty"`
	First int64    `json:"first_seen"`
	Last  int64    `json:"last_seen"`
	Msgs  uint64   `json:"messages"`
	Bytes uint64   `json:"bytes"`
	Node  string   `json:"node,omitempty"`
	To    []string `json:"to,omitempty"` // the addresses of this node it sent to in the last 15 minutes (the Topology page draws it on that gateway)
}

type LdService struct {
	sup      *core.Supervisor
	Cl       *cluster.Cluster  `json:"-"` // the cluster this node is in (nil: none)
	stateDir string            // the default home of the store ("logs" in it)
	certs    *core.CertManager // the certificate of the TLS syslog listener
	Store    *LdStore          `json:"-"`
	dns      *ldDNS
	dnsBusy  atomic.Bool // a background reverse lookup of senders is running
	keepRaw  atomic.Bool // a shipping destination sends the bytes senders sent: they are kept with each message
	ship     ldShipper
	TestCfg  atomic.Pointer[core.LogsConfig] `json:"-"`

	mu         sync.Mutex
	udp        map[ldListenKey]net.PacketConn
	lns        map[ldListenKey]net.Listener
	failed     map[ldListenKey]string
	BySender   map[netip.Addr]*LdSender `json:"-"`
	RefusedAt  map[netip.Addr]time.Time `json:"-"`
	trapWarnAt map[netip.Addr]time.Time
	cancel     context.CancelFunc
	conns      map[net.Conn]struct{}

	expMu   sync.RWMutex
	ExpNets []LdSenderNet             `json:"-"`
	ExpDNS  map[string][]netip.Prefix `json:"-"`

	nconn atomic.Int64
	lastN atomic.Uint64

	drops      atomic.Pointer[ldDropSet] // the drop rules as compiled (ld_drop.go)
	dropCounts sync.Map                  // lower-case rule name -> *ldDropCounter
}

// ldSvc is this node's collector (nil in a test that has none).
var LdSvc *LdService

func NewLdService(sup *core.Supervisor, cl *cluster.Cluster, stateDir string, certs *core.CertManager) *LdService {
	dir := ""
	if stateDir != "" {
		dir = filepath.Join(stateDir, "logs")
	}
	if sup != nil {
		if dc := sup.Config(); dc != nil && dc.LogDir != "" {
			dir = filepath.Clean(dc.LogDir)
		}
	}
	return &LdService{
		sup: sup, Cl: cl, stateDir: stateDir, certs: certs, Store: NewLdStore(dir, core.SelfHost()), dns: newLdDNS(),
		udp: map[ldListenKey]net.PacketConn{}, lns: map[ldListenKey]net.Listener{}, failed: map[ldListenKey]string{},
		BySender: map[netip.Addr]*LdSender{}, RefusedAt: map[netip.Addr]time.Time{}, ExpDNS: map[string][]netip.Prefix{},
		conns: map[net.Conn]struct{}{},
	}
}

func (s *LdService) Config() core.LogsConfig {
	if s.sup == nil {
		if c := s.TestCfg.Load(); c != nil { // a test sets the settings directly
			return *c
		}
		return core.LogsConfig{}
	}
	dc := s.sup.Config()
	if dc == nil || dc.Settings.Logs == nil {
		return core.LogsConfig{}
	}
	return *dc.Settings.Logs
}

// listLimit is how many messages the Logs page lists (Configure ▸ Settings ▸ Log collector ▸ Messages listed; 1000 unless changed).
func (s *LdService) ListLimit() int {
	if n := s.Config().ListLimit; n >= 100 && n <= core.LdMaxLimit {
		return n
	}
	return ldListDefault
}

func (s *LdService) applyConfig() {
	c := s.Config()
	s.Store.mu.Lock()
	s.Store.capPct, s.Store.retDays = c.DirMaxPercent, c.RetentionDays
	s.Store.mu.Unlock()
	s.Store.cache.set(c.MemMaxPercent, 0)
	core.ApplyGoMemLimit(machineMemory(), c.MemMaxPercent)
}

// Start opens the store and runs the loops.
func (s *LdService) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	s.applyConfig()
	s.Store.Load()
	go func() { s.Store.Warm(); s.Store.MakeSums(ctx); s.Store.MakeWixes(ctx) }()
	s.reconcileDrops() // before the first message is read
	go s.loop(ctx, 3*time.Second, func() { s.reconcile(); s.syncSenders() })
	go s.loop(ctx, 3*time.Second, s.reconcileDrops)
	go s.loop(ctx, time.Second, s.Store.Flush)
	go s.loop(ctx, time.Minute, func() { s.applyConfig(); s.Store.Maintain() })
	go s.loop(ctx, time.Minute, s.resolveSenders)
	go s.loop(ctx, 3*time.Second, func() { s.reconcileShipping(ctx) })
}

func (s *LdService) loop(ctx context.Context, every time.Duration, f func()) {
	f()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f()
		}
	}
}

// Stop closes the sockets and writes what is buffered.  The update's re-exec calls it first: a deferred Stop never runs there.
func (s *LdService) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	for k, c := range s.udp {
		c.Close()
		delete(s.udp, k)
	}
	for k, l := range s.lns {
		l.Close()
		delete(s.lns, k)
	}
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.stopShipping() // the cursors are written
	s.Store.Flush()
	s.Store.Maintain()
	s.Store.Close()
}

// wantListen is what this node should listen on.
func (s *LdService) wantListen() []ldListenKey {
	cfg := s.Config()
	if !cfg.On() {
		return nil
	}
	udp, tcp, tlsp := cfg.Ports()
	var addrs []netip.Addr
	dc := s.sup.Config()
	if dc != nil {
		addrs = core.GatewayAddrs(dc, true)
		for _, l := range dc.LogListen {
			if a, err := netip.ParseAddr(l); err == nil {
				addrs = append(addrs, a.Unmap())
			}
		}
	}
	// Nothing says where to listen (no log_listen): take every address of this node, so a sender can send to the node's own address
	// as well as to a gateway's.  The wildcard also receives what is sent to a gateway address this node holds.
	if dc == nil || len(dc.LogListen) == 0 {
		addrs = []netip.Addr{netip.IPv4Unspecified(), netip.IPv6Unspecified()}
	}
	seen := map[ldListenKey]bool{}
	var want []ldListenKey
	for _, a := range addrs {
		for _, pp := range []struct {
			proto string
			port  int
		}{{"udp", udp}, {"tcp", tcp}, {"tls", tlsp}, {"trap", cfg.TrapPortOrDefault()}} {
			k := ldListenKey{a, pp.port, pp.proto}
			if pp.port > 0 && !seen[k] {
				seen[k] = true
				want = append(want, k)
			}
		}
	}
	return want
}

// reconcile makes the sockets match the configuration.
func (s *LdService) reconcile() {
	want := s.wantListen()
	wanted := map[ldListenKey]bool{}
	for _, k := range want {
		wanted[k] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil {
		return
	}
	for k, c := range s.udp {
		if !wanted[k] {
			c.Close()
			delete(s.udp, k)
			core.Infof("logs: stopped listening on %s", k)
		}
	}
	for k, l := range s.lns {
		if !wanted[k] {
			l.Close()
			delete(s.lns, k)
			core.Infof("logs: stopped listening on %s", k)
		}
	}
	for k := range s.failed {
		if !wanted[k] {
			delete(s.failed, k)
		}
	}
	for _, k := range want {
		if s.udp[k] != nil || s.lns[k] != nil {
			continue
		}
		hp := net.JoinHostPort(k.addr.String(), strconv.Itoa(k.port))
		fail := func(err error) {
			if s.failed[k] != err.Error() && !errors.Is(err, syscall.EAFNOSUPPORT) { // a host without IPv6 has nothing to listen on there
				core.Errorf("logs: cannot listen on %s: %v", k, err)
			}
			s.failed[k] = err.Error()
		}
		if k.proto == "udp" || k.proto == "trap" {
			network := "udp4"
			if k.addr.Is6() {
				network = "udp6"
			}
			lc := net.ListenConfig{Control: ldListenControlUDP}
			pc, err := lc.ListenPacket(context.Background(), network, hp)
			if err != nil {
				fail(err)
				continue
			}
			if u, ok := pc.(*net.UDPConn); ok {
				u.SetReadBuffer(ldUDPReadBuf)
			}
			delete(s.failed, k)
			s.udp[k] = pc
			core.Infof("logs: listening on %s", k)
			go s.readUDP(pc, k.proto)
			continue
		}
		network := "tcp4"
		if k.addr.Is6() {
			network = "tcp6"
		}
		lc := net.ListenConfig{Control: core.FreebindControl}
		ln, err := lc.Listen(context.Background(), network, hp)
		if err != nil {
			fail(err)
			continue
		}
		if k.proto == "tls" {
			if s.certs == nil {
				ln.Close()
				fail(errors.New("no certificate"))
				continue
			}
			ln = tls.NewListener(ln, &tls.Config{GetCertificate: s.certs.GetCertificate, MinVersion: tls.VersionTLS12})
		}
		delete(s.failed, k)
		s.lns[k] = ln
		core.Infof("logs: listening on %s", k)
		go s.accept(ln, k.proto)
	}
}

func (s *LdService) readUDP(pc net.PacketConn, proto string) {
	u, ok := pc.(*net.UDPConn)
	if !ok {
		return
	}
	buf := make([]byte, 65536)
	oob := make([]byte, 256)
	for {
		n, oobn, _, ap, err := u.ReadMsgUDPAddrPort(buf, oob)
		if err != nil {
			return // closed
		}
		if proto == "trap" {
			s.handleTrap(u, ap, pktinfoDst(oob[:oobn]), buf[:n], time.Now())
			continue
		}
		s.Handle(ap.Addr().Unmap(), pktinfoDst(oob[:oobn]), "udp", buf[:n], time.Now())
	}
}

// handleTrap keeps an SNMP trap as a log message (ld_trap.go), and answers an inform.
func (s *LdService) handleTrap(u *net.UDPConn, from netip.AddrPort, dst netip.Addr, b []byte, now time.Time) {
	src := from.Addr().Unmap()
	defer func() {
		if r := recover(); r != nil {
			ldStats.dropped.Add(1)
			core.Errorf("logs: trap decoder panic from %s: %v", src, r)
		}
	}()
	if !s.allowed(src) {
		ldStats.refused.Add(1)
		s.noteRefused(src)
		return
	}
	t, err := decodeTrap(b)
	if err != nil {
		ldStats.trapUnread.Add(1)
		s.noteTrapProblem(src, "not read: "+err.Error())
		return
	}
	cfg := s.Config()
	if !cfg.TrapCommunityOK(t.Community) {
		ldStats.trapCommunity.Add(1)
		s.noteTrapProblem(src, "refused: its community is not one of settings.logs.trap_communities")
		return
	}
	if t.Inform {
		u.WriteMsgUDPAddrPort(t.informResponse(), pktinfoOOB(dst), from) // a lost answer: the sender sends the inform again
	}
	ldStats.received.Add(1)
	ldStats.bytes.Add(uint64(len(b)))
	ldStats.traps.Add(1)
	text, sev := trapMessage(t, src)
	if ml := cfg.MaxLineOrDefault(); len(text) > ml {
		text = strings.ToValidUTF8(text[:ml], "")
	}
	m := core.LdMsg{T: now.UnixMilli(), Host: src.String(), Prog: trapProg, Fac: 1, Sev: sev, Msg: text, Src: src.String(), Via: "trap"}
	if s.dropRule(&m) != "" {
		s.noteSender(src, dst, "trap", &m, len(b), now)
		return
	}
	s.Store.Add(m)
	ldStats.parsed.Add(1)
	s.noteSender(src, dst, "trap", &m, len(b), now)
}

// noteTrapProblem logs, at most once in 10 minutes per sender, why its trap was not kept.
func (s *LdService) noteTrapProblem(src netip.Addr, why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.trapWarnAt) > 1000 || s.trapWarnAt == nil {
		s.trapWarnAt = map[netip.Addr]time.Time{}
	}
	if t, ok := s.trapWarnAt[src]; !ok || time.Since(t) > 10*time.Minute {
		s.trapWarnAt[src] = time.Now()
		core.Warnf("logs: SNMP trap from %s %s", src, why)
	}
}

func (s *LdService) accept(ln net.Listener, proto string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return // closed
		}
		go s.serve(c, proto)
	}
}

func connAddr(c net.Conn) (src, dst netip.Addr) {
	if ap, err := netip.ParseAddrPort(c.RemoteAddr().String()); err == nil {
		src = ap.Addr().Unmap()
	}
	if ap, err := netip.ParseAddrPort(c.LocalAddr().String()); err == nil {
		dst = ap.Addr().Unmap()
	}
	return
}

func (s *LdService) serve(c net.Conn, proto string) {
	defer c.Close()
	src, dst := connAddr(c)
	if !src.IsValid() || !s.allowed(src) {
		ldStats.refused.Add(1)
		s.noteRefused(src)
		return
	}
	if s.nconn.Add(1) > ldMaxConns {
		s.nconn.Add(-1)
		ldStats.dropped.Add(1)
		return
	}
	defer s.nconn.Add(-1)
	s.mu.Lock()
	if s.cancel == nil {
		s.mu.Unlock()
		return
	}
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}
	if tc, ok := c.(*tls.Conn); ok {
		c.SetDeadline(time.Now().Add(15 * time.Second))
		if err := tc.Handshake(); err != nil {
			return
		}
		c.SetDeadline(time.Time{})
	}
	defer func() {
		if r := recover(); r != nil {
			ldStats.dropped.Add(1)
			core.Errorf("logs: reader panic from %s: %v", src, r)
		}
	}()
	cfg := s.Config()
	max := cfg.MaxLineOrDefault()
	br := bufio.NewReaderSize(c, 64<<10)
	for {
		msg, err := ldReadFramed(br, max)
		if len(msg) > 0 {
			s.Handle(src, dst, proto, msg, time.Now())
		}
		if err != nil {
			return
		}
	}
}

// ldReadFramed reads one message: an octet-counted one ("LEN SP MSG") when it starts that way, else one line.  A line longer than max
// is cut at max and the rest of it skipped.
func ldReadFramed(br *bufio.Reader, max int) ([]byte, error) {
	for {
		b, err := br.Peek(1)
		if err != nil {
			return nil, err
		}
		if b[0] == '\n' || b[0] == '\r' { // empty lines between messages
			br.ReadByte()
			continue
		}
		if b[0] >= '0' && b[0] <= '9' {
			if p, _ := br.Peek(8); len(p) > 1 {
				n, i := 0, 0
				for i < len(p) && p[i] >= '0' && p[i] <= '9' && i < 7 {
					n = n*10 + int(p[i]-'0')
					i++
				}
				if i < len(p) && p[i] == ' ' && i > 0 && n > 0 {
					if n > ldMaxOctets {
						return nil, errors.New("frame too long")
					}
					br.Discard(i + 1)
					buf := make([]byte, n)
					if _, err := io.ReadFull(br, buf); err != nil {
						return buf[:0], err
					}
					if len(buf) > max {
						buf = buf[:max]
					}
					return buf, nil
				}
			}
		}
		var line []byte
		for {
			part, err := br.ReadSlice('\n')
			if len(line) < max {
				room := max - len(line)
				if len(part) > room {
					part = part[:room]
				}
				line = append(line, part...)
			}
			if err == bufio.ErrBufferFull {
				continue // the rest of a long line is read and thrown away
			}
			return bytes.TrimRight(line, "\r\n"), err
		}
	}
}

// handle takes one message (dst: the local address it came in on, invalid when that is not known).
func (s *LdService) Handle(src, dst netip.Addr, via string, b []byte, now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			ldStats.dropped.Add(1)
			core.Errorf("logs: parser panic from %s: %v", src, r)
		}
	}()
	if !s.allowed(src) {
		ldStats.refused.Add(1)
		s.noteRefused(src)
		return
	}
	ldStats.received.Add(1)
	ldStats.bytes.Add(uint64(len(b)))
	switch via {
	case "udp":
		ldStats.udp.Add(1)
	case "tcp":
		ldStats.tcp.Add(1)
	case "tls":
		ldStats.tls.Add(1)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return
	}
	cfg := s.Config()
	m := parseSyslog(b, src, via, now, cfg.MaxLineOrDefault())
	if s.dropRule(&m) != "" { // a drop rule: not kept, but the sender was heard
		s.noteSender(src, dst, via, &m, len(b), now)
		return
	}
	if s.keepRaw.Load() {
		m.Raw = ldRawText(b, cfg.MaxLineOrDefault())
	}
	s.Store.Add(m)
	ldStats.parsed.Add(1)
	s.noteSender(src, dst, via, &m, len(b), now)
}

func (s *LdService) noteRefused(src netip.Addr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.RefusedAt) > 1000 {
		s.RefusedAt = map[netip.Addr]time.Time{}
	}
	if t, ok := s.RefusedAt[src]; !ok || time.Since(t) > 10*time.Minute {
		s.RefusedAt[src] = time.Now()
		core.Warnf("logs: message from %s refused: not in settings.logs.senders", src)
	}
}

func (s *LdService) noteSender(src, dst netip.Addr, via string, m *core.LdMsg, n int, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.BySender[src]
	if e == nil {
		if len(s.BySender) >= ldSenderLimit {
			return
		}
		e = &LdSender{First: now}
		s.BySender[src] = e
		core.Infof("logs: new sender %s (%s)", src, via)
	}
	e.Last, e.Via = now, via
	if m.Host != "" && m.Host != m.Src {
		e.Host = m.Host
	}
	if dst.IsValid() {
		if e.To == nil {
			e.To = map[netip.Addr]time.Time{}
		}
		if _, ok := e.To[dst]; ok || len(e.To) < 8 {
			e.To[dst] = now
		}
	}
	e.Msgs++
	e.Bytes += uint64(n)
}

// ── who may send ────────────────────────────────────────────────────────────

type LdSenderNet struct {
	P    netip.Prefix `json:"-"`
	Name string       `json:"-"`
}

func (s *LdService) allowed(a netip.Addr) bool { return s.senderName(a) != "" }

// syncSenders makes the accepted addresses match the configuration (every few seconds, so a change takes effect at once).
func (s *LdService) syncSenders() {
	cfg := s.Config()
	var out []LdSenderNet
	s.expMu.RLock()
	dns := s.ExpDNS
	s.expMu.RUnlock()
	for _, e := range cfg.Senders {
		if e.Disabled {
			continue
		}
		if p, err := netip.ParsePrefix(e.Address); err == nil {
			out = append(out, LdSenderNet{p.Masked(), e.Name})
		} else if a, err := netip.ParseAddr(e.Address); err == nil {
			a = a.Unmap()
			out = append(out, LdSenderNet{netip.PrefixFrom(a, a.BitLen()), e.Name})
		} else {
			for _, p := range dns[strings.ToLower(e.Address)] {
				out = append(out, LdSenderNet{p, e.Name})
			}
		}
	}
	s.expMu.Lock()
	s.ExpNets = out
	s.expMu.Unlock()
}

// resolveSenders looks up the host names among the senders (every minute: a name may move).
func (s *LdService) resolveSenders() {
	cfg := s.Config()
	m := map[string][]netip.Prefix{}
	for _, e := range cfg.Senders {
		if e.Disabled {
			continue
		}
		if _, err := netip.ParsePrefix(e.Address); err == nil {
			continue
		}
		if _, err := netip.ParseAddr(e.Address); err == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ips, _ := net.DefaultResolver.LookupNetIP(ctx, "ip", e.Address)
		cancel()
		var ps []netip.Prefix
		for _, a := range ips {
			a = a.Unmap()
			ps = append(ps, netip.PrefixFrom(a, a.BitLen()))
		}
		if len(ps) == 0 { // a lookup that failed keeps what the name resolved to before
			s.expMu.RLock()
			ps = s.ExpDNS[strings.ToLower(e.Address)]
			s.expMu.RUnlock()
		}
		m[strings.ToLower(e.Address)] = ps
	}
	s.expMu.Lock()
	s.ExpDNS = m
	s.expMu.Unlock()
	s.syncSenders()
}

// senderName is the name of the entry whose address or network holds a ("" when none does; the narrowest network wins).
func (s *LdService) senderName(a netip.Addr) string {
	s.expMu.RLock()
	defer s.expMu.RUnlock()
	best, bits := "", -1
	for _, n := range s.ExpNets {
		if n.P.Contains(a) && n.P.Bits() > bits {
			best, bits = n.Name, n.P.Bits()
		}
	}
	return best
}

// Senders lists what this node has heard from.
func (s *LdService) Senders() []LdSenderInfo {
	node := core.SelfHost()
	s.mu.Lock()
	out := make([]LdSenderInfo, 0, len(s.BySender))
	for a, e := range s.BySender {
		info := LdSenderInfo{Addr: a.String(), Host: e.Host, Via: e.Via, First: e.First.Unix(), Last: e.Last.Unix(), Msgs: e.Msgs, Bytes: e.Bytes, Node: node}
		for d, t := range e.To {
			if time.Since(t) < 15*time.Minute {
				info.To = append(info.To, d.String())
			}
		}
		sort.Strings(info.To)
		out = append(out, info)
	}
	s.mu.Unlock()
	var unknown []netip.Addr
	for i := range out {
		a, _ := netip.ParseAddr(out[i].Addr)
		out[i].Name = s.senderName(a)
		if name, known := s.dns.peek(a); known {
			out[i].RDNS, out[i].Rev = name, hostPart(name)
		} else {
			unknown = append(unknown, a)
		}
	}
	if len(unknown) > 0 && s.dnsBusy.CompareAndSwap(false, true) {
		go func() { // the names are looked up in the background; the next call has them
			defer s.dnsBusy.Store(false)
			s.dns.names(unknown, 8*time.Second)
		}()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out
}

// sendersByName is the addresses (of the senders this node has heard) whose configured name, reverse name or host name matches the
// pattern (case ignored, * wildcards): what a search by source=sw*.example.com means on this node.
func (s *LdService) sendersByName(pat string) map[string]bool {
	pat = strings.ToLower(strings.TrimSpace(pat))
	s.mu.Lock()
	var addrs []netip.Addr
	hosts := map[netip.Addr]string{}
	for a, e := range s.BySender {
		addrs = append(addrs, a)
		hosts[a] = e.Host
	}
	s.mu.Unlock()
	rev := s.dns.names(addrs, 1500*time.Millisecond)
	out := map[string]bool{}
	for _, a := range addrs {
		for _, n := range []string{s.senderName(a), rev[a], hostPart(rev[a]), hosts[a]} {
			if n != "" && core.LdGlob(pat, strings.ToLower(n)) {
				out[a.String()] = true
				break
			}
		}
	}
	return out
}

// senderWords is, for each sender this node has heard, the names the Logs page calls it by (its name in Configure ▸ Senders, its DNS name and
// the first part of that), lower case: the words of a search match them as well as the text of the message.
func (s *LdService) senderWords() map[string]string {
	s.mu.Lock()
	var addrs []netip.Addr
	for a := range s.BySender {
		addrs = append(addrs, a)
	}
	s.mu.Unlock()
	rev := s.dns.names(addrs, 1500*time.Millisecond)
	out := make(map[string]string, len(addrs))
	for _, a := range addrs {
		var w []string
		for _, n := range []string{s.senderName(a), rev[a], hostPart(rev[a])} {
			if n != "" {
				w = append(w, strings.ToLower(n))
			}
		}
		if len(w) > 0 {
			out[a.String()] = strings.Join(w, " ")
		}
	}
	return out
}

// LdNodeStatus is the state of one node's collector.
type LdNodeStatus struct {
	Node       string        `json:"node"`
	Enabled    bool          `json:"enabled"`
	Listening  []string      `json:"listening"`
	Failed     []string      `json:"failed,omitempty"`
	Dir        string        `json:"dir,omitempty"`
	DefaultDir string        `json:"default_dir,omitempty"`
	DirError   string        `json:"dir_error,omitempty"`
	DirBytes   uint64        `json:"dir_bytes,omitempty"`
	DirCap     uint64        `json:"dir_cap_bytes,omitempty"`
	MemBytes   uint64        `json:"mem_bytes,omitempty"`     // what the hour cache holds
	MemCap     uint64        `json:"mem_cap_bytes,omitempty"` // the most it may hold
	MemHours   int           `json:"mem_hours,omitempty"`     // hours in it
	CacheHits  uint64        `json:"cache_hits,omitempty"`
	CacheMiss  uint64        `json:"cache_misses,omitempty"`
	CapRemoved uint64        `json:"removed_hours,omitempty"` // hours deleted by the retention or the cap
	Received   uint64        `json:"received"`
	Bytes      uint64        `json:"bytes"`
	Refused    uint64        `json:"refused"`
	Dropped    uint64        `json:"dropped"`
	RuleDrop   uint64        `json:"rule_dropped"` // by the drop rules, since this node started
	DropRules  []LdDropCount `json:"drop_rules,omitempty"`
	WriteErrs  uint64        `json:"write_errors,omitempty"`
	UDP        uint64        `json:"udp"`
	TCP        uint64        `json:"tcp"`
	TLS        uint64        `json:"tls"`
	Traps      uint64        `json:"traps"`                     // SNMP traps kept
	TrapUnread uint64        `json:"traps_unread,omitempty"`    // not read: not a trap, SNMPv3, cut short
	TrapComm   uint64        `json:"traps_community,omitempty"` // refused for their community
	Conns      int64         `json:"connections"`
	Hours      int           `json:"hours"`
	Stored     uint64        `json:"stored"`
	Oldest     int64         `json:"oldest,omitempty"` // ms of the oldest message this node holds
}

func (s *LdService) Status() LdNodeStatus {
	cfg := s.Config()
	st := LdNodeStatus{Node: core.SelfHost(), Enabled: cfg.On(), Listening: []string{}, Oldest: s.Store.Oldest()}
	s.mu.Lock()
	for k := range s.udp {
		st.Listening = append(st.Listening, k.String())
	}
	for k := range s.lns {
		st.Listening = append(st.Listening, k.String())
	}
	for k, why := range s.failed {
		st.Failed = append(st.Failed, k.String()+": "+why)
	}
	s.mu.Unlock()
	sort.Strings(st.Listening)
	sort.Strings(st.Failed)
	st.Received, st.Bytes, st.Refused, st.Dropped, st.WriteErrs = ldStats.received.Load(), ldStats.bytes.Load(), ldStats.refused.Load(), ldStats.dropped.Load(), ldStats.writeErrs.Load()
	st.UDP, st.TCP, st.TLS, st.Conns = ldStats.udp.Load(), ldStats.tcp.Load(), ldStats.tls.Load(), s.nconn.Load()
	st.Traps, st.TrapUnread, st.TrapComm = ldStats.traps.Load(), ldStats.trapUnread.Load(), ldStats.trapCommunity.Load()
	s.Store.mu.Lock()
	st.Dir, st.DirError, st.DirBytes, st.DirCap, st.CapRemoved = s.Store.dir, s.Store.dirErr, s.Store.dirBytes, s.Store.capBytes, s.Store.removed
	s.Store.mu.Unlock()
	cs := s.Store.cache.stat()
	st.MemBytes, st.MemCap, st.MemHours, st.CacheHits, st.CacheMiss = cs.Bytes, cs.Cap, cs.Hours, cs.Hits, cs.Misses
	if s.stateDir != "" {
		st.DefaultDir = filepath.Join(s.stateDir, "logs")
	}
	st.Hours, st.Stored = s.Store.Count()
	st.RuleDrop, st.DropRules = ldStats.ruleDropped.Load(), s.dropCountsNow()
	return st
}
