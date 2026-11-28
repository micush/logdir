package logs

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"sync"
)

const ldLzMagic = "\x00LD1"

const ldLzBlock = 256 << 10

var lzTables = sync.Pool{New: func() any { return new([1 << 16]int32) }} // a frame claiming more than this is damage, not data

// lzCompress packs src into LZ4 block format (greedy matching, 64 KB window).
func lzCompress(src []byte) []byte {
	dst := make([]byte, 0, len(src)/2+64)
	ht := lzTables.Get().(*[1 << 16]int32)
	defer lzTables.Put(ht)
	for i := range ht {
		ht[i] = -1
	}
	putLen := func(r int) {
		for ; r >= 255; r -= 255 {
			dst = append(dst, 255)
		}
		dst = append(dst, byte(r))
	}
	emit := func(lit []byte, off, mlen int) {
		tok := byte(0)
		if len(lit) >= 15 {
			tok = 15 << 4
		} else {
			tok = byte(len(lit)) << 4
		}
		ml := mlen - 4
		if mlen > 0 {
			if ml >= 15 {
				tok |= 15
			} else {
				tok |= byte(ml)
			}
		}
		dst = append(dst, tok)
		if len(lit) >= 15 {
			putLen(len(lit) - 15)
		}
		dst = append(dst, lit...)
		if mlen > 0 {
			dst = append(dst, byte(off), byte(off>>8))
			if ml >= 15 {
				putLen(ml - 15)
			}
		}
	}
	n, anchor, i := len(src), 0, 0
	for i+8 <= n {
		cur := binary.LittleEndian.Uint32(src[i:])
		h := (cur * 2654435761) >> 16
		ref := int(ht[h])
		ht[h] = int32(i)
		if ref >= 0 && i-ref < 65536 && binary.LittleEndian.Uint32(src[ref:]) == cur {
			m := 4
			for i+m < n-4 && src[ref+m] == src[i+m] {
				m++
			}
			emit(src[anchor:i], i-ref, m)
			i += m
			anchor = i
			continue
		}
		i++
	}
	emit(src[anchor:], 0, 0)
	return dst
}

var errLZ = errors.New("flow: damaged compressed block")

// lzDecompress unpacks a block into exactly size bytes; damaged input is an error, never a panic.
func lzDecompress(src []byte, size int) ([]byte, error) { return lzDecompressTo(nil, src, size) }

// lzDecompressTo is lzDecompress into buf's memory when it is big enough (the result then shares it).
func lzDecompressTo(buf, src []byte, size int) ([]byte, error) {
	dst := buf[:0]
	if cap(dst) < size {
		dst = make([]byte, 0, size)
	}
	i := 0
	getLen := func(v int) (int, bool) {
		for {
			if i >= len(src) {
				return 0, false
			}
			b := int(src[i])
			i++
			v += b
			if v > size {
				return 0, false
			}
			if b != 255 {
				return v, true
			}
		}
	}
	for i < len(src) {
		tok := src[i]
		i++
		ll := int(tok >> 4)
		if ll == 15 {
			var ok bool
			if ll, ok = getLen(ll); !ok {
				return nil, errLZ
			}
		}
		if i+ll > len(src) || len(dst)+ll > size {
			return nil, errLZ
		}
		dst = append(dst, src[i:i+ll]...)
		i += ll
		if i >= len(src) {
			break
		}
		if i+2 > len(src) {
			return nil, errLZ
		}
		off := int(src[i]) | int(src[i+1])<<8
		i += 2
		ml := int(tok & 15)
		if ml == 15 {
			var ok bool
			if ml, ok = getLen(ml); !ok {
				return nil, errLZ
			}
		}
		ml += 4
		if off == 0 || off > len(dst) || len(dst)+ml > size {
			return nil, errLZ
		}
		p := len(dst) - off
		if off >= ml {
			dst = append(dst, dst[p:p+ml]...) // no overlap: one copy
		} else {
			for k := 0; k < ml; k++ {
				dst = append(dst, dst[p+k])
			}
		}
	}
	if len(dst) != size {
		return nil, errLZ
	}
	return dst, nil
}

