# Changelog

## [v50] - 2026-11-28 — MCP: list_nodes on a node with no cluster, logs_shipping in the instructions, src= no longer hides src_ip

### Fixed
- **`list_nodes` on a node that is not in a cluster** said `primary: false`, `paused: false` and `addr: ""`, while `get_topology` called the same node the primary and showed it paused. The MCP layer built that node with only its name filled in (`mcpCtx.peers`, `mcp.go`). It now reports it as the primary of a cluster of one (`role: "primary"`, `primary: true`), with its real pause (Operate ▸ Node) and update state, and no `addr` (it has no cluster address). In a cluster nothing changes.
- **Fields: `src=` hid a message's own `src_ip`.** For a message with both, e.g. `src=2001:db8::1 src_ip=10.0.0.7`, `src=` filled in `src_ip` under the alias first and the real `src_ip=10.0.0.7` was then dropped as a duplicate, so `fields=true` showed the wrong address and a search for `src_ip=10.0.0.7` missed the message (the same for `dst`/`dst_ip` and every other alias). A field the message has under its own name now replaces one an alias filled in (`ldFieldSet.alias`, `ld_fields.go`, no allocation); between two real fields the first still counts, and an alias still fills in a field the message does not have.

### Changed
- `list_nodes` gives each node a `role` (`primary` or `replica`), as its description already said; `primary` stays for callers that read it. `addr` is left out for a node that is not in a cluster; the description says so.
- The MCP instructions (`initialize`) name `logs_shipping` (where each node sends the logs on to); it was in `tools/list` but not in the instructions. README (the MCP table and tool list) and CLAUDE.md list it too.

### Added
- `TestMCPLogTools`: the log tools through `/mcp` on real messages (there was no such test; flowdir has `TestMCPFlowTools`): search, severity, `q` with a field and `fields=true`, tail, stats with a field, status, senders, shipping, a bad severity refused, and no public IPv4 or IPv6 address, e-mail domain or public AS number in any answer, in the message or its fields.
- `TestMCPListNodesStandalone` (primary, role, no `addr`, pause seen as `get_topology` sees it) and `TestFieldAliasDoesNotHideRealField` (both orders, alias still fills in, the first of two real fields counts, a search by the real value finds the message). Both fail on v49.

### Verified
- gofmt, `go build`, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go vet ./...` and `go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64 (cross builds use the PAM stub; only amd64 is built with PAM).
- `initialize`, `tools/list` and every tool's `tools/call` were read through the test server before and after the change, for read-only and read-write callers.

### Not verified
- `list_nodes` in a real multi-node cluster (no live daemons run; the cluster path is unchanged and covered by the existing tests).
- No GUI change; not opened in a browser.

## [v49] - 2026-11-26 — SNMP traps: Cisco MAC moves

### Added
- Names for CISCO-MAC-NOTIFICATION-MIB (`snmp-server enable traps mac-notification`), from a trap a live switch sent to v48:
  - `cmnMacMoveNotification` (was `trap=1.3.6.1.4.1.9.9.215.2.0.2`), notice: `cmnMacMoveAddress=42:ec:e8:f5:6a:15 cmnMacMoveVlanNumber=193 cmnMacMoveFromPortId=13 cmnMacMoveToPortId=20 cmnMacMoveTime=7d10h34m11s`. The port numbers are bridge ports (`dot1dBasePort`), which on most Catalyst switches are not the ifIndex;
  - `cmnMacChangedNotification` (notice) and `cmnMacThresholdExceedNotif` (warning), names only: their objects stay `oid_…`.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- A trap made with net-snmp with the OIDs and values of the switch's decoded to the exact message above; the other thirteen unchanged.

### Not verified
- Not sent from the switch itself after the change. The objects of the other two MAC traps were left unnamed rather than named from memory.

## [v48] - 2026-11-26 — SNMP traps: more Cisco names, no "_gateway"

### Added
- Trap names, from traps a live IOS-XE switch sent to v47:
  - `ccmCLIRunningConfigChanged` (was `trap=1.3.6.1.4.1.9.9.43.2.0.2`) and `ccmCTIDRolledOver`;
  - the config-change objects that were `oid_…`: `ccmHistoryEventTerminalUser` (who), `ccmHistoryEventTerminalType` (`virtual`, `console`…), terminal number and location, the source address, and `ccmHistoryRunningLastChanged` / `…LastSaved` / `ccmHistoryStartupLastChanged` as times (`7d10h12m53s`);
  - `tcpConnectionClose` (a telnet or SSH session ended, `snmp-server enable traps tty`; was `trap=1.3.6.1.4.1.9.0.1`), info: `line=1 local=192.168.255.3:22 remote=192.168.193.29:44422 tslineSesType=tcp tcpConnState=established loctcpConnElapsed=1m35s loctcpConnInBytes=4458 loctcpConnOutBytes=7443 tsLineUser=la`;
  - `reload` (Cisco), warning.
- Objects that hold hundredths of a second but are sent as plain numbers are written as times too.

### Fixed
- **Sender DNS name `_gateway`**: on a host using systemd-resolved, the reverse lookup of the host's default gateway answered `_gateway`, a name systemd-resolved makes up, and the Logs page showed it. It (and `_outbound`, `_localdnsstub`, `_localdnsproxy`) is no longer taken as a name; the other lookups are tried, and the address shows without a name if none has one.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- Three traps made with net-snmp with the OIDs, indexes and values of the switch's (from its CSV export): each decoded to the exact message and severity listed above. The ten v47 traps unchanged.
- The name filter on `_gateway`, `_outbound.`, mixed case and a real name.

### Not verified
- Not sent from the switch itself: the types the switch used for the times are inferred from how v47 showed them (plain numbers).
- The enumerations of `tslineSesType` (OLD-CISCO-TS-MIB) are from memory of the MIB, not checked against the file.
- FortiGate's own traps (FORTINET-FORTIGATE-MIB) are still `oid_…`.

## [v47] - 2026-11-26 — SNMP traps into the log

