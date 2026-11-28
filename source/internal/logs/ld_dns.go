package logs

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"logdir/internal/core"
)

// Reverse names of the addresses that send (or are named in a search): looked up in the background, kept for 30 minutes, never waited
// on for long.  A sender is shown by its reverse name when it has one (the Topology page, the Senders card, the sender of a message),
// and a search can pick senders by it (source=sw*.example.com).  The same cache flowdir uses for its reports.

type ldDNS struct {
	mu sync.Mutex
	m  map[netip.Addr]ldDNSEnt
}

type ldDNSEnt struct {
	name string
	at   time.Time
	ttl  time.Duration // how long this answer stands: ldDNSTTL for a name or a definite "no name", ldDNSRetry for a failed lookup
}

const (
	ldDNSTTL   = 30 * time.Minute
	ldDNSRetry = time.Minute // a lookup that timed out or failed is tried again after this, not remembered as "no name" for 30 minutes
	ldDNSEach  = 2 * time.Second
)

func (e ldDNSEnt) fresh() bool {
	ttl := e.ttl
	if ttl == 0 {
		ttl = ldDNSTTL
	}
	return time.Since(e.at) < ttl
}

func newLdDNS() *ldDNS { return &ldDNS{m: map[netip.Addr]ldDNSEnt{}} }

// peek is what is known of an address without asking anyone: its name (empty when it has none) and whether it is known.
func (d *ldDNS) peek(a netip.Addr) (string, bool) {
	if d == nil {
		return "", true // no resolver: nothing to wait for
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.m[a]
	return e.name, ok && e.fresh()
}

// names looks addresses up, a few at a time, within the budget; what is not answered in time is left out (and tried again next time).
func (d *ldDNS) names(addrs []netip.Addr, budget time.Duration) map[netip.Addr]string {
	out := map[netip.Addr]string{}
	if d == nil {
		return out
	}
	var todo []netip.Addr
	seen := map[netip.Addr]bool{}
	d.mu.Lock()
	for _, a := range addrs {
		if !a.IsValid() || seen[a] {
			continue
		}
		seen[a] = true
		if e, ok := d.m[a]; ok && e.fresh() {
			if e.name != "" {
				out[a] = e.name
			}
			continue
		}
		todo = append(todo, a)
	}
	d.mu.Unlock()
	if len(todo) == 0 {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, a := range todo {
		wg.Add(1)
		sem <- struct{}{}
		go func(a netip.Addr) {
			defer wg.Done()
			defer func() { <-sem }()
			lctx, lc := context.WithTimeout(ctx, ldDNSEach)
			defer lc()
			names, err := core.ReverseNames(lctx, a.String())
			name, ttl := "", ldDNSTTL
			if err == nil && len(names) > 0 {
				name = strings.TrimSuffix(names[0], ".")
			} else if err != nil {
				var de *net.DNSError
				if !errors.As(err, &de) || !de.IsNotFound {
					ttl = ldDNSRetry // a timeout or a server failure is not an answer
				}
			}
			if err != nil && ctx.Err() != nil {
				return // out of time: not an answer
			}
			d.mu.Lock()
			if len(d.m) > 20000 {
				d.m = map[netip.Addr]ldDNSEnt{}
			}
			d.m[a] = ldDNSEnt{name, time.Now(), ttl}
			d.mu.Unlock()
			if name != "" {
				mu.Lock()
				out[a] = name
				mu.Unlock()
			}
		}(a)
	}
	wg.Wait()
	return out
}

// hostPart is the first label of a name: sw1 for sw1.example.com.
func hostPart(name string) string {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}
