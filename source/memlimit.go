package main

import (
	"math"
	"os"
	"runtime/debug"
)

// goMemLimit is the soft limit given to Go's garbage collector, in bytes: 20 points above the cache's memory cap (capPct,
// 0 = the default), at most 92 %, so the collector works harder as the process nears what the machine (or its cgroup) allows
// instead of letting a big report push it past that and get the process killed.  The gap is the room searches have for the hours
// they read beyond the cache: with only a few points, a full cache kept the collector running all the time and every search
// crawled.  The cache's cap is a share of the machine's memory and is checked once a minute; this one is the runtime's.  0 total
// (the memory is not known) is no limit.
func goMemLimit(total uint64, capPct int) int64 {
	if total == 0 {
		return math.MaxInt64
	}
	if capPct <= 0 {
		capPct = ldDefaultMemPct
	}
	p := capPct + 20
	if p > 92 {
		p = 92
	}
	return int64(total / 100 * uint64(p))
}

// applyGoMemLimit sets the limit, unless the operator set GOMEMLIMIT in the environment.
func applyGoMemLimit(total uint64, capPct int) {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	debug.SetMemoryLimit(goMemLimit(total, capPct))
}
