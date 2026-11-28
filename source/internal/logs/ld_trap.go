package logs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

const ldTrapMaxVarbind = 64

const ldTrapMaxOctets = 256

const trapProg = "snmptrap"

const snmpVersion1 = 0

const snmpVersion2c = 1

const snmpVersion3 = 3

// ── BER ──────────────────────────────────────────────────────────────────────

var errTrapShort = errors.New("cut short")

// snmpTLV reads one tag-length-value from b: the tag, the value and what follows it.
func snmpTLV(b []byte) (tag byte, val, rest []byte, err error) {
	if len(b) < 2 {
		return 0, nil, nil, errTrapShort
	}
	tag = b[0]
	if tag&0x1f == 0x1f {
		return 0, nil, nil, errors.New("multi-byte tag")
	}
	n, i := int(b[1]), 2
	if n&0x80 != 0 {
		k := n & 0x7f
		if k == 0 || k > 3 || len(b) < 2+k {
			return 0, nil, nil, errors.New("bad length")
		}
		n = 0
		for _, c := range b[2 : 2+k] {
			n = n<<8 | int(c)
		}
		i = 2 + k
	}
	if n > len(b)-i {
		return 0, nil, nil, errTrapShort
	}
	return tag, b[i : i+n], b[i+n:], nil
}

// snmpTLVRaw is snmpTLV that also gives the whole encoding (tag and length included), for an inform's answer.
func snmpTLVRaw(b []byte) (tag byte, val, raw, rest []byte, err error) {
	tag, val, rest, err = snmpTLV(b)
	if err != nil {
		return
	}
	return tag, val, b[:len(b)-len(rest)], rest, nil
}

func snmpInt(v []byte) (int64, error) {
	if len(v) == 0 || len(v) > 8 {
		return 0, errors.New("bad integer")
	}
	n := int64(int8(v[0])) // the sign
	for _, c := range v[1:] {
		n = n<<8 | int64(c)
	}
	return n, nil
}

func snmpUint(v []byte) (uint64, error) {
	if len(v) == 0 || len(v) > 9 || (len(v) == 9 && v[0] != 0) {
		return 0, errors.New("bad unsigned")
	}
	var n uint64
	for _, c := range v {
		n = n<<8 | uint64(c)
	}
	return n, nil
}

type snmpOID []uint32

func snmpOIDOf(v []byte) (snmpOID, error) {
	if len(v) == 0 || len(v) > 512 {
		return nil, errors.New("bad oid")
	}
	var o snmpOID
	var n uint32
	for i, c := range v {
		if n > 1<<25 {
			return nil, errors.New("oid part too big")
		}
		n = n<<7 | uint32(c&0x7f)
		if c&0x80 != 0 {
			if i == len(v)-1 {
				return nil, errors.New("bad oid")
			}
			continue
		}
		if len(o) == 0 {
			switch {
			case n < 40:
				o = append(o, 0, n)
			case n < 80:
				o = append(o, 1, n-40)
			default:
				o = append(o, 2, n-80)
			}
		} else {
			o = append(o, n)
		}
		n = 0
	}
	return o, nil
}

func (o snmpOID) String() string {
	var b strings.Builder
	for i, n := range o {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.FormatUint(uint64(n), 10))
	}
	return b.String()
}

func parseOID(s string) snmpOID {
	var o snmpOID
	for _, p := range strings.Split(s, ".") {
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			panic("bad oid in the table: " + s)
		}
		o = append(o, uint32(n))
	}
	return o
}

// ── a trap ───────────────────────────────────────────────────────────────────

type snmpVarbind struct {
	OID  snmpOID
	Tag  byte
	Val  []byte
	Text string // the value as shown (snmpValue)
	Num  int64  // the value of an INTEGER (for the enumerations)
}

type snmpTrap struct {
	Version   int
	Community string
	Inform    bool
	TrapOID   snmpOID
	Uptime    uint64 // hundredths of a second
	HasUptime bool
	Agent     netip.Addr // v1: the agent-addr field
	Varbinds  []snmpVarbind
	// for an inform's answer
	reqIDRaw, varbindsRaw []byte
}

var (
	oidSysUpTime      = parseOID("1.3.6.1.2.1.1.3.0")
	oidSnmpTrapOID    = parseOID("1.3.6.1.6.3.1.1.4.1.0")
	oidSnmpTraps      = parseOID("1.3.6.1.6.3.1.1.5")
	errTrapV3         = errors.New("SNMPv3 is not read yet")
	errTrapNotTrap    = errors.New("not a trap or an inform")
	errTrapBadVersion = errors.New("unknown SNMP version")
)