### Added
- **SNMP traps and informs are collected like syslog.** The collector listens on UDP 162 on the same addresses as syslog (VIPs, further and anycast addresses, the node's own), and only for the senders listed under Configure ▸ Senders; any other source is refused and counted, as for syslog. SNMP v1 and v2c traps and v2c informs are read (v1 converted as RFC 3584 says). **An inform is answered**, from the address it was sent to (a VIP answers as the VIP), so the device stops resending it.
- A trap is stored as a message from the program `snmptrap`, received via `trap`, written as fields the Logs page already searches, groups and charts: `trap=linkDown ifIndex=3 ifAdminStatus=up ifOperStatus=down ifDescr=GigabitEthernet0/3 uptime=2m3s`. Search them with `program=snmptrap`, `trap=linkDown`, `ifdescr=Gi0/3`; drop rules, pattern alerts and shipping see them like any message.
- **Names without MIB files**: a built-in table names the SNMPv2 generic traps, IF-MIB, BGP4 (both numberings) and Cisco BGP, OSPF, VRRP, ENTITY, RMON, UPS, PoE, LLDP, Cisco config, environment and syslog (`clogMessageGenerated`) traps, and Juniper chassis traps, with their objects and the values that are numbers on the wire (`ifOperStatus=down`, `bgpPeerState=idle`). An interface, peer or neighbour index is written once in front (`ifIndex=3`, `peer=10.0.0.1`). Anything else is written `oid_<OID>=value`; MAC addresses as `00:11:22:aa:bb:cc`, other bytes as hex, time ticks as `2m3s`.
- Severity: warning for traps that report a failure or something going down, notice for the rest; a Cisco syslog trap keeps the device's own severity.
- **Settings ▸ General ▸ Log collector**: *SNMP trap port* (`settings.logs.trap_port`: 162 by default, -1 turns it off, not the syslog UDP port) and *Trap communities* (`settings.logs.trap_communities`: up to 32; empty accepts any community from a listed sender; hidden from MCP callers). A trap or inform with another community is dropped, not answered, and counted.
- `logdir --logs` counts traps, and on a second line those not read (SNMPv3 or not SNMP) and those with a community not listed. A problem trap is logged once per sender every 10 minutes.
- Help: Configure: Senders ▸ SNMP traps, and the two fields. README: Log collector ▸ SNMP traps.

### Fixed
- Settings: saving the page wrote `mem_max_percent: 50` (the default) into the configuration; the page compared it with 70. The README said 70 and "5 points above" for Go's memory limit: it is 50 and 20 points (at most 92 %), as the code and the help say.

### Upgrading
- **The trap listener is on by default** wherever the collector runs. If snmptrapd (or anything else) holds UDP 162 on a node, that node logs that it cannot listen on trap 162 and the rest works; stop snmptrapd or set another trap port.
- A version before v47 refuses a configuration that has `trap_port` or `trap_communities`: upgrade every node before setting them.
- **SNMPv3 is not read yet**: such traps are counted as not read and logged, not stored.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- One run of `CGO_ENABLED=0 go test ./...` failed while a `-race` run had just loaded the machine; its output was cut before the test's name. The next two runs passed. Probably the load-dependent `TestClusterLogsHostStrain` noted in v46, but not confirmed.
- Unit tests on ten traps net-snmp 5.9.4 sent, captured off the wire (v1 and v2c, generic, BGP, Cisco syslog, an unknown enterprise trap with a MAC, bytes, a counter, an address and an OID value, two interfaces, an inform, SNMPv3, another community): the exact message and severity of each; the Logs page's field reader on the result; every cut-short and every byte changed to 00, 7f, 80 and ff of every trap decoded without a panic; the inform's answer a Response with the same version, community, request id and varbinds.
- Over real UDP sockets: listed sender kept (program `snmptrap`, via `trap`), another sender refused, another community refused, SNMPv3 counted as not read, the inform answered. On a socket for every address, an inform sent to 127.0.0.53 was answered from 127.0.0.53; with the answer sent without the source address the same test fails (checked).
- Live: a daemon on port 162 with communities `public` and `n0c`. `snmptrap` v2c and v1 kept; `snmpinform -c public` exited 0 (answered), `-c wrong` timed out; v3 counted; `logdir --logs` showed 4 traps, 1 not read, 1 with another community. In headless Chromium: `program=snmptrap` listed the traps with their fields and severities, `ifdescr=GigabitEthernet0/3` found it; Settings showed the trap port and both communities, and changing the port to 1162 saved it (the communities kept) and the node moved its listener to 1162 at once.

### Not verified
- Not tried with a real router or switch; the traps were made with net-snmp to match what Cisco and Juniper send.
- SNMPv3 (to come), and informs over IPv6 (the sandbox has no IPv6; the IPv6 control message is built the same way).

## [v46] - 2026-11-26 — Logs: the bookmark before the disk

### Changed
- Logs ▸ Messages: the two icons after the filter box swapped places: the **bookmark** (saved searches) comes first, then the **disk** (save). Help ▸ Logs says it in that order.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- In one `-race` run of the whole suite `TestClusterLogsHostStrain` failed its first check (a quiet host logs no strain): it reads this machine's own load, which the rest of the suite was raising. Alone with `-race` it passed 5 of 5, and the run without cgo passed; not about this change, still to be made independent of the machine's load.

### Not verified
- Not opened in a browser: only the order of the two buttons in the header changed (their styles do not depend on it).

## [v45] - 2026-11-26 — Logs: a search that takes a while says so; save and saved searches beside the filter

### Added
- **Logs: "Searching… N s" next to the menus** (a spinner and the seconds so far) while a load runs longer than 0.7 s; a quicker one shows nothing, so the page does not flicker. The cards it will replace are dimmed (their bodies and the count tiles; the headers and the filter box stay bright, so typing is not dimmed) and marked `aria-busy` until the new answer for each comes in: the messages when the search answers, the tiles, chart and busiest lists when the counts do, the values over time last. A new filter typed while one is still out keeps the counter up and counts from the new one.
- **A red ✕ beside it stops the search**: the request is dropped (the daemon stops reading), what was shown before stays and the counter says *Stopped* until the next load. Plain red text, a real button underneath (keyboard and screen readers reach it as *Stop search*).
- The 15-second refresh shows the counter when it is slow but dims nothing (what is shown is still right).

### Changed
- **Save and the saved searches moved from the top bar to right after the filter box**, as two small grey icons: a **disk** saves what the page shows (the same dialog as before; hidden for read-only users) and a **bookmark** opens the list of saved searches under it, each with what it holds below its name and a ✓ on the one the page shows; the bookmark is filled when the page shows a saved search, and its tooltip names it. The list closes on Esc (back to the bookmark), a click elsewhere, the bookmark again, the window losing focus or changing size, and leaving the page; up and down move through it. Both icons are real buttons (*Save search*, *Saved searches*). The top bar keeps the range and severity.
- **The Messages header stays on one line**: the filter box is 22rem (was 36rem) and gives up width first, down to 9rem, so the count, the pages, the page size and CSV stay beside it; below a 1085 px window it wraps, as before. "1–25 of 400" and "of 16" no longer break inside themselves. The page size shows just the number (**25**, not *25 per page*; its tooltip says *Messages per page*).
- Help ▸ Logs: the counter, the ✕, the disk and the bookmark.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- Live, a daemon with a real PAM sign-in and 800 messages over syslog UDP, in headless Chromium (light and dark):
  - With the search and counts held back 3 s by the browser: nothing shown at 0.3 s; at 1.6 s *Searching… 1 s* with the ✕, five areas dimmed and `aria-busy`; the card bodies and tiles dimmed, the headers and filter box not; the ✕ red with no background or border; all gone when the answer came. A second filter while the first was out: still shown, counting from the new one. ✕: *Stopped*, nothing dimmed. A load without the delay showed nothing. No page errors.
  - Header on one line at 1086, 1100, 1300, 1440 and 1920 px with the pager and CSV showing, nothing past the card's edge (the filter box 150, 164 and 352 px); wrapped at 1085 and 1060.
  - The top bar has only the range and severity menus. The bookmark with none saved: *No saved searches*; Esc closed it and put focus back on it. Three searches saved with the disk; the bookmark filled with the saved one on screen; the list sorted, ✓ and focus on the current one, its right edge under the bookmark's; down and Enter opened the next (the filter changed, the list closed). A click elsewhere, the bookmark again and going to another page each closed it.

### Not verified
- The delay for the counter was made in the browser, not timed against a node with a real day of data.
- Not tried with a screen reader.

## [v44] - 2026-11-26 — BGP: AS Prepend from 0 to 8, and BFD on or off

### Changed
- **AS Prepend is a drop-down from 0 to 8** (Anycast ▸ BGP, was a tick box that prepended three times). **0 (off)** is the default; 1 to 8 add the local AS that many more times to every anycast announcement (`set as-path prepend ASN …` in both outbound route-maps), so several backup nodes can be given an order. Kept as `as_prepend_count` in the node's config, written only when not 0: a config that does not use it stays readable by v43 and before, but one that does is refused by them (they refuse keys they do not know), so update every node you set it on before going back. A config with the old `"as_prepend": true` reads as 3 (what on did) and is written back with the new key only. CLI: `--as-prepend 0-8` (`on` and `off` still work, as 3 and 0); `--bgp` prints the number.
- `--bgp` and the other BGP commands print the daemon's answer fresh: a setting the answer leaves out (because it is the default) no longer shows the value from before the change.

### Added
- **BFD: Yes or No**, beside AS Prepend. **Yes** (the default, as before) runs BFD to every neighbor (`neighbor … bfd`); **No** leaves those lines out of `frr.conf`, for a router that does not run BFD. Kept as `bfd_off`, written only when No (the same rule for older versions as above); v41's old `bfd` key does not turn it off. CLI: `--bfd yes|no`; `--bgp` says *BFD on every neighbor* or *BFD off*.
- Help ▸ Anycast: both fields, the backup steps and the command line.
- Both apply when saved: `frr.conf` is rewritten and FRR reloaded, with the BGP sessions kept (no daemon is switched on or off). Without `frr-reload.py` (package `frr-pythontools`) FRR is restarted instead, as for any BGP change, and the sessions drop for a few seconds.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go vet ./... && go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- New tests: `TestRenderFRRASPrepend` (1, 3 and 8 times in both outbound route-maps, none by default, inbound unchanged); `TestBGPPrependCount` (0 to 8 accepted, -1, 9 and 100 refused, old `as_prepend: true` reads as 3 and is written back as `as_prepend_count`, the new key wins over the old); `TestRenderFRRBFD` (on by default for every neighbor, off leaves no `bfd` line, an old `bfd: false` does not turn it off, the default is not written).
- Live, a node whose config had `"as_prepend": true`, in headless Chromium (light and dark): AS Prepend showed 3 with 0 (off) and 1–8 to choose from; choosing 5 saved `as_prepend_count: 5` and dropped the old key; BFD showed Yes, choosing No saved `bfd_off: true`; no page errors. CLI: `--as-prepend 8` printed 8, `9` was refused, `off` removed the key; `--bfd no` / `yes` printed *BFD off* / *BFD on every neighbor*, `maybe` was refused.

### Not verified
- Not run against FRR (not installed here): the route-maps and neighbor lines are checked in the generated `frr.conf` only. FRR's bfdd keeps running with BFD set to No (it serves OSPF too); it just has no BGP sessions.

## [v43] - 2026-11-26 — Logs at 400 devices: a fast 1-day view, a node that stays within its memory, faster Values

A load test (400 devices: 40 firewalls, 60 routers, 300 switches; 500 messages a second, 43.2 million a day, 2.4 GB on disk) found three problems in v42: over 1 day the counts and chart read every message and stopped at 30 s with part of the total; a node started with a full day grew past its memory limit and was killed; and the Values card counted about 365,000 messages a second, so a day did not finish. Receiving was not a problem: up to 40,000 messages a second from 400 senders were all stored.

### Changed
- **1 day is drawn in bars of an hour** (24 bars, was 60 of 24 minutes). From a span of 12 hours up, bars are whole hours, so closed hours come from their rollups instead of being read. `ldSeriesStep`: 12 h and 1 d give 3600 s; 6 h still 6 minutes.
- **The hour being written has a rollup kept as messages arrive** (`ld_rollup.go`: `liveAdd`, `liveSnapshot`), so the counts and chart up to now do not read it either. It is used only when it counts every message of the hour (not after a restart during the hour: that hour is read as before) and the range runs to now; filters with words still read the hour.
- **Memory limit: the daemon's own cgroup.** The limit was read only from `/sys/fs/cgroup/memory.max` (or v1 `memory/memory.limit_in_bytes`), which is the root's, so a limit on the service (systemd `MemoryMax=`) or a group above it was missed; the test node saw 8 GB with 6 GB allowed. Now it follows `/proc/self/cgroup` and takes the smallest limit of its group, the groups above it and the root (v2 and v1). Monitor ▸ Node's memory figure uses the same.
- **The hour cache defaults to 50 % of memory (was 70 %), and Go's memory limit is 20 points above it (was 5), at most 92 %.** With the cache full, 5 points left the garbage collector running all the time and every search crawled. An hour read into the cache is sized from the index (it grew by doubling, up to twice what it held), and its size is counted with what was allocated. An hour the cache would not keep is read line by line, not parsed whole and let go. Help ▸ Configure ▸ Senders states the defaults (memory 50, disk 90; it said 90 and 70).
- **Counting over many hours reads several at once** (as many as a search: about half the cores, at most 8), each with its own part of the answer, added up at the end; those reads do not fill the cache, so a long range does not push the newest hours out.
- **The values of a field are kept per closed hour** (`ld_hits.go`: `fieldPut`, under a tenth of the cache's cap, at least 64 MB, oldest hours out first), unsealed, so they add up exactly as reading would. The page's refresh reads only the current hour, and **Over time** takes its lines from the counts the card's count kept. A part that measured *Different* or *Sum* also serves the plain count; one that did not is made again when a measure is asked.
- Reading a message's fields no longer builds a map per message (a look along the list instead) or a byte set per key.
- Help ▸ Logs: the Values card's long ranges.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go vet ./... && go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- New tests: `TestLiveSum` (the newest hour's counts from its rollup equal reading it, with severity, host and words; with the hour's file moved away the counts are still there and words find nothing; after a restart during the hour it is read); `TestStatsParallel` (one hour at a time and several at once give the same answer for counts, series, values, exact and estimated *Different*, and sums, with and without words; kept parts give the same answer as reading, with the closed hours' files moved away, for bars of 1 and 2 hours and none; Over time from a measured count's parts equals reading; a count-only part does not answer *Different*); `TestCgroupOwnGroup` (a service's limit, a slice's, the smaller wins, unlimited levels skipped, v1 under the memory controller); `TestSeriesStep`, `TestGoMemLimit` and the cache test updated for the new defaults.
- Load test, same day of data, 2-core sandbox limited to 6 GB, default settings:
  - v42: killed 30 s after start (6.3 GB); with the cache at 50 %, 1-day counts and chart 30 s and partial (11 of 43 million), filters 20–33 s every time, Values over 1 hour 2 s, over 6 hours and 1 day partial at 10 and 30 s.
  - v43: cache cap 3.2 GB (50 % of the 6 GB limit), 3.3 GB resident after warming in 15 s, steady. 1-day counts and chart 0.8 s, complete (7 days 0.4 s). Filters over a day 11–17 s the first time, 0.7–3 s after. Values (*Different dst_port* by source) over a day: the first two loads stop at 30 s (the hours they finish are kept), then 2–3 s; over 6 hours 9 s; Over time 2.5 s. A day of field counting runs at about 0.8–1 million messages a second on these 2 cores (v42: 0.37 million).
  - Receiving at 10,000 and 40,000 messages a second for 30 s each: all stored, nothing dropped by logdir or the kernel.

### Not verified
- Not run on more than 2 cores, where the Values card should be faster in proportion, or with more than one node. The Values card's first load over a day still stops at the 30 s limit on a node this small; the next load carries on from the hours kept. The first 1-day view after a restart reads the partial hour at each end (about 1 s here). Field summaries made when an hour is compressed (so even the first load is fast) are not done.

## [v42] - 2026-11-26 — Logs: saved searches, values over time, and count, different and sum per value

### Added
- **Saved searches (`ld_saved.go`).** The Logs page's toolbar has a **Saved searches** menu and **Save…**. A saved search keeps the filter, range, severity, host, program and sender, and the Values card's field, Count choice and Over time; picking it puts all of them back. They are shared by everyone on the cluster, kept in `settings.logs.saved` (at most 200, names unique ignoring case, each with who saved it and when). The Save popup names it, saves it again under the same name (replacing it), renames it, or deletes it. The menu shows the saved search that matches the page, and goes blank after a change. `GET /api/logs/saved` lists them; `PUT /api/logs/saved` takes `{save:{…}, rename, delete}`, reads the live config and retries if another change lands in between. Read-only users see and use them but cannot save (no Save…, and the API refuses).
- **Count per value.** The Values card has a **Count** menu: *Messages* (as before), *Different X* (how many different values of another field each value has, such as how many destination ports one source address reached) and *Sum of X* (for fields with numbers, such as bytes). The column shows the result and the card sorts by it. Up to 128 different values per row are counted exactly; above that it is an estimate (HyperLogLog, about 1.6% either way), shown as **≈** with a tooltip. Each node sends its counts or sketches and the node that asked merges them, so a value seen on two nodes is counted once. The API takes `measure=distinct:F` or `measure=sum:F` with `field`, and each value gets `distinct` or `sum`.
- **Over time.** The Values card's **Over time** toggle draws its top 5 values as lines over the range (legend, CSV download). The API takes up to 10 `values=` with `field` and answers `value_series`.
- `host`, `program`, `sender` and `severity` can be used as fields (Values card, Count menu, API), not only fields parsed from the message.
- The MCP `logs_stats` tool takes `measure` and `values` (comma-separated).
- Help ▸ Logs explains the Count menu, Over time and saved searches.

### Changed
- A toggle button that is on (Over time) is drawn highlighted in both themes.
- A node older than v42 answers a Count of *Different* or *Sum* with message counts only; its share is left out and the page's note says how many nodes that was.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go vet ./... && go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- New tests: `TestLdAggSketch` (estimate within 5% from 129 to 200,000 values, encode and decode, merging sketches is the union, exact sets merge exactly, bad encodings refused, measure parsing); `TestStatsMeasure` (two stores: a scanner reaching ports 1–3000 on one and 2001–5000 on the other gives about 5000, another source exactly 2; value series; sum of bytes by host 1007 and 350.5 with numeric counts; an older node is noted); `TestStatsMeasureParams`; `TestSavedSearches`; `TestSavedSearchesWeb` (save, a bad severity is 422, an empty body 400, the config keeps who and when, list, delete, no CSRF token is 403).
- Live, two nodes with 250 ms round trips between them (as in v41): a scanner reaching ports 1–1000 on node A and 501–1500 on node B showed **≈ 1,499** different ports (true: 1,500); sums of bytes and the 61 points of the value series matched the messages sent. In headless Chromium (light and dark): the Count menu, the ≈ tooltip, Over time with 5 lines and a legend, Save, the menu after a reload putting back the filter, range, Count and chart, rename and delete; no page errors.

### Not verified
- Not run over a real WAN or with more than two nodes. A field count reads the messages of each hour one after the other (no rollups), so *Different* and *Sum* over 7 or 30 days on a busy log are slow. Each node sends sets or sketches for at most its 200 values with the most different values; a value that is past that on every node is shown with its message count but a low *Different*. A message received by two nodes is counted on both, as before.

## [v41] - 2026-11-26 — Cluster: searches over far links in one round trip, and results shown as each node answers

### Changed
- **Connections between nodes are kept open (`clusterpin.go`).** Every call to another node opened a new TCP and TLS connection and closed it afterwards (keep-alive was off), so each search, count and refresh paid the handshakes on top of the question itself: on a link with 250 ms round trips, about 750 ms before the far node started reading. Now one transport per peer address, pinned fingerprint and own identity certificate keeps up to 8 idle connections for 90 s; every connection is still made by the pinned dial, so it is verified against the pinned certificate exactly as before, and a kept connection is never shared between fingerprints or identities. The peer server keeps an idle connection 2 minutes (was 30 s). A call sent on a kept connection that the peer has just closed is sent again once on a new one (`X-Idempotency-Key`, the request's nonce, so the peer acts on it at most once). The rest of a response the caller did not read is drained, so the connection can be used again. This covers every call between nodes, not only the logs.

### Added
- **The Logs page shows each node's answer as it arrives.** `/api/logs/search` and `/api/logs/stats` take `stream=1`: the answer is `application/x-ndjson`, one line per part (`{"ok":true,"data":…,"done":false}`), each the whole answer so far, merged and cut as before, and the last with `"done":true` is the same as the answer without `stream`. Nodes that answer within 20 ms of each other go out as one line, and a slow reader never holds up the nodes. The page draws every part: the counts, chart and busiest cards, and the list, where a far node's messages take their place by time. While nodes are out the Messages card says **1 of 2 nodes…** (hover: which). Answers without `stream`, the CLI and the MCP tools are unchanged.
- **Time per node:** a search or count over more than one node has `node_ms` (how long each node took, as the node that asked saw it, the link included) and, in a part before the last, `pending`. Once all have answered the Messages card says **2 nodes**; hover it for the times.
- Help ▸ Logs explains both.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go vet ./... && go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- New tests: `TestPinnedClientKeepsConnection` (5 calls open one connection, plus the one that learns the certificate; another own identity opens its own; another fingerprint is refused; with keep-alive off it fails: 6 connections); `TestStreamedSearchParts` (two cluster nodes, one answering 400 ms late: the first part holds this node's messages and names the other as pending, well before 400 ms; the last part has both and no pending; `node_ms` shows which was slow; the plain search is the same; over HTTP the first line of `stream=1` arrives before the far node answers, and it does not without the flush; a node that is down is reported and the answer still ends).
- Live, two nodes on this host with a proxy holding every packet 125 ms each way (250 ms round trip) in front of node B, 400 messages, the Logs page's two requests over one day: v40 about 520 ms each, v41 about 260 ms (`node_ms`: node A 0–2 ms, node B 256–260 ms). The proxy answers the TCP handshake itself, so on a real link v40 would pay one round trip more (about 770 ms) and v41 the same. With `stream=1` the first line (node A's 800 messages, B pending) came after 27 ms and the last (1000, the limit) after 285 ms. In headless Chromium (light and dark): the Messages card went from *1 of 2 nodes…* with 800 messages at about 80 ms to *2 nodes* with 1000 at about 320 ms, the hover showed each node's time, and a search typed while the far node was still out was dropped for the new one (403 messages: 200 on each node plus 3 sent during the test).

### Not verified
- Not run over a real WAN link or with more than two nodes. A page that is looking through another node (Node ▸ another node, via `/api/proxy`) gets the parts only if the proxy passes them on as they come; otherwise it gets the whole answer at the end, as before. Kept connections are closed by the peer after 2 minutes idle (30 s on a node older than v41), so the first search after a quiet spell still pays the handshakes.

## [v40] - 2026-11-26 — Logs: drop rules keep chosen messages out of the log

### Added
- **Drop rules (`ld_drop.go`).** Configure ▸ Senders has a **Drop rules** table: each rule has a name, a filter in the Logs page's language (words, phrases, `not`, `host=`, `program=`, `src=`, field terms such as `action=accept and dst_port=53`), an optional severity (that or worse, only that, or that or less severe) and an on/off switch. A message that matches an enabled rule is thrown away after it is parsed and before it is stored: it is not stored, searched, counted in the hour rollups, shipped or alerted on. Its sender is still marked heard, and the Senders table still counts every message it received. Rules are kept in `settings.logs.drop_rules` (at most 100, names unique; a rule with no filter and no severity is refused, since it would drop everything) and act on new messages only: what is already stored stays.
- **Counts:** each node counts what each rule dropped since it started and when it last did; the table shows the total over the nodes (Dropped, Last). `/api/logs/status` has `rule_dropped` and `drop_rules` per node, `--status-socket … --logs` prints a *drop rules* block, and the MCP `logs_status` description mentions them.
- **The popup** to add or edit a rule has **Preview**, which opens the Logs page with the rule's filter and severity over one day, to see what it would drop. The Logs page's right-click menu has **Drop messages like this…**, which opens the popup filled with the page's filter and severity, or with that message's program and its first words.
- **Severity "or less severe":** the Logs page's severity menu has *Warning and less severe*, *Notice and less*, *Info and debug* and *Debug only*; the API takes `severity=info-` (`-` = that or less severe, `=` = only that), as do drop rules.
- Help ▸ Configure ▸ Senders explains drop rules, and Help ▸ Logs the new menu items.

### Changed
- The shipping filter (`ld_ship.go`) and drop rules share one matcher: a selection keeps the parsed query and the sender names, and one lookup of the configured and DNS names serves every rule. Each collector goroutine uses its own copy of a rule's filter (from a pool), so matching takes no lock.
- A cluster refuses a config with drop rules while any node is older than v40 (as v37 does for shipping), since an older node would keep everything.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go vet ./... && go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- New tests (`ld_drop_test.go`): `TestDropValidate`; `TestDropRules` (16 messages over every kind of rule: words, a phrase, `not`, host, program, sender by configured name and by `src=` word, fields, each severity mode, a disabled rule; the counts; a sender whose every message is dropped is still heard; a sender heard for the first time is matched by its configured name at once, and the rules are compiled again when the number of senders changes; changing the config keeps the counts by name; no rules keeps everything); `TestDropConcurrent` (8 goroutines, `-race`); `TestDropNeedsNewNodes`; `TestSevLessSearch` (every severity in every mode, with and without words, two rounds so the kept hits are used too; a wrong hour mask makes it fail 60 checks).
- Live, one daemon with a PAM login over HTTPS: 140 messages of 7 kinds kept with no rules. In headless Chromium (light and dark): right-click a keepalive ▸ *Drop messages like this…* filled the popup with the page's filter; Preview opened the Logs page with that filter over one day; three rules made in the popup (`keepalive`; `program=lldpd` with *Info and debug*, saved as the program field; `action=accept and dst_port=53`); a rule with only a name was refused in the popup. Then 140 more messages: the 60 expected were kept (neighbor Down, the warning from lldpd, the firewall's port 443), and `/api/logs/status`, the table and the CLI all showed 20, 40 and 20 dropped. `severity=info-` returned only info and debug messages.

### Not verified
- Not run on a cluster of several nodes. A sender's DNS name takes effect in rules only once they are compiled again (within 30 s, or when a new sender is heard); its configured name takes effect at once. In a search across nodes of mixed versions, older nodes ignore `sev_less` and return that severity or worse. Drop rules do not lower network traffic: the messages are still sent to logdir and parsed.

## [v39] - 2026-11-26 — Logs: a filter over 7 and 30 days is fast, and its refresh near instant

### Fixed
- **Filtering the Logs page was slow past one day.** With a filter, the counts and chart (`/api/logs/stats`) cannot use the hour rollups, so every message of every hour in the range was read, and on a store bigger than the hour cache that meant reading the disk again on every refresh (every 15 s): about 4–5 s for 3 days in the live test below, 10 s for 7 days in the benchmark. The message list did the same for a `not` word, a field or a word that is in few messages. Four changes:
  - **Stored lines are read by a small reader of their own (`ld_line.go`)** instead of `encoding/json`, which took about three quarters of the time and made most of the garbage. It reads only what logdir writes (the keys of a message, no spaces, integers, the JSON escapes); anything else, such as a line from an older version with other keys, is read by `encoding/json` as before. Decompression and line reading reuse their buffers (pooled), and one message variable serves all the lines of an hour.
  - **Lines are ruled out from their bytes:** before a line of an hour that is not in the cache is read, it is checked for the pieces of the words and field values the filter needs (as v36 does for the severity). Pieces that JSON writes differently (quotes, backslashes, `<`, `>`, `&`, control characters), lines with a `\u` escape or with any byte outside ASCII (whose lower case may differ, such as the Kelvin sign), pieces that may be in a sender's name, and field values on the ASA's messages (which make up `action=deny`) are never used to rule a line out.
  - **What a filter with words finds in a closed hour is kept (`ld_hits.go`):** its matches when there are at most 1000, for the message list, and its counts per host, program, sender and severity, for the counts and the chart (ranges of whole hours: 7 days and more). An entry is keyed by everything the filter matches on, sender names included, used only while the hour's count is the one it was made from, removed with its hour, and all are let go past 64 MB. The refresh then reads only the open hours.
  - **Several hours are read at the same time** (about half the cores, at most 8): the counts read all the hours they need that way; the message list reads 1, 2, 4… hours at a time newest first, so a common word still reads only the newest hour. Oldest-first lists read one hour at a time, as before.
- **The page dropped no request.** Typing a filter starts a search at every pause of 600 ms; an earlier one went on to the end on the daemon (and on every node), and the refresh could start another on top of one still running. Now a new load drops the one still running (the daemon stops reading when its request is dropped, and so do the other nodes), the refresh waits while one is running, Enter after the pause does not start the same search again, and leaving the page drops it.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go vet ./... && go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- New tests: `TestDecodeLine` and `FuzzDecodeLine` (whatever the reader reads, `encoding/json` reads the same, and every line `json.Marshal` writes is read without it; 610,000 fuzz runs); `TestRawWordsNeverMiss` (every word, phrase, wildcard, upper-case word and field term of eight awkward messages, with and without sender names: no matching line ruled out; switching off the sender-name, `\u`, non-ASCII or ASA guard each makes it fail); `TestStreamLongLine` (a 2.5 MB line, longer than the reader's buffer, from an open and a compressed hour); `TestHitsSameAsReading` (192 cases: 12 filters, two start times, newest and oldest first, with sender names, a host filter or a severity: the same messages and counts as reading afresh, three rounds with everything kept, including a start 7 minutes later; renaming a sender gives other counts; a changed hour count and a removed hour are not used; switching off the time check, the whole-hour check or parts of the key each makes it fail); `TestHitsCap`. The log tests also under `-race -cpu 1,4`. `BenchmarkFilter7d` (7 days of 20,000 messages an hour, nothing cached, the page's two requests): counts with a word in every message 10.2 s → 1.9 s, in half 10.4 s → 1.2 s; a `not` word 10.0 s → 2.7 s (list) and 2.3 s (counts); a field 10.1 s → 0.6 s; a rare phrase 1.1 s → 0.05 s; the same requests again (the refresh) 4–31 ms.
- `TestShipSyslogTCP` (v37) failed once in a full race run: the receiver had the 171st message before the shipper had counted it. The test now waits for the count as it waits for the message.
- Live, one daemon with a PAM login over HTTPS, 72 hours of 20,000 messages, the hour cache set to 1% (about 80 MB) so the store does not fit, as on a server past a day; the page's two requests at once over 7 days, v38 → v39, first and again: `accept` 4.2 s and 3.9 s → 0.88 s and 0.02 s; `not accept` 4.7 s and 4.6 s → 2.2 s and 0.01 s; `dstport=3389` 4.8 s → 0.50 s and 0.01 s; `192.168.1` 4.2 s → 0.62 s and 0.02 s; `sessionid=17000123` 4.9 s → 0.77 s and 0.01 s. The messages and the counts were byte for byte the same as v38's. In headless Chromium (light and dark), typing `not accept` slowly over 7 days: the search for `not acc` was dropped when `not accept` started, Enter did not start it again, and the page showed the 6 messages.

### Not verified
- Not run on a cluster of several nodes (that a dropped request stops the other nodes' reading follows from the cluster requests carrying it, but was not watched). The first count with a word found in most messages still reads them all: about 2 s for 7 days on the 2-core test machine, so about 8 s for 30 days, less with more cores. Ranges shorter than 7 days (buckets smaller than an hour) keep no counts and read again on refresh, as before; they are short.

## [v38] - 2026-11-26 — Logs: fields from message bodies, and a word index for closed hours

### Added
- **Fields (`ld_fields.go`, `ld_expr.go`).** A message's body is read for fields when a search runs (nothing more is stored, so it works on every hour already kept): key=value (quoted values, `,` or `;` between pairs), JSON (flattened with dots, three levels, arrays skipped), CEF (the header as `cef_vendor`, `cef_product`, `cef_name`… and the extension), PAN-OS CSV (TRAFFIC and THREAT columns by name; other types give serial, type and subtype), RFC 5424 structured data, Cisco `%FAC-SEV-MNEMONIC` (`cisco_facility`, `cisco_severity`, `cisco_mnemonic`) and the ASA's deny messages 106001, 106006, 106007, 106015, 106023 and 106100 (source and destination address, port and interface, `action=deny`). Common names get an alias, so one search covers several formats: `src_ip`, `dst_ip`, `src_port`, `dst_port`, `action`, `proto`, `user` (`src`/`dst` only when the value is an address). At most 80 fields a message, names up to 64 bytes, values up to 512.
- **Field terms in the search language:** `name=value` (case-insensitive, `*` wildcards), and `>`, `<`, `>=`, `<=` with a number (`dst_port>1024`). They mix with words, `and`, `or` and `not` as before (`action=deny and not dst_port=53`). A field term inside a phrase is split out of it.
- **Logs page:** each message with fields has an *N fields* button that opens a row of chips; click a chip to add it to the filter, right-click for *Only this value*, *Not this value*, *Top values of …* and *Copy value*. *Top values* adds a **Values of <field>** card to the page with the count of different values and a × to close it. Help ▸ Logs explains fields and the index.
- **Word index (`ld_wix.go`).** Each closed hour gets a `.wix` file beside it: a bitmap of the three-letter pieces of its text (host, program, message and field values, lower-cased), folded as small as it can stay under 20% full, compressed and checksummed (a few KB an hour). A search with words skips the closed hours whose index says a word can't be there. It is made when an hour is compressed; hours from before this release get theirs by a background pass after start. An index is used only while its count matches the hour's and it was built by this field reader version; a damaged or stale one is ignored and made again. Negated words, numbers, pieces shorter than three letters and words that may match a sender's name never rule an hour out. Retention and the disk cap remove the index with its hour. Up to 64 MB of indexes are kept in memory.
- **API / CLI / MCP:** `&fields=1` on `/api/logs/search` and `/api/logs/tail` returns each message's fields; `/api/logs/stats?field=NAME` returns its top values (up to 5000 counted). `--logs-search … --logs-args fields=1` prints them under each message. `logs_search` and `logs_tail` take `fields`, `logs_stats` takes `field`, and the `q` description explains field terms.

### Changed
- `src=` and `source=` on the Logs page still mean the sender (logdir's own filter), so a firewall's source address is `src_ip=`.
- Stats with a `field` read the hours message by message (rollups don't hold field values).

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go vet ./... && go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- New tests: `ld_fields_test.go` (every format above, hostile and truncated input, a fuzz target, term parsing, searches with fields, top values, and that looking for a field's value in the text first gives exactly what reading every message's fields gives, including the ASA's made-up `action=deny`, JSON `\u` escapes and escaped CEF and key=value values; switching that ASA guard off makes the test fail); `ld_wix_test.go` (the same results with and without the index over many words, no false negatives on random text, the folded size, a damaged or truncated file is not used, a stale index is not used, the index goes with its hour).
- `BenchmarkSearchWords30d` (30 days of 20,000 messages an hour): a phrase in 72 of the hours, 4.0 s with the index against 47 s without (1.45 M messages read instead of 14.4 M); a word in one hour, 0.13 s.
- Live, one daemon with a PAM login over HTTPS, 72 hours of 20,000 generated messages written by v37 (no index): v38 made the 72 indexes in about 12 s (288 KB for 41 MB of logs). Over 7 days, `ospf neighbor` 0.35 s cold, 0.07 s warm (v37: 3.75 s and 0.45 s) with the same messages; `sessionid=17000123` 0.51 s warm (v37, as a plain substring: 0.31 s) with the same 72 messages; `dstport=3389` 0.33 s. A first build read every message's fields and took 6.4 s and 3.2 s for those two; the text is now looked at first. In headless Chromium (light and dark): the fields button and chips, filtering by a chip, *Not this value*, the *Values of* card, and `dst_port>1000`. API `dst_port=443` and `/api/logs/stats?field=dst_port`; the CLI with `fields=1`.

### Not verified
- The formats were checked against sample lines written from vendor documentation, not against real devices. The ASA's connection messages (302013 and the like) give no addresses. Not run on a cluster of several nodes. Fields are read again on every search, so a field search over a busy month is still bounded by reading the hours whose text holds the value.

## [v37] - 2026-11-26 — Log shipping: send the collected syslog on, without losing where it came from

### Added
- **Log shipping (`ld_ship.go`, `ld_ship_spoof.go`).** `settings.logs.destinations` (shared by the cluster, up to 32): each node sends on what it received itself, read from its own stored hours, to a syslog server (RFC 5424 over UDP, TCP or TLS, octet-counted), a JSON-over-TCP receiver (NDJSON, TCP or TLS), Splunk HEC, Elasticsearch (`_bulk`, *create* actions, so data streams work; a refused document fails the batch) or Loki (push API, labels job, node, host, severity, source). Every node keeps its place per destination in `<logdir>/.ship/<name>.json` (hour, line, counts): a destination that is down misses nothing, a failed batch is sent again (at least once, so a message can arrive twice), with backoff from 1 to 30 s; a restart carries on where it stopped. Batches of up to 500 messages or 4 MB; a message is sent within about a second of arriving.
- **Nothing about the source is lost.** Syslog keeps the sender's own time, host, program, PID and MSGID in the header and its structured data unchanged; the sender's address goes in the standard `[origin ip="…"]` element and logdir's own in `[logdir@32473 rcvd=… ip=… sender=… node=… via=… sent=…]`. JSON destinations carry the same as fields (time, sent_time, host, program, pid, facility, severity, msgid, structured_data, message, source, source_name, via, node). 32473 is the example enterprise number of RFC 5612.
- **Per-destination choices:** a filter in the Logs page's language (words, host=, program=, source= with one or more senders), a severity (or worse, or exactly), where a new destination starts (now, 1 h, 24 h, 7 d back or the oldest kept), a rate limit, *Max behind* (24 h unless set: older ones are skipped and counted), *Also send the original bytes* (the bytes as received are kept with each message from then on and sent as `raw`), *Send from the sender's address* (syslog over UDP: a raw socket with the sender's IPv4 address as source and its own bytes; needs root or CAP_NET_RAW; an IPv6 sender or server is sent the ordinary way; cut at 1472 bytes), a CA certificate for TLS/HTTPS, and switching it off (the place is kept).
- **Configure ▸ Shipping**: the list, and a popup to add or edit one with **Test** (a test message from this node, before saving) and **Preview** (the Logs page with that filter and severity). **Monitor ▸ Shipping**: every destination on every node: state (OK, BEHIND past 2 minutes, ERROR, OFF, STARTING), per minute, sent, how far behind, last sent, skipped, the last error on hover; right-click for *Send a test message*, *Send again from…* (1 h to everything kept), *Skip to now* and *Edit*; CSV. Shortcuts: Logs page ▸ right-click a message ▸ *Ship messages like this…* (the page's filter and severity, or that message's host and program), Monitor ▸ Senders ▸ right-click ▸ *Ship this sender…*. Help topics for both pages.
- **CLI:** `--logs-ship` (the table), `--logs-ship-test NAME [--node N]`, `--logs-ship-reset NAME --logs-ship-from now|oldest|6h [--node N]`. **API:** `GET /api/logs/shipping`, `POST /api/logs/shipping/test` and `/reset` (full access, CSRF, logged with the user). **MCP:** `logs_shipping` (status only; the token field is hidden by the privacy layer).
- A node is picked by host name or by cluster address (two nodes can share a host name).

### Changed
- A config with destinations is refused while any node of the cluster runs a version before 37 (as for gateway names).
- The token is kept in the settings like other secrets: every node has it, full-access users can see it in the configuration, read-only users get it redacted.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go vet ./... && go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64; `node --check` of app.js and help.js.
- New tests (`ld_ship_test.go`): validation; the RFC 5424 form (origin and logdir elements, the sender's SD kept, bad SD carried as a value, escaping); syslog over TCP to a real listener, with a restart carrying on without duplicates, a reset to the oldest, a test message and removal; retries while the receiver is down then nothing lost; filters (severity, network, start now, disabled); max behind; UDP and JSON over TCP; Splunk HEC, Elasticsearch (including a refused document then a retry) and Loki against test HTTP servers, and an unsaved test; raw bytes; the spoofed packet's headers and checksums, and a real spoofed send (the receiver saw 127.0.0.55); the version gate. `logs_shipping` added to the MCP gate test.
- Live: two daemons in a cluster (log listeners on 127.0.0.11 and .12), a real PAM login over HTTPS, receivers on 127.0.0.50 (TCP and UDP). In headless Chromium (light and dark): added a TCP destination starting 24 hours back and a UDP errors-only one sending from the sender's address, through the popup (Test said *Sent*; a duplicate name was refused in the popup); Monitor ▸ Shipping showed both on both nodes with the right counts (the UDP errors-only one sent nothing from the node that had only notices); right-click ▸ Send a test message reached the receiver once; the Logs page shortcut and Preview, and the Senders shortcut, filled in the right filter. On the wire: the sender's `timeQuality` element and the origin address arrived unchanged; a datagram from 127.0.0.77 was delivered from 127.0.0.77 with its original bytes; restarting both daemons sent nothing twice; `--logs-ship-reset … --logs-ship-from oldest --node 127.0.0.1:11165` re-sent only that node's messages; a POST without the CSRF token was refused.

### Not verified
- Real Splunk, Elasticsearch, Loki, rsyslog or syslog-ng servers (test servers that speak their APIs were used). Sending over TLS or HTTPS was not tested, only its configuration and validation. Spoofing across a real network (reverse-path filters and firewalls often drop such packets) and on IPv6 (not supported).

## [v36] - 2026-11-26 — Logs: the Errors and Warnings tiles fast over 7 and 30 days

### Fixed
- **Clicking Errors or Warnings on the Logs page was slow past one day.** The message list (`/api/logs/search` with a severity) wants the newest 1000 matches; when errors are rare that means reading back through hundreds of hours, and every message of every one of them was decompressed and parsed as JSON just to look at its severity, then put in the hour cache (pushing the recent hours out). Now, for an hour that is not already in the cache, each stored line's severity is read straight from its bytes (`ldRawSev`) and only the lines that can match are parsed; such hours are not added to the cache. The same applies to the stats of the hours that are still read message by message. Hours in the cache are used as before. A line whose severity cannot be read that way is parsed, as before.

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go vet ./... && go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64.
- New tests: `TestRawSev` (the severity is read right even when the host, program or PID contain `,"s":`, and an unreadable line gives none), `TestSeveritySearchSkipsLines` (with nothing cached, searches and stats for error or worse, warning only, emergency, and a severity with a host give exactly what reading every message gives).
- `BenchmarkSearchSeverity30d` (30 days of 20,000 messages an hour, 5 errors an hour, 1% warnings, a 64 MB cache), the newest 1000 errors: 7 days 11.4 s → 1.19 s, 30 days 14.0 s → 1.42 s; warnings only 30 days about 0.04 s in both.
- Live, one daemon with a PAM login over HTTPS on 30 days of data: v35 and v36 return the same 1000 messages for error or worse and for warning only, over 7 and 30 days.

### Not verified
- Not run on a cluster of several nodes. What remains of the time over 30 days is decompressing the hours; a search with words still reads every hour.

## [v35] - 2026-11-26 — Logs over 7 and 30 days: counts and chart from hourly rollups

### Fixed
- **Logs ▸ 7 Days was slow and 30 Days / All did not finish.** The counts, the busiest hosts and programs and the chart (`/api/logs/stats`, also the Senders page and the `logs_stats` MCP tool) read, decompressed and parsed every message of every hour in the range on every refresh, every 15 s. Thirty days do not fit in the hour cache, so each refresh read the disk again (and pushed the recent hours out of the cache, slowing the short ranges too) until the 90 s node timeout.

### Added
- **Hour rollups (`ld_rollup.go`).** Each closed hour gets a small `.jsonl.lz.sum` file beside it: how many messages it holds of each severity, host, program and sender. It is made when the hour is compressed; hours from before this release get theirs by a background pass after start (newest first, read straight from the file so the cache is not disturbed). Rollups are kept in memory once read. Stats take a whole closed hour from its rollup when no words are typed (severity, host, program and sender filters, wildcards and sender names all still work); the first, partial hour of the range and the open hour are read as before. A rollup is used only while its count equals the hour's count in the index; otherwise the hour is read and the rollup made again. An hour with more than 20,000 distinct rows is marked as having none and is read as before. Retention and the disk cap remove the rollup with its hour.

### Changed
- **The Logs chart's bar width over long ranges is a whole number of hours** (1, 2, 3, 4, 6, 8, 12 or 24, then whole days), so each bar is made of whole hours: 7 days is 3-hour bars (56), 30 days 12-hour bars (60). Ranges up to a day are unchanged. An explicit `step` that is not a whole number of hours still works, by reading the hours.
- A search with words over 30 days still reads every hour (the rollups hold no text).

### Verified
- gofmt, `go vet ./...`, `go test -race -count=1 ./...`, `CGO_ENABLED=0 go vet ./... && go test ./...`, builds for linux amd64, arm64, arm, 386 and riscv64.
- New tests (`ld_rollup_test.go`): stats from rollups equal stats from reading every message, for no filter, severity (or worse, and exact), host, host wildcard, program, sender, senders by name and host-by-sender-name, with no series, 1-hour and 3-hour buckets, a partial first hour and the open hour; a restarted store reads the rollup files; a stale rollup is not used and is made again; words turn rollups off; an hour with too many rows is marked and counted from its messages; the chart step for each range.
- Benchmark, 30 days of 20,000 messages an hour (14.4 million) with a 64 MB cache: the 30-day stats took 35.4 s in v34 and 0.077 s now.
- Live, one daemon with a real PAM login over HTTPS on 30 days of v34-format data (no rollups): v34 took 7.6 s for the first 30-day stats; v35 made the missing rollups in the background after start, gave the same totals, and answered 30 days in about 0.05 s after a restart. 1-day and 30-day searches unchanged.

### Not verified
- Not run on a cluster of several nodes (each node uses its own rollups; the merge is unchanged). The page itself was not opened in a browser (no front-end change).

## [v34] - 2026-11-24 — Updates page: Nodes table laid out evenly, Fetch from online ticked by default

### Changed
- **Operate ▸ Updates ▸ Nodes.** The Running and Source versions now come right after the node name, ahead of Reachable. The table has set column widths (the node name widest, the versions narrow, Reachable, Queued and State evenly spaced), everything left-aligned, and a long name is cut with an ellipsis. The same layout as filedir v106–v110.
- **Operate ▸ Updates ▸ Upload a Release.** Fetch from online is ticked when the page opens, so Upload fetches the newest release from GitHub; untick it to choose an archive file (the file chooser is greyed out until then).

### Verified
- `node --check webui/app.js`, gofmt, `go vet .`, and the version, update and web asset tests. The page was checked in headless Chrome for filedir (the same code); it was not opened in a browser for this product.

### Not verified
- The Go code is unchanged, so the platform cross-compiles were not repeated. Not run against a live cluster.

## [v33] - 2026-11-23 — Gateway names in DNS, popup buttons on the right, a page icon of its own

### Added

- **Every gateway has two new optional fields, VIP FQDN and Anycast FQDN** (Topology ▸ New gateway and Edit gateway, and Configure ▸ Gateways; `fqdn` and `anycast_fqdn` in the file). They are shared by the cluster, checked as host names, and left out of the file when empty, so a configuration that never used them reads the same in older versions. Setting one while a node of the cluster runs an older version is refused, naming the node, because that node would drop the names when the settings reach it.
- **DNS registration keeps the two names right.** The VIP FQDN gets the gateway's shared addresses (A and AAAA) and the Anycast FQDN its anycast addresses that are not paused. Every address of a name gets a PTR record pointing back at it. Every node with registration turned on does this, so whichever node is up keeps the names right, and **Remove records** (`--dnsreg-remove`) takes out only this node's own records, never the gateway names.

### Changed

- **The buttons of every popup are on the right**, as in filedir (they were on the left).
- **The page icon (the one on the tab and the bookmark) is now green, an L**, so the six products can be told apart in a list of bookmarks. It was the same blue D in every product.

### Verified

- gofmt, vet, `go test -race ./...`, `node --check` on `app.js` and `help.js`, builds for linux amd64, arm64, arm, 386 and riscv64. New tests: the names survive a save and a read back, reach the primary from a replica, survive a sync between two nodes, an older node refuses a change to them, and a registration run puts both names and their PTR records in a fake DNS server and leaves them when this node's records are removed.

### Not verified

- A live run against a real DNS server and real nodes: the DNS side was tried against the fake server in the tests only. The popups and the icon were looked at in Chromium only through the mock page, not on a running daemon.

## [v32] - 2026-11-21 — Memory cap defaults to 70%

### Changed

- **The default Memory cap is 70% of the machine's (or container's) memory, down from 90%.** At 90% a full cache or set of rollups left too little room for searches, reports, the Go runtime and the OS, and a container with no swap would kill the process. The message cache now stops growing at 70%; Go's own memory limit follows at 75%. Older data is read from disk, only slower. The Memory cap field in Configure ▸ Settings ▸ General shows 70.
- **A cap you saved as an explicit number other than 90 is kept.** A cap that was left at the old default of 90 (never saved, or saved as 90 and so stored as no value) now means 70. To keep 90, set it to 90 again after the update: the field then stores the value.

## [v31] - 2026-11-21 — Faster searches, and a memory limit for the runtime

### Changed

- **A search with words scans about 15% faster and allocates about 40% less.** Matching used to build two new strings for every message it looked at (the text joined together, then lower-cased). It now lower-cases into a buffer the search keeps. The matches are the same; non-ASCII text goes through the same lower-casing as before. Full scan of 180,000 messages: 213 → 183 ms, 82 → 38 MB allocated.
- **A cached hour holds about 10% less memory.** The host, program, address, via and node of each message are kept once per hour instead of once per message (411 instead of 459 bytes a message on test data). The cache's size estimate still counts every copy, so it stays on the cautious side.
- **Go's memory limit is set 5 points above the message cache's cap** (95% of the machine's or container's memory with the default 90%, at most 97%), so a large search can't push the process past what the machine allows and get it killed. `GOMEMLIMIT` in the environment overrides it. The cache's own cap is unchanged.

## [v30] - 2026-11-21 — Logs: `*` works in the words you search for

### Fixed

- **A `*` in the Messages filter was searched for as a star**, so `!fw1*` excluded nothing (no message holds the text `fw1*`). In a word it now stands for any run of characters: `fw1*`, `!fw1*`, `*.cox.net`, `fa*ed`. The same goes for pattern alerts and the daemon's own log search.

## [v28] - 2026-11-21 — README rewritten, screenshots added

### Changed

- **README** rewritten in plainer words, with screenshots of the real GUI from `snaps/`. The screenshots use sample data (made-up hosts, `example.net` names). `snaps/` is new.

## [v27] - 2026-11-21 — Logs: a word finds the sender's name

### Fixed

- **A plain word in the Messages filter also matches the name of the message's sender.** `fw1-mgmt` found nothing for a firewall whose messages never contain that name (they say `devname="fw1"`); it now finds that sender's messages, by the name in Configure ▸ Senders, its DNS name or the first part of it. `and`, `or` and `not` work with it as with any word.

## [v26] - 2026-11-21 — Logs: host= finds the sender's name

### Fixed

- **`host=` in the Messages filter matches the name the Host column shows.** It matched only the host name written inside the message, so `host=fw1-mgmt` found nothing for a firewall that calls itself `cush`. It now also matches every message from a sender whose name (its DNS name or its name in Configure ▸ Senders) matches, with `*` wildcards as before.

## [v25] - 2026-11-21 — Logs: the Host column uses the DNS name first

### Changed

- The Host column takes the sender's DNS name first (`fw1-mgmt`), then its name in Configure ▸ Senders, which is the order the Senders page and the charts use. In v24 the Senders name came first, so a sender configured as `cush` still showed `cush`.

## [v24] - 2026-11-21 — Logs: opens on the last 15 minutes

### Changed

- **Monitor ▸ Logs** opens on **15 Min**, not 1 Hour. The other ranges are one choice away, and the page keeps the one you pick while you stay on it.
- **The Host column in the Messages table is the sender's name**, the one the Senders page and the charts use: its name in Configure ▸ Senders, else the first part of its DNS name (`fw1-mgmt`, not the `cush` the firewall calls itself in its messages). A sender that is not in the list shows the host name its message gives. The name the message gives is in the tooltip, and a click on the host shows only that sender.

## [v23] - 2026-11-21 — Logs: Host and Address columns

### Fixed

- **The Logs page loaded no tiles, chart or sender tables in v22.** A comment added in v22 swallowed the line that stores the statistics and the sender list, so the tiles, the chart and the Busiest tables stayed empty. v22 should not be used.

### Changed

- **The Messages table has a Host column and an Address column.** **Address** is where the message was received from (click it to see only that sender). **Host** is the name the message gives itself or, when it gives none (a FortiGate, for one), the sender's name from Configure ▸ Senders, else its DNS name, in place of repeating the address.

## [v22] - 2026-11-21 — Logs: the page's two list controls are settings

### Changed

- **The "messages" drop-down is gone from the Logs page.** The page lists the newest 1000 messages of the range, and that number is now **Configure ▸ Settings ▸ General ▸ Log collector ▸ Messages listed** (100 to 5000; saved for the whole cluster). The tiles and the chart still count every message in the range; the list says "newest" when the range holds more than it lists. Assistants and the command line keep their own `limit` (default 200).
- **The Follow button is gone too.** Following new messages, every 2 seconds, is now **Follow new messages** in the same card (off unless ticked; saved for the whole cluster).

## [v21] - 2026-11-21 — Senders: pick one sender in the chart

### Changed

- On **Monitor ▸ Senders**, the pills of the **Top senders** chart are under the chart, and a click on a pill shows that sender alone (the scale follows it); a second click on it shows them all again. The choice stays through the page's refreshes. The chart's CSV still holds every sender.

## [v20] - 2026-11-21 — Logs: the tiles' colours say what they are

### Changed
- The four tiles on the Logs page are **green** for Messages, **red** for Errors, **amber** for Warnings and **blue** for Senders. Before, Messages was blue, Errors orange, Warnings green and Senders amber, so the colours pointed the wrong way (a green Warnings tile, an amber Senders tile).

### Verified
- `gofmt`, `go build`, `go vet`, `go test -race -count=1`; `node --check`; the Logs page in headless Chromium.

## [v19] - 2026-11-21 — Logs: click a tile to filter by it

### Added
- **The tiles on the Logs page are buttons.** **Errors** shows only errors or worse, **Warnings** shows only warnings, **Messages** clears the severity filter, and **Senders** opens Monitor ▸ Senders. The Severity menu follows (the Errors and Warnings tiles show as pressed while they are the filter).
- A new **Warning only** choice in the Severity menu. Until now every choice was "this severe or worse", so there was no way to see just the warnings, and the Warnings tile's count (warnings only) did not match what a warning filter listed (warnings and errors). `severity=warning=` (a trailing `=` means exactly that severity) does it in `GET /api/logs/search`, `/api/logs/stats` and the live tail; `sev_exact` carries it between the nodes. `severity=warning` still means warning or worse.

### Verified
- `gofmt`, `go build`, `go vet`, `go test -race -count=1` (new `TestSearchSeverityExact`); `node --check`; the Logs page in headless Chromium (mocked API): each tile sends the right request, the Severity menu follows, no page errors.

## [v18] - 2026-11-21 — Monitor ▸ Senders in alphabetical order

### Changed
- The table is sorted alphabetically by the name in its first column (numbers in order, so 10.0.0.2 comes before 10.0.0.10), then by address. Before, it followed the order the nodes listed them in.

### Verified
- `node --check`; `gofmt`, `go build`, `go vet`, `go test -race -count=1`.

## [v17] - 2026-11-21 — Monitor ▸ Senders: axis labels of the Senders heard chart

### Fixed
- The **Senders heard** chart rounded its axis labels to whole numbers, so a chart with few senders read 0, 3, 5, 8, 10 for steps of 2.5. It shows the real values now (0, 2.5, 5, 7.5, 10).

### Verified
- `node --check`; the page in headless Chromium (light theme, mocked API), no page errors.

## [v16] - 2026-11-21 — Monitor ▸ Senders: tiles and charts, and it moves above MCP

### Added
- **Monitor ▸ Senders is laid out like Monitor ▸ Node**: a time range (Last Hour, Day, Week, Month, Custom), six tiles (senders heard, message rate and peak, messages, errors or worse, silent senders, busiest sender) and four charts drawn with the Node page's chart (hover, CSV): **Messages per minute**, **Top senders** (the five busiest), **By severity** (green debug/info/notice, amber warning, red error or worse) and **Senders heard** per interval. The table, filter and pager are below.
- A long name in the **Busiest** tile is cut with an ellipsis (the full name and address are in its tooltip); a tile never grows wider for it.
- `GET /api/logs/stats` with `series=1` also returns `sources`, `top_sources`, `source_series`, `severity_series` and `heard` (and the nodes' `stats` answer carries `by_source`, `source_series` and `severity_series`); the MCP and CLI stats are unchanged. At most 500 senders a node get a series of their own; the totals count them all.

### Changed
- **Senders** is the first page under Monitor, above MCP.
- The tiles and charts read the stored messages: every 15 s for the last hour, once a minute for longer ranges, on Apply for a custom range. The table still refreshes every 5 s.

### Verified
- `node --check`; gofmt, `go vet`, `go test -race -count=1 ./...`, `CGO_ENABLED=0` tests, cross-builds for linux/amd64, arm64, arm, 386 and riscv64. New test `TestStoreStatsPerSenderAndSeverity`.
- The page in headless Chromium, light and dark, against a mocked API (60 senders, a 44-character sender name): tiles, four charts, truncation, filter and paging, no page errors.

### Not verified
- Against a real store and a cluster: the merge of the per-sender counts across nodes is read, not run; how long a 30-day scan takes with a large store.
- The tiles and charts do not follow the filter box (they cover all senders).
- **Data rate** from the mockup is not there: the store does not keep bytes per message over time, only a total per sender since start. A **Silent** tile took its place.
- **By severity** is lines, not stacked areas.

## [v15] - 2026-11-20 — Monitor ▸ Senders columns: Name, Address, Proto, Last seen, Messages, Node

### Changed
- The table's columns are **Name** (the reverse-DNS host part, else the Configure ▸ Senders name, else the host its messages give), **Address**, **Proto**, **Last seen**, **Messages** and **Node**. The Host column is gone; the host its messages give is in the Name tooltip and the filter still matches it. The CSV is unchanged. Help topic updated.

### Verified
- `node --check`; the page in headless Chromium (light theme, mocked API): the six columns, filter and paging as before, no page errors; gofmt, `go vet`, the help, version and field-hint tests.

### Not verified
- The dark theme; the full suite was not rerun for this change.

## [v14] - 2026-11-20 — Monitor ▸ Senders: the filter and pager laid out like the Settings tables

### Changed
- The filter box, the count, **« ‹ page › »** and **per page** sit in one row under the card header, as on the Settings tables (**Filter rows**, 24 rem wide), instead of stretched across the header. The download button stays in the header.

### Verified
- `node --check`; the Senders page in headless Chromium (light theme, 60 mock senders): the row looks like the Settings tables, filter, paging and the refresh behave as in v13, no page errors; gofmt, `go vet`, the help, version and field-hint tests.

### Not verified
- The dark theme; the full suite was not rerun for this layout-only change (it was run for v13).

## [v13] - 2026-11-20 — reverse lookups that find what `host` finds; Monitor ▸ Senders filter and pager

### Fixed
- **A reverse lookup found no name where `host` did** (192.168.5.11 → `media1.cush.local`, shown as "no reverse (PTR) record" in Lookup, and the sender kept its entry name). Go's resolver may go through the C library (nsswitch, systemd-resolved, mDNS modules), which gives nothing on some hosts. Every reverse lookup (sender names, the Lookup and Traceroute dialogs, the message tooltip, search by sender name) now tries that, then Go's own resolver, then a plain PTR query (no EDNS, UDP then TCP) to the nameservers in `/etc/resolv.conf`, as `host` does. Only a server's "no such record" is kept for 30 minutes (v12).
- v12 shipped with the Senders page and the retry rule only; this release carries the lookup fix itself.

### Added
- **Monitor ▸ Senders has a filter box and a pager** (**« ‹ page › »**, a page box and **per page**, 25 to 500, **25 by default**). The filter matches the name, address, host, format or node; a new filter goes back to page 1; the refresh keeps the filter, the page and the focus. The CSV holds the filtered rows. Help topic updated.

### Verified
- `node --check`; gofmt, `go vet`, `go test -race -count=1 ./...`, `CGO_ENABLED=0` vet and tests, cross-builds for linux/amd64, arm64, arm, 386 and riscv64. New tests: PTR names for IPv4 and IPv6, a fake DNS server answering a compressed PTR and a not-found, and the answer parser on every truncation of a reply and a self-pointing name.
- Monitor ▸ Senders in headless Chromium against a mocked API with 60 senders: 25 rows, `1–25 of 60`, filter `host5` gives 6 rows, next page `26–50 of 60`, the page held across the 5 s refresh, no page errors.

### Not verified
- Against the real DNS server behind your cluster: whether the fallback finds `media1.cush.local` there was not run; the cause on that host (C library path versus Go) was not confirmed. Light and dark themes of the Senders card were not looked at.

## [v12] - 2026-11-20 — a sender is named by the host part of its reverse DNS name

### Changed
- **Monitor ▸ Senders** names a sender by the host part of its reverse DNS name (`sw1` for `sw1.example.com`), as Topology already did; the Configure ▸ Senders entry name is used only when the address has no reverse name. The tooltip holds the full DNS name and the entry name. Several addresses covered by one network entry (all named `cush`) now each show their own host name once looked up.

### Fixed
- **A lookup that timed out was remembered as "no name" for 30 minutes**, so a slow DNS server left senders under their entry name. Only a name or a definite "not found" is kept 30 minutes now; a timeout or server failure is tried again after a minute, and each lookup waits up to 2 s (was 0.5 s).

### Verified
- `node --check`; gofmt, `go vet`, `go test -race -count=1 ./...`, `CGO_ENABLED=0` vet and tests, cross-builds for linux/amd64, arm64, arm, 386 and riscv64; new test `TestLdDNSFailedLookupIsRetriedSoon`.

### Not verified
- Against a real DNS server or in a browser: whether the senders in the screenshot have PTR records was not checked. A sender with no PTR record is still shown by its entry name.

## [v11] - 2026-11-20 — Logs ▸ Messages pages of 25 by default

### Changed
- The **Messages** card on Logs now shows 25 messages per page instead of 100 until you pick another size (**per page** menu, 25 to 500, unchanged). The help topic says 25.

### Verified
- `node --check webui/app.js`; gofmt, `go vet`, `go test -race -count=1 ./...` and `CGO_ENABLED=0 go vet` / `go test`; cross-builds for linux/amd64, arm64, arm, 386 and riscv64.

### Not verified
- The page in a browser (a one-number change in the page state; not looked at in Chromium).

## [v10] - 2026-11-20 — closed hours cached in memory, under a memory cap

### Added
- **A cache of closed hours.** A search, a statistics request or an alert check reads each hour's file, decompresses it and parses every line. The parsed messages of closed hours are now kept in memory, so the next search over the same hours does none of that. It is capped like flowdir's rollups: **Configure ▸ Settings ▸ General ▸ Log collector ▸ Memory cap** (`settings.logs.mem_max_percent`, 90 unless set) is a percentage of the machine's memory, or of the container's limit when that is smaller. At the cap the oldest hours go first; an hour that does not fit is read from its file as before, so the cap costs speed only. The hour being written is never cached, and an hour removed by the retention or the disk cap leaves the cache.
- The newest 24 closed hours are read into the cache when the daemon starts, in the background.
- `logdir --logs` shows, for each node, the hours cached, their size against the cap, and how often a search found an hour cached or read it from disk; the same numbers are in the status (`mem_bytes`, `mem_cap_bytes`, `mem_hours`, `cache_hits`, `cache_misses`).

### Verified
- gofmt, `go vet`, the full suite (only the PAM-header update test fails in this sandbox); new tests: closed hours are cached and the second search is answered from the cache with the same messages, the hour being written is not cached, a cap too small keeps nothing and still answers, a cap for two hours is held, a removed hour leaves, and the start-up read takes the closed hours.
- A measurement on 500,000 messages in five closed hours (a search that matches nothing, so every message is read): 2.6 s the first time, 0.26–0.30 s afterwards; 131 MB cached.

### Not verified
- On a real log volume, or under memory pressure: the cache is about 260 bytes a message here, and at the default 90 % it can use most of the machine's memory, as flowdir's rollups can. Lower the cap on a node that does other work.
- The Settings box in a browser against a live daemon (the form was only syntax-checked).

## [v9] - 2026-11-20 — a pager on Logs ▸ Messages

### Added
- The **Messages** card has the pager of the other pages: **« ‹ page › »**, a page box and a **per page** menu (25 to 500, 100 by default), and the header says which messages are shown (`101–200 of 200`). The messages fetched (the **How many messages** menu) are paged in the browser; a new search, range, severity or limit goes back to page 1, the automatic refresh keeps the page, and **Follow** shows the newest. The CSV download still holds every message fetched.

### Verified
- `node --check`; in headless Chromium with the CSP on: next page, page box, 25 per page, and the pager hidden when there is one page.

### Not verified
- With thousands of real messages (the mock returns 200).

## [v8] - 2026-11-20 — names for senders (reverse DNS), and Senders moved to Monitor

### Added
- **Reverse DNS names, as in flowdir.** A sender's address is looked up in the background (kept 30 minutes, never waited on), and the name shows wherever a sender does: **Monitor ▸ Senders** (hover for the full DNS name), the **Topology** squares (the host part, `sw1` for `sw1.example.com`, before the Configure ▸ Senders name), the tooltip of a message row, and the senders CSV (a **DNS name** column). `logs_senders` and `GET /api/logs/senders` carry `rdns` and `rev`.
- **Search by sender name.** `source=` takes an address or a name with `*` wildcards (`source=sw*.example.com`); the name is the Configure ▸ Senders name, the reverse-DNS name (or its host part) or the host name the messages give. Results carry `source_name`.

### Changed
- **The Senders card moved from Logs to Monitor ▸ Senders**, its own page with the same table, CSV download and right-click menu (**Only this sender** opens Logs on it). The Logs page keeps the **Senders** tile.

### Verified
- gofmt, `go vet`, the full suite (only the PAM-header update test fails in this sandbox); a test of the names on senders and in search; the pages in headless Chromium with the CSP on: the Logs page without the card, Monitor ▸ Senders, the right-click **Only this sender** filling the filter box.

### Not verified
- Against a real DNS server or a real cluster (the name is from the test resolver, `127.0.0.1` → `localhost`). The Topology label order (DNS host, entry name, message host, address) mirrors flowdir.

## [v7] - 2026-11-20 — Settings: two different "Logs" renamed

### Changed
- **Configure ▸ Settings ▸ General ▸ Logs** is now **Log collector** (where the syslog messages are kept, how long, the disk cap, the ports), and the tab **Configure ▸ Settings ▸ Logs** is now **Daemon Log** (the level of logdir's own log and the copy of it sent to a syslog server). The help, README, QUICKSTART and Logs-page help use the new names; settings keys and help topic ids are unchanged.

### Verified
- help and field tests; both scripts parse.

## [v6] - 2026-11-20 — DNS registration: say why the zone lookup failed

### Changed
- When the node's `/etc/resolv.conf` lists no nameserver and **Server** is empty, the DNS registration used to ask `127.0.0.1` and report only `read udp4 127.0.0.1:…->127.0.0.1:53: connection refused`. The error now adds that the resolver file lists no nameserver, so the node asked itself, and to put the DNS server's address in the Server field. Nothing else about DNS registration changed (the code is flowdir's).

### Verified
- A test with a resolver file that has a `search` line only, and with a `nameserver` line.

## [v5] - 2026-11-20 — new default ports and first gateway id

### Changed
- The web GUI listens on **11154** (was 19994) and the cluster on **11155** (was 19995) by default; `install.sh`, the example configuration, the help and the docs say so. A configuration that sets `listen` itself is unchanged.
- A new gateway is numbered from **154** (was 230), in the daemon, the GUI form and CLAUDE.md.

### Verified
- gofmt, `go vet`, the full test suite (only the PAM-header update test fails, as before); no `19994`, `19995` or `230` left outside this changelog's history.

## [v4] - 2026-11-20 — logdir --logs-cat

### Added
- **`logdir --logs-cat FILE...`** writes stored hours (`.jsonl` or `.jsonl.lz`) as plain JSON lines on stdout, for `grep`, `jq` and the like. It needs no daemon, so it also works on a copied folder.

### Verified
- Output of a `.lz` file is byte-identical to its source (md5), several files in order, a missing file and a non-logdir file give a message and exit 1.

## [v3] - 2026-11-20 — closed hours use the LZ4-style codec

### Changed
- **Closed hours are compressed with flowdir's LZ4-style codec** (`.jsonl.lz`, 256 KB frames with a checksum each) instead of gzip. Measured on 64 MB of syslog-shaped JSON lines: writing took about a quarter of the time and reading about 60% of it (50 ms against 83 ms), at roughly 9 MB against 5 MB on disk. A torn or damaged frame ends that hour at the last good frame. The files can no longer be read with `zcat`. Hours written as `.jsonl.gz` by v1 or v2 are not read: this is before any release, so there are none in use.

### Verified
- Round trip, ratio, torn-end and damaged-frame test; the store tests; gofmt and `go vet`.

## [v2] - 2026-11-20 — Logs: one filter box

### Changed
- **Logs ▸ Messages has one filter box in its header**, like flowdir's. The four boxes and the Search button above the page are gone. It takes words (and, or, not, !word) plus `host=sw*`, `program=sshd` and `source=10.1.1.1`; Enter or a short pause applies it, and clicking a host or program or using the row menu writes those terms into it. Range, severity, the message count and Follow stay at the top.

### Verified
- Headless Chromium with the page's CSP: no toolbar inputs left; typing words, then clicking a host, gives `host=… words` in the box and the right `/api/logs/search` call; the row menu, Follow, Senders, Settings and Topology pages still work with no errors. Help and field-hint tests.

## [v1] - 2026-11-20 — logdir: a clustered syslog collector, forked from flowdir v37

### Added
- **Syslog collector.** UDP, TCP and TLS (ports 514/514/6514, each changeable or `-1` for off) on every gateway address and, with no `log_listen`, on every address. TLS uses the GUI certificate. TCP and TLS take newline-framed and octet-counted (RFC 6587) messages, told apart per message.
- **Parsing** of RFC 5424 (with structured data), RFC 3164 (year rollover handled), ISO stamps, Cisco sequence numbers, `*Mar 1 ... UTC:` stamps and `%FAC-SEV-MNEMONIC:`, and PAN-OS bodies. Anything else is kept whole. Messages carry the node's receive time.
- **Senders list** (`settings.logs.senders`): address, network or host name (re-resolved each minute); the narrowest network wins; others are refused before parsing.
- **Per-node store**: hourly JSON-line files, closed hours gzip-compressed with an index sidecar, a memory ring for tail and alerts, retention by days and a disk cap (default 90% of the volume). The open file is flushed before an update restarts the daemon.
- **Cluster search**: asks every node, merges by time, drops duplicates sent to two nodes, names nodes that did not answer.
- **MCP tools** `logs_search`, `logs_tail`, `logs_stats`, `logs_senders`, `logs_status`; HTTP `/api/logs/*`; CLI `--logs`, `--logs-search`, `--logs-args`.
- **Logs page** (search, filters, row menu, Follow, charts, busiest hosts and programs), **Configure ▸ Senders** with pattern alerts, and a Logs card in Settings ▸ General. Topology draws the senders.
- **Pattern alerts** (`log_pattern`): a query that matches `count` messages in `window_min` minutes, counted per node.

### Changed
- Everything NetFlow, IPFIX and sFlow is removed. The failover (gateway, VIP, anycast), clustering, alerts, updater and the MCP endpoint are flowdir's, unchanged.

### Verified
- gofmt, `go vet`; the new `ld_*` tests (parser forms, framing, store search/restart/compression/retention/cap, merge dedupe, config validation, allow-list, TCP and TLS loopback, alert samples); the help, MCP, proxy and config tests; headless Chromium with the page's CSP: Logs (search, filters, row menu, Follow), Senders, Settings round trip, Topology.

### Not verified
- Real sockets on gateway addresses through `reconcile`, a multi-node cluster over the real peer channel, TLS with the real certificate manager, install.sh and uninstall.sh, and `TestUpdateBuildApplyAndRollbackGuard` (needs PAM headers, fails in this sandbox).
- Pattern alerts count per node; stats count a message sent to two nodes twice.
- On high-latency links between nodes, use gateway hello 500 ms and hold 3000 ms.

## [v37] - 2026-11-20 — Configure ▸ Settings: turn off the chart's mouseover

### Added
- **Configure ▸ Settings ▸ General ▸ Flow ▸ Chart mouseover.** On by default. Untick it and the Flow page's chart no longer shows the line and the box of values when the pointer is over it. Shared by the whole cluster (`settings.flow.chart_hover`; written only when it is off, so a configuration that never used it stays readable by older versions). The page reads it again at most once a minute.

### Verified
- Headless Chromium with the page's CSP: on, the hover box appears; with `chart_hover: false` the chart has no hover area and no box; unticking the box in Settings saves `chart_hover: false` next to the exporters. gofmt, `go vet`, a new test (absent = on, false is read, the default is not written), the help-field test.

## [v36] - 2026-11-20

### Changed
- The tool popups say "running..." (no space) while a ping, traceroute or scan is going.

## [v35] - 2026-11-20

### Fixed
- **The Close button of the tool popups was on the left.** The popups were positioned with `style` attributes, which the page's Content-Security-Policy (`style-src 'self'`) refuses, so the button and the output box lost their layout in a real browser (my test page sent no such policy). The layout is in `style.css` now. Checked with the same policy as the daemon sends.

## [v34] - 2026-11-20

### Changed
- The Close button of the tool popups sits flush with the right edge of the output. The popup no longer keeps room for a scrollbar beside it (the output box scrolls by itself), which is what left the button inset on some browsers.

## [v33] - 2026-11-20

### Changed
- The Ping, Traceroute, Scan, Lookup and Whois popups have no × in the corner; the Close button is on the right. The Whois labels are one space wider so "Organisation" no longer touches its value.

## [v32] - 2026-11-20

### Changed
- The Topology legend no longer carries the sentence about the squares under the gateway.

## [v31] - 2026-11-20 — the network tools menu works

### Fixed
- **Ping, Traceroute, Scan, Lookup and Whois (v29) showed nothing.** The API wraps every answer as `{ok, data}` and the popups read the answer as if it were not wrapped. They unwrap it now. The test page I used in v29 answered without the wrapper, which is why it passed there.

## [v30] - 2026-11-20 — one row per exporter

### Changed
- **Flow ▸ Exporters shows each exporter once.** An exporter heard by both nodes (its traffic follows the active gateway) had a row per node. It is one row now: the records are added up, the last-seen time is the latest, and the Node column lists the nodes, the one that heard it last first (`flowdir2, flowdir1`). The CSV export has the same rows.

## [v30] - 2026-11-20 — network tools on right-click

### Added
- **Right-click an exporter on the Topology (box or dot), a host on the Flow page, or an exporter in the Flow ▸ Exporters table: Ping, Traceroute, Scan, Lookup, Whois**, as in netdir. Ping, Traceroute and Scan run on the node picked in the Node menu and the popup shows the output as it grows (Traceroute also as a table of hops); Lookup gives the reverse name of an address, Whois the registry data. On a Conversations row each of the two addresses has its own menu. The tools were already in the daemon (`--ping`, `--traceroute`, `--scan`, `--whois`, MCP `net_*`); this is the menu that was missing.

### Verified
- Headless Chromium with the API mocked: the menu and all five popups on a Flow host, and the menu on a Topology exporter box. Not run against a real node (ping, mtr and nmap are needed on it; the Scan popup says so when nmap is missing).

## [v28] - 2026-11-20 — an update no longer loses the minutes a node was collecting

### Fixed
- **Every update restart threw away the last two minutes the node had collected, and more.** The restart replaces the process (`exec`), so the flush of unwritten minutes that runs at a normal stop never ran. A node is only written to once a minute has been closed for two minutes, so whatever it received just before the update was gone. Because the exporters follow the active gateway, those minutes were exactly the ones in which the node being updated was the one receiving: the Flow chart had a hole each time (22:08–22:10, 22:19–22:22 and 22:26–22:29 in the bundle you sent, each ending at an update). The flush now runs before the restart. The templates are written then too.
- **Flows added to a minute that was already written were never written again.** That is flows that arrive late (an exporter reports a flow some minutes after it ended) and flows that come in the minute a restart read back. They were kept in memory, shown in the chart, and lost at the next restart. They are now written as a second record of the minute (Load adds the records of a minute together).

### Verified
- New Go test: flows, a restart, more flows in the same minute, the minute written, a late flow for it, a second restart: all 900 bytes are back (v26 gives 300). In the bundle, every gap in the chart lines up with a window in which the other node held the exporters (the log shows the VIP moving: node2 active 05:08:20–05:10:34, 05:19:33–05:21:47, 05:26:41–05:28:55), and each of those windows ends with that node's restart. gofmt, `go vet`, `go test` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

### Not fixed
- Datagrams that arrive while a node is between its stop and its start (about a second) are lost, as before.

## [v27] - 2026-11-20 — templates survive a restart

### Fixed
- **Holes in the Flow chart after a restart (probable cause, not confirmed on your data).** The templates NetFlow v9 and IPFIX exporters send were kept in memory only, so after a restart those exporters' data records could not be read until the next template arrived (many exporters wait 10 to 30 minutes between sends), and those flows were lost. They are now written to `templates.json` in the flow folder (once a minute, when something changed) and read back at start. The sampling rates are kept with them. Minutes already lost stay lost.

### Verified
- New Go test: templates and rates written by one service are found by a new one. gofmt, `go vet`, `go test` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers). A series over two exporters gives the same minute-by-minute totals in v20 and now, so the query path is not the cause.

## [v26] - 2026-11-20 — Flow chart time labels on round times

### Fixed
- **The time labels under the Flow chart are on round clock times and evenly spaced.** They were picked by slot number, so with a count that did not divide evenly the gaps differed (22:14 then 22:15). Now they fall on every 2 minutes, 5, 10, 15, 30 minutes, 1, 2, 3, 6, 12 hours or a day, whichever keeps them to eight or fewer.

### Not changed
- Minutes inside the range with no flows still draw as zero; that is the data, not the axis.

## [v25] - 2026-11-19 — an exporter's box is titled with its DNS host name

### Changed
- **The top line of an exporter's square on Topology is the host part of the name its address reverse-looks-up to** (sw1 for sw1.example.com). Before, it was the name of the Configure ▸ Exporters entry, which is the same for every exporter an entry that covers a subnet stands for. With no name in DNS it falls back to the entry's name, then the address. The second line is the address only (the format, as before, is in the tooltip, which also names the entry). The filter box matches the host too.
- `/api/flow/exporters` carries `host` for each exporter. The lookup runs in the background (the first call after a new exporter appears has no host for it; the next has), is cached for 30 minutes, and never delays the page.

### Verified
- New Go test: the host is the first label of the cached reverse name, empty when the address has none. In headless Chromium against a mocked API an exporter with host `core1` in entry `cush` is titled core1 and found by the filter `core1`; one without a host keeps its entry name or address. gofmt, `CGO_ENABLED=0 go vet ./...`, `CGO_ENABLED=0 go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

### Not verified
- Not run against a real resolver.

## [v24] - 2026-11-19 — an exporter's box goes green, amber, red, then away

### Changed
- **On Topology an exporter's square is green while its last datagram is under 5 minutes old, amber from 5 to 10 minutes, red from 10 to 15 minutes, and then it is gone.** (v23 had green and amber only.) A red square's line to the gateway is drawn as a down line; with more exporters than the Small square limit the amber and red ones are the ones drawn full size. Help, legend and README say the same.

### Verified
- In headless Chromium against a mocked API: exporters last heard 400 s, 700 s and 1000 s ago are drawn amber, red and not at all; the others are unchanged. gofmt, `CGO_ENABLED=0 go vet ./...`.

## [v23] - 2026-11-19 — exporters are drawn on the gateway they send to, with the squares-to-dots limit and the filter box back

### Added
- **Topology draws the exporters.** Under each gateway's circle, every exporter that is being heard is a square joined to the circle: its name (or address) and format, green while its last datagram is less than 5 minutes old, amber when it has gone quiet, gone from the drawing after 15 minutes. An exporter is drawn on the gateway whose address (VIP, IPv6 VIP or an anycast address) its datagrams were sent to, as it is heard on any node (one square, however many nodes hear it; the tooltip lists them, where it sends, when it was last heard and how much it has sent). Not heard, or sending to an address that no gateway holds: no square. A click opens Flow on that exporter.
- **To tie an exporter to a gateway the collector now learns which local address each datagram arrived on** (`IP_PKTINFO` / `IPV6_RECVPKTINFO`, asked for on the collector's sockets). `/api/flow/exporters` and the cluster answer carry it as `to` (the addresses of the last 15 minutes). A node whose kernel does not give it leaves `to` empty, and its exporters are not drawn.
- **The small-square limit is a setting again** (`settings.topology_squares_above`, Configure ▸ Settings ▸ General ▸ Topology, 1 to 5000, shared, default 36; it was retired in an earlier release and is no longer dropped from a saved configuration). A gateway with more exporters than that draws each as a small square joined to the circle; the full-size square for the one under the pointer shows above it, and only the amber ones are drawn full size.
- **The filter box is back at the top left of Topology** ("Filter exporters", level with the Node menu): only the exporters whose name, address, format, destination address or node holds every word are kept; `AND`, `OR` and `!`/`NOT` work as in the other filters, and with a filter set the matches are drawn full size above the small squares.
- Help for Topology and Settings ▸ General, README.

### Verified
- New Go tests: the kernel reports the address a datagram came in on (IPv4 over a real socket; the IPv6 case is skipped when the host has no IPv6, as here); an exporter remembers where it sends and forgets an address after 15 minutes; the setting is validated (1 to 5000). gofmt, `CGO_ENABLED=0 go vet ./...`, `CGO_ENABLED=0 go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).
- The GUI, in headless Chromium against a mocked API: three exporters on a gateway (one heard on two nodes is one square; one that sends to an anycast address is drawn, amber when quiet; one heard an hour ago, one sending elsewhere and one with no destination are not drawn; an IPv6 address written two ways matches); with 63 exporters the squares become small squares with the amber ones full size; the filter keeps the right ones; the Small square limit saves as 50, is left out at 36, and 9000 is sent for the daemon to refuse.

### Not verified
- Not run against a live exporter, a cluster, or an IPv6 collector socket.

## [v22] - 2026-11-19 — reports skip hours that cannot match, reuse more of what they computed, and reach history that is only on disk

### Added
- **An index of every hour** (its exporters, its ports when there are at most 256, its protocols). A report with an `exporter`, `port` or `proto` term leaves out the hours that cannot have a match, without reading their keys. It is built when an hour is made and when history is read back, and kept for hours that are only on disk.
- **Hours that the memory cap left out of memory are reported.** At start, history is read newest first until the memory cap; older hourly files used to stay on disk and were not part of any report, so with 20 GB of history most of it was invisible. A report that reaches back to them now reads the files (in parallel, outside the lock the collector needs), adds them up and drops them again; the hour's index and its cached sums are kept, so the next report over the same hours does not read the files. `range=all` starts at the oldest file. Wildcard names in a filter still look only at what is in memory.
- **The common groupings of every new hour are put in the cache as the hour is made** (everything, service, protocol, exporter, source AS, destination AS), so a first report over the days before is already quick.
- **One exporter in the cluster means no deduplication round.** Before the digest round the asking node asks every node for the exporters it has in the range (two are enough to know); when there is at most one in the whole cluster nothing is reported twice and the round (about a second per six million keys) is left out. A node that does not answer, does not know the answer (an hour on disk it has not read yet) or runs an older version makes it a no, and the digest round runs as before.

### Changed
- **Reports count in maps made for the grouping** (an address, a pair of addresses, a port and protocol, an AS number) instead of the 80-byte general key, which hashed slowly. Series and the interface and flow groupings use the general map as before. The answers are the same (tested against a plain, slow count for eleven groupings).
- **Host, source, destination, conversation and flow reports over a day or more use each hour's biggest groups only** (1,000 by bytes, 1,000 by packets, and by flows when they are not mostly equal), cached per hour, so a repeat report over a month costs about the same as a service report. The answer says it is approximate, the totals are exact, and the first run and the later ones give the same answer. Reports over less than a day are exact, as before.
- The cache's size follows the machine: 1/50 of its memory, 200,000 to 2,000,000 group entries (30 to 300 MB). It is not part of the memory-cap figure.

### Measured
- Synthetic 300 hours × 20,000 keys (6 million keys) on a 2-core sandbox, one core / two cores: service report with no cache 440 / 143 ms (v21: 560 / 260 ms); host report, first run 2.2 s / 1.1 s, repeat 0.78 s / 0.40 s (v21: 1.5 s / 0.8 s every time); conversation repeat 0.19 s. The first host run is slower than v21 because it also fills the cache.

### Limits
- Cached host and conversation reports are approximate for long ranges, as above. A host that is never among an hour's biggest 1,000 cannot appear in a long report's list, however long it was there.
- The first report over hours that are only on disk is limited by reading and decoding the files; a cold 20 GB takes minutes, not seconds. The next one does not read them again for the low-cardinality groupings; for host and conversation it reads a file again only when its cached sums were evicted.
- Hours that are only on disk can be dropped from the cache when it is full; the index is kept.
- A query does not stop reading files when the asking side gives up; the work is finished and thrown away.

### Verified
- New tests: the compact group maps give what a plain count gives for eleven groupings; parallel answers equal serial ones for twelve groupings and the digest adds up; the index leaves out the right hours for exporter, port and protocol terms and none otherwise; long host reports are marked approximate, stable between first and cached runs, with exact totals, and short ones are exact; hours that exist only as files give the same report as when they were in memory, keep their index and are served from the cache the second time; a store loaded under a tiny memory cap reports its older hours; the exporter set of a range. gofmt, `CGO_ENABLED=0 go vet ./...`, `CGO_ENABLED=0 go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

### Not verified
- No `-race` run (cgo and the PAM headers are missing here). The shared state is the cache and the index map, each behind its own mutex, and one accumulator per worker.
- Not tried on real 20 GB of data, on a many-core machine or in a cluster of mixed versions (an older peer answers "bad op" to the exporter question, which counts as a no).

## [v21] - 2026-11-20 — long-range reports use every core, reuse finished hours, and get longer to finish

### Changed
- **A flow query is spread across all cores.** A report (and the first, deduplication round of one) is a sum over buckets; each worker now adds its share into an accumulator of its own and the accumulators are added up at the end, so the answer is identical to the serial one. Small queries (under 20,000 keys) still run on one goroutine. Each worker makes its accumulator on its own goroutine and keeps its running sums in locals: before that, two workers' counters shared a cache line and the cores slowed each other down instead of helping.
- **Finished hours are cached for reports grouped by service, protocol, exporter, interface, source AS or destination AS (and for totals).** An hourly bucket is made once and never changes, so what a query takes from it is kept, keyed by the hour, the filter, the grouping and the deduplication losers. The next report over the same or an overlapping range adds the kept groups instead of walking the hour's keys. A bucket whose key count, folded count or "other" no longer matches is recomputed. The cache holds at most 400,000 group entries (about 60 MB) and drops the least recently used. Groupings with about one group per key (host, source, destination, conversation, flow) and time series (`step`) are not cached: there is nothing to save, or the slots differ.
- **Long ranges get longer to finish.** A node gets 10 s per round for a range up to 6 h (as before), 30 s up to 7 days and 100 s beyond. The asking side waits twice that plus 10 s (30 s at least, so short reports are unchanged): 210 s for a 30 day or longer range. The web, MCP and CLI paths use it; the web and MCP servers' write timeouts are 5 minutes (were 90 s) and the cluster's 4 minutes (were 2) so a slow report is not cut off by the server.

### Measured
- Synthetic 300 hours × 20,000 keys (6 million keys) on a 2-core sandbox: a service report 560 ms → 260 ms, a host report 1.5 s → 0.8 s (both with the cache off); the same service report with every hour cached takes well under a millisecond. These are the numbers of a small machine with data that fits in memory: a server with more cores gains more, and a report over data that has to come from disk is limited by the disk, as before.

### Limits
- The cache helps most for repeated reports over the same ranges (a dashboard, a rolling 7 or 30 days). A report with deduplication is cached under its losers: when new flows change the set of losers, the hours are computed again once.
- Host and conversation reports over very long ranges are still the heaviest: they have to add up millions of distinct groups.

### Verified
- New tests: parallel answers equal the serial ones (reports and series, five groupings) and the digest adds up; cache hits, a changed bucket, different filters, losers and their order; the cache stays within its budget; the timeouts scale with the range. gofmt, `CGO_ENABLED=0 go vet ./...`, `CGO_ENABLED=0 go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

### Not verified
- No `-race` run (needs cgo and the PAM headers, which the sandbox lacks); the sharing between goroutines is the accumulators (one per worker) and the cache (its own mutex).
- Not tried on real 20 GB of data or on a many-core machine.

## [v20] - 2026-11-20 — the Flow page's top bar is laid out like every other page's

### Changed
- **The Node picker on the Flow page has the same room beside the Help button as on every other page.** v19 added padding to the wide Flow page's top bar; the cause was that this page alone widened the top bar to 1900 px. It now keeps the width of the others (the content below and the banner stay wide), and the padding from v19 is gone.

### Verified
- In headless Chromium at 1900, 1600 and 1300 px the Node picker and the Help button are at the same positions on Flow, Settings and Monitor ▸ Node (gaps of 225, 75 and stacked px). gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

## [v19] - 2026-11-20 — the Node picker is no longer under the Help button on the Flow page

### Changed
- **On the Flow page the Node picker sits clear of the ? button.** The Flow page is wider than the others (up to 1900 px), so on a wide screen its top bar reached the right edge, where the fixed Help button is, and the button covered the picker's arrow. The wide page now leaves room for the button, so the picker looks as it does on the Topology page. Narrower windows already put the bar below the button and are unchanged.

### Verified
- The Flow page in headless Chromium at 1900 and 1400 px against a two-node mock: the picker, its arrow and the ? button are all visible and apart. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

## [v18] - 2026-11-20 — "All" starts at the oldest data of any node

### Changed
- **`range=all` (the All choice on the Flow page) starts at the oldest bucket of any node in the cluster**, not of the node that was asked. Reports always read every node; only the start of All came from the asking node, so older data held only on another node was left out. The node status now carries `oldest` (the time of the node's oldest bucket), and the asking node takes the earliest of them. A node that cannot be reached, or runs an older version that does not report it, is left out of the choice of start and the report says so as before.

### Verified
- `go test`: a node's status reports its oldest bucket and, with no other node, `oldestAll` is that one. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

### Not verified
- With two or more real nodes: the cluster path reuses the status call the Flow status page already makes, but it was not run against a live cluster here.

## [v17] - 2026-11-20 — the Flow chart no longer falls to zero at the right edge

### Changed
- **The Traffic Over Time chart ends at the last time slot that has data and never draws the slot still being filled.** The chart used to lay out every slot up to the end of the range, so the current minute (or hour, on the longer ranges) and anything after the newest data was drawn as zero and the chart ended in a fall to the baseline that never happened. Slots with no data inside the chart still show as zero, as before. A range with fewer than two finished slots says "Not enough data yet to draw a chart." instead of drawing a line from nothing. The CSV download follows the same slots.

### Verified
- The Flow page in headless Chromium against a mocked API: the top edge of the stack stays level at the right edge (no fall to the baseline). gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

### Not verified
- Against a real collector's data over a day boundary or a long range; the cut-off of the unfinished slot uses the browser's clock.

## [v16] - 2026-11-20 — each node can register its own addresses in DNS

### Added
- **DNS registration** (Configure ▸ Settings ▸ DNS, `--dnsreg`, `dns_register` in the node's config file): the `autoregdns` script, built in. A node puts its own interface addresses into DNS with RFC 2136 dynamic updates over TCP, signed with a TSIG key: `host.domain` for its first IPv4 and IPv6 address, `host-interface.domain` for every address of an interface, and a PTR record for each address. The domain defaults to the first search domain in `/etc/resolv.conf`, the server to the primary in the zone's SOA record (each reverse zone has its own SOA lookup and falls back to the forward zone's server). A check runs every 300 s plus up to 30 % jitter (both settable) and sends an update only for a record that is wrong or missing.
- The key is a BIND key file or `name:base64secret[:algorithm]` (hmac-md5, -sha1, -sha256 default, -sha384, -sha512). It is hidden from read-only accounts, from MCP and from the troubleshooting bundle, and the GUI box hides what is typed.
- **Register now** and **Remove records** buttons and `--dnsreg-now` / `--dnsreg-remove`; the state of the last run (right, updated, removed, failed, the error of each failed record) is shown under the settings and by `--dnsreg`. Help topic for the new tab, `--dnsreg*` in `--help`, README section, `flowdir/dnsreg.json` in the troubleshooting bundle.
- The setting is per node and never replicated, and leaves no key in the file until it is used, so older versions still read the file.

### Changed (compared with the script)
- A name's whole address set is replaced in one UPDATE message, so a second address on an interface no longer overwrites the first, and a client never sees a name half done. `host.domain` gets the first address of *each* family (the script gave the first address only to one family).
- Not registered: the gateways' VIPs and anycast addresses (they move between nodes), interfaces that are down, IPv4 loopback of the whole 127/8, and interfaces matching `skip_interfaces` (for `docker*`, `veth*` and the like).
- A zone is found from the SOA record in the answer or, for a name below the apex, in the authority section; an SOA of the root or of `in-addr.arpa` / `ip6.arpa` is not taken for a zone of this network.
- Remove deletes a PTR record only while it still points at this node's name.
- The result of an update is the reply's result code, with a hint for REFUSED (no or wrong TSIG key) and NOTAUTH (not the primary).

### Verified
- `go test`: a fake authoritative server that checks the TSIG signature on its own (the MAC recomputed from the message) covers first registration, an unchanged second run sending nothing, one changed record put right, remove leaving other nodes' records alone, no key / wrong key refused and nothing written, the server and domain settings, the zone lookup through a resolver file, reverse names for IPv4 and IPv6, key formats (file and `name:secret:alg`), validation, an unused setting leaving no key, the loop starting and stopping, VIPs excluded.
- A real daemon (CGO off) driven through the CLI against a small stdlib DNS server: `--dnsreg-key … --dnsreg-enable` registers A, A and PTR with signed updates, the loop starts again after a daemon restart from the saved file, `--dnsreg-now` finds all three right, `--dnsreg-remove` removes them, `--dnsreg-disable` and clearing the settings leave no `dns_register` key, a bad key is refused with a message, the troubleshooting bundle shows `<removed>` for the key.
- The Settings page in headless Chromium against a mocked API: the new DNS tab, the key box hides its text, the saved block carries the key and the interface patterns (defaults are not written), emptying every box leaves no `dns_register` key, Register now and the two-click Remove records call the right routes. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

### Not verified
- Against a real BIND, Knot or Windows DNS server (only the fake and the TSIG maths of RFC 8945 were checked), and IPv6 reverse zones delegated at a different length than /64. The reply's own TSIG signature is not checked.
- `go test -race` (cgo and the PAM headers are not available here), the real PAM login and a multi-node cluster run.

## [v15] - 2026-11-19 — the Flow measure is a drop-down too

### Changed
- **Bytes, Packets and Flows on the Flow page are a drop-down** next to the time range and Group by, so the bar is three drop-downs and no buttons.

### Verified
- The Flow page in headless Chromium against a mocked API: three choices, no buttons left in the bar, picking Packets sends `metric=packets`. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

## [v14] - 2026-11-19 — the Flow time range is a drop-down

### Changed
- **The time range on the Flow page is a drop-down** (15 Min, 1 Hour, 6 Hours, 1 Day, 7 Days, 30 Days, 90 Days, All) like the Group by one next to it, in place of a row of eight buttons. Bytes, Packets and Flows stay buttons.

### Verified
- The Flow page in headless Chromium against a mocked API: the eight choices, picking 30 Days sends `range=30d` and the box shows it. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

## [v13] - 2026-11-19 — history is kept until the disk cap, and the Flow page can look at all of it

### Changed
- **Hourly history is no longer limited to 7 days.** It is kept until the disk cap (Configure ▸ Settings ▸ Flow ▸ Disk cap) removes the oldest files. `hour_days` still sets a limit in days when one is wanted. Existing configs that set it keep it; a config that did not set it now keeps everything the cap allows. Minute detail is still kept 6 hours.
- **The Flow page has 30 Days, 90 Days and All** next to the 15 Min to 7 Days buttons. All goes back as far as this node's data goes. The API takes `range=30d`, `90d`, `1y` or `all`, and the longest range is 10 years (it was 400 days).
- **When the disk cap removes an hourly file, that hour also leaves the reports**, as it would after a restart (it used to stay in memory until its time ran out).

- **The product is called FlowDirector**, not MCP Flow Director: the window title, the login and menu header, the help, the installer and uninstaller, the certificate subject, the MCP server's title and the docs.

### Cost
- Each hour held in memory takes about 220 bytes a key, up to 4.4 MB at 20,000 keys. A year of busy hours is gigabytes. The memory cap (`mem_max_percent`) drops the oldest hours when it is reached, so how far back you can look is the lower of what the disk cap and the memory cap allow. Reports are answered from memory; the files are read back at start, newest first.

### Verified
- An hour 40 days old is kept with no limit and dropped with `hour_days` set to 7; `range=all` starts at the oldest hour and finds it; 90d and 1y are accepted; the disk cap removing an hourly file removes that hour from the reports. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).
- Not verified: a long history on a real box (memory and the time a report over a year takes).
- `all` starts at the oldest data of the node that is asked; a node whose peers hold older data does not see it.

## [v12] - 2026-11-19 — wildcards in the Flow filter, no red text beside the box

### Added
- **Wildcards in `host`, `src`, `dst` and in bare words.** `10.1.1.*` is the subnet 10.1.1.0/24, `10.1.*.*` is 10.1.0.0/16 (`10.*` is a /8); `*.cox.net` is every host whose DNS name ends in .cox.net; `2001:db8:*` and `10.*.1.1` are matched against the address text. They work with `AND`, `OR`, `NOT`, commas (`host=10.2.3.3,*.cox.net`), the MCP tools and the other nodes of the cluster.
- A trailing-star IPv4 pattern becomes a prefix when the filter is read. The others are patterns that each node turns into addresses of its own, from the addresses its rollups hold for the range (the busiest 4,000), using the reverse-name cache the reports already use (30 minutes). The first query with a name pattern can take a few seconds while names are looked up; the next ones are instant.

### Changed
- **No red message beside the filter box.** A filter that is not understood turns the box red and the reason is its tooltip; the last report stays on screen.

### Verified
- Pattern matching, the IPv4 conversions, and filters run through the same trip a peer's copy makes: subnets, names, IPv6, a star in the middle, NOT, OR, commas, a pattern that matches nothing (zero, not everything). The Flow page in headless Chromium: a bad filter gives a red box with a tooltip and no text beside it. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).
- Not verified: name patterns against a real DNS server (the tests give the lookup a fixed answer).

## [v11] - 2026-11-19 — flow files are written off the lock, on more than one core

### Changed
- **Encoding, compressing and writing the flow files no longer happen while the store is locked.** A minute is copied under the lock (a copy of its keys, nothing more) and a writer goroutine of its own does the rest, so a busy minute cannot hold up the collector or a query. `Close` and a clean stop wait until everything handed to the writer is on disk.
- **A long record is packed on several cores.** A record over 1 MB is cut into 512 KB blocks that are compressed at once (up to GOMAXPROCS). Files of v9 and v10 are still read.
- The writer's queue holds 8 records. If the disk cannot keep up, the collector waits for it rather than let memory grow.

### Measured
- A 3 MB record (100,000 keys): the gob encoding takes about 45 ms, the compression itself about 9 ms. Splitting the compression across two cores saves a millisecond or two, so the gain is in moving the whole write off the lock, not in the threads.

### Verified
- New tests: a record packed in blocks (read back, mixed with a plain frame, cut, damaged), a writer that is busy while the store lock is held (no deadlock), and Close waiting for the queue. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).
- Not verified: under the race detector (the package does not build with cgo here, the PAM headers are missing). The writer owns the open file alone, and the one field it shares with the store (the current file name) has its own mutex.

## [v10] - 2026-11-19 — the Flow page is wider, and a conversation no longer scrolls sideways

### Changed
- **The Flow page uses up to 1900 px** (it was 1150), cards, top bar and banner together, so two full IPv6 addresses fit in one conversation row.
- **On a window too narrow for both addresses of a conversation, the second goes under the first** instead of the list scrolling left and right.

### Verified
- The Flow page in headless Chromium against a mocked API with 39-character IPv6 addresses at 1280, 1440 and 1920 px wide: the list does not scroll at any of them. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

## [v9] - 2026-11-19 — the flow files are compressed

### Changed
- **The minute and hourly files in the flow folder are compressed** with a small LZ4-style codec written into flowdir (standard library only, no setting). On test data the files are about 60% of their old size, for about 5 ms of CPU per minute at 20,000 keys (about 180 MB/s to write, 330 MB/s to read back at start).
- Each record is a frame with its length and a checksum. A frame cut short by a kill, or damaged, ends that file and the records before it are kept.
- Files written by v8 and older (plain) are still read and need no conversion; they are removed as they age out.

### Verified
- Round trip for empty, short, repetitive, random and 64 KB-plus matches; 2,000 damaged blocks (flipped and cut) return an error and never panic; compressed, plain and torn files; the store restart test reads compressed files back. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).
- Not verified: the saving on your real traffic. The 40% is from random synthetic keys; real files should compress at least as well.

## [v8] - 2026-11-20 — long host names no longer widen the Flow list

### Changed
- **A host name under an address is cut at 22 characters with an ellipsis**, and the whole name shows in the hover tooltip. The Flow lists no longer scroll sideways because of long names (a reverse-DNS name such as a CDN's can be 70 characters).

### Verified
- The Flow page in headless Chromium against a mocked API with a 70-character name: the list does not scroll, the name is cut, the tooltip holds the whole name. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

## [v7] - 2026-11-20 — a plainer Flow page

### Changed
- **No pills above the Flow chart.** The ranked list under it already carries each colour, and the hover box names every series.
- **The host name is under the address** in the Flow lists (it was beside it), so a long name no longer pushes the row wide. In a conversation each side has its own name under it.

### Verified
- The Flow page in headless Chromium against a mocked API: no legend above the chart, the name under each address. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

## [v6] - 2026-11-20 — flows sent to a node's own address are received

### Fixed
- **Flows sent to a node's own address were never read.** The collector listened only on the gateway addresses and on the node's `flow_listen` addresses, so an exporter pointed at the node itself (the usual first setup) reached a port with no socket behind it: tcpdump saw the packets, the Flow page stayed empty and nothing was counted as refused. With no `flow_listen`, the collector now listens on every address of the node (`0.0.0.0` and `::`), which also covers the gateway addresses the node holds. With `flow_listen` set, it still listens only on those addresses and the gateway addresses. A host without IPv6 no longer logs an error for the `::` socket.

### Changed
- **The Flow chart is drawn at the width it has.** It was drawn 760 units wide and stretched to the page, so on a wide window its text, legend and hover box came out about twice the size of the rest of the page. It now takes the width of its box, and is drawn again when the window is resized. The Flow page's column could also stay wider than a narrowed window (a grid column that would not shrink); it shrinks now.
- **The Storage folder box shows the folder in use**, not an empty box: the page asks the node being configured (`GET /api/flow/status?local=1`, which also reports `default_dir`). Saving with the default folder does not write `flow_dir`; any other path does.

### Verified
- The Flow chart in headless Chromium at 1700 and 1300 px wide: the drawing is 1:1 with the page (1076 and 986 px) and is redrawn on resize.
- The page in headless Chromium against a mocked API: the box shows the folder in use, a save that leaves it alone writes no `flow_dir`, a changed path is written.
- A daemon with no gateway and no `flow_listen`, an exporter network listed: it listens on `0.0.0.0` for 2055, 4739 and 6343, and a NetFlow v5 packet sent to `127.0.0.1:2055` from a listed address appears in `--flow-top host` and as an exporter in `--flow`. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

### Not verified
- A real firewall's NetFlow v9 or IPFIX (templates, field layout), `::` on a host with IPv6, and a node that holds a gateway address next to the wildcard socket.

## [v5] - 2026-11-20 — the Flow page works when another node is picked

### Fixed
- **The Flow page showed "this part of the GUI always acts on the node you are logged in to"** and no data as soon as a node other than the one you signed in to was picked in the Node menu (a two-node cluster, signed in on one, the other picked). `/api/flow` was missing from the list of API areas that can be relayed to another node (`proxyPrefixes`), so the page's report, exporters and status calls were refused. It is added; a report is cluster-wide whichever node answers. `TestProxyRelaysFlowCalls`.

### Verified
- A daemon started on a config with one exporter network, a NetFlow v5 packet sent to it from an address in that network: `--flow-top host` shows the two conversations, `--flow` lists the exporter by its name, 0 refused. gofmt, `go vet`, `go test ./...` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers).

### Not verified
- The page against a real two-node cluster with the Node menu on the other node (the relay is the same code path as the other pages; the test pins the path list). NetFlow v9 or IPFIX from a real firewall.

## [v4] - 2026-11-20 — the flow storage folder is in the GUI

### Added
- **Storage folder** under Configure ▸ Settings ▸ General ▸ Flow: the folder this node keeps the flow rollups in (`flow_dir`, an absolute path; empty is `flow` in the state directory). It is per node and not shared, because a path belongs to one host. It is read when the daemon starts, so a change applies after the node is restarted, and files in the old folder are not moved. The page keeps writing the node's `flow_listen`, which still has no box.

### Verified
- The page in headless Chromium against a mocked API: the field shows the saved folder and a change is saved as `flow_dir` without touching `settings.flow`. The help test lists the field. gofmt, `go vet`, `node --check`.

### Not verified
- A node moved to a new folder on a real daemon (the folder is created at start; the code is unchanged).

## [v3] - 2026-11-20 — an upgrade from v1 and from v2's tree must work

### Fixed
- **A v1 configuration did not load.** The device inventory was removed from the code but not from the reader: a config that still held `devices`, `device_credentials`, a device on a gateway, an MCP key or allowed source limited to devices (`"devices"`, `devices=`), the privacy secret words, `settings.flow.sources` or the per-node `flow_dir_max_percent` / `flow_mem_max_percent` was refused as having unknown keys, and the daemon exited with status 1. These keys are now dropped when the file is read. Nothing is lost that still matters: the addresses of the old devices and `flow.sources` become entries of `settings.flow.exporters` (named after the device, or the address), so the same senders are still accepted, and the two caps become `settings.flow.dir_max_percent` and `mem_max_percent`. The next save writes a v2-style file. `TestV1ConfigStillLoads` reads `testdata/v1_config.json`, a v1 file with all of these.
- **Files were deleted from the tree**, against the rule in CLAUDE.md (trees are copied over older ones). A leftover `devcapture.go`, `devices.go` and the rest then no longer compiled (`go build failed`). Every removed file is back as an empty file (`package main`, or `package device` under `device/`) and stays; the leftover `devprobe` is an empty `main`. A copy of this tree over the previous one builds and vets.

### Verified
- The daemon was started on `testdata/v1_config.json` (a v1 file): it loads, listens for NetFlow, IPFIX and sFlow and the MCP endpoint, and runs until stopped.
- This tree copied over the old one (the tree before the device removal) builds and vets.
- gofmt, `go vet ./...`, `go test ./...` without cgo (all pass except `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers), cross-builds for linux/amd64, arm64, arm, 386 and riscv64.

### Not verified
- `install.sh` upgrading a real node (the sandbox has no systemd and no PAM headers); the start with PAM, and the GUI, against the migrated config.

## [v2] - 2026-11-19 — a flow collector, not a device manager

### Added
- **Configure ▸ Exporters** (`settings.flow.exporters`): the senders whose flow packets are read, each a `name` and an `address`: an IPv4 or IPv6 address, a network (`10.9.0.0/16`) or a host name (looked up every minute). Anything not listed is refused and counted. The name is what the Flow page and `exporter=` call it; a network's name stands for every address in it, the narrowest network wins. An entry can be switched off (`disabled`). The collector listens as soon as there is one exporter.
- **Disk cap and Memory cap as shared settings** (Configure ▸ Settings ▸ General ▸ Flow; `settings.flow.dir_max_percent` and `mem_max_percent`, 1 to 100, default 90). They were per-node file keys (`flow_dir_max_percent`, `flow_mem_max_percent`); each node applies the shared percentage to its own volume and memory. `flow_dir` and `flow_listen` stay per node.
- **Filter bare words**: in the Flow filter a bare number 0–65535 is a port, and any other bare word (an address, a network, a name) is a host; commas make lists (`443,8443`, `10.0.0.1,10.0.0.2`). A bare `0` matches nearly every flow, because the client's ephemeral port is stored as port 0 (the help says so).
- Tests for exporter validation, for which sender is accepted and what it is called (networks, the narrowest wins, host names, disabled entries, a removed entry), and for the `exporter=` filter by name, address and network.

### Changed
- **Flow is the default page after sign-in.** The Flow filter box is twice as long (36 rem).
- **Configure ▸ Devices is gone, and so is everything that polled a device**: the device drivers, the Logins, Checks and Tags pages, the device health checks, the `device_*` MCP tools and their `/api/devices/...` calls, the packet capture, the change and raw-change tools, the Entra, Meraki and firewall troubleshooting, the user and device troubleshooting, the federation with other directors, the answer cache, the device squares under a gateway on Topology, the secret-word list and the per-device scope of MCP keys and sources. A flow collector only needs to know who may send it flows. The MCP endpoint offers `flow_top`, `flow_timeseries`, `flow_exporters`, `flow_status`, `list_nodes` and `get_topology`, and only reads.
- **`settings.flow.exporters` replaces the device inventory and `settings.flow.sources`**; the exporter filter takes a name, an address or a network, and works with every address a network holds.
- New gateway group numbers start at 230. The default cluster port is 19995; an existing `cluster.listen` in a config file is not changed.
- The help panels, the README, QUICKSTART and CLAUDE.md describe the collector only; no text calls flowdir a fork of anything.

### Fixed
- The Flow page showed "tot.points is null" when a report had no series.
- A fix in this release's own work: the MCP flow tools were registered from a file removed with the device tools and would have vanished; they are registered in `mcp.go` and the MCP tests now require them.

### Verified
- gofmt clean, `go vet ./...`, and `go test ./...` without cgo: everything passes except `TestUpdateBuildApplyAndRollbackGuard` (it runs `go build` with cgo and fails in a sandbox without the PAM headers, `security/pam_appl.h`). Cross-builds for linux/amd64, arm64, arm, 386 and riscv64. `node --check` on `app.js` and `help.js`.
- The GUI was run in headless Chromium against a mocked API: Flow as the default page; Configure ▸ Exporters shows, edits and saves the list (a changed address, a disabled entry kept); Settings ▸ General ▸ Flow shows 90 and 90 for the caps and saves `dir_max_percent: 80` while keeping the exporters; the help panels open on Topology, Exporters, MCP, Privacy, Monitor ▸ MCP and Flow.

### Not verified
- The GUI against the real daemon (there is no PAM in the build environment), and the collector running with a configured exporter list and real exporters (host names, networks, IPv6, a disabled entry); the logic is covered by unit tests only.
- `snaps/` (screenshots of the old product: the device pages and the old name) is left out of the archive and no document refers to it; I was not allowed to delete the folder from the working tree, so remove it there.
- Left in place although nothing needs them any more: the `ro`/`rw` access of MCP keys and sources (MCP is read-only either way), the privacy rules for serial and MAC addresses, the command-line network tools (ping, trace, scan, whois, lookup) and their server endpoints, packet capture on the node itself, and `nmap`/`mtr` in `install.sh`.
- The default update repository `https://github.com/micush/flowdir` is an assumption: set the real one in Configure ▸ Upgrades.

## [v1] - 2026-11-19 — flowdir: a clustered flow collector

The first release of flowdir: a clustered NetFlow, IPFIX and sFlow collector with the gateway VIP election, anycast over BGP and OSPF, the cluster (shared settings, join codes, updates), the web GUI and the MCP endpoint.

### Added
- **Flow collector**: UDP listeners for NetFlow (2055), IPFIX (4739) and sFlow (6343) on every gateway address a node serves (VIPs, further and anycast addresses) and on the node's own `flow_listen` addresses, bound with IP_FREEBIND like the MCP listeners. Exporters are accepted when they are in `settings.flow.sources`; anything else is counted as refused and dropped before decoding.
- **Decoders** (`flow_decode.go`, standard library only): NetFlow v5, v9, IPFIX (enterprise and variable-length fields) and sFlow v5 raw-packet-header samples; sampling from the v5 header, v9/IPFIX options records (fields 34, 50, 305) and the sFlow sample. Every read is bounds-checked; templates are limited per exporter and in total.
- **Template replication** (`flow_cluster.go`): a template or sampling rate one node learns, or that changes, goes to the other nodes within a moment over the signed cluster channel, and a node that starts fetches everything the others know, so an exporter whose packets move to another node is read at once.
- **Store** (`flow_store.go`): per-node rollups: minute buckets by exporter, addresses, service port, protocol, interfaces and AS numbers (the client's ephemeral port is not a key), a key limit per bucket, hourly buckets keeping the biggest keys, written to `flow/` in the state directory and read back at start. `settings.flow` is shared; `flow_listen` is per node.
- **Flow** (GUI; its own entry at the top of the menu, above Topology): time range, group by (conversations, hosts, sources, destinations, services, protocols, flows, exporters, interfaces, AS), bytes/packets/flows, a stacked chart of the top eight with the rest as "Other", the ranked list with shares and average rates, click-through filters that move one level down, a filter box (in the header of the ranked list, centred) that is applied as you type (a moment after the last key) and takes AND, OR, NOT (or `!`) and brackets (`(src=10.1.0.0/16 OR dst=10.1.0.0/16) AND !port=53`; the same `filter` argument is in `--flow-top`, `/api/flow/report` and the MCP tools; a filter with an expression leaves out the folded "everything smaller" entries; the tree is checked for size and depth when a peer sends it), the exporters list, and CSV download of each card. Help panel `flow`.
- **Deduplication** (`dedupe`, on by default, a box under Configure ▸ Settings ▸ General ▸ Flow; `dedupe=on|off` per report): a flow several exporters report counts once in a report, as the exporter that counted most bytes in the range reports it. It works across nodes (anycast): a first round collects each node's digest of (flow, exporter, bytes), the asking node picks the reports to leave out and sends them with the query. Not applied when grouping by exporter or interface. A node's digest holds its biggest 100000 flows (`dedupe_cut` says so); NAT'd flows count twice.
- **Caps** (per node): `flow_dir` sets the folder; `flow_dir_max_percent` (default 90) caps it as a percent of the volume it is on, and `flow_mem_max_percent` (default 90) caps what the rollups hold in memory as a percent of the machine's or cgroup's memory. Both are first in, first out: the oldest files, or the oldest buckets, go first; the file being written and the newest minute stay. History is read back newest first and stops at the memory cap. `flowdir --flow`, `/api/flow/status` and `flow_status` show use, cap and what was removed.
- **Reports** (`flow_query.go`, `flow_api.go`, `flow_cli.go`): by conversation, flow, src, dst, host, service, protocol, exporter, interface, src_as or dst_as; filters by exporter, host, src, dst, port, proto, interface and AS; reports and time series. The node asked fans out over `/cluster/flow/query` and adds the nodes up (a flow is on exactly one node); a node that does not answer is named in the answer. Rows carry share, average rate and names (device, reverse DNS, service). Reached as MCP tools `flow_top`, `flow_timeseries`, `flow_exporters` and `flow_status`, as `GET /api/flow/{report,exporters,status}`, and as `flowdir --flow` and `flowdir --flow-top BY [--flow-args ...]`. A key limited to some devices is neither offered the flow tools nor served the API.

### Changed
- **Saving from Configure ▸ Settings keeps the flow settings.** The page builds the configuration from its own boxes, so `settings.flow` (`enabled`, `sources`, `max_keys`, ...) and this node's `flow_listen`, `flow_dir`, `flow_dir_max_percent` and `flow_mem_max_percent` would have been dropped by any save from it; they are now carried through (only `dedupe` has a box).
- The web GUI's default port is 19994 (was 22161): `web.listen`, the installer, the help text and the default port of a federated flowdir device (`platform: flowdir`) all follow. Nodes of one cluster keep working on any port; an existing `web.listen` in a config file is not changed.

### Verified
- After the rename: gofmt clean, `go vet ./...`, and `go test ./...` without cgo: everything passes except `TestUpdateBuildApplyAndRollbackGuard` (all but `TestUpdateBuildApplyAndRollbackGuard`, which needs the PAM headers). Cross-compiles for linux/amd64, arm64, arm, 386 and riscv64. `bash -n` on `install.sh`, `uninstall.sh` and `get.sh`; `install.sh --dry-run -y` on an Ubuntu os-release prints the new names and paths.
- Flow collector, before and after the rename: tests of every decoder against hand-built packets, 25 000 mutated, truncated and random packets without a panic, template replication, sampling, the store (key limit, compaction, persistence and reload), the merge of node answers, and the MCP tools through `tools/call`.
- Two real daemons joined into a cluster, exporters sending NetFlow v5, v9, IPFIX and sFlow from their own addresses: a report asked of one node adds both nodes up; data-only packets of an exporter whose template only the other node received are decoded, and an exporter nobody has a template for is counted in `no_template`; with one node stopped the report answers and names it; after a restart the history is read back and a template learned in the meantime is fetched. The cluster join, the template replication and the report were run again with the renamed binary.
- Throughput on a 2-core sandbox: 173 ns per stored flow, 706 ns to decode and store a two-record NetFlow v9 packet (about 2.8 million flows/s in one goroutine, before the UDP socket).

### Not verified
- The Flow page was run in headless Chromium (light and dark) against a mocked API, not against the daemon: there is no PAM in the build environment. The data shapes it reads are those of `/api/flow/report` and `/api/flow/exporters` as the Go tests pin them.
- With PAM: the GUI and `/mcp` do not start in a build without cgo, so the API and MCP tools were exercised in-process and the live clusters through the CLI only. `TestUpdateBuildApplyAndRollbackGuard` runs `go build` with cgo and fails in a sandbox without `libpam0g-dev`. `shellcheck` was not available, so the scripts were only syntax-checked.
- Real exporters, a VIP or anycast move under a running exporter, IPv6, and replication of one node's flow history to another (a lost node's flows are unavailable until it returns) were not done.
