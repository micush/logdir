package main

import (
	"encoding/hex"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// trapFixtures are traps net-snmp 5.9.4 sent (snmptrap and snmpinform), captured off the wire:
//
//	v2c_linkdown  snmptrap -v2c 12345 linkDown ifIndex.3=3 ifAdminStatus.3=1 ifOperStatus.3=2 ifDescr.3="GigabitEthernet0/3"
//	v1_linkdown   snmptrap -v1 enterprise 1.3.6.1.4.1.9.1.1, agent 127.0.0.1, generic 2 (linkDown), uptime 5000, ifIndex.7, ifDescr.7
//	v1_relay      snmptrap -v1 enterprise 1.3.6.1.4.1.99999, agent 192.0.2.5, specific 17, one varbind
//	v2c_bgp       bgpBackwardTransNotification, bgpPeerLastError and bgpPeerState of peer 10.0.0.1
//	v2c_clog      Cisco clogMessageGenerated with clogHistSeverity 4 (error) and a text with a comma and spaces
//	v2c_unknown   an enterprise trap with a MAC, bytes, a counter, an address and an OID value
//	v2c_twoifs    ifDescr of two interfaces
//	v2c_inform    snmpinform -v2c 777 linkUp ifIndex.4
//	v3            snmptrap -v3 noAuthNoPriv
//	v2c_private   coldStart with the community s3cret
//	cisco_*       shaped as an IOS-XE switch sent them (v48): ccmCLIRunningConfigChanged, ciscoConfigManEvent with the user, and
//	              tcpConnectionClose for an SSH session (the times sent as plain integers, as that switch did)
var trapFixtures = map[string]string{
	"cisco_ccm_cli":   "30818a020101040773776974636831a77c020413ad6152020100020100306e301006082b06010201010300430403d2f67a3019060a2b060106030101040100060b2b0601040109092b0200023014060c2b0601040109092b01010100020403d2f67a3013060e2b0601040109092b0101060106090201053014060e2b0601040109092b01010601080904026c61",
	"cisco_ccm_event": "3081a0020101040773776974636831a78191020429fb6160020100020100308182301006082b06010201010300430403d2f67a3019060a2b060106030101040100060b2b0601040109092b0200013013060e2b0601040109092b0101060103790201013013060e2b0601040109092b0101060104790201033013060e2b0601040109092b0101060105790201043014060e2b0601040109092b01010601087904026c61",
	"cisco_tcpclose":  "30820101020101040773776974636831a781f2020402a7c6590201000201003081e3301006082b06010201010300430403d2dd743016060a2b06010603010104010006082b060104010900013012060d2b0601040109020903010101010201063020061b2b06010201060d010181408128817f03168140812881411d82db060201053023061d2b0601040109020601010581408128817f03168140812881411d82db06020225523023061d2b0601040109020601010181408128817f03168140812881411d82db060202116a3023061d2b0601040109020601010281408128817f03168140812881411d82db0602021d133012060c2b060104010902090201120104026c61",
	"v1_linkdown":     "304f02010004067075626c6963a44206082b0601040109010140047f000001020102020100430213883026300f060a2b0601020102020101070201073013060a2b06010201020201020704054769302f37",
	"v1_relay":        "303d02010004067075626c6963a43006082b06010401868d1f4004c000020502010602011143016430153013060a2b06010401868d1f0102040568656c6c6f",
	"v2c_bgp":         "306c02010104067075626c6963a75f02042491bd4f0201000201003051300e06082b06010201010300430223283016060a2b06010603010104010006082b060102010f00023013060d2b060102010f03010e0a000001040206043012060d2b060102010f0301020a000001020101",
	"v2c_clog":        "3081c802010104067075626c6963a781ba02044e3062bd0201000201003081ab300d06082b060102010103004301643019060a2b060106030101040100060b2b060104010909290200013016060e2b0601040109092901020301020504044c494e4b3013060e2b060104010909290102030103050201043018060e2b0601040109092901020301040504065550444f574e3038060e2b060104010909290102030105050426496e74657266616365204769302f312c206368616e67656420737461746520746f20646f776e",
	"v2c_inform":      "305502010104067075626c6963a648020470af5c1e020100020100303a300e06082b06010201010300430203093017060a2b06010603010104010006092b0601060301010504300f060a2b060102010202010104020104",
	"v2c_linkdown":    "30819a02010104067075626c6963a7818c020472d1e91d020100020100307e300e06082b06010201010300430230393017060a2b06010603010104010006092b0601060301010503300f060a2b060102010202010103020103300f060a2b060102010202010703020101300f060a2b0601020102020108030201023020060a2b06010201020201020304124769676162697445746865726e6574302f33",
	"v2c_private":     "30430201010406733363726574a736020411ec707f0201000201003028300d06082b060102010103004301643017060a2b06010603010104010006092b0601060301010501",
	"v2c_twoifs":      "306e02010104067075626c6963a761020424d55a960201000201003053300d06082b060102010103004301643018060a2b060106030101040100060a2b06010401868d1f00083013060a2b06010201020201020104054769302f313013060a2b06010201020201020204054769302f32",
	"v2c_unknown":     "3081b202010104067075626c6963a781a402041cb0c0bf020100020100308195300d06082b060102010103004301643018060a2b060106030101040100060a2b06010401868d1f00073015060b2b06010401868d1f0201000406001122aabbcc3012060b2b06010401868d1f020200040300ff103010060b2b06010401868d1f02030041012a3013060b2b06010401868d1f02040040040a0102033018060b2b06010401868d1f02050006092b0601060301010504",
	"v3":              "3081830201033011020425f05af6020300ffe3040100020103041c301a04088000000001020304020101020257e40403626f6204000400304d041180001f88803761e240b7f6086b000000000400a73602045dee61220201000201003028300d06082b060102010103004301643017060a2b06010603010104010006092b0601060301010501",
}

func trapFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := hex.DecodeString(trapFixtures[name])
	if err != nil || len(b) == 0 {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func TestTrapDecode(t *testing.T) {
	lo := netip.MustParseAddr("127.0.0.1")
	for _, c := range []struct {
		name, want string
		sev        int8
		ver        int
		inform     bool
	}{
		{"v2c_linkdown", "trap=linkDown ifIndex=3 ifAdminStatus=up ifOperStatus=down ifDescr=GigabitEthernet0/3 uptime=2m3s", 4, 1, false},
		{"v1_linkdown", "trap=linkDown ifIndex=7 ifDescr=Gi0/7 uptime=50s", 4, 0, false},
		{"v1_relay", "trap=1.3.6.1.4.1.99999.0.17 oid_1.3.6.1.4.1.99999.1.2=hello agent=192.0.2.5 uptime=1s", 5, 0, false},
		{"v2c_bgp", "trap=bgpBackwardTransNotification peer=10.0.0.1 bgpPeerLastError=0x0604 bgpPeerState=idle uptime=1m30s", 4, 1, false},
		{"v2c_clog", `trap=clogMessageGenerated clogHistFacility=LINK clogHistSeverity=error clogHistMsgName=UPDOWN clogHistMsgText="Interface Gi0/1, changed state to down" uptime=1s`, 3, 1, false},
		{"v2c_unknown", "trap=1.3.6.1.4.1.99999.0.7 oid_1.3.6.1.4.1.99999.2.1.0=00:11:22:aa:bb:cc oid_1.3.6.1.4.1.99999.2.2.0=0x00ff10 oid_1.3.6.1.4.1.99999.2.3.0=42 oid_1.3.6.1.4.1.99999.2.4.0=10.1.2.3 oid_1.3.6.1.4.1.99999.2.5.0=linkUp uptime=1s", 5, 1, false},
		{"v2c_twoifs", "trap=1.3.6.1.4.1.99999.0.8 ifIndex=1 ifDescr=Gi0/1 ifDescr.2=Gi0/2 uptime=1s", 5, 1, false},
		{"v2c_inform", "trap=linkUp ifIndex=4 uptime=7s", 5, 1, true},
		{"v2c_private", "trap=coldStart uptime=1s", 5, 1, false},
		{"cisco_ccm_cli", "trap=ccmCLIRunningConfigChanged ccmHistoryRunningLastChanged=7d10h12m53s ccmHistoryEventTerminalType=virtual ccmHistoryEventTerminalUser=la uptime=7d10h12m53s", 5, 1, false},
		{"cisco_ccm_event", "trap=ciscoConfigManEvent ccmHistoryEventCommandSource=commandLine ccmHistoryEventConfigSource=running ccmHistoryEventConfigDestination=startup ccmHistoryEventTerminalUser=la uptime=7d10h12m53s", 5, 1, false},
		{"cisco_tcpclose", "trap=tcpConnectionClose line=1 local=192.168.255.3:22 remote=192.168.193.29:44422 tslineSesType=tcp tcpConnState=established loctcpConnElapsed=1m35s loctcpConnInBytes=4458 loctcpConnOutBytes=7443 tsLineUser=la uptime=7d10h11m49s", 6, 1, false},
	} {
		tr, err := decodeTrap(trapFixture(t, c.name))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if tr.Version != c.ver || tr.Inform != c.inform {
			t.Errorf("%s: version %d inform %v, want %d %v", c.name, tr.Version, tr.Inform, c.ver, c.inform)
		}
		msg, sev := trapMessage(tr, lo)
		if msg != c.want || sev != c.sev {
			t.Errorf("%s:\n got  %s (severity %d)\n want %s (severity %d)", c.name, msg, sev, c.want, c.sev)
		}
	}
	if _, err := decodeTrap(trapFixture(t, "v3")); err != errTrapV3 {
		t.Errorf("v3: %v, want %v", err, errTrapV3)
	}
}

// The fields the Logs page reads from a trap: the key=value reader, unchanged.
func TestTrapFields(t *testing.T) {
	tr, _ := decodeTrap(trapFixture(t, "v2c_clog"))
	msg, _ := trapMessage(tr, netip.MustParseAddr("127.0.0.1"))
	got := map[string]string{}
	for _, kv := range ldFields(&LdMsg{Msg: msg, Prog: trapProg}) {
		got[kv.Key] = kv.Val
	}
	for k, v := range map[string]string{"trap": "clogMessageGenerated", "cloghistseverity": "error", "cloghistmsgtext": "Interface Gi0/1, changed state to down", "uptime": "1s"} {
		if got[k] != v {
			t.Errorf("field %s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}

// Whatever arrives, cut short anywhere or with any byte changed, the decoder answers without a panic.
func TestTrapDecodeNeverPanics(t *testing.T) {
	for name := range trapFixtures {
		b := trapFixture(t, name)
		for n := 0; n <= len(b); n++ {
			if tr, err := decodeTrap(b[:n]); err == nil {
				trapMessage(tr, netip.Addr{})
			}
		}
		for i := range b {
			for _, x := range []byte{0x00, 0x7f, 0x80, 0xff} {
				c := append([]byte(nil), b...)
				c[i] = x
				if tr, err := decodeTrap(c); err == nil {
					trapMessage(tr, netip.Addr{})
				}
			}
		}
	}
}

func TestTrapInformResponse(t *testing.T) {
	tr, err := decodeTrap(trapFixture(t, "v2c_inform"))
	if err != nil {
		t.Fatal(err)
	}
	checkInformResponse(t, trapFixture(t, "v2c_inform"), tr.informResponse())
}

// checkInformResponse: a Response-PDU with the inform's version, community, request-id and varbinds, and no error.
func checkInformResponse(t *testing.T, req, resp []byte) {
	t.Helper()
	parse := func(b []byte) (ver, comm, pduTag byte, reqID, errStat, errIdx, vbs []byte) {
		_, msg, _, err := snmpTLV(b)
		if err != nil {
			t.Fatalf("not BER: %v", err)
		}
		_, v, rest, _ := snmpTLV(msg)
		ver = v[0]
		_, c, rest, _ := snmpTLV(rest)
		comm = byte(len(c))
		pduTag, pdu, _, _ := snmpTLV(rest)
		_, _, reqID, rest, _ = snmpTLVRaw(pdu)
		_, errStat, rest, _ = snmpTLV(rest)
		_, errIdx, rest, _ = snmpTLV(rest)
		_, _, vbs, _, _ = snmpTLVRaw(rest)
		return
	}
	v1, c1, _, r1, _, _, b1 := parse(req)
	v2, c2, tag, r2, es, ei, b2 := parse(resp)
	if tag != 0xa2 || v1 != v2 || c1 != c2 || string(r1) != string(r2) || string(b1) != string(b2) || es[0] != 0 || ei[0] != 0 {
		t.Fatalf("answer: tag %#x version %d/%d community length %d/%d request id %x/%x error %v %v varbinds same %v", tag, v1, v2, c1, c2, r1, r2, es, ei, string(b1) == string(b2))
	}
}

func TestTrapConfig(t *testing.T) {
	ok := []LogsConfig{{}, {TrapPort: -1}, {TrapPort: 1162}, {UDPPort: -1, TrapPort: 514}, {TrapCommunities: []string{"public", "n0c-Tr4ps"}}}
	for _, c := range ok {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []LogsConfig{{TrapPort: 65536}, {TrapPort: -2}, {TrapPort: 514}, {UDPPort: 1162, TrapPort: 1162}, {TrapCommunities: []string{""}},
		{TrapCommunities: []string{"has space"}}, {TrapCommunities: []string{"a", "a"}}, {TrapCommunities: []string{strings.Repeat("x", 65)}}}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v: accepted", c)
		}
	}
	var none *LogsConfig
	if none.trapPort() != 162 || (&LogsConfig{TrapPort: -1}).trapPort() != 0 || (&LogsConfig{TrapPort: 1162}).trapPort() != 1162 {
		t.Error("trap port")
	}
	if !(&LogsConfig{}).trapCommunityOK("anything") || (&LogsConfig{TrapCommunities: []string{"a"}}).trapCommunityOK("b") || !(&LogsConfig{TrapCommunities: []string{"a"}}).trapCommunityOK("a") {
		t.Error("communities")
	}
	if !mcpSecret("trap_communities") {
		t.Error("trap communities are not hidden from MCP callers")
	}
}

// End to end over UDP: a listed sender's traps are kept, an inform is answered from the address it was sent to, and a trap from a
// sender not listed, with another community or in SNMPv3 is not kept.
func TestTrapCollector(t *testing.T) {
	s := dropService(t, nil)
	s.expNets = []ldSenderNet{{p: netip.MustParsePrefix("127.0.0.1/32"), name: "lab-sw"}}
	s.testCfg.Store(&LogsConfig{Enabled: true, TrapCommunities: []string{"public"}})
	lc := net.ListenConfig{Control: ldListenControlUDP}
	pc, err := lc.ListenPacket(t.Context(), "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go s.readUDP(pc, "trap")
	to := pc.LocalAddr().(*net.UDPAddr)
	send := func(from string, b []byte) *net.UDPConn {
		c, err := net.DialUDP("udp4", &net.UDPAddr{IP: net.ParseIP(from)}, to)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Write(b); err != nil {
			t.Fatal(err)
		}
		return c
	}
	unread0, comm0, refused0, traps0 := ldStats.trapUnread.Load(), ldStats.trapCommunity.Load(), ldStats.refused.Load(), ldStats.traps.Load()
	send("127.0.0.1", trapFixture(t, "v2c_linkdown")).Close()
	send("127.0.0.1", trapFixture(t, "v1_linkdown")).Close()
	send("127.0.0.1", trapFixture(t, "v3")).Close()
	send("127.0.0.1", trapFixture(t, "v2c_private")).Close()
	send("127.0.0.2", trapFixture(t, "v2c_bgp")).Close()
	c := send("127.0.0.1", trapFixture(t, "v2c_inform"))
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, err := c.Read(buf) // a connected socket: only an answer from the address the inform went to gets here
	if err != nil {
		t.Fatalf("no answer to the inform: %v", err)
	}
	checkInformResponse(t, trapFixture(t, "v2c_inform"), buf[:n])
	deadline := time.Now().Add(3 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		if got = keptMsgs(t, s); len(got) >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	want := []string{"trap=linkDown ifIndex=3 ifAdminStatus=up ifOperStatus=down ifDescr=GigabitEthernet0/3 uptime=2m3s", "trap=linkDown ifIndex=7 ifDescr=Gi0/7 uptime=50s", "trap=linkUp ifIndex=4 uptime=7s"}
	if strings.Join(sortedCopy(got), "\n") != strings.Join(sortedCopy(want), "\n") {
		t.Fatalf("kept:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if d := ldStats.trapUnread.Load() - unread0; d != 1 {
		t.Errorf("not read: %d, want 1 (the v3 one)", d)
	}
	if d := ldStats.trapCommunity.Load() - comm0; d != 1 {
		t.Errorf("refused for the community: %d, want 1", d)
	}
	if d := ldStats.refused.Load() - refused0; d != 1 {
		t.Errorf("refused senders: %d, want 1 (127.0.0.2)", d)
	}
	if d := ldStats.traps.Load() - traps0; d != 3 {
		t.Errorf("traps kept: %d, want 3", d)
	}
	a := s.store.Search(t.Context(), LdQuery{From: time.Now().Add(-time.Hour).UnixMilli(), MinSev: -1, Limit: 10})
	for _, m := range a.Msgs {
		if m.Prog != trapProg || m.Via != "trap" || m.Src != "127.0.0.1" {
			t.Errorf("kept as program %q via %q from %q", m.Prog, m.Via, m.Src)
		}
	}
}

func sortedCopy(a []string) []string {
	b := append([]string(nil), a...)
	for i := range b {
		for j := i + 1; j < len(b); j++ {
			if b[j] < b[i] {
				b[i], b[j] = b[j], b[i]
			}
		}
	}
	return b
}

// On a socket for every address (as the collector listens when log_listen is empty), the answer to an inform sent to one of the
// node's other addresses (a VIP) comes from that address, not from the one the route would pick.
func TestTrapInformFromTheAddressItWasSentTo(t *testing.T) {
	s := dropService(t, nil)
	s.expNets = []ldSenderNet{{p: netip.MustParsePrefix("127.0.0.0/8"), name: "lab"}}
	lc := net.ListenConfig{Control: ldListenControlUDP}
	pc, err := lc.ListenPacket(t.Context(), "udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go s.readUDP(pc, "trap")
	port := pc.LocalAddr().(*net.UDPAddr).Port
	c, err := net.DialUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")}, &net.UDPAddr{IP: net.ParseIP("127.0.0.53"), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write(trapFixture(t, "v2c_inform")); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, err := c.Read(buf) // connected to 127.0.0.53: an answer from 127.0.0.1 would not get here
	if err != nil {
		t.Fatalf("no answer from 127.0.0.53: %v", err)
	}
	checkInformResponse(t, trapFixture(t, "v2c_inform"), buf[:n])
}

// systemd-resolved answers a reverse lookup of the host's default gateway with "_gateway": not a name for the Logs page.
func TestRealNames(t *testing.T) {
	got := realNames([]string{"_gateway", "_outbound.", "switch2.cush.local.", "_localdnsstub", "_LocalDNSProxy"})
	if len(got) != 1 || got[0] != "switch2.cush.local." {
		t.Fatalf("%v", got)
	}
	if realNames([]string{"_gateway"}) != nil {
		t.Fatal("_gateway kept")
	}
}
