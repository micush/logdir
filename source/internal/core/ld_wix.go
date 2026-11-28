package core

import (
	"strings"
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

const LdWixMaxLog = 23

type LdWix struct {
	Count   uint64   `json:"-"`
	FieldV  uint32   `json:"-"` // ldFieldsVersion the field values were read with (0: none)
	LogBits uint8    `json:"-"`
	Words   []uint64 `json:"-"`
}

func LdTrigramBit(a, b, c byte) uint32 {
	v := uint32(a)<<16 | uint32(b)<<8 | uint32(c)
	return (v * 0x9E3779B1) >> (32 - LdWixMaxLog)
}

func (w *LdWix) has(bit uint32) bool {
	bit &= 1<<w.LogBits - 1
	return w.Words[bit>>6]&(1<<(bit&63)) != 0
}

// mayHold says whether the hour can hold s (lower case, no *): every trigram of it must be set.  Shorter than 3 bytes: maybe.
func (w *LdWix) MayHold(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		if !w.has(LdTrigramBit(s[i], s[i+1], s[i+2])) {
			return false
		}
	}
	return true
}

// wixUseful: is there a term with a run of 3 bytes or more to look for?
func (f *LdFilter) WixUseful() bool {
	if f.wixUse != 0 {
		return f.wixUse > 0
	}
	f.wixUse = -1
	for _, g := range f.Groups {
		for _, t := range g {
			if t.neg {
				continue
			}
			v := t.text
			if t.Field != nil {
				if t.Field.op != "=" {
					continue
				}
				v = t.Field.val
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
func (f *LdFilter) GroupMay(g LdGroup, w *LdWix) bool {
	for _, t := range g {
		if t.neg {
			continue // a term that must be missing rules no hour out
		}
		v := t.text
		if t.Field != nil {
			if t.Field.op != "=" {
				continue
			}
			v = t.Field.val
		} else if len(f.SrcWords) > 0 && f.mayBeSenderName(v) {
			continue // text can also match the sender's name, which is not in the index
		}
		for _, p := range strings.Split(v, "*") {
			if len(p) >= 3 && !w.MayHold(p) {
				return false
			}
		}
	}
	return true
}

// mayBeSenderName: could the term match (partly) in a sender's name rather than in the message?
func (f *LdFilter) mayBeSenderName(term string) bool {
	if strings.Contains(term, " ") {
		return true // could run from the end of the message into the name
	}
	for _, p := range strings.Split(term, "*") {
		if p == "" {
			continue
		}
		for _, names := range f.SrcWords {
			if strings.Contains(names, p) {
				return true
			}
		}
	}
	return false
}
