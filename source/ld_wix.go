package main

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/bits"
	"os"
	"sort"
	"strings"
	"time"
)

// The word index: for each closed hour, which three-byte runs (trigrams) occur in its messages' lower-cased text (host, program,
// message, as a search reads them) and in their fields' values (ld_fields.go).  A search finds text anywhere in a message (fail finds
// failed), so a list of words would not do; a run of text can only be in an hour whose index has every trigram of it.  A search
// with words skips the hours that cannot hold them, without reading them.
//
// The index is a bitmap: each trigram sets one bit, picked by a hash.  It is built at 2^23 bits and folded in half while it stays
// sparse (at most ldWixFill of its bits set), so a quiet hour has a small one; then it is compressed.  A bit can be set by
// another trigram (a false "maybe": the hour is read), never left clear for one that is there.  The file sits beside the hour
// (`2026112005.jsonl.lz.wix`), made when the hour is compressed and, for older hours, by a pass after start.  It is used only
// while the count it was made from is the hour's count in its index; its field part only while it was made by the same
// ldFieldsVersion.

const (
	ldWixMaxLog  = 23 // bits of the largest bitmap: 1 MB
	ldWixMinLog  = 13 // and of the smallest: 1 KB
	ldWixFill    = 0.20
	ldWixMagic   = "LDW1"
	ldWixMemMax  = 64 << 20 // bitmaps kept in memory, in bytes; past it they are read from their files again
	ldWixMaxRead = 4 << 20  // a file bigger than this is damage
)

type ldWix struct {
	count   uint64
	fieldV  uint32 // ldFieldsVersion the field values were read with (0: none)
	logBits uint8
	words   []uint64
}

type ldWixEntry struct {
	count uint64
	wix   *ldWix // nil: the hour has none
}

func ldTrigramBit(a, b, c byte) uint32 {
	v := uint32(a)<<16 | uint32(b)<<8 | uint32(c)
	return (v * 0x9E3779B1) >> (32 - ldWixMaxLog)
}

func (w *ldWix) has(bit uint32) bool {
	bit &= 1<<w.logBits - 1
	return w.words[bit>>6]&(1<<(bit&63)) != 0
}

// mayHold says whether the hour can hold s (lower case, no *): every trigram of it must be set.  Shorter than 3 bytes: maybe.
func (w *ldWix) mayHold(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		if !w.has(ldTrigramBit(s[i], s[i+1], s[i+2])) {
			return false
		}
	}
	return true
}

type ldWixBuilder struct{ words []uint64 }

func (b *ldWixBuilder) addText(s []byte) {
	for i := 0; i+2 < len(s); i++ {
		bit := ldTrigramBit(s[i], s[i+1], s[i+2])
		b.words[bit>>6] |= 1 << (bit & 63)
	}
}

// finish folds the bitmap while it stays sparse.
func (b *ldWixBuilder) finish(count uint64) *ldWix {
	words, lg := b.words, uint8(ldWixMaxLog)
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
	return &ldWix{count: count, fieldV: ldFieldsVersion, logBits: lg, words: words}
}

func (s *ldStore) wixPath(h int64) string { return s.path(h, true) + ".wix" }

