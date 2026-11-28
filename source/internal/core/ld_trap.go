package core

// SNMP traps into the log.  The collector also listens on UDP 162 (settings.logs.trap_port; -1 turns it off) on the same addresses
// as syslog: the gateways' VIPs, the further and anycast addresses, or every address of the node.  A trap is accepted only from a
// listed sender, like syslog, and, when settings.logs.trap_communities lists any, only with one of those communities.  It is kept as
// a log message of the program "snmptrap":
//
//	trap=linkDown ifIndex=3 ifDescr=Gi0/3 ifAdminStatus=up ifOperStatus=down uptime=3d4h5m6s
//
// so every field the Logs page reads from key=value text (search, values over time, group by, pattern alerts, drop rules) works
// on traps as it is.  SNMPv1 and v2c traps and v2c informs are read; an inform is answered (the same varbinds back, as RFC 3416
// says), so the sender stops sending it again.  SNMPv3 is not read yet: it is counted as not read and dropped.
//
// Names come from a table of the traps network equipment sends (ldTrapNames) and the objects they carry (ldTrapObjects), with
// their enumerations, so linkDown's ifOperStatus reads "down" and not "2".  What is not in the table keeps its numbers: the trap
// as its OID (trap=1.3.6.1.4.1.9.9.999.0.1), an object as oid_<OID>=value.  The severity is the table's (link down: warning;
// cold start: notice; a failed power supply: critical), notice for a trap it does not know; Cisco's clogMessageGenerated takes
// the severity of the syslog message it carries.

const defaultTrapPort = 162
