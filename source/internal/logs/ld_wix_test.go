package logs

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"logdir/internal/core"
)

// hourWord is a word only hour h has, whose trigrams are its own too (zKxLq: K and L letters from h).
func hourWord(h int) string { return fmt.Sprintf("tokz%cx%cq", 'a'+h/26%26, 'a'+h%26) }

// wordStore: hours of messages where hour h has a word of its own (hourWord) and every tenth hour a rare event.
func wordStore(t testing.TB, dir string, hours, perHour int) (*LdStore, time.Time) {
	now := ldNow
	s := NewLdStore(dir, "n1")
	s.Now = func() time.Time { return now }
	s.Load()
	base := now.Add(-time.Duration(hours) * time.Hour)
	for h := 0; h <= hours; h++ {
		for i := 0; i < perHour; i++ {
			at := base.Add(time.Duration(h)*time.Hour + time.Duration(i)*time.Hour/time.Duration(perHour))
			if at.After(now) {
				break
			}
			msg := fmt.Sprintf(`srcip=192.168.%d.%d dstip=151.101.%d.1 dstport=443 action="accept" sessionid=%d %s`, i%200, i%250, i%200, 17000000+i, hourWord(h))
			if h%10 == 3 && i == 7 {
				msg = `OSPF neighbor 10.0.0.9 Down: dead timer expired dstport=3389 action="deny"`
			}
			s.Add(core.LdMsg{T: at.UnixMilli(), Host: fmt.Sprintf("fw%d", i%4), Prog: "fortigate", Sev: 6, Fac: 1, Src: fmt.Sprintf("10.1.1.%d", i%4), Msg: msg, Via: "udp"})
		}
	}
	s.Flush()
	s.Maintain()
	return s, now
}

// noWix runs f with the word indexes out of the way (moved aside and forgotten), then puts them back.
func noWix(t testing.TB, s *LdStore, f func()) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(s.dir, "*.wix"))
	for _, p := range files {
		os.Rename(p, p+".off")
	}
	s.wixMu.Lock()
	saved := s.wixes
	s.wixes = map[int64]ldWixEntry{}
	s.wixMu.Unlock()
	noHits(s)
	f()
	noHits(s)
	for _, p := range files {
		os.Rename(p+".off", p)
	}
	s.wixMu.Lock()
	s.wixes = saved
	s.wixMu.Unlock()
}

// noHits lets go of what filters found in closed hours (ld_hits.go), so the next search reads the hours.
func noHits(s *LdStore) {
	s.hitMu.Lock()
	s.hits, s.hitBytes = map[ldHitKey]*ldHits{}, 0
	s.hitMu.Unlock()
}

func msgKeys(a LdAnswer) string {
	var b strings.Builder
	for _, m := range a.Msgs {
		fmt.Fprintf(&b, "%d|%s\n", m.T, m.Msg)
	}
	return b.String()
}

