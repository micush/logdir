package core

import (
	"fmt"
	"sort"
	"strings"
)

// key is a string that differs whenever the filter could match differently.
func (f *LdFilter) Key() string {
	if f.hkey != "" {
		return f.hkey
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q\x00%q\x00%q\x00%d\x00%v\x00%v\x00%q\x00", f.Expr, f.Host, f.prog, f.minSev, f.sevExact, f.sevLess, f.src)
	set := func(m map[string]bool) {
		if m == nil {
			b.WriteString("-\x00")
			return
		}
		keys := make([]string, 0, len(m))
		for k, v := range m {
			if v {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "%q\x00", keys)
	}
	set(f.srcSet)
	set(f.HostSrc)
	keys := make([]string, 0, len(f.SrcWords))
	for k := range f.SrcWords {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%q=%q,", k, f.SrcWords[k])
	}
	f.hkey = b.String()
	return f.hkey
}

// hitsUseful: is it worth keeping what this filter finds per hour (it has words; without, nothing is read that a rollup or the
// index cannot answer cheaply)?
func (f *LdFilter) HitsUseful() bool { return len(f.Expr) > 0 }
