package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// The command line of the log collector:
//
//	--logs                                  the collector on every node: what it listens on, counters, the senders
//	--logs-search "words" [--logs-args "k=v&k=v"]   (fields=1: each message's fields under it; words may be fields: action=deny dst_port=443)
//	--logs-ship                             every shipping destination on every node: state, sent, behind, last error
//	--logs-ship-test NAME [--node N]        send a test message to a destination (from every node, or one)
//	--logs-ship-reset NAME --logs-ship-from now|oldest|6h [--node N]
//	                                        move where a destination's sending carries on from (send again, or skip)
//	                                        a search over every node; the words are those of the API (and, or, not); the arguments:
//	                                        range=1h from= to= host=sw* program=sshd severity=err source=10.0.0.5 limit=200 oldest_first=1

type ldOpArgs struct {
	Q string `json:"q"`
}

// LdOpSearch is a search for the CLI, as the daemon builds it.
func (m *Mgmt) LdOpSearch(a ldOpArgs) (any, error) {
	if ldSvc == nil {
		return nil, fmt.Errorf("no log collector in this process")
	}
	v, err := url.ParseQuery(a.Q)
	if err != nil {
		return nil, fmt.Errorf("arguments: %v", err)
	}
	q, err := ldSvc.parseLdQuery(v)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), ldReportTimeout(q))
	defer cancel()
	return ldSvc.Search(ctx, q), nil
}