func TestWixSearchSameAndSkips(t *testing.T) {
	s, now := wordStore(t, t.TempDir(), 40, 200)
	if files, _ := filepath.Glob(filepath.Join(s.dir, "*.wix")); len(files) < 38 {
		t.Fatalf("%d word indexes made when the hours were compressed", len(files))
	}
	from := now.Add(-41 * time.Hour).UnixMilli()
	cases := []struct {
		q       string
		srcSet  bool // q matches only a sender's name
		maxRead uint64
	}{
		{q: hourWord(7), maxRead: 600},                                 // one hour (and the two newest, not compressed yet: no index)
		{q: "ospf neighbor", maxRead: 1200},                            // a phrase in 4 hours
		{q: "dead*expired", maxRead: 1200},                             // a wildcard
		{q: hourWord(7) + " or " + hourWord(12), maxRead: 1000},        // two hours
		{q: hourWord(7) + " and not accept", maxRead: 600},             // a term that must be missing rules nothing out; the other does
		{q: "dstport=3389", maxRead: 1200},                             // a field value
		{q: "action=deny tokz*", maxRead: 1200},                        // a field and a wildcard with a short first part
		{q: "zzzznothere", maxRead: 200},                               // nothing: only the open hour is read
		{q: "not " + hourWord(7), maxRead: 1 << 30},                    // nothing to rule out
		{q: "ok", maxRead: 1 << 30},                                    // too short for the index
		{q: "dstport>3000", maxRead: 1 << 30},                          // a number: not in the index
		{q: "fw2 " + hourWord(5), maxRead: 600},                        // the host is in the indexed text
		{q: "fortigate " + hourWord(5), maxRead: 600},                  // and the program
		{q: "mysender", srcSet: true, maxRead: 1 << 30},                // a sender's name: not in the index, so nothing is skipped
		{q: "mysender " + hourWord(9), srcSet: true, maxRead: 1 << 30}, // a phrase could run into the name
	}
	for _, c := range cases {
		q := core.LdQuery{From: from, MinSev: -1, Q: c.q, Limit: 5000}
		if c.srcSet {
			q.SrcWords = map[string]string{"10.1.1.2": "mysender"}
		}
		with := s.Search(context.Background(), q)
		var without LdAnswer
		noWix(t, s, func() { without = s.Search(context.Background(), q) })
		if msgKeys(with) != msgKeys(without) {
			t.Errorf("%q: %d messages with the index, %d without", c.q, len(with.Msgs), len(without.Msgs))
		}
		if with.Scanned > c.maxRead {
			t.Errorf("%q: read %d messages (without the index %d), want at most %d", c.q, with.Scanned, without.Scanned, c.maxRead)
		}
		// the stats agree too
		var sw, so LdStatsAnswer
		sw = s.Stats(context.Background(), q, 3600)
		noWix(t, s, func() { so = s.Stats(context.Background(), q, 3600) })
		if sw.Total != so.Total || fmt.Sprint(sw.Series) != fmt.Sprint(so.Series) {
			t.Errorf("%q: stats %d with the index, %d without", c.q, sw.Total, so.Total)
		}
	}
	if a := s.Search(context.Background(), core.LdQuery{From: from, MinSev: -1, Q: "ospf neighbor", Limit: 5000}); len(a.Msgs) != 4 {
		t.Errorf("ospf: %d messages", len(a.Msgs))
	}
}

// every run of text of a message, and of its field values, is in its hour's index
func TestWixNoFalseNegatives(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	b := &ldWixBuilder{words: make([]uint64, 1<<core.LdWixMaxLog/64)}
	var msgs []core.LdMsg
	alpha := "abcdefghijklmnopqrstuvwxyz0123456789 .:=/\"{}-_ÄéŁ"
	for i := 0; i < 3000; i++ {
		var w strings.Builder
		n := 5 + r.Intn(60)
		for j := 0; j < n; j++ {
			w.WriteString(string([]rune(alpha)[r.Intn(len([]rune(alpha)))]))
		}
		m := core.LdMsg{Host: fmt.Sprintf("H%d", r.Intn(50)), Prog: "Prog", Msg: "K" + fmt.Sprint(i) + "=" + w.String()}
		msgs = append(msgs, m)
		buf := core.AppendLower(nil, m.Host)
		buf = append(append(buf, ' '), core.AppendLower(nil, m.Prog)...)
		buf = append(append(buf, ' '), core.AppendLower(nil, m.Msg)...)
		b.addText(buf)
		for _, f := range core.LdFields(&m) {
			b.addText(core.AppendLower(nil, f.Val))
		}
	}
	w := b.finish(uint64(len(msgs)))
	for _, m := range msgs {
		text := strings.ToLower(m.Host + " " + m.Prog + " " + m.Msg)
		for i := 0; i < len(text); i++ {
			for j := i + 3; j <= len(text) && j <= i+12; j++ {
				if !w.MayHold(text[i:j]) {
					t.Fatalf("%q of %q is missing", text[i:j], text)
				}
			}
		}
		for _, f := range core.LdFields(&m) {
			if v := strings.ToLower(f.Val); !w.MayHold(v) {
				t.Fatalf("field value %q is missing", v)
			}
		}
	}
	// and it says no to most text that is not there
	miss := 0
	for i := 0; i < 1000; i++ {
		if !w.MayHold(fmt.Sprintf("zq%dxj%dwv", i, i*7)) {
			miss++
		}
	}
	if miss < 900 {
		t.Errorf("only %d of 1000 absent words ruled out (fill too high? %d bits)", miss, 1<<w.LogBits)
	}
}

