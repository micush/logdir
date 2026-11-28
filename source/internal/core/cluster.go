package core

import (
	"crypto/rand"
	"encoding/hex"
)

// Management-cluster membership and role state, after umiss's
// internal/cluster: exactly one node is primary at any time, every promotion
// increments a cluster-wide epoch, and any node that learns of a higher epoch
// defers to it unconditionally.  Promotion is admin-confirmed (no automatic
// election), so this only has to make promotions durable and monotonic.
//
// This package-level state is network-free; clusternet.go drives it.

type Role string

func RandHex(nbytes int) string {
	b := make([]byte, nbytes)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func ContainsStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