func (m *Mgmt) LdOpStatus() (any, error) {
	if ldSvc == nil {
		return nil, fmt.Errorf("no log collector in this process")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, errs := ldSvc.AllStatus(ctx)
	sn, errs2 := ldSvc.AllSenders(ctx)
	return map[string]any{"nodes": st, "senders": sn, "node_errors": append(errs, errs2...)}, nil
}

// LdOpShip is the shipping status for the CLI.
func (m *Mgmt) LdOpShip() (any, error) {
	if ldSvc == nil {
		return nil, fmt.Errorf("no log collector in this process")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, errs, n := ldSvc.AllShipStatus(ctx)
	return map[string]any{"destinations": st, "node_errors": errs, "nodes": n}, nil
}

// LdOpShipDo is a test or a reset from the CLI.
func (m *Mgmt) LdOpShipDo(kind string, a ldShipOp, actor string) (any, error) {
	if ldSvc == nil {
		return nil, fmt.Errorf("no log collector in this process")
	}
	a.Dest = nil
	if kind == "shipreset" {
		if _, err := ldShipStart(a.From, time.Now()); err != nil {
			return nil, err
		}
	}
	if _, ok := ldSvc.destination(a.Name); !ok {
		return nil, fmt.Errorf("no destination %q (Configure ▸ Shipping)", a.Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	res, errs := ldSvc.ShipAll(ctx, kind, a)
	infof("logs: shipping %s: %s, by %s", a.Name, map[string]string{"shiptest": "test message", "shipreset": "cursor moved to " + firstNonEmpty(a.From, "now")}[kind], actor)
	return map[string]any{"results": res, "node_errors": errs}, nil
}

func runLogsShip(sock string, f *cliFlags) {
	if name := firstNonEmpty(*f.logsShipTest, *f.logsShipReset); name != "" {
		kind, from := "logs.shiptest", ""
		if *f.logsShipReset != "" {
			kind, from = "logs.shipreset", *f.logsShipFrom
			if from == "" {
				fatalf("--logs-ship-reset needs --logs-ship-from now, oldest or a time back (6h, 2d)")
			}
		}
		var out struct {
			Results    []LdShipResult `json:"results"`
			NodeErrors []NodeError    `json:"node_errors"`
		}
		decode(op(sock, kind, ldShipOp{Name: name, Node: *f.node, From: from}), &out)
		bad := false
		for _, r := range out.Results {
			if r.OK {
				fmt.Printf("%s: done%s\n", r.Node, map[bool]string{true: " (" + r.Note + ")", false: ""}[r.Note != ""])
			} else {
				fmt.Printf("%s: FAILED: %s\n", r.Node, r.Error)
				bad = true
			}
		}
		for _, e := range out.NodeErrors {
			fmt.Printf("NODE DID NOT ANSWER: %s: %s\n", e.Node, e.Error)
			bad = true
		}
		if len(out.Results) == 0 && *f.node != "" {
			fatalf("no node called %s answered", *f.node)
		}
		if bad {
			os.Exit(1)
		}
		return
	}
	var out struct {
		Destinations []LdShipStatus `json:"destinations"`
		NodeErrors   []NodeError    `json:"node_errors"`
	}
	decode(op(sock, "logs.ship", nil), &out)
	if len(out.Destinations) == 0 {
		fmt.Println("no shipping destinations (Configure ▸ Shipping)")
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	if len(out.Destinations) > 0 {
		fmt.Fprintln(w, "DESTINATION\tNODE\tTYPE\tTARGET\tSTATE\tSENT\tPER MIN\tBEHIND\tSKIPPED\tLAST ERROR")
	}
	for _, d := range out.Destinations {
		behind := "-"
		if d.BehindS > 0 {
			behind = (time.Duration(d.BehindS) * time.Second).String()
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%d\t%s\n", d.Name, d.Node, d.Type, d.Target, d.State, d.Sent, d.PerMin, behind, d.Skipped, d.LastError)
	}
	w.Flush()
	for _, e := range out.NodeErrors {
		fmt.Printf("NODE DID NOT ANSWER: %s: %s\n", e.Node, e.Error)
	}
}

func runLogs(sock string, f *cliFlags) {
	if words := *f.logsSearch; words != "" {
		q := *f.logsArgs
		if !strings.Contains(q, "q=") {
			if q != "" {
				q += "&"
			}
			q += "q=" + url.QueryEscape(words)
		}
		var res LdResult
		decode(op(sock, "logs.search", ldOpArgs{Q: q}), &res)
		for _, m := range res.Messages {
			t := time.UnixMilli(m.T).Local().Format("2006-01-02 15:04:05")
			prog := m.Program
			if m.PID != "" {
				prog += "[" + m.PID + "]"
			}
			fmt.Printf("%s %-7s %s %s: %s\n", t, m.Severity, m.Host, prog, m.Message)
			if len(m.Fields) > 0 { // --logs-args "fields=1"
				var b strings.Builder
				for i, kv := range m.Fields {
					if i > 0 {
						b.WriteByte(' ')
					}
					v := kv.Val
					if v == "" || strings.ContainsAny(v, " \t\"") {
						v = strconv.Quote(v)
					}
					b.WriteString(kv.Key + "=" + v)
				}
				fmt.Println("    " + b.String())
			}
		}
		fmt.Printf("-- %d message(s) from %d node(s), %s to %s", res.Matched, res.Nodes,
			time.UnixMilli(res.From).Local().Format("2006-01-02 15:04"), time.UnixMilli(res.To).Local().Format("2006-01-02 15:04"))
		if res.Duplicates > 0 {
			fmt.Printf(", %d copy(ies) dropped", res.Duplicates)
		}
		fmt.Println()
		for _, e := range res.NodeErrors {
			fmt.Printf("NODE DID NOT ANSWER: %s: %s\n", e.Node, e.Error)
		}
		if res.Note != "" {
			fmt.Println("note:", res.Note)
		}
		return
	}
	var out struct {
		Nodes      []LdNodeStatus `json:"nodes"`
		Senders    []LdSenderInfo `json:"senders"`
		NodeErrors []NodeError    `json:"node_errors"`
	}
	decode(op(sock, "logs.status", nil), &out)
	for _, n := range out.Nodes {
		fmt.Printf("%s: %s; %d received (%d udp, %d tcp, %d tls), %d refused, %d dropped; %d stored in %d hour(s); %d connection(s)\n",
			n.Node, map[bool]string{true: "collecting", false: "off"}[n.Enabled], n.Received, n.UDP, n.TCP, n.TLS, n.Refused, n.Dropped, n.Stored, n.Hours, n.Conns)
		for _, l := range n.Listening {
			fmt.Println("    listening on", l)
		}
		if n.Dir != "" {
			fmt.Printf("    kept in %s: %s of a cap of %s; %d hour(s) removed by the retention or the cap\n", n.Dir, humanBytes(n.DirBytes), humanBytes(n.DirCap), n.CapRemoved)
		} else if n.DirError != "" {
			fmt.Println("    NO DISK: messages are in memory only:", n.DirError)
		}
		if n.MemCap > 0 {
			fmt.Printf("    memory: %d hour(s) cached, %s of a cap of %s; searches found an hour cached %d time(s) and read it from disk %d time(s)\n", n.MemHours, humanBytes(n.MemBytes), humanBytes(n.MemCap), n.CacheHits, n.CacheMiss)
		}
		if len(n.DropRules) > 0 {
			fmt.Printf("    drop rules: %d message(s) not kept since this node started\n", n.RuleDrop)
			for _, d := range n.DropRules {
				last := "never"
				if d.Last > 0 {
					last = "last " + time.UnixMilli(d.Last).Format("2006-01-02 15:04:05")
				}
				fmt.Printf("      %-24s %10d  %s\n", d.Name, d.Dropped, last)
			}
		}
		if n.WriteErrs > 0 {
			fmt.Printf("    %d WRITE ERROR(S)\n", n.WriteErrs)
		}
		for _, l := range n.Failed {
			fmt.Println("    CANNOT LISTEN on", l)
		}
	}
	for _, e := range out.NodeErrors {
		fmt.Printf("NODE DID NOT ANSWER: %s: %s\n", e.Node, e.Error)
	}
	if len(out.Senders) > 0 {
		w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "SENDER\tNAME\tHOST\tVIA\tNODE\tMESSAGES\tLAST HEARD")
		for _, e := range out.Senders {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s ago\n", e.Addr, e.Name, e.Host, e.Via, e.Node, e.Msgs, time.Since(time.Unix(e.Last, 0)).Round(time.Second))
		}
		w.Flush()
	}
}

// runLogsCat writes the plain JSON lines of each stored hour to stdout: `.jsonl` as it is, `.jsonl.lz` unpacked.  It reads files only,
// so it works with the daemon stopped, or on a copy of a folder.  Several files are written in the order given.
func runLogsCat(files []string) {
	out := bufio.NewWriterSize(os.Stdout, 1<<18)
	defer out.Flush()
	for _, name := range files {
		fh, err := os.Open(name)
		if err != nil {
			out.Flush()
			fatal(err)
		}
		var r io.Reader = fh
		if strings.HasSuffix(name, ".lz") {
			zr, err := ldLzReader(fh)
			if err != nil {
				fh.Close()
				out.Flush()
				fatalf("%s: not a logdir hour file", name)
			}
			r = zr
		}
		_, err = io.Copy(out, r)
		fh.Close()
		if err != nil {
			out.Flush()
			fatal(err)
		}
	}
}