func TestWixFoldSize(t *testing.T) {
	quiet := (&ldWixBuilder{words: make([]uint64, 1<<core.LdWixMaxLog/64)})
	quiet.addText([]byte("one short message"))
	if w := quiet.finish(1); w.LogBits != ldWixMinLog {
		t.Errorf("a quiet hour has 2^%d bits", w.LogBits)
	}
	busy := &ldWixBuilder{words: make([]uint64, 1<<core.LdWixMaxLog/64)}
	r := rand.New(rand.NewSource(2))
	chars := []byte(" !#$%&()*+,-./0123456789:;<=>?@abcdefghijklmnopqrstuvwxyz[]^_{|}~ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	line := make([]byte, 16)
	for i := 0; i < 200000; i++ {
		for j := range line {
			line[j] = chars[r.Intn(len(chars))]
		}
		busy.addText(line)
	}
	w := busy.finish(200000)
	if w.LogBits <= ldWixMinLog+4 {
		t.Errorf("a busy hour has only 2^%d bits", w.LogBits)
	}
	set := 0
	for _, x := range w.Words {
		for ; x != 0; x &= x - 1 {
			set++
		}
	}
	if fill := float64(set) / float64(int(1)<<w.LogBits); fill > 0.45 {
		t.Errorf("fill %.2f at 2^%d bits", fill, w.LogBits)
	}
}

func TestWixFileRoundTripAndDamage(t *testing.T) {
	s, now := wordStore(t, t.TempDir(), 3, 50)
	h := now.Add(-2*time.Hour).Unix() / 3600
	w, err := s.loadWix(h)
	if err != nil || w.Count != 50 || w.FieldV != ldFieldsVersion {
		t.Fatalf("load: %v %+v", err, w)
	}
	b, _ := os.ReadFile(s.wixPath(h))
	// every cut and a flipped byte anywhere: an error, never a panic and never a wrong index
	for i := 0; i < len(b); i++ {
		if _, err := parseWix(b[:i]); err == nil {
			t.Fatalf("cut at %d parsed", i)
		}
	}
	for i := 0; i < len(b); i += 1 + len(b)/200 {
		c := append([]byte(nil), b...)
		c[i] ^= 0x41
		if x, err := parseWix(c); err == nil && (x.Count != w.Count && i >= 4 && i < 12) {
			continue // the count itself: parsed, and then not used for this hour
		}
	}
	// a damaged file: the hour is read, the answer is right
	os.WriteFile(s.wixPath(h), []byte("LDW1 junk"), 0o640)
	s.dropWix(h)
	q := core.LdQuery{From: now.Add(-4 * time.Hour).UnixMilli(), MinSev: -1, Q: hourWord(1), Limit: 100}
	if a := s.Search(context.Background(), q); len(a.Msgs) != 50 {
		t.Errorf("with a damaged index: %d messages", len(a.Msgs))
	}
	// the pass after a start makes it again
	s.MakeWixes(context.Background())
	if w2, err := s.loadWix(h); err != nil || w2.Count != 50 {
		t.Errorf("not made again: %v", err)
	}
}

func TestWixStaleNotUsed(t *testing.T) {
	s, now := wordStore(t, t.TempDir(), 3, 50)
	h := now.Add(-2*time.Hour).Unix() / 3600
	// an index made from fewer messages than the hour now holds (from an older file): not used, so nothing is missed
	w := &core.LdWix{Count: 50, FieldV: ldFieldsVersion, LogBits: ldWixMinLog, Words: make([]uint64, 1<<ldWixMinLog/64)}
	s.saveWix(h, w)
	s.dropWix(h)
	s.mu.Lock()
	s.meta[h].Count = 51
	s.mu.Unlock()
	if s.hourWix(h, 51, false) != nil {
		t.Error("a stale index was used")
	}
	// an index made by another version of the field reader: not used
	w.FieldV = ldFieldsVersion + 1
	s.saveWix(h, w)
	if s.hourWix(h, 50, false) != nil {
		t.Error("an index of another field version was used")
	}
	_ = now
}

func TestWixRemovedWithItsHour(t *testing.T) {
	s, _ := wordStore(t, t.TempDir(), 5, 20)
	before, _ := filepath.Glob(filepath.Join(s.dir, "*.wix"))
	s.mu.Lock()
	s.retDays = 1
	s.mu.Unlock()
	s.Now = func() time.Time { return ldNow.Add(48 * time.Hour) }
	s.enforce()
	after, _ := filepath.Glob(filepath.Join(s.dir, "*.wix"))
	if len(before) == 0 || len(after) != 0 {
		t.Errorf("word indexes before %d, after the hours went %d", len(before), len(after))
	}
	ents, _ := os.ReadDir(s.dir)
	for _, e := range ents {
		if _, ok := parseHourName(e.Name()); !ok {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}

// BenchmarkSearchWords30d: a search with words over 30 days of 20,000 messages an hour, with and without the word index.
func BenchmarkSearchWords30d(b *testing.B) {
	if testing.Short() {
		b.Skip("slow to set up")
	}
	s, now := wordStore(b, b.TempDir(), 30*24, 20000)
	s.cache.set(0, 64)
	for _, c := range []struct{ name, q string }{{"rare-phrase", "ospf neighbor"}, {"one-hour-word", hourWord(100)}, {"field", "dstport=3389"}, {"common", "accept"}} {
		for _, mode := range []string{"index", "noindex"} {
			b.Run(c.name+"-"+mode, func(b *testing.B) {
				q := core.LdQuery{From: now.Add(-30 * 24 * time.Hour).UnixMilli(), MinSev: -1, Q: c.q, Limit: 1000}
				run := func() {
					for i := 0; i < b.N; i++ {
						a := s.Search(context.Background(), q)
						if i == 0 {
							b.Logf("%d msgs, %d scanned", len(a.Msgs), a.Scanned)
						}
					}
				}
				if mode == "index" {
					run()
				} else {
					noWix(b, s, run)
				}
			})
		}
	}
}

// BenchmarkFilter7d: what the Logs page asks for with a filter over 7 days: the newest 1000 messages and the stats (hourly chart,
// top hosts and programs), nothing in the cache.
func BenchmarkFilter7d(b *testing.B) {
	if testing.Short() {
		b.Skip("slow to set up")
	}
	s, now := wordStore(b, b.TempDir(), 7*24, 20000)
	for _, c := range []struct{ name, q string }{{"rare", "ospf neighbor"}, {"common", "accept"}, {"half", "192.168.1"}, {"field", "dstport=3389"}, {"not", "not accept"}} {
		q := core.LdQuery{From: now.Add(-7 * 24 * time.Hour).UnixMilli(), MinSev: -1, Q: c.q, Limit: 1000}
		b.Run(c.name+"-search", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				s.cache.set(0, 1) // emptied
				s.cache.set(0, 64)
				noHits(s)
				a := s.Search(context.Background(), q)
				if i == 0 {
					b.Logf("%d msgs, %d scanned", len(a.Msgs), a.Scanned)
				}
			}
		})
		b.Run(c.name+"-stats", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				s.cache.set(0, 1) // emptied
				s.cache.set(0, 64)
				noHits(s)
				a := s.Stats(context.Background(), q, 3600)
				if i == 0 {
					b.Logf("%d counted, %d scanned", a.Total, a.Scanned)
				}
			}
		})
		b.Run(c.name+"-refresh", func(b *testing.B) { // the page again 15 s later: the same filter, the open hour read again
			s.Search(context.Background(), q)
			s.Stats(context.Background(), q, 3600)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.Search(context.Background(), q)
				s.Stats(context.Background(), q, 3600)
			}
		})
	}
}
