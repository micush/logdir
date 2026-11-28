package mcp

import (
	"net/url"
	"strings"

	"logdir/internal/core"
	"logdir/internal/logs"
)

func logQS(c *mcpCtx, extra map[string]string) string {
	v := url.Values{}
	for _, p := range logs.LogSearchProps {
		if s := c.str(p.Name); s != "" {
			v.Set(p.Name, s)
		}
	}
	for _, k := range []string{"step", "top", "after", "field", "measure"} {
		if s := c.str(k); s != "" {
			v.Set(k, s)
		}
	}
	for _, x := range strings.Split(c.str("values"), ",") {
		if x = strings.TrimSpace(x); x != "" {
			v.Add("values", x)
		}
	}
	for k, x := range extra {
		v.Set(k, x)
	}
	return v.Encode()
}

// logMCPTools are the log tools, added to the list in mcp.go's init.
func logMCPTools() []mcpTool {
	return []mcpTool{
		{name: "logs_search", title: "Search the logs",
			desc: "Search the syslog the cluster collects: every node keeps what it received, so the search asks every node and merges the answers by time (newest last). " +
				"Each message has time (UTC), host, program, pid, facility, severity, message, the sender address (source), how it arrived (udp, tcp, tls) and the node that received it. " +
				"Narrow with q (words, and/or/not), host, program, severity (this severe or worse), source and a range; the answer says when there are more matches than the limit (then it holds the newest) and node_errors names a node that did not answer. " +
				"Start with logs_stats to see which hosts and programs are noisy, then search them.",
			noNode: true, props: logs.LogSearchProps,
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/search?" + logQS(c, nil))
				return asAny(b), err
			}},
		{name: "logs_tail", title: "Newest log messages",
			desc:   "The newest messages that match (default the last 100), from every node, newest last. Give `after` (a unix time in milliseconds, a message's t) to get only what came since, to follow the log.",
			noNode: true,
			props: []core.McpProp{
				{Name: "q", Typ: "string", Desc: "Words to find (see logs_search)."},
				{Name: "host", Typ: "string", Desc: "Only this host."},
				{Name: "program", Typ: "string", Desc: "Only this program."},
				{Name: "severity", Typ: "string", Desc: "This severe or worse.", Enum: []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}},
				{Name: "limit", Typ: "integer", Desc: "How many, default 100."},
				{Name: "after", Typ: "string", Desc: "Only messages after this time (milliseconds since 1970, as t in an earlier answer)."},
			},
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/tail?" + logQS(c, nil))
				return asAny(b), err
			}},
		{name: "logs_stats", title: "Log counts",
			desc: "How many messages match, in total, by severity, by the busiest hosts and programs, and (with step) per interval: the quickest way to see what is noisy, when something started or whether a host went quiet. " +
				"Takes the same filters as logs_search. A message a sender sent to two nodes is counted on both.",
			noNode: true,
			props: append([]core.McpProp{
				{Name: "step", Typ: "string", Desc: "Width of a point in the series: 1m, 5m, 1h ... Absent: no series."},
				{Name: "top", Typ: "integer", Desc: "How many hosts and programs (and values of field), default 20."},
				{Name: "field", Typ: "string", Desc: "Also count the values of this field among the matching messages (top_values): dst_port, action, src_ip, rule, or host, program, sender, severity … Reads every matching message, so a long range takes longer."},
				{Name: "measure", Typ: "string", Desc: "With field: per value, also count distinct:FIELD (how many different values of another field, e.g. distinct:dst_port per src_ip: a port scan; exact up to 128, about 1.6% off past that) or sum:FIELD (add up its numbers, e.g. sum:bytes). top_values are then ordered by it."},
				{Name: "values", Typ: "string", Desc: "With field and step: these values of the field (comma-separated, up to 10) counted per bucket, in value_series."},
			}, logs.LogSearchProps[:8]...),
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/stats?" + logQS(c, nil))
				return asAny(b), err
			}},
		{name: "logs_senders", title: "Log senders",
			desc:   "The hosts sending syslog to the cluster: address, the name it was given on Configure ▸ Senders, the host name its messages give, how it sends (udp, tcp, tls), when first and last heard, messages and bytes, and which node receives them.",
			noNode: true,
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/senders")
				return asAny(b), err
			}},
		{name: "logs_shipping", title: "Log shipping status",
			desc: "Where each node sends the syslog on to (Configure ▸ Shipping): for every destination on every node, its state (ok, behind, error, disabled), messages sent " +
				"(in total and in the last minute), how far behind it is (seconds), messages skipped because it fell too far behind, when it last sent and its last error.",
			noNode: true,
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/shipping")
				return asAny(b), err
			}},
		{name: "logs_status", title: "Log collector status",
			desc:   "The state of the log collector on every node: what it listens on, messages received (by udp, tcp and tls), refused and dropped, how many each drop rule kept out (drop_rules: messages that match one are never stored, so a search cannot find them), what is stored and how much disk it uses, and the oldest message each node holds.",
			noNode: true,
			run: func(c *mcpCtx) (any, error) {
				b, err := c.get("/api/logs/status")
				return asAny(b), err
			}},
	}
}
