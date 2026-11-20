package main

import (
	"context"
	"fmt"
	"time"
)

// Pattern alerts (settings.logs.alerts): every minute the alert engine asks this node's store how many messages match each rule in
// its window.  At least Count of them opens an alert (rule log_pattern, subject: the rule's name); fewer closes it.  The count is of
// what this node received: a burst split by a failover between two nodes is counted on each, and each node raises its own alert.

func ldAlertSamples() []alertSample {
	s := ldSvc
	if s == nil {
		return nil
	}
	return ldAlertSamplesFor(s.config(), s.store)
}

func ldAlertSamplesFor(cfg LogsConfig, store *ldStore) []alertSample {
	var out []alertSample
	for _, a := range cfg.Alerts {
		if a.Disabled {
			continue
		}
		count, window := a.Count, a.WindowMin
		if count <= 0 {
			count = 1
		}
		if window <= 0 {
			window = 5
		}
		q := LdQuery{From: time.Now().Add(-time.Duration(window) * time.Minute).UnixMilli(), Q: a.Query, Host: a.Host, MinSev: -1}
		if a.MinSev != nil {
			q.MinSev = *a.MinSev
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		n := store.CountSince(ctx, q)
		cancel()
		out = append(out, alertSample{Rule: "log_pattern", Subject: a.Name, Value: float64(n), Bad: int(n) >= count,
			Message: fmt.Sprintf("%d message(s) matched %q in the last %d minute(s) on this node (the rule opens at %d)", n, ldDescribe(a), window, count)})
	}
	return out
}

func ldDescribe(a LogAlertCfg) string {
	s := a.Query
	if a.Host != "" {
		s += " host=" + a.Host
	}
	if a.MinSev != nil {
		s += " severity<=" + sevName(int8(*a.MinSev))
	}
	return s
}
