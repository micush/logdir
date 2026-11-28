package main

import (
	"net/netip"
	"testing"
	"time"

	"logdir/internal/logs"
)

var ldSrc = netip.MustParseAddr("10.1.1.5")

func newTestStore(t *testing.T, now *time.Time) *logs.LdStore {
	s := logs.NewLdStore(t.TempDir(), "n1")
	s.Now = func() time.Time { return *now }
	s.Load()
	return s
}
