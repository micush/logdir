// logdir — LogDirector: a clustered, highly available syslog collector with an MCP server.  A cluster of nodes shares a VIP
// (the Distributed Gateway Load Balancing Protocol) and anycast addresses, every node keeps the messages sent to it, a search asks
// every node and merges the answers by time, and MCP answers on the same addresses.
package core

import (
	"fmt"
	"os"
)

func Fatal(err error)           { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
func Fatalf(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...); os.Exit(1) }