// decodeTrap reads an SNMPv1 or v2c trap or a v2c inform.
func decodeTrap(b []byte) (*snmpTrap, error) {
	tag, msg, _, err := snmpTLV(b)
	if err != nil {
		return nil, err
	}
	if tag != 0x30 {
		return nil, errors.New("not SNMP")
	}
	tag, v, rest, err := snmpTLV(msg)
	if err != nil || tag != 0x02 {
		return nil, errors.New("no version")
	}
	ver, err := snmpInt(v)
	if err != nil {
		return nil, err
	}
	switch ver {
	case snmpVersion1, snmpVersion2c:
	case snmpVersion3:
		return nil, errTrapV3
	default:
		return nil, errTrapBadVersion
	}
	t := &snmpTrap{Version: int(ver)}
	tag, v, rest, err = snmpTLV(rest)
	if err != nil || tag != 0x04 {
		return nil, errors.New("no community")
	}
	t.Community = string(v)
	pduTag, pdu, _, err := snmpTLV(rest)
	if err != nil {
		return nil, err
	}
	switch {
	case ver == snmpVersion1 && pduTag == 0xa4:
		err = t.decodeV1(pdu)
	case ver == snmpVersion2c && (pduTag == 0xa7 || pduTag == 0xa6):
		t.Inform = pduTag == 0xa6
		err = t.decodeV2(pdu)
	default:
		return nil, errTrapNotTrap
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (t *snmpTrap) decodeV1(p []byte) error {
	tag, v, rest, err := snmpTLV(p)
	if err != nil || tag != 0x06 {
		return errors.New("no enterprise")
	}
	ent, err := snmpOIDOf(v)
	if err != nil {
		return err
	}
	tag, v, rest, err = snmpTLV(rest)
	if err != nil || tag != 0x40 {
		return errors.New("no agent address")
	}
	if len(v) == 4 {
		t.Agent = netip.AddrFrom4([4]byte(v))
	}
	var gen, spec int64
	for _, dst := range []*int64{&gen, &spec} {
		tag, v, rest, err = snmpTLV(rest)
		if err != nil || tag != 0x02 {
			return errors.New("no trap number")
		}
		if *dst, err = snmpInt(v); err != nil {
			return err
		}
	}
	tag, v, rest, err = snmpTLV(rest)
	if err != nil || tag != 0x43 {
		return errors.New("no time stamp")
	}
	if t.Uptime, err = snmpUint(v); err != nil {
		return err
	}
	t.HasUptime = true
	// RFC 3584 3.1: the generic traps are snmpTraps.(n+1); an enterprise-specific one is enterprise.0.specific
	if gen >= 0 && gen < 6 {
		t.TrapOID = append(append(snmpOID{}, oidSnmpTraps...), uint32(gen+1))
	} else {
		t.TrapOID = append(append(append(snmpOID{}, ent...), 0), uint32(spec))
	}
	tag, v, _, err = snmpTLV(rest)
	if err != nil || tag != 0x30 {
		return errors.New("no varbinds")
	}
	return t.decodeVarbinds(v)
}

func (t *snmpTrap) decodeV2(p []byte) error {
	tag, _, raw, rest, err := snmpTLVRaw(p)
	if err != nil || tag != 0x02 {
		return errors.New("no request id")
	}
	t.reqIDRaw = raw
	for range 2 { // error-status, error-index
		if tag, _, rest, err = snmpTLV(rest); err != nil || tag != 0x02 {
			return errors.New("no error fields")
		}
	}
	tag, v, raw, _, err := snmpTLVRaw(rest)
	if err != nil || tag != 0x30 {
		return errors.New("no varbinds")
	}
	t.varbindsRaw = raw
	if err := t.decodeVarbinds(v); err != nil {
		return err
	}
	// RFC 3416 4.2.6: sysUpTime.0 first, snmpTrapOID.0 second; taken out of the list, they are trap= and uptime=
	keep := t.Varbinds[:0]
	for _, vb := range t.Varbinds {
		switch {
		case !t.HasUptime && oidEqual(vb.OID, oidSysUpTime) && vb.Tag == 0x43:
			t.Uptime, _ = snmpUint(vb.Val)
			t.HasUptime = true
		case t.TrapOID == nil && oidEqual(vb.OID, oidSnmpTrapOID) && vb.Tag == 0x06:
			t.TrapOID, _ = snmpOIDOf(vb.Val)
		default:
			keep = append(keep, vb)
		}
	}
	t.Varbinds = keep
	if t.TrapOID == nil {
		return errors.New("no snmpTrapOID")
	}
	return nil
}

func (t *snmpTrap) decodeVarbinds(b []byte) error {
	for len(b) > 0 && len(t.Varbinds) < ldTrapMaxVarbind {
		tag, vb, rest, err := snmpTLV(b)
		if err != nil || tag != 0x30 {
			return errors.New("bad varbind")
		}
		b = rest
		tag, v, vrest, err := snmpTLV(vb)
		if err != nil || tag != 0x06 {
			return errors.New("bad varbind name")
		}
		oid, err := snmpOIDOf(v)
		if err != nil {
			return err
		}
		vtag, val, _, err := snmpTLV(vrest)
		if err != nil {
			return errors.New("bad varbind value")
		}
		t.Varbinds = append(t.Varbinds, snmpVarbind{OID: oid, Tag: vtag, Val: val})
	}
	return nil
}

func oidEqual(a, b snmpOID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func oidHasPrefix(o, p snmpOID) bool { return len(o) >= len(p) && oidEqual(o[:len(p)], p) }

// ── the inform's answer ──────────────────────────────────────────────────────

func snmpLen(n int) []byte {
	switch {
	case n < 0x80:
		return []byte{byte(n)}
	case n < 0x100:
		return []byte{0x81, byte(n)}
	case n < 0x10000:
		return []byte{0x82, byte(n >> 8), byte(n)}
	default:
		return []byte{0x83, byte(n >> 16), byte(n >> 8), byte(n)}
	}
}

func snmpWrap(tag byte, parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := append([]byte{tag}, snmpLen(n)...)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// informResponse is the answer to an inform: a Response-PDU with its request-id and its varbinds, no error.
func (t *snmpTrap) informResponse() []byte {
	zero := []byte{0x02, 0x01, 0x00}
	pdu := snmpWrap(0xa2, t.reqIDRaw, zero, zero, t.varbindsRaw)
	return snmpWrap(0x30, []byte{0x02, 0x01, byte(t.Version)}, snmpWrap(0x04, []byte(t.Community)), pdu)
}

// pktinfoOOB is the control message that sends a datagram from src (the address the trap was sent to, a VIP perhaps), so the
// answer comes from the address the sender knows.
func pktinfoOOB(src netip.Addr) []byte {
	if !src.IsValid() {
		return nil
	}
	var level, typ int
	var data []byte
	if src.Is4() {
		level, typ = syscall.IPPROTO_IP, syscall.IP_PKTINFO
		data = make([]byte, 12) // ifindex, spec_dst, addr
		a := src.As4()
		copy(data[4:8], a[:])
	} else {
		level, typ = syscall.IPPROTO_IPV6, syscall.IPV6_PKTINFO
		data = make([]byte, 20) // addr, ifindex
		a := src.As16()
		copy(data[0:16], a[:])
	}
	b := make([]byte, syscall.CmsgSpace(len(data)))
	n := syscall.CmsgLen(len(data))
	if syscall.SizeofCmsghdr == 16 {
		binary.NativeEndian.PutUint64(b[0:8], uint64(n))
		binary.NativeEndian.PutUint32(b[8:12], uint32(level))
		binary.NativeEndian.PutUint32(b[12:16], uint32(typ))
	} else {
		binary.NativeEndian.PutUint32(b[0:4], uint32(n))
		binary.NativeEndian.PutUint32(b[4:8], uint32(level))
		binary.NativeEndian.PutUint32(b[8:12], uint32(typ))
	}
	copy(b[syscall.CmsgLen(0):], data)
	return b
}

// ── names ────────────────────────────────────────────────────────────────────

type ldTrapName struct {
	name string
	sev  int8
}

// ldTrapNames: the traps network equipment sends, by OID, with the severity each is kept at.
var ldTrapNames = map[string]ldTrapName{
	// SNMPv2-MIB snmpTraps (the v1 generic traps)
	"1.3.6.1.6.3.1.1.5.1": {"coldStart", 5},
	"1.3.6.1.6.3.1.1.5.2": {"warmStart", 5},
	"1.3.6.1.6.3.1.1.5.3": {"linkDown", 4},
	"1.3.6.1.6.3.1.1.5.4": {"linkUp", 5},
	"1.3.6.1.6.3.1.1.5.5": {"authenticationFailure", 4},
	"1.3.6.1.6.3.1.1.5.6": {"egpNeighborLoss", 4},
	// BGP4-MIB, both the v1-style and the RFC 4273 numbers
	"1.3.6.1.2.1.15.7.1": {"bgpEstablished", 5},
	"1.3.6.1.2.1.15.7.2": {"bgpBackwardTransition", 4},
	"1.3.6.1.2.1.15.0.1": {"bgpEstablishedNotification", 5},
	"1.3.6.1.2.1.15.0.2": {"bgpBackwardTransNotification", 4},
	// CISCO-BGP4-MIB
	"1.3.6.1.4.1.9.9.187.0.1": {"cbgpFsmStateChange", 5},
	"1.3.6.1.4.1.9.9.187.0.2": {"cbgpBackwardTransition", 4},
	// OSPF-TRAP-MIB
	"1.3.6.1.2.1.14.16.2.1":  {"ospfVirtIfStateChange", 5},
	"1.3.6.1.2.1.14.16.2.2":  {"ospfNbrStateChange", 5},
	"1.3.6.1.2.1.14.16.2.3":  {"ospfVirtNbrStateChange", 5},
	"1.3.6.1.2.1.14.16.2.16": {"ospfIfStateChange", 5},
	// VRRP-MIB
	"1.3.6.1.2.1.68.0.1": {"vrrpTrapNewMaster", 5},
	"1.3.6.1.2.1.68.0.2": {"vrrpTrapAuthFailure", 4},
	// ENTITY-MIB, RMON, UPS-MIB, POWER-ETHERNET-MIB, LLDP-MIB
	"1.3.6.1.2.1.47.2.0.1":     {"entConfigChange", 6},
	"1.3.6.1.2.1.16.0.1":       {"risingAlarm", 4},
	"1.3.6.1.2.1.16.0.2":       {"fallingAlarm", 5},
	"1.3.6.1.2.1.33.2.1":       {"upsTrapOnBattery", 4},
	"1.3.6.1.2.1.33.2.2":       {"upsTrapTestCompleted", 5},
	"1.3.6.1.2.1.33.2.3":       {"upsTrapAlarmEntryAdded", 4},
	"1.3.6.1.2.1.33.2.4":       {"upsTrapAlarmEntryRemoved", 5},
	"1.3.6.1.2.1.105.0.1":      {"pethPsePortOnOffNotification", 5},
	"1.0.8802.1.1.2.0.0.1":     {"lldpRemTablesChange", 6},
	"1.3.6.1.4.1.9.9.43.2.0.1": {"ciscoConfigManEvent", 5},
	"1.3.6.1.4.1.9.9.43.2.0.2": {"ccmCLIRunningConfigChanged", 5},
	"1.3.6.1.4.1.9.9.43.2.0.3": {"ccmCTIDRolledOver", 6},
	// OLD-CISCO: ciscoTrap (enterprise 1.3.6.1.4.1.9, sent as v1 or v2c): a reload, and a telnet/SSH session ending (snmp-server enable traps tty)
	"1.3.6.1.4.1.9.0.0": {"reload", 4},
	"1.3.6.1.4.1.9.0.1": {"tcpConnectionClose", 6},
	// CISCO-MAC-NOTIFICATION-MIB (snmp-server enable traps mac-notification): a MAC address moved between ports, or a table filled up
	"1.3.6.1.4.1.9.9.215.2.0.1": {"cmnMacChangedNotification", 5},
	"1.3.6.1.4.1.9.9.215.2.0.2": {"cmnMacMoveNotification", 5},
	"1.3.6.1.4.1.9.9.215.2.0.3": {"cmnMacThresholdExceedNotif", 4},
	// CISCO-ENVMON-MIB
	"1.3.6.1.4.1.9.9.13.3.0.1": {"ciscoEnvMonShutdownNotification", 2},
	"1.3.6.1.4.1.9.9.13.3.0.2": {"ciscoEnvMonVoltageNotification", 4},
	"1.3.6.1.4.1.9.9.13.3.0.3": {"ciscoEnvMonTemperatureNotification", 4},
	"1.3.6.1.4.1.9.9.13.3.0.4": {"ciscoEnvMonFanNotification", 4},
	"1.3.6.1.4.1.9.9.13.3.0.5": {"ciscoEnvMonRedundantSupplyNotification", 4},
	// CISCO-SYSLOG-MIB: a syslog message sent as a trap (its severity is the message's)
	"1.3.6.1.4.1.9.9.41.2.0.1": {"clogMessageGenerated", 5},
	// JUNIPER-MIB jnxChassisTraps
	"1.3.6.1.4.1.2636.4.1.1":  {"jnxPowerSupplyFailure", 2},
	"1.3.6.1.4.1.2636.4.1.2":  {"jnxFanFailure", 2},
	"1.3.6.1.4.1.2636.4.1.3":  {"jnxOverTemperature", 2},
	"1.3.6.1.4.1.2636.4.1.4":  {"jnxRedundancySwitchover", 4},
	"1.3.6.1.4.1.2636.4.1.5":  {"jnxFruRemoval", 5},
	"1.3.6.1.4.1.2636.4.1.6":  {"jnxFruInsertion", 5},
	"1.3.6.1.4.1.2636.4.1.7":  {"jnxFruPowerOff", 4},
	"1.3.6.1.4.1.2636.4.1.8":  {"jnxFruPowerOn", 5},
	"1.3.6.1.4.1.2636.4.1.9":  {"jnxFruFailed", 3},
	"1.3.6.1.4.1.2636.4.1.10": {"jnxFruOffline", 4},
	"1.3.6.1.4.1.2636.4.1.11": {"jnxFruOnline", 5},
}

// ldTrapObject is an object a trap carries: its name, what its INTEGER values mean, and which field its table index is shown as
// (ifIndex=3 for an interface's objects, peer=10.0.0.1 for a BGP peer's).
type ldTrapObject struct {
	name  string
	enum  map[int64]string
	index string // "ifIndex", "peer" (an IPv4 address index), "neighbor" (ospfNbrTable: an IPv4 address and a number), "line" (a
	// Cisco tty line), "tcpconn" (tcpConnTable: local address and port, remote address and port, written local= and remote=)
	ticks bool // hundredths of a second sent as a plain number (Cisco does for some TimeTicks): written 2m3s
}

var (
	enumIfStatus = map[int64]string{1: "up", 2: "down", 3: "testing", 4: "unknown", 5: "dormant", 6: "notPresent", 7: "lowerLayerDown"}
	enumBGPState = map[int64]string{1: "idle", 2: "connect", 3: "active", 4: "opensent", 5: "openconfirm", 6: "established"}
	enumOSPFNbr  = map[int64]string{1: "down", 2: "attempt", 3: "init", 4: "twoWay", 5: "exchangeStart", 6: "exchange", 7: "loading", 8: "full"}
	enumOSPFIf   = map[int64]string{1: "down", 2: "loopback", 3: "waiting", 4: "pointToPoint", 5: "designatedRouter", 6: "backupDesignatedRouter", 7: "otherDesignatedRouter"}
	enumEnvState = map[int64]string{1: "normal", 2: "warning", 3: "critical", 4: "shutdown", 5: "notPresent", 6: "notFunctioning"}
	enumCcmCmd   = map[int64]string{1: "commandLine", 2: "snmp"}
	enumCcmPlace = map[int64]string{1: "erase", 2: "commandSource", 3: "running", 4: "startup", 5: "local", 6: "networkTftp", 7: "networkRcp", 8: "networkFtp", 9: "networkScp"}
	enumCcmTerm  = map[int64]string{1: "notApplicable", 2: "unknown", 3: "console", 4: "terminal", 5: "virtual", 6: "auxiliary"}
	enumTCPState = map[int64]string{1: "closed", 2: "listen", 3: "synSent", 4: "synReceived", 5: "established", 6: "finWait1", 7: "finWait2", 8: "closeWait", 9: "lastAck", 10: "closing", 11: "timeWait", 12: "deleteTCB"}
	enumTsSes    = map[int64]string{1: "unknown", 2: "pad", 3: "stream", 4: "rlogin", 5: "telnet", 6: "tcp", 7: "lat", 8: "mop", 9: "slip", 10: "xremote", 11: "rshell"}
	enumClogSev  = map[int64]string{1: "emergency", 2: "alert", 3: "critical", 4: "error", 5: "warning", 6: "notice", 7: "info", 8: "debug"}
)

// ldTrapObjects: by the OID of the object (the column, without its index).
var ldTrapObjects = map[string]ldTrapObject{
	"1.3.6.1.2.1.1.3":     {name: "sysUpTime"},
	"1.3.6.1.2.1.1.5":     {name: "sysName"},
	"1.3.6.1.6.3.1.1.4.3": {name: "enterprise"},
	"1.3.6.1.6.3.18.1.3":  {name: "trapAddress"},
	"1.3.6.1.6.3.18.1.4":  {name: "trapCommunity"},
	// IF-MIB
	"1.3.6.1.2.1.2.2.1.1":      {name: "ifIndex"},
	"1.3.6.1.2.1.2.2.1.2":      {name: "ifDescr", index: "ifIndex"},
	"1.3.6.1.2.1.2.2.1.3":      {name: "ifType", index: "ifIndex"},
	"1.3.6.1.2.1.2.2.1.7":      {name: "ifAdminStatus", enum: enumIfStatus, index: "ifIndex"},
	"1.3.6.1.2.1.2.2.1.8":      {name: "ifOperStatus", enum: enumIfStatus, index: "ifIndex"},
	"1.3.6.1.2.1.31.1.1.1.1":   {name: "ifName", index: "ifIndex"},
	"1.3.6.1.2.1.31.1.1.1.18":  {name: "ifAlias", index: "ifIndex"},
	"1.3.6.1.4.1.9.2.2.1.1.20": {name: "locIfReason", index: "ifIndex"}, // Cisco: why the link went down
	// BGP4-MIB bgpPeerTable
	"1.3.6.1.2.1.15.3.1.2":  {name: "bgpPeerState", enum: enumBGPState, index: "peer"},
	"1.3.6.1.2.1.15.3.1.7":  {name: "bgpPeerRemoteAddr", index: "peer"},
	"1.3.6.1.2.1.15.3.1.9":  {name: "bgpPeerRemoteAs", index: "peer"},
	"1.3.6.1.2.1.15.3.1.14": {name: "bgpPeerLastError", index: "peer"},
	// OSPF-MIB
	"1.3.6.1.2.1.14.1.1":    {name: "ospfRouterId"},
	"1.3.6.1.2.1.14.10.1.1": {name: "ospfNbrIpAddr", index: "neighbor"},
	"1.3.6.1.2.1.14.10.1.3": {name: "ospfNbrRtrId", index: "neighbor"},
	"1.3.6.1.2.1.14.10.1.6": {name: "ospfNbrState", enum: enumOSPFNbr, index: "neighbor"},
	"1.3.6.1.2.1.14.7.1.1":  {name: "ospfIfIpAddress"},
	"1.3.6.1.2.1.14.7.1.12": {name: "ospfIfState", enum: enumOSPFIf},
	// VRRP-MIB
	"1.3.6.1.2.1.68.1.5":     {name: "vrrpTrapPacketSrc"},
	"1.3.6.1.2.1.68.1.3.1.7": {name: "vrrpOperMasterIpAddr"},
	// ENTITY-MIB
	"1.3.6.1.2.1.47.1.1.1.1.2": {name: "entPhysicalDescr"},
	"1.3.6.1.2.1.47.1.1.1.1.7": {name: "entPhysicalName"},
	// CISCO-CONFIG-MAN-MIB
	"1.3.6.1.4.1.9.9.43.1.1.6.1.3":  {name: "ccmHistoryEventCommandSource", enum: enumCcmCmd},
	"1.3.6.1.4.1.9.9.43.1.1.6.1.4":  {name: "ccmHistoryEventConfigSource", enum: enumCcmPlace},
	"1.3.6.1.4.1.9.9.43.1.1.6.1.5":  {name: "ccmHistoryEventConfigDestination", enum: enumCcmPlace},
	"1.3.6.1.4.1.9.9.43.1.1.6.1.6":  {name: "ccmHistoryEventTerminalType", enum: enumCcmTerm},
	"1.3.6.1.4.1.9.9.43.1.1.6.1.7":  {name: "ccmHistoryEventTerminalNumber"},
	"1.3.6.1.4.1.9.9.43.1.1.6.1.8":  {name: "ccmHistoryEventTerminalUser"},
	"1.3.6.1.4.1.9.9.43.1.1.6.1.9":  {name: "ccmHistoryEventTerminalLocation"},
	"1.3.6.1.4.1.9.9.43.1.1.6.1.10": {name: "ccmHistoryEventCommandSourceAddress"},
	"1.3.6.1.4.1.9.9.43.1.1.1":      {name: "ccmHistoryRunningLastChanged", ticks: true},
	"1.3.6.1.4.1.9.9.43.1.1.2":      {name: "ccmHistoryRunningLastSaved", ticks: true},
	"1.3.6.1.4.1.9.9.43.1.1.3":      {name: "ccmHistoryStartupLastChanged", ticks: true},
	// the session in tcpConnectionClose: OLD-CISCO-TS-MIB, OLD-CISCO-TCP-MIB and TCP-MIB's tcpConnTable
	"1.3.6.1.4.1.9.2.9.3.1.1":  {name: "tslineSesType", enum: enumTsSes, index: "line"},
	"1.3.6.1.4.1.9.2.9.2.1.18": {name: "tsLineUser", index: "line"},
	"1.3.6.1.2.1.6.13.1.1":     {name: "tcpConnState", enum: enumTCPState, index: "tcpconn"},
	"1.3.6.1.4.1.9.2.6.1.1.1":  {name: "loctcpConnInBytes", index: "tcpconn"},
	"1.3.6.1.4.1.9.2.6.1.1.2":  {name: "loctcpConnOutBytes", index: "tcpconn"},
	"1.3.6.1.4.1.9.2.6.1.1.3":  {name: "loctcpConnInPkts", index: "tcpconn"},
	"1.3.6.1.4.1.9.2.6.1.1.4":  {name: "loctcpConnOutPkts", index: "tcpconn"},
	"1.3.6.1.4.1.9.2.6.1.1.5":  {name: "loctcpConnElapsed", index: "tcpconn", ticks: true},
	// the move: which address, in which VLAN, from and to which bridge port (dot1dBasePort, not ifIndex), and when
	"1.3.6.1.4.1.9.9.215.1.3.3": {name: "cmnMacMoveAddress"},
	"1.3.6.1.4.1.9.9.215.1.3.4": {name: "cmnMacMoveVlanNumber"},
	"1.3.6.1.4.1.9.9.215.1.3.5": {name: "cmnMacMoveFromPortId"},
	"1.3.6.1.4.1.9.9.215.1.3.6": {name: "cmnMacMoveToPortId"},
	"1.3.6.1.4.1.9.9.215.1.3.7": {name: "cmnMacMoveTime", ticks: true},
	// CISCO-ENVMON-MIB
	"1.3.6.1.4.1.9.9.13.1.2.1.2": {name: "ciscoEnvMonVoltageStatusDescr"},
	"1.3.6.1.4.1.9.9.13.1.2.1.3": {name: "ciscoEnvMonVoltageStatusValue"},
	"1.3.6.1.4.1.9.9.13.1.2.1.7": {name: "ciscoEnvMonVoltageState", enum: enumEnvState},
	"1.3.6.1.4.1.9.9.13.1.3.1.2": {name: "ciscoEnvMonTemperatureStatusDescr"},
	"1.3.6.1.4.1.9.9.13.1.3.1.3": {name: "ciscoEnvMonTemperatureStatusValue"},
	"1.3.6.1.4.1.9.9.13.1.3.1.6": {name: "ciscoEnvMonTemperatureState", enum: enumEnvState},
	"1.3.6.1.4.1.9.9.13.1.4.1.2": {name: "ciscoEnvMonFanStatusDescr"},
	"1.3.6.1.4.1.9.9.13.1.4.1.3": {name: "ciscoEnvMonFanState", enum: enumEnvState},
	"1.3.6.1.4.1.9.9.13.1.5.1.2": {name: "ciscoEnvMonSupplyStatusDescr"},
	"1.3.6.1.4.1.9.9.13.1.5.1.3": {name: "ciscoEnvMonSupplyState", enum: enumEnvState},
	// CISCO-SYSLOG-MIB clogHistoryEntry
	"1.3.6.1.4.1.9.9.41.1.2.3.1.2": {name: "clogHistFacility"},
	"1.3.6.1.4.1.9.9.41.1.2.3.1.3": {name: "clogHistSeverity", enum: enumClogSev},
	"1.3.6.1.4.1.9.9.41.1.2.3.1.4": {name: "clogHistMsgName"},
	"1.3.6.1.4.1.9.9.41.1.2.3.1.5": {name: "clogHistMsgText"},
	"1.3.6.1.4.1.9.9.41.1.2.3.1.6": {name: "clogHistTimestamp"},
	// JUNIPER-MIB
	"1.3.6.1.4.1.2636.3.1.8.1.6":  {name: "jnxContentsDescr"},
	"1.3.6.1.4.1.2636.3.1.15.1.5": {name: "jnxFruName"},
}

// trapObject finds the object an OID is an instance of: the longest prefix in the table, and the index after it.
func trapObject(o snmpOID) (ldTrapObject, snmpOID, bool) {
	for n := len(o); n > 0 && len(o)-n <= 14; n-- {
		if ob, ok := ldTrapObjects[o[:n].String()]; ok {
			return ob, o[n:], true
		}
	}
	return ldTrapObject{}, nil, false
}

// ── as a log message ─────────────────────────────────────────────────────────

// trapValue is a value as text, for key=value.
func trapValue(vb *snmpVarbind, ob *ldTrapObject) string {
	switch vb.Tag {
	case 0x02: // INTEGER
		n, err := snmpInt(vb.Val)
		if err != nil {
			return "?"
		}
		vb.Num = n
		if ob != nil && ob.ticks && n >= 0 {
			return trapTicks(uint64(n))
		}
		if ob != nil && ob.enum != nil {
			if s, ok := ob.enum[n]; ok {
				return s
			}
		}
		return strconv.FormatInt(n, 10)
	case 0x04: // OCTET STRING
		return trapOctets(vb.Val)
	case 0x05:
		return "null"
	case 0x06:
		o, err := snmpOIDOf(vb.Val)
		if err != nil {
			return "?"
		}
		if t, ok := ldTrapNames[o.String()]; ok {
			return t.name
		}
		return o.String()
	case 0x40: // IpAddress
		if len(vb.Val) == 4 {
			return netip.AddrFrom4([4]byte(vb.Val)).String()
		}
		return trapHex(vb.Val)
	case 0x41, 0x42, 0x46: // Counter32, Gauge32, Counter64
		n, err := snmpUint(vb.Val)
		if err != nil {
			return "?"
		}
		if ob != nil && ob.ticks {
			return trapTicks(n)
		}
		return strconv.FormatUint(n, 10)
	case 0x43: // TimeTicks
		n, err := snmpUint(vb.Val)
		if err != nil {
			return "?"
		}
		return trapTicks(n)
	case 0x80:
		return "noSuchObject"
	case 0x81:
		return "noSuchInstance"
	case 0x82:
		return "endOfMibView"
	}
	return trapHex(vb.Val)
}

// trapOctets: text when it is printable, a MAC address when it is six bytes of something else, hex otherwise.
func trapOctets(b []byte) string {
	if len(b) > ldTrapMaxOctets {
		b = b[:ldTrapMaxOctets]
	}
	s := strings.TrimRight(string(b), "\x00")
	if utf8.ValidString(s) {
		ok := true
		for _, r := range s {
			if r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0x7f {
				ok = false
				break
			}
		}
		if ok {
			return strings.Join(strings.Fields(s), " ")
		}
	}
	if len(b) == 6 {
		return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
	}
	return trapHex(b)
}

func trapHex(b []byte) string {
	if len(b) > 64 {
		b = b[:64]
	}
	return fmt.Sprintf("0x%x", b)
}

// trapTicks: hundredths of a second as 3d4h5m6s.
func trapTicks(n uint64) string {
	s := n / 100
	d, h, m, sec := s/86400, s/3600%24, s/60%60, s%60
	var b strings.Builder
	if d > 0 {
		fmt.Fprintf(&b, "%dd", d)
	}
	if d > 0 || h > 0 {
		fmt.Fprintf(&b, "%dh", h)
	}
	if d > 0 || h > 0 || m > 0 {
		fmt.Fprintf(&b, "%dm", m)
	}
	fmt.Fprintf(&b, "%ds", sec)
	return b.String()
}

// trapQuote quotes a value that the key=value reader would otherwise cut (a space, a quote, a , or ;) or that is empty.
func trapQuote(v string) string {
	if v != "" && !strings.ContainsAny(v, " \t\"',;") {
		return v
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
}

// trapIndexValue is a table index shown as the field it stands for.
func trapIndexValue(kind string, idx snmpOID) ([][2]string, bool) {
	ip4 := func(o snmpOID) (netip.Addr, bool) {
		if len(o) < 4 || o[0] > 255 || o[1] > 255 || o[2] > 255 || o[3] > 255 {
			return netip.Addr{}, false
		}
		return netip.AddrFrom4([4]byte{byte(o[0]), byte(o[1]), byte(o[2]), byte(o[3])}), true
	}
	switch kind {
	case "line":
		if len(idx) >= 1 {
			return [][2]string{{"line", strconv.FormatUint(uint64(idx[0]), 10)}}, true
		}
	case "tcpconn": // local address, local port, remote address, remote port
		if len(idx) == 10 && idx[4] <= 65535 && idx[9] <= 65535 {
			l, ok1 := ip4(idx[0:4])
			r, ok2 := ip4(idx[5:9])
			if ok1 && ok2 {
				return [][2]string{{"local", netip.AddrPortFrom(l, uint16(idx[4])).String()}, {"remote", netip.AddrPortFrom(r, uint16(idx[9])).String()}}, true
			}
		}
	case "ifIndex":
		if len(idx) == 1 {
			return [][2]string{{kind, strconv.FormatUint(uint64(idx[0]), 10)}}, true
		}
	case "peer", "neighbor":
		if a, ok := ip4(idx); ok {
			return [][2]string{{kind, a.String()}}, true
		}
	}
	return nil, false
}

// trapMessage is the trap as the log keeps it: its text and its severity.
func trapMessage(t *snmpTrap, src netip.Addr) (string, int8) {
	var b strings.Builder
	sev := int8(5)
	name := t.TrapOID.String()
	if tn, ok := ldTrapNames[name]; ok {
		name, sev = tn.name, tn.sev
	}
	b.WriteString("trap=" + name)
	used := map[string]bool{"trap": true, "uptime": true}
	type pair struct{ k, v string }
	var pairs, idxPairs []pair
	for i := range t.Varbinds {
		vb := &t.Varbinds[i]
		ob, idx, ok := trapObject(vb.OID)
		key := "oid_" + vb.OID.String()
		var obp *ldTrapObject
		if ok {
			key, obp = ob.name, &ob
			if len(idx) > 0 && !(len(idx) == 1 && idx[0] == 0) && used[key] {
				key += "." + idx.String() // the same object twice (two interfaces): told apart by the index
			}
			if ob.index != "" && !used[ob.index] {
				if kvs, ok := trapIndexValue(ob.index, idx); ok {
					for _, kv := range kvs {
						if !used[kv[0]] {
							used[kv[0]] = true
							idxPairs = append(idxPairs, pair{kv[0], kv[1]})
						}
					}
					used[ob.index] = true
				}
			}
		}
		v := trapValue(vb, obp)
		if ok && ob.name == "clogHistSeverity" && vb.Num >= 1 && vb.Num <= 8 {
			sev = int8(vb.Num - 1) // the syslog message's own severity
		}
		if used[key] {
			continue
		}
		used[key] = true
		pairs = append(pairs, pair{key, v})
	}
	// the index first (ifIndex=3 before ifDescr=…), unless the trap carries ifIndex itself
	for _, p := range append(idxPairs, pairs...) {
		b.WriteString(" " + p.k + "=" + trapQuote(p.v))
	}
	if t.Version == snmpVersion1 && t.Agent.IsValid() && !t.Agent.IsUnspecified() && t.Agent != src {
		b.WriteString(" agent=" + t.Agent.String()) // sent on by a relay: who it came from
	}
	if t.HasUptime {
		b.WriteString(" uptime=" + trapTicks(t.Uptime))
	}
	return b.String(), sev
}
