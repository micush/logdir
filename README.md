# LogDirector

LogDirector (`logdir`) is a clustered, highly available syslog collector with a built-in [MCP](https://modelcontextprotocol.io) server. Routers, switches, firewalls and servers send their syslog to one address; an assistant such as Claude connects to the same address and asks what a host logged around the time something broke, which hosts are noisy, or whether anyone failed to log in last night. It is one Go program with no dependencies beyond the standard library.

What you get:

- **One address that survives a node failure.** Several nodes share a virtual IP (the gateway's **VIP**). The nodes elect a controller among themselves and spread clients across every node. A node can fail or restart without the exporters or the assistant losing the address.
- **Anycast over BGP or OSPF.** A gateway can also announce extra addresses to your routers through FRR, so every site exports to its nearest node. A node withdraws them when it cannot answer.
- **Distributed logging.** Syslog over UDP, TCP and TLS (RFC 3164 and RFC 5424, plus what Cisco, PAN-OS and others send in their place), received on the gateway addresses. Every node stores what it received; a search asks every node and merges the answers by time, so one node's loss loses only what it held and the page says which node did not answer.
- **Only listed senders.** Syslog carries no login, so logdir accepts it only from the senders you list: a name and an address, a network or a host name.
- **Pattern alerts.** A search that matches enough messages in a window opens an alert, with the webhook and silences the other alerts have.
- **Read-only MCP, with guard rails.** Access is by address list or bearer key. The endpoint only reads: log searches and the state of the cluster. Public addresses are hidden from the assistant by default (see [Privacy levels](#privacy-levels)), and every call is logged.
- **A cluster you manage from any node.** Shared settings, config history, certificates and software updates stay in step across the nodes, through a web GUI or the command line.

## Contents

- [Status](#status)
- [How it works](#how-it-works)
- [Install, upgrade, uninstall](#install-upgrade-uninstall)
- [Connecting an assistant](#connecting-an-assistant)
- [MCP tools](#mcp-tools)
- [Security](#security)
- [MCP usage](#mcp-usage)
- [Privacy levels](#privacy-levels)
- [Clustering](#clustering)
- [Anycast addresses, BGP and OSPF](#anycast-addresses-bgp-and-ospf)
- [Neighbor discovery (LLDP)](#neighbor-discovery-lldp)
- [Log collector](#log-collector)
- [Configuration file](#configuration-file)
- [Build from source](#build-from-source)
- [License](#license)

## Status

| Area | State |
|---|---|
| VIP election, anycast, BGP, OSPF, LLDP, cluster, users, backups, scheduler, updates | built in |
| Log collector | syslog over UDP, TCP and TLS received on the gateway addresses, kept per node, searched cluster-wide (`--logs-search`, MCP `logs_search`, `/api/logs/search`, **Logs** in the GUI) |
| Senders | **Configure ▸ Senders** (`settings.logs.senders`): a name and an address, a network or a host name; anything else is refused |
| Pattern alerts | **Configure ▸ Senders ▸ Pattern alerts** (`settings.logs.alerts`) |
| MCP | read-only: `logs_search`, `logs_tail`, `logs_stats`, `logs_senders`, `logs_status`, `list_nodes`, `get_topology` |
| Web GUI | Logs (the default page), Topology, Monitor, Configure |

## How it works

A **gateway** is one shared address, its VIP (IPv4, IPv6 or both), served by several nodes. The nodes of a gateway find each other over multicast, or over a unicast list where multicast is blocked, and elect one **controller (AGC)**. The others are **forwarders (AFN)**. The controller answers ARP and IPv6 neighbor solicitations for the VIP with the MAC address of a node chosen by the gateway's load-balancing method, so clients are spread over all the nodes. The MAC is either a virtual one per node, which another node takes over when that node fails, or, in real-MAC mode (the default for a new gateway), the node's own.

Every node that is active or forwarding answers on the VIP (and the other gateway addresses) with a listener of its own that serves only the MCP endpoint, `/mcp`, on port 443 by default (`settings.mcp_port`). The GUI does not answer on those addresses: it listens on port 11154 of the node's own addresses. A node whose MCP listener is not serving withdraws its anycast addresses, so traffic goes to a node that can answer.

One answer to an assistant is at most 1 MB by default and is cut, with a note, beyond that; `settings.mcp_max_result_mb` (Configure ▸ MCP ▸ Largest answer) raises it to at most 8. An assistant reads the answer into its context, and 1 MB of text is roughly 250,000 tokens, so ask for less before raising it.

## Install, upgrade, uninstall

Supported systems are Ubuntu, Debian, Fedora, RHEL, Rocky, Alma, Arch and Manjaro, and their derivatives. logdir is Linux-only (macvlan, AF_PACKET, inotify).

```
tar xzf logdir_v10.tgz && cd logdir
sudo ./install.sh [--add-user NAME] [--no-start] [-y] [--dry-run]
sudo logdir-uninstall [--purge]
```

The installer builds logdir from the source tree with Go 1.24 or newer (it fetches a toolchain when the distribution's is too old). It installs only the missing prerequisites: gcc and the PAM headers, iproute2, FRR, nmap, ping and mtr, snmpd and lldpd. Then it sets up the `logdir` group, the PAM service, the systemd unit and `/var/lib/logdir`. Running it again on a newer tree upgrades. A start that fails rolls back to the previous binary.

`QUICKSTART.md` walks through a first gateway and connecting an assistant.

## Connecting an assistant

1. **Allow the caller.**
   - By address: add it to `settings.mcp_acl` (`"10.0.0.0/24"`). A privacy level may follow: `"10.0.0.0/24 strict"` (see [Privacy levels](#privacy-levels)). An entry may end in a note after a `#` (`"10.0.0.20 # mike's laptop"`, up to 200 characters, one line): it says what the entry is for and changes nothing.
   - An entry (or a key) can be switched off without deleting it: right-click ▸ **Disable**. In the file an address then ends with `disabled` and a key has `"disabled": true`; it allows nothing until enabled.
   - Or with a key that works from any address: Configure ▸ MCP ▸ Keys ▸ right-click ▸ **Add key** makes one and shows it once. In the file, `{"name": "laptop", "key": "<at least 32 random characters>"}` in `settings.mcp_keys` does the same. Only a SHA-256 hash of the key is kept. Both a key (`"expires": <unix time>`) and an address-list entry (`10.1.1.50 until=2026-12-31T17:00Z`) can have an expiry, in UTC; after it the key is refused as expired and the entry allows nothing, and the row stays in the list.
2. **Give the gateway a certificate the client trusts.** The GUI starts with a self-signed one; install yours with `logdir --tls-install --cert-file F --key-file F`. The primary's certificate is copied to every node.
3. **Point the client at the VIP.** For Claude Code:

```
claude mcp add --transport http logdir https://GATEWAY/mcp --header "Authorization: Bearer KEY"
```

The endpoint is MCP over streamable HTTP with JSON answers (protocol versions 2024-11-05 to 2025-11-25). Requests from a browser page on another origin are refused.

## MCP tools

| Tool | What it does |
|---|---|
| `logs_search` | the messages that match words (`failed password and not root`), a host, a program, a severity (this severe or worse), a sender (an address, or a name with `*` wildcards: `source=sw*.example.com`) and a time range, from every node, merged by time |
| `logs_tail` | the newest messages, or only what came after a time, to follow the log |
| `logs_stats` | how many match, by severity, by the busiest hosts and programs, and per interval |
| `logs_senders` | the senders (and when each was last heard from), with the name each address reverse-looks-up to in DNS |
| `logs_status` | the collector on every node: what it listens on, counters, disk |
| `list_nodes`, `get_topology` | the cluster status: the nodes (reachable, paused, primary, version, update state) and the gateways with their active node and anycast addresses |
| *(nothing else)* | **An MCP caller can only read logs and the state of the cluster.** There is no tool for logdir's own configuration, the GUI's API, alerts, the scheduler, updates, the host's load, LLDP, the log, users, backups or cluster tokens, and no tool takes a `node`: the node that serves the endpoint answers, and a `node` argument is refused. Those are read in the GUI or the CLI by an administrator. |

## Security

- **MCP reads, never writes.** Nothing can be changed over MCP: settings, the cluster, gateways, schedules, updates, alerts, users and certificates are changed in the GUI. The rule is in the request path, so no route is missed.
- **Secrets never reach a caller.** Keys, SNMP communities, passwords and join codes read `(hidden)` to every MCP caller. A read-only GUI account sees `(hidden)` too.
- **Syslog is not authenticated.** UDP and TCP syslog carry no login, so only the senders you list are read (Configure ▸ Senders); any other sender is dropped before a byte is parsed and counted as refused. TLS encrypts the connection but, as the collector does not ask senders for a certificate, it too relies on the sender's address. List only addresses that cannot be spoofed from outside your network.
- **Everything is logged.** Each MCP call is logged with the caller's name (`KEYNAME@ADDRESS` or the address) and the tool.
- **Treat answers as data.** Whatever an answer holds (log messages, host names, sender names) is data for the assistant, never instructions. The MCP server says so in its instructions to the client.
- **Where credentials live.** They are kept in the configuration file (`/var/lib/logdir/logdir.conf`, mode 0600, root) and replicated to the cluster's nodes over the pinned, signed cluster channel.

## MCP usage

**Monitor ▸ MCP** shows what the MCP endpoint is used for on the node the Node menu has picked: calls by **key** (or *(address list)* for a caller let in by its address), by **source address** and by **tool**, with errors, refused calls, the data returned and the average time; a chart of calls per hour; and the newest 200 calls. The page is laid out like Monitor ▸ Node (time range buttons, tiles, charts per hour, tables), sits just below it in the menu, and its time range is the last hour, day or week. The daemon keeps the counts by the hour for 8 days (`mcpusage.json` in the state directory, so they survive a restart and an upgrade); the newest-calls list lives in memory. Only names, sizes and times are kept, never what a caller sent or was answered. The same numbers are `GET /api/mcpusage?window=24h`. Each node counts the calls it answers, so pick each node in turn to see a whole cluster.

## Privacy levels

What an assistant is told can be limited, because the assistant is usually a public AI service. Every answer is rewritten on its way out: a hidden address becomes `x.x.x.x` (or shows some of its parts), a hidden AS number shows its last three digits, an e-mail address shows only the name before the `@`, and a serial number or word of yours becomes a token that is the same for the same value. The tokens change when the daemon restarts and cannot be turned back into the value. Three levels:

| Level | Hides |
|---|---|
| `none` | nothing: every address is shown as it is. logdir's own keys and tokens stay hidden. Every such answer is logged. |
| `standard` (default) | public IP addresses and AS numbers, e-mail addresses, SNMP contact and location, serial numbers except the last four characters |
| `strict` | all of that, private IP addresses and AS numbers, MAC addresses, serial numbers completely |

Configure ▸ Privacy sets the **default** level, for callers that have none of their own, and what each of `standard` and `strict` hides: public and private addresses, AS numbers, e-mail addresses, contacts, serial numbers (shown, last four, hidden), MAC addresses (shown, vendor part, last three bytes, hidden), how many leading or trailing digits of a hidden address stay readable (`203.0.x.x`, or `x.x.114.7`), and a list of your own words (company, site and host names) that are always hidden. A key has a **Privacy** setting in Configure ▸ MCP ▸ Keys and an allowed source has one in the same table. A caller with both a key and an address gets the strictest.

In the file:

```json
"settings": {
  "mcp_acl": ["10.0.0.20 strict # mike's laptop", "10.0.0.0/24 none"],
  "mcp_keys": [{"name": "laptop", "access": "ro", "privacy": "strict", "notes": "for the audit", "key": "…"}],
  "privacy": {"default": "standard", "strict": {"public_ips": true, "private_ips": true, "ip_keep": -2,
    "public_asns": true, "private_asns": true, "serials": "hide", "macs": "nic", "emails": true,
    "contacts": true, "words": ["contoso", "lab-edge"]}}
}
```

Hiding is best effort: a value written in an unusual way, an address split over two fields or a host name is not recognised. With no `privacy` setting the standard level applies.

## Clustering

Several nodes are managed as one cluster. The cluster is for management and is independent of the gateway election.

- **Roles.** One node is the primary and the others are replicas.
- **Shared and per-node settings.** The `settings` block, the gateways' VIPs, keys and timers, the web login groups and the sign-in rules are shared. They can be edited on any node and are applied on the primary. Interface, priority, weight and the web and cluster blocks stay per node.
- **Joining.** Create a join code on a member (`--cluster-token`) and use it on the new node (`--cluster-join CODE`). A code works once, for an hour. A node keeps at most 20 unused codes; `--cluster-token-clear` (Operate ▸ Cluster ▸ **Clear join codes**) withdraws them.
- **Trust.** Nodes pin each other's certificates and sign every request.
- **Promotion and failover.** Promotion is explicit (`--cluster-promote`). With three or more members there is also automatic failover after 5 minutes, and the primary role is handed over before a planned restart. For an update the hand-over is temporary: the node that was primary when the update began is remembered (by whichever node holds the role, as it passes from node to node), and once nothing is queued, installing or restarting any more (30 seconds in a row) the role goes back to it. A node whose update failed, a member that was removed, or a promotion by hand (or by failover) drops it; a plain restart or shutdown leaves the role where it landed.
- **Updates.** Upload a release archive on any node (`--update-upload`). Each node then builds it, swaps its binary and restarts, one node at a time, with automatic rollback.

## Anycast addresses, BGP and OSPF

A gateway's anycast addresses (`extra_vips`) are held on `lo` of every node that runs the gateway, with a 10 s lifetime renewed every 3 s. FRR announces them over BGP (with BFD) or OSPF. An address is withdrawn when:

- the node's listener is not serving,
- the gateway is paused or stops,
- no BGP neighbor of its family is established (and OSPF is not carrying it), or
- logdir stops.

A `kill -9` takes it away within the lifetime. BGP and OSPF settings are per node: `--asn`, `--bgp-neighbor-add`, `--ospf-interface-add`; see `logdir --help`.

## Neighbor discovery (LLDP)

With `settings.lldp` on, every node runs lldpd. The switches then see the nodes, and each node reports the switch port it is on (`logdir --lldp` and the GUI). This is reporting only.

## Log collector

Point a device's syslog at a **gateway address** (a VIP, a further address or an anycast address) and list the device under **Configure ▸ Senders**. The collector listens on UDP 514, TCP 514 and TLS 6514 on every address of the node, so the node's own address works too, and a sender that points at the VIP follows the active gateway: when that node fails the messages go to the next node, and each node keeps what it received.

| Setting | Meaning |
|---|---|
| `enabled` | listen even with no sender listed (a sender entry also turns it on) |
| `udp_port`, `tcp_port`, `tls_port` | another port; `-1` turns that listener off |
| `senders` (Configure ▸ Senders) | the senders accepted: a `name` and an `address` (an IPv4 or IPv6 address, a network such as `10.9.0.0/16`, or a host name that DNS resolves, looked up again every minute); `"disabled": true` keeps an entry but refuses it. The narrowest network wins when entries overlap |
| `retention_days` (Configure ▸ Settings ▸ General ▸ Log collector ▸ Keep) | delete hours older than this; 0 keeps them until the disk cap removes the oldest |
| `mem_max_percent` (Configure ▸ Settings ▸ General ▸ Log collector ▸ Memory cap) | cap on the cache of closed hours kept in memory for searches: this percent (default 90) of the machine's memory, or of the container limit when that is smaller, oldest hour first; the hour being written is never cached. Each node applies it to its own memory |
| `dir_max_percent` (Configure ▸ Settings ▸ General ▸ Log collector ▸ Disk cap) | cap on the folder: this percent (default 90) of the volume it is on, oldest hour first. Each node applies it to its own volume |
| `max_line` | longest message kept, in bytes (default 8192); more is cut off |
| `alerts` (Configure ▸ Senders ▸ Pattern alerts) | `name`, `query`, optional `host` and `min_severity` (0 to 7), `count` (default 1) and `window_min` (default 5), `disabled`; see below |
| `log_listen` (per node) | when set, listen only on these local addresses and on the gateway addresses this node serves, instead of on every address |
| `log_dir` (per node; Configure ▸ Settings ▸ General ▸ Log collector ▸ Storage folder) | the folder the segments are written to (default `logs` in the state directory); read when the daemon starts |

**What is accepted.** RFC 5424 and RFC 3164 messages with or without a priority, ISO and BSD timestamps, Cisco sequence numbers and `*Mar  1 18:48:50.483 UTC:` stamps, and PAN-OS CSV bodies; text that matches none of these is kept whole as the message. TCP and TLS take newline-framed lines and octet-counted frames (RFC 6587), told apart message by message. A message is stored with the time **this node received it**: senders' clocks are not trusted, and receive time is what every node can sort by (the sender's own timestamp is kept beside it when it could be read).

**What is stored.** One file of JSON lines per hour (UTC), `2026112005.jsonl`, appended through a buffer that is written every second and before an update restarts the daemon. A closed hour is compressed (`.jsonl.lz`) with a small index beside it (the hosts, programs and severities it holds), so a search reads only the hours that can hold what it asks for. Retention and the disk cap delete whole hours, oldest first. To read a stored hour outside logdir, `logdir --logs-cat 2026112005.jsonl.lz | grep sshd` writes it as plain JSON lines (it reads the file only, so the daemon need not run; several files can be given).

**In the cluster.** Every message is stored on the node that received it. A search (`/api/logs/search`, `--logs-search`, the Logs page, `logs_search`) asks every node, merges the answers by time and keeps the newest up to the limit; a node that does not answer is named (`node_errors`) and what it holds is missing from the answer. A message a sender sent to two nodes is on both and appears once. Counts (`logs_stats`) add the nodes up and count such a message twice.

**Searching.** Words are found in the host, program and message, in any case; `and`, `or`, `not` (or `!word`) combine them, and plain words next to each other are one phrase. `host` and `program` take `*` wildcards. `severity=err` means error or worse (emergency, alert, critical, error).

**Pattern alerts.** Every minute each node counts the messages *it received* that match each rule in its window. At `count` or more, the alert `Log pattern matched` opens, with the rule's name as its subject; it closes when the count falls under. It uses the webhook, repeat and silences of **Configure ▸ Alerts**. Because each node counts its own, a burst that a failover splits between two nodes is counted in two parts.

**Limits.** Searches read the files of the hours in the range, so a very wide range over a busy store takes a while (a node that runs out of time says so). There is no full-text index: words are matched by reading, and only the host, program and severity can rule an hour out beforehand. No parsing of vendor message bodies beyond the syslog header. Messages are not replicated between nodes: a failed node's disk is the only copy of what it held.

## DNS registration

Each node can put its own addresses into DNS, so its name follows the address it has (Configure ▸ Settings ▸ DNS, or `--dnsreg`). It uses RFC 2136 dynamic updates over TCP, signed with a TSIG key (hmac-md5, -sha1, -sha256, -sha384 or -sha512):

- `host.domain`: the node's first IPv4 address (A) and first IPv6 address (AAAA).
- `host-interface.domain`: every address of that interface.
- A PTR record for each address, in its reverse zone (a /24 for IPv4, a /64 for IPv6), pointing at the first of its names.

The domain is the first search domain in `/etc/resolv.conf` and the server is the primary named by the zone's SOA record, unless the `domain` and `server` settings say otherwise. A check runs every `interval_sec` (default 300, plus up to 30 % at random) and sends an update only for a record that is wrong or missing. Loopback, link-local and multicast addresses, interfaces that are down, interfaces matching `skip_interfaces`, and the gateways' own VIPs and anycast addresses are never registered. The setting is per node and never replicated (`dns_register` in the node's config file; the `key` is hidden from read-only accounts and from the troubleshooting bundle).

```
logdir --dnsreg-key ddns-key:c2VjcmV0c2VjcmV0 --dnsreg-enable
logdir --dnsreg-now        # check now and list every record
logdir --dnsreg-remove     # take this node's records out (turn registration off first)
```

## Configuration file

`/var/lib/logdir/logdir.conf` (JSON, mode 0600). Changes apply live: the daemon watches the file. `source/logdir.conf.example` is a starting point, and `logdir --help` lists every key. The top-level blocks are:

- `groups`: the gateways
- `settings`: shared by the cluster
- `web`: the GUI and MCP listener
- `cluster`, `bgp`, `ospf`, `snmp`, `syslog`, `dns_register`, `log_level`

Every change from any source is kept as a version in `/var/lib/logdir/versions`: `--versions`, `--version-diff`, `--version-restore`.

## Build from source

From `source/`:

```
go build -o logdir .
go test ./...                # Linux only
```

logdir uses Go's standard library only. The GUI's PAM login needs cgo and the PAM headers. A build without cgo serves no GUI, and with it no MCP endpoint.

## License

GPL-3.0; see `LICENSE`.

