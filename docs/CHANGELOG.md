# Changelog

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

## [v29] - 2026-11-20 — network tools on right-click

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
