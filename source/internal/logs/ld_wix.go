package logs

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/bits"
	"os"
	"sort"
	"time"

	"logdir/internal/core"
)

const ldWixMinLog = 13

const ldWixFill = 0.20

const ldWixMagic = "LDW1"

const ldWixMemMax = 64 << 20

const ldWixMaxRead = 4 << 20

type ldWixEntry struct {
	count uint64
	wix   *core.LdWix // nil: the hour has none
}

type ldWixBuilder struct{ words []uint64 }

func (b *ldWixBuilder) addText(s []byte) {
	for i := 0; i+2 < len(s); i++ {
		bit := core.LdTrigramBit(s[i], s[i+1], s[i+2])
		b.words[bit>>6] |= 1 << (bit & 63)
	}
}

// finish folds the bitmap while it stays sparse.
func (b *ldWixBuilder) finish(count uint64) *core.LdWix {
	words, lg := b.words, uint8(core.LdWixMaxLog)
	for lg > ldWixMinLog {
		half := len(words) / 2
		set := 0
		for i := 0; i < half; i++ {
			set += bits.OnesCount64(words[i] | words[i+half])
		}
		if float64(set) > ldWixFill*float64(half*64) {
			break
		}
		nw := make([]uint64, half)
		for i := range nw {
			nw[i] = words[i] | words[i+half]
		}
		words, lg = nw, lg-1
	}
	return &core.LdWix{Count: count, FieldV: ldFieldsVersion, LogBits: lg, Words: words}
}

func (s *LdStore) wixPath(h int64) string { return s.path(h, true) + ".wix" }

// buildWix reads an hour and makes its index; ok false when the hour has no file.
func (s *LdStore) buildWix(h int64) (*core.LdWix, bool) {
	b := &ldWixBuilder{words: make([]uint64, 1<<core.LdWixMaxLog/64)}
	var total uint64
	var buf []byte
	each := func(m *core.LdMsg) bool {
		total++
		buf = core.AppendLower(buf[:0], m.Host)
		buf = append(buf, ' ')
		buf = core.AppendLower(buf, m.Prog)
		buf = append(buf, ' ')
		buf = core.AppendLower(buf, m.Msg)
		b.addText(buf)
		for _, f := range core.LdFields(m) {
			buf = core.AppendLower(buf[:0], f.Val)
			b.addText(buf)
		}
		return true
	}
	if msgs, ok := s.cache.get(h); ok {
		for i := range msgs {
			each(&msgs[i])
		}
	} else if !s.streamFile(h, each) {
		return nil, false
	}
	return b.finish(total), true
}

// the file: magic, count u64, fields version u32, log2 of the bits u8, raw length u32, crc32 of the raw bitmap u32, the compressed bitmap
func (s *LdStore) saveWix(h int64, w *core.LdWix) {
	raw := make([]byte, len(w.Words)*8)
	for i, x := range w.Words {
		binary.LittleEndian.PutUint64(raw[i*8:], x)
	}
	comp := lzCompress(raw)
	out := make([]byte, 0, len(comp)+25)
	out = append(out, ldWixMagic...)
	out = binary.LittleEndian.AppendUint64(out, w.Count)
	out = binary.LittleEndian.AppendUint32(out, w.FieldV)
	out = append(out, w.LogBits)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(raw)))
	out = binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(raw))
	out = append(out, comp...)
	tmp := s.wixPath(h) + ".tmp"
	if os.WriteFile(tmp, out, 0o640) == nil {
		if os.Rename(tmp, s.wixPath(h)) != nil {
			os.Remove(tmp)
		}
	}
}

var errWix = errors.New("damaged word index")

func (s *LdStore) loadWix(h int64) (*core.LdWix, error) {
	st, err := os.Stat(s.wixPath(h))
	if err != nil {
		return nil, err
	}
	if st.Size() > ldWixMaxRead {
		return nil, errWix
	}
	b, err := os.ReadFile(s.wixPath(h))
	if err != nil {
		return nil, err
	}
	return parseWix(b)
}

