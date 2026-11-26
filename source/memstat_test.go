package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemUsageReadsMeminfoAndCgroup(t *testing.T) {
	dir := t.TempDir()
	w := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	oldV2m, oldV2c, oldV2s, oldV1l, oldV1u, oldV1s, oldMI := cgroupV2Max, cgroupV2Current, cgroupV2Stat, cgroupV1Limit, cgroupV1Usage, cgroupV1Stat, procMeminfo
	t.Cleanup(func() {
		cgroupV2Max, cgroupV2Current, cgroupV2Stat, cgroupV1Limit, cgroupV1Usage, cgroupV1Stat, procMeminfo = oldV2m, oldV2c, oldV2s, oldV1l, oldV1u, oldV1s, oldMI
	})
	oldSelf, oldRoot := procSelfCgroup, cgroupRoot
	t.Cleanup(func() { procSelfCgroup, cgroupRoot = oldSelf, oldRoot })
	procSelfCgroup = filepath.Join(dir, "none0") // only the files above, not this machine's own groups
	procMeminfo = w("meminfo", "MemTotal: 1000 kB\nMemFree: 100 kB\nMemAvailable: 400 kB\n")
	cgroupV2Max, cgroupV2Current, cgroupV2Stat = filepath.Join(dir, "none1"), filepath.Join(dir, "none2"), filepath.Join(dir, "none3")
	cgroupV1Limit, cgroupV1Usage, cgroupV1Stat = filepath.Join(dir, "none4"), filepath.Join(dir, "none5"), filepath.Join(dir, "none6")
	if p, err := memUsagePercent(); err != nil || p < 59.9 || p > 60.1 {
		t.Fatalf("host only: %v %v", p, err)
	}
	// a container limited to 100 MB that uses 90 MB of which 20 MB is inactive file cache: 70 %, below the host's 60 %? no, above
	cgroupV2Max = w("memory.max", "104857600\n")
	cgroupV2Current = w("memory.current", "94371840\n")
	cgroupV2Stat = w("memory.stat", "anon 1\ninactive_file 20971520\nactive_file 5\n")
	if p, _ := memUsagePercent(); p < 69.9 || p > 70.1 {
		t.Fatalf("cgroup v2: %v", p)
	}
	cgroupV2Max = w("memory.max2", "max\n") // no limit: the host figure again
	if p, _ := memUsagePercent(); p < 59.9 || p > 60.1 {
		t.Fatalf("unlimited cgroup: %v", p)
	}
	cgroupV1Limit, cgroupV1Usage, cgroupV1Stat = w("l", "100000\n"), w("u", "95000\n"), w("s", "total_inactive_file 5000\n")
	if p, _ := memUsagePercent(); p < 89.9 || p > 90.1 {
		t.Fatalf("cgroup v1: %v", p)
	}
	cgroupV1Limit = w("l2", "9223372036854771712\n") // v1 "unlimited"
	if p, _ := memUsagePercent(); p < 59.9 || p > 60.1 {
		t.Fatalf("v1 unlimited: %v", p)
	}
	procMeminfo = w("bad", "nothing here\n")
	if _, err := memUsagePercent(); err == nil {
		t.Fatal("no MemTotal must be an error")
	}
}

// TestCgroupOwnGroup: a limit on the daemon's own group (systemd MemoryMax=) or on a group above it counts, the smallest wins, and
// a level that is not there or has no limit is passed over; v1 the same under the memory controller.
func TestCgroupOwnGroup(t *testing.T) {
	dir := t.TempDir()
	oldV2m, oldV1l, oldSelf, oldRoot := cgroupV2Max, cgroupV1Limit, procSelfCgroup, cgroupRoot
	t.Cleanup(func() { cgroupV2Max, cgroupV1Limit, procSelfCgroup, cgroupRoot = oldV2m, oldV1l, oldSelf, oldRoot })
	cgroupV2Max, cgroupV1Limit = filepath.Join(dir, "none1"), filepath.Join(dir, "none2")
	cgroupRoot = filepath.Join(dir, "cg")
	put := func(rel, name, body string) {
		p := filepath.Join(cgroupRoot, rel, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	procSelfCgroup = filepath.Join(dir, "self")
	os.WriteFile(procSelfCgroup, []byte("0::/system.slice/logdir.service\n"), 0o644)
	if _, _, ok := cgroupUsage(); ok {
		t.Fatal("no limit anywhere")
	}
	put("system.slice/logdir.service", "memory.max", "max\n")
	put("system.slice/logdir.service", "memory.current", "300\n")
	put("system.slice", "memory.max", "6000\n")
	put("system.slice", "memory.current", "1000\n")
	put("system.slice", "memory.stat", "inactive_file 200\n")
	if l, u, ok := cgroupUsage(); !ok || l != 6000 || u != 800 {
		t.Fatalf("the slice's limit: %d %d %v", l, u, ok)
	}
	put("system.slice/logdir.service", "memory.max", "4000\n")
	if l, u, ok := cgroupUsage(); !ok || l != 4000 || u != 300 {
		t.Fatalf("the service's limit: %d %d %v", l, u, ok)
	}
	// v1: the memory controller's line, among others
	os.WriteFile(procSelfCgroup, []byte("5:cpu,cpuacct:/x\n4:memory:/process_api/a/bash\n0::/\n"), 0o644)
	put("memory/process_api/a/bash", "memory.limit_in_bytes", "9223372036854771712\n")
	put("memory/process_api/a/bash", "memory.usage_in_bytes", "10\n")
	put("memory/process_api/a", "memory.limit_in_bytes", "6442450944\n")
	put("memory/process_api/a", "memory.usage_in_bytes", "1000\n")
	put("memory/process_api/a", "memory.stat", "total_inactive_file 100\n")
	if l, u, ok := cgroupUsage(); !ok || l != 6442450944 || u != 900 {
		t.Fatalf("v1 group above: %d %d %v", l, u, ok)
	}
}