// buildWix reads an hour and makes its index; ok false when the hour has no file.
func (s *ldStore) buildWix(h int64) (*ldWix, bool) {
	b := &ldWixBuilder{words: make([]uint64, 1<<ldWixMaxLog/64)}
	var total uint64
	var buf []byte
	each := func(m *LdMsg) bool {
		total++
		buf = appendLower(buf[:0], m.Host)
		buf = append(buf, ' ')
		buf = appendLower(buf, m.Prog)
		buf = append(buf, ' ')
		buf = appendLower(buf, m.Msg)
		b.addText(buf)
		for _, f := range ldFields(m) {
			buf = appendLower(buf[:0], f.Val)
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
func (s *ldStore) saveWix(h int64, w *ldWix) {
	raw := make([]byte, len(w.words)*8)
	for i, x := range w.words {
		binary.LittleEndian.PutUint64(raw[i*8:], x)
	}
	comp := lzCompress(raw)
	out := make([]byte, 0, len(comp)+25)
	out = append(out, ldWixMagic...)
	out = binary.LittleEndian.AppendUint64(out, w.count)
	out = binary.LittleEndian.AppendUint32(out, w.fieldV)
	out = append(out, w.logBits)
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

func (s *ldStore) loadWix(h int64) (*ldWix, error) {
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

func parseWix(b []byte) (*ldWix, error) {
	if len(b) < 25 || string(b[:4]) != ldWixMagic {
		return nil, errWix
	}
	w := &ldWix{count: binary.LittleEndian.Uint64(b[4:]), fieldV: binary.LittleEndian.Uint32(b[12:]), logBits: b[16]}
	n := binary.LittleEndian.Uint32(b[17:])
	sum := binary.LittleEndian.Uint32(b[21:])
	if w.logBits < ldWixMinLog || w.logBits > ldWixMaxLog || n != 1<<w.logBits/8 {
		return nil, errWix
	}
	raw, err := lzDecompress(b[25:], int(n))
	if err != nil || len(raw) != int(n) || crc32.ChecksumIEEE(raw) != sum {
		return nil, errWix
	}
	w.words = make([]uint64, n/8)
	for i := range w.words {
		w.words[i] = binary.LittleEndian.Uint64(raw[i*8:])
	}
	return w, nil
}

// hourWix is the index of a closed hour that holds count messages: from memory, its file, or made now (make true); nil when it has
// none or it is not what the hour holds now.  build: make it when it is missing (otherwise nil).
func (s *ldStore) hourWix(h int64, count uint64, build bool) *ldWix {
	s.wixMu.Lock()
	if x, ok := s.wixes[h]; ok && x.count == count {
		s.wixMu.Unlock()
		return x.wix
	}
	s.wixMu.Unlock()
	w, err := s.loadWix(h)
	if err != nil || w.count != count || w.fieldV != ldFieldsVersion {
		if !build {
			return nil
		}
		s.wixMake.Lock()
		w2, ok := s.buildWix(h)
		s.wixMake.Unlock()
		if !ok || w2.count != count { // still being written, or not what the index says: use nothing, keep nothing
			return nil
		}
		s.saveWix(h, w2)
		w = w2
	}
	s.wixMu.Lock()
	size := len(w.words) * 8
	if s.wixBytes+size > ldWixMemMax { // memory is full: start again (the files are still there)
		s.wixes, s.wixBytes = map[int64]ldWixEntry{}, 0
	}
	if old, ok := s.wixes[h]; ok && old.wix != nil {
		s.wixBytes -= len(old.wix.words) * 8
	}
	s.wixes[h] = ldWixEntry{count: count, wix: w}
	s.wixBytes += size
	s.wixMu.Unlock()
	return w
}

func (s *ldStore) dropWix(h int64) {
	s.wixMu.Lock()
	if old, ok := s.wixes[h]; ok && old.wix != nil {
		s.wixBytes -= len(old.wix.words) * 8
	}
	delete(s.wixes, h)
	s.wixMu.Unlock()
}

// MakeWixes makes the word indexes the closed hours are missing (the newest first), after a start; one hour at a time.
func (s *ldStore) MakeWixes(ctx context.Context) {
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
			if w, err := s.loadWix(x.h); err == nil && w.count == x.n && w.fieldV == ldFieldsVersion {
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
func (s *ldStore) wixOK(h int64, meta *ldHourMeta, f *ldFilter) bool {
	if meta == nil || len(f.groups) == 0 || !s.closedHour(h) || !f.wixUseful() {
		return true
	}
	w := s.hourWix(h, meta.Count, false)
	if w == nil {
		return true
	}
	for _, g := range f.groups {
		if f.groupMay(g, w) {
			return true
		}
	}
	return false
}

// wixUseful: is there a term with a run of 3 bytes or more to look for?
func (f *ldFilter) wixUseful() bool {
	if f.wixUse != 0 {
		return f.wixUse > 0
	}
	f.wixUse = -1
	for _, g := range f.groups {
		for _, t := range g {
			if t.neg {
				continue
			}
			v := t.text
			if t.field != nil {
				if t.field.op != "=" {
					continue
				}
				v = t.field.val
			}
			for _, p := range strings.Split(v, "*") {
				if len(p) >= 3 {
					f.wixUse = 1
					return true
				}
			}
		}
	}
	return false
}

// groupMay: can a message of the hour meet every term of the group, as far as the index can tell?
func (f *ldFilter) groupMay(g ldGroup, w *ldWix) bool {
	for _, t := range g {
		if t.neg {
			continue // a term that must be missing rules no hour out
		}
		v := t.text
		if t.field != nil {
			if t.field.op != "=" {
				continue
			}
			v = t.field.val
		} else if len(f.srcWords) > 0 && f.mayBeSenderName(v) {
			continue // text can also match the sender's name, which is not in the index
		}
		for _, p := range strings.Split(v, "*") {
			if len(p) >= 3 && !w.mayHold(p) {
				return false
			}
		}
	}
	return true
}

// mayBeSenderName: could the term match (partly) in a sender's name rather than in the message?
func (f *ldFilter) mayBeSenderName(term string) bool {
	if strings.Contains(term, " ") {
		return true // could run from the end of the message into the name
	}
	for _, p := range strings.Split(term, "*") {
		if p == "" {
			continue
		}
		for _, names := range f.srcWords {
			if strings.Contains(names, p) {
				return true
			}
		}
	}
	return false
}