func parseWix(b []byte) (*core.LdWix, error) {
	if len(b) < 25 || string(b[:4]) != ldWixMagic {
		return nil, errWix
	}
	w := &core.LdWix{Count: binary.LittleEndian.Uint64(b[4:]), FieldV: binary.LittleEndian.Uint32(b[12:]), LogBits: b[16]}
	n := binary.LittleEndian.Uint32(b[17:])
	sum := binary.LittleEndian.Uint32(b[21:])
	if w.LogBits < ldWixMinLog || w.LogBits > core.LdWixMaxLog || n != 1<<w.LogBits/8 {
		return nil, errWix
	}
	raw, err := lzDecompress(b[25:], int(n))
	if err != nil || len(raw) != int(n) || crc32.ChecksumIEEE(raw) != sum {
		return nil, errWix
	}
	w.Words = make([]uint64, n/8)
	for i := range w.Words {
		w.Words[i] = binary.LittleEndian.Uint64(raw[i*8:])
	}
	return w, nil
}

// hourWix is the index of a closed hour that holds count messages: from memory, its file, or made now (make true); nil when it has
// none or it is not what the hour holds now.  build: make it when it is missing (otherwise nil).
func (s *LdStore) hourWix(h int64, count uint64, build bool) *core.LdWix {
	s.wixMu.Lock()
	if x, ok := s.wixes[h]; ok && x.count == count {
		s.wixMu.Unlock()
		return x.wix
	}
	s.wixMu.Unlock()
	w, err := s.loadWix(h)
	if err != nil || w.Count != count || w.FieldV != ldFieldsVersion {
		if !build {
			return nil
		}
		s.wixMake.Lock()
		w2, ok := s.buildWix(h)
		s.wixMake.Unlock()
		if !ok || w2.Count != count { // still being written, or not what the index says: use nothing, keep nothing
			return nil
		}
		s.saveWix(h, w2)
		w = w2
	}
	s.wixMu.Lock()
	size := len(w.Words) * 8
	if s.wixBytes+size > ldWixMemMax { // memory is full: start again (the files are still there)
		s.wixes, s.wixBytes = map[int64]ldWixEntry{}, 0
	}
	if old, ok := s.wixes[h]; ok && old.wix != nil {
		s.wixBytes -= len(old.wix.Words) * 8
	}
	s.wixes[h] = ldWixEntry{count: count, wix: w}
	s.wixBytes += size
	s.wixMu.Unlock()
	return w
}

func (s *LdStore) dropWix(h int64) {
	s.wixMu.Lock()
	if old, ok := s.wixes[h]; ok && old.wix != nil {
		s.wixBytes -= len(old.wix.Words) * 8
	}
	delete(s.wixes, h)
	s.wixMu.Unlock()
}

// MakeWixes makes the word indexes the closed hours are missing (the newest first), after a start; one hour at a time.
func (s *LdStore) MakeWixes(ctx context.Context) {
	s.mu.Lock()
	type hc struct {
		h int64
		n uint64
	}
	var hs []hc
	for h, m := range s.meta {
		if s.closedHour(h) && m.Count > 0 {
			hs = append(hs, hc{h, m.Count})
		}
	}
	s.mu.Unlock()
	sort.Slice(hs, func(i, j int) bool { return hs[i].h > hs[j].h })
	for _, x := range hs {
		if ctx.Err() != nil {
			return
		}
		if _, err := os.Stat(s.wixPath(x.h)); err == nil {
			if w, err := s.loadWix(x.h); err == nil && w.Count == x.n && w.FieldV == ldFieldsVersion {
				continue // there and right: not loaded into memory until a search wants it
			}
		}
		s.hourWix(x.h, x.n, true)
		s.dropWix(x.h) // made for later: memory is for the hours searches ask about
		time.Sleep(time.Millisecond)
	}
}

// wixOK says whether a closed hour can hold what the filter's words and field values ask for (false: skip it).  An hour with no
// index, or a search with nothing an index can rule out, is read.
func (s *LdStore) wixOK(h int64, meta *core.LdHourMeta, f *core.LdFilter) bool {
	if meta == nil || len(f.Groups) == 0 || !s.closedHour(h) || !f.WixUseful() {
		return true
	}
	w := s.hourWix(h, meta.Count, false)
	if w == nil {
		return true
	}
	for _, g := range f.Groups {
		if f.GroupMay(g, w) {
			return true
		}
	}
	return false
}