// ldLzWrite packs everything r holds into w as frames.
func ldLzWrite(w io.Writer, r io.Reader) error {
	if _, err := io.WriteString(w, ldLzMagic); err != nil {
		return err
	}
	buf := make([]byte, ldLzBlock)
	var hdr [12]byte
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			c := lzCompress(buf[:n])
			binary.LittleEndian.PutUint32(hdr[0:], uint32(n))
			binary.LittleEndian.PutUint32(hdr[4:], uint32(len(c)))
			binary.LittleEndian.PutUint32(hdr[8:], crc32.ChecksumIEEE(buf[:n]))
			if _, e := w.Write(hdr[:]); e != nil {
				return e
			}
			if _, e := w.Write(c); e != nil {
				return e
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// ldLzReader returns the plain bytes of a file written by ldLzWrite; a torn or damaged frame ends the stream.
func LdLzReader(r io.Reader) (io.Reader, error) {
	br := bufio.NewReaderSize(r, 1<<18)
	head := make([]byte, len(ldLzMagic))
	if _, err := io.ReadFull(br, head); err != nil || string(head) != ldLzMagic {
		return nil, errLZ
	}
	return &ldLzStream{br: br}, nil
}

type ldLzStream struct {
	br   *bufio.Reader
	cur  []byte
	comp []byte
	out  []byte // the unpacked block (cur is the part of it not read yet)
	done bool
}

// ldLzPool keeps streams (with their buffers, about 0.8 MB) between reads: a search over many hours reads one file after another.
var ldLzPool = sync.Pool{New: func() any { return &ldLzStream{br: bufio.NewReaderSize(nil, 1<<18)} }}

// ldLzGet is ldLzReader from the pool; give it back with ldLzPut once nothing read from it is used any more.
func ldLzGet(r io.Reader) (*ldLzStream, error) {
	z := ldLzPool.Get().(*ldLzStream)
	z.br.Reset(r)
	z.cur, z.done = nil, false
	var head [len(ldLzMagic)]byte
	if _, err := io.ReadFull(z.br, head[:]); err != nil || string(head[:]) != ldLzMagic {
		ldLzPut(z)
		return nil, errLZ
	}
	return z, nil
}

func ldLzPut(z *ldLzStream) {
	z.br.Reset(nil)
	z.cur = nil
	ldLzPool.Put(z)
}

func (z *ldLzStream) Read(p []byte) (int, error) {
	for len(z.cur) == 0 {
		if z.done {
			return 0, io.EOF
		}
		var hdr [12]byte
		if _, err := io.ReadFull(z.br, hdr[:]); err != nil {
			z.done = true
			return 0, io.EOF
		}
		raw, cl, sum := int(binary.LittleEndian.Uint32(hdr[0:])), int(binary.LittleEndian.Uint32(hdr[4:])), binary.LittleEndian.Uint32(hdr[8:])
		if raw <= 0 || raw > ldLzBlock || cl <= 0 || cl > ldLzBlock+ldLzBlock/200+64 {
			z.done = true
			return 0, io.EOF
		}
		if cap(z.comp) < cl {
			z.comp = make([]byte, cl)
		}
		z.comp = z.comp[:cl]
		if _, err := io.ReadFull(z.br, z.comp); err != nil {
			z.done = true
			return 0, io.EOF
		}
		out, err := lzDecompressTo(z.out, z.comp, raw)
		if err != nil || crc32.ChecksumIEEE(out) != sum {
			z.done = true
			return 0, io.EOF
		}
		z.out, z.cur = out, out
	}
	n := copy(p, z.cur)
	z.cur = z.cur[n:]
	return n, nil
}
