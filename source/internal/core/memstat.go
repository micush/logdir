package core

import (
	"fmt"
	"os"
	"path/filepath"
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
	ProcMeminfo     = "/proc/meminfo"
	// the daemon's own cgroup: a limit set on its service (systemd MemoryMax=) or on a group above it is under these, not in
	// the files above, which are the root's (or, in a container with its own cgroup namespace, the container's)
	procSelfCgroup = "/proc/self/cgroup"
	cgroupRoot     = "/sys/fs/cgroup"
)

// cgroupDirs are the directories of the daemon's own memory cgroup and of each group above it, the innermost first, for the
// unified hierarchy (v2) or the memory controller's (v1).  A directory that is not there (a container that sees its own group as
// the root) is skipped by the reader.
func cgroupDirs(v2 bool) []string {
	b, err := os.ReadFile(procSelfCgroup)
	if err != nil {
		return nil
	}
	var rel string
	found := false
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.SplitN(l, ":", 3)
		if len(f) != 3 {
			continue
		}
		if v2 && f[0] == "0" && f[1] == "" {
			rel, found = f[2], true
		}
		if !v2 {
			for _, c := range strings.Split(f[1], ",") {
				if c == "memory" {
					rel, found = f[2], true
				}
			}
		}
	}
	if !found {
		return nil
	}
	base := cgroupRoot
	if !v2 {
		base = filepath.Join(cgroupRoot, "memory")
	}
	var dirs []string
	for rel = filepath.Clean("/" + rel); rel != "/"; rel = filepath.Dir(rel) {
		dirs = append(dirs, filepath.Join(base, rel))
	}
	return dirs
}

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
func CgroupUsage() (limit, used uint64, ok bool) {
	// the smallest limit of the daemon's own group, the groups above it, and the root's (the container's) files, with what that
	// group uses
	type files struct{ max, cur, stat, inactive string }
	pick := func(fs []files, unlimited uint64) (uint64, uint64, bool) {
		var l, u uint64
		found := false
		for _, f := range fs {
			x, k := readUint(f.max) // v2 "max" does not parse: no limit
			if !k || x >= unlimited || (found && x >= l) {
				continue
			}
			cur, k := readUint(f.cur)
			if !k {
				continue
			}
			in := statValue(f.stat, f.inactive)
			if in > cur {
				in = cur
			}
			l, u, found = x, cur-in, true
		}
		return l, u, found
	}
	v2 := []files{{cgroupV2Max, cgroupV2Current, cgroupV2Stat, "inactive_file"}}
	for _, d := range cgroupDirs(true) {
		v2 = append(v2, files{filepath.Join(d, "memory.max"), filepath.Join(d, "memory.current"), filepath.Join(d, "memory.stat"), "inactive_file"})
	}
	if l, u, k := pick(v2, 1<<62); k {
		return l, u, true
	}
	v1 := []files{{cgroupV1Limit, cgroupV1Usage, cgroupV1Stat, "total_inactive_file"}}
	for _, d := range cgroupDirs(false) {
		v1 = append(v1, files{filepath.Join(d, "memory.limit_in_bytes"), filepath.Join(d, "memory.usage_in_bytes"), filepath.Join(d, "memory.stat"), "total_inactive_file"})
	}
	return pick(v1, 1<<60) // an unlimited v1 group shows a huge number
}

// memUsagePercent is the share of memory in use, the larger of the machine's and the container's.
func memUsagePercent() (float64, error) {
	b, err := os.ReadFile(ProcMeminfo)
	if err != nil {
		return 0, err
	}
	total, avail, _, _ := ParseMeminfo(string(b))
	if total == 0 {
		return 0, fmt.Errorf("no MemTotal in %s", ProcMeminfo)
	}
	pct := 100 * float64(total-minU(avail, total)) / float64(total)
	if limit, used, ok := CgroupUsage(); ok && limit > 0 {
		if c := 100 * float64(used) / float64(limit); c > pct {
			pct = c
		}
	}
	return pct, nil
}
