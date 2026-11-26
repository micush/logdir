package main

import (
	"os"
	"sort"
	"sync"
	"unsafe"
)

// The hour cache: the messages of closed hours, parsed, kept in memory so a search of the last few hours does not read, decompress and
// parse the files again.  Like flowdir's rollups it is capped at a percentage of the machine's memory (the container limit when that is
// smaller), 50 unless set, and the oldest hours go first.  The hour being written is never cached, and an hour that does not fit is read
// from its file as before, so the cap costs speed only.
const (
	ldDefaultMemPct = 50 // leaves room for what searches read beyond the cache (see memlimit.go)
	ldWarmHours     = 24 // the hours read into the cache when the daemon starts
	ldMsgBase       = uint64(unsafe.Sizeof(LdMsg{}))
)

type ldCachedHour struct {
	msgs []LdMsg
	size uint64
}

type ldCache struct {
	mu       sync.Mutex
	hours    map[int64]*ldCachedHour
	bytes    uint64
	pct, mb  int
	total    func() uint64 // the machine's memory
	hit      uint64
	miss     uint64
	rejected uint64 // hours that did not fit (or, by admits, would not have)
	dropped  uint64 // hours the cap pushed out
}

func newLdCache() *ldCache {
	return &ldCache{hours: map[int64]*ldCachedHour{}, total: machineMemory}
}

// machineMemory is the machine's memory, or the container's limit when that is smaller (0: unknown).
func machineMemory() uint64 {
	var total uint64
	if b, err := os.ReadFile(procMeminfo); err == nil {
		total, _, _, _ = parseMeminfo(string(b))
	}
	if limit, _, ok := cgroupUsage(); ok && limit > 0 && (total == 0 || limit < total) {
		total = limit
	}
	return total
}

func (c *ldCache) set(pct, mb int) {
	c.mu.Lock()
	c.pct, c.mb = pct, mb
	c.shrinkLocked(0, 1<<62)
	c.mu.Unlock()
}

// capLocked is the most the cache may hold; ok is false when the machine's memory is not known (then nothing is cached).
func (c *ldCache) capLocked() (uint64, bool) {
	if c.mb > 0 {
		return uint64(c.mb) << 20, true
	}
	pct := c.pct
	if pct <= 0 {
		pct = ldDefaultMemPct
	}
	t := c.total()
	if t == 0 {
		return 0, false
	}
	return t / 100 * uint64(pct), true
}

// ldHourBytes estimates what a parsed hour holds.
func ldHourBytes(msgs []LdMsg) uint64 {
	n := uint64(cap(msgs)) * ldMsgBase // the array as allocated, not only the part in use
	for i := range msgs {
		m := &msgs[i]
		n += uint64(len(m.Host) + len(m.Prog) + len(m.PID) + len(m.Msg) + len(m.Src) + len(m.Via) + len(m.Node) + len(m.MID) + len(m.SD))
	}
	return n
}

func (c *ldCache) get(h int64) ([]LdMsg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.hours[h]
	if e == nil {
		c.miss++
		return nil, false
	}
	c.hit++
	return e.msgs, true
}

// shrinkLocked drops the oldest hours older than keep until the cache plus extra fits the cap; it reports whether it does.
func (c *ldCache) shrinkLocked(extra uint64, keep int64) bool {
	limit, ok := c.capLocked()
	if !ok {
		return false
	}
	if c.bytes+extra <= limit {
		return true
	}
	var hs []int64
	for h := range c.hours {
		hs = append(hs, h)
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i] < hs[j] })
	for _, h := range hs {
		if c.bytes+extra <= limit {
			break
		}
		if h >= keep {
			break
		}
		c.bytes -= c.hours[h].size
		delete(c.hours, h)
		c.dropped++
	}
	return c.bytes+extra <= limit
}

// admits says whether an hour of n messages could be kept, by what the hours held weigh per message: false when it would not fit
// even after the hours older than it went, so the caller reads it line by line instead of parsing it whole for nothing.
func (c *ldCache) admits(h int64, n uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	limit, ok := c.capLocked()
	if !ok {
		return false
	}
	per, msgs := uint64(ldMsgBase+120), uint64(0) // a guess until something is held
	for _, e := range c.hours {
		msgs += uint64(len(e.msgs))
	}
	if msgs > 0 {
		per = c.bytes / msgs
	}
	free := c.bytes
	for x, e := range c.hours {
		if x >= h {
			free -= e.size
		}
	}
	// what stays (the hours newer than h) plus this one must fit
	if c.bytes-free+n*per > limit {
		c.rejected++
		return false
	}
	return true
}

// put keeps an hour when it fits, pushing out older hours to make room; an hour older than everything held, or too big, is not kept.
func (c *ldCache) put(h int64, msgs []LdMsg) bool {
	size := ldHourBytes(msgs)
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.hours[h]; old != nil {
		c.bytes -= old.size
		delete(c.hours, h)
	}
	if !c.shrinkLocked(size, h) {
		c.rejected++
		return false
	}
	c.hours[h] = &ldCachedHour{msgs: msgs, size: size}
	c.bytes += size
	return true
}

func (c *ldCache) drop(h int64) {
	c.mu.Lock()
	if e := c.hours[h]; e != nil {
		c.bytes -= e.size
		delete(c.hours, h)
	}
	c.mu.Unlock()
}

// LdCacheStat is what the status shows.
type LdCacheStat struct {
	Bytes, Cap, Hits, Misses, Rejected, Dropped uint64
	Hours                                       int
}

func (c *ldCache) stat() LdCacheStat {
	c.mu.Lock()
	defer c.mu.Unlock()
	limit, _ := c.capLocked()
	return LdCacheStat{Bytes: c.bytes, Cap: limit, Hits: c.hit, Misses: c.miss, Rejected: c.rejected, Dropped: c.dropped, Hours: len(c.hours)}
}

// closedHour: an hour that is over and fully written (the writer is flushed every second, so wait a few seconds past the boundary).
func (s *ldStore) closedHour(h int64) bool {
	return s.dir != "" && h < (s.now().Unix()-10)/3600
}

// Warm reads the newest closed hours into the cache, so the first search after a start is as fast as the next.
func (s *ldStore) Warm() {
	s.mu.Lock()
	var hs []int64
	for h := range s.meta {
		if s.closedHour(h) {
			hs = append(hs, h)
		}
	}
	s.mu.Unlock()
	sort.Slice(hs, func(i, j int) bool { return hs[i] > hs[j] })
	if len(hs) > ldWarmHours {
		hs = hs[:ldWarmHours]
	}
	for _, h := range hs {
		if _, ok := s.cache.get(h); ok {
			continue
		}
		if msgs := s.readHourN(h, s.hourCount(h)); msgs != nil && !s.cache.put(h, msgs) {
			return // full: the older hours would not fit either
		}
	}
}

// stat2 is the cap and what is held, without the counters.
func (c *ldCache) stat2() (limit, held uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	limit, _ = c.capLocked()
	return limit, c.bytes
}
