package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// How much memory the machine and its cgroup have and use.  The flow collector's memory cap (flow_store.go) takes its total
// from here; Monitor ▸ Node reads the same files.

// cgroup files; variables so a test can point them elsewhere
var (
	cgroupV2Max     = "/sys/fs/cgroup/memory.max"
	cgroupV2Current = "/sys/fs/cgroup/memory.current"
	cgroupV2Stat    = "/sys/fs/cgroup/memory.stat"
	cgroupV1Limit   = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
	cgroupV1Usage   = "/sys/fs/cgroup/memory/memory.usage_in_bytes"
	cgroupV1Stat    = "/sys/fs/cgroup/memory/memory.stat"
	procMeminfo     = "/proc/meminfo"
)

func readUint(path string) (uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n, err == nil
}

func statValue(path, key string) uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == key {
			n, _ := strconv.ParseUint(f[1], 10, 64)
			return n
		}
	}
	return 0
}

// cgroupUsage returns the memory limit of the container the daemon runs in, and what it uses (caches that can
// be dropped excluded), when there is a limit.
func cgroupUsage() (limit, used uint64, ok bool) {
	if l, k := readUint(cgroupV2Max); k { // "max" does not parse: no limit
		if cur, k := readUint(cgroupV2Current); k {
			in := statValue(cgroupV2Stat, "inactive_file")
			if in > cur {
				in = cur
			}
			return l, cur - in, true
		}
	}
	if l, k := readUint(cgroupV1Limit); k && l < 1<<60 { // an unlimited v1 group shows a huge number
		if cur, k := readUint(cgroupV1Usage); k {
			in := statValue(cgroupV1Stat, "total_inactive_file")
			if in > cur {
				in = cur
			}
			return l, cur - in, true
		}
	}
	return 0, 0, false
}

// memUsagePercent is the share of memory in use, the larger of the machine's and the container's.
func memUsagePercent() (float64, error) {
	b, err := os.ReadFile(procMeminfo)
	if err != nil {
		return 0, err
	}
	total, avail, _, _ := parseMeminfo(string(b))
	if total == 0 {
		return 0, fmt.Errorf("no MemTotal in %s", procMeminfo)
	}
	pct := 100 * float64(total-minU(avail, total)) / float64(total)
	if limit, used, ok := cgroupUsage(); ok && limit > 0 {
		if c := 100 * float64(used) / float64(limit); c > pct {
			pct = c
		}
	}
	return pct, nil
}
