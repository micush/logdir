package logs

import (
	"encoding/base64"
	"errors"
	"hash/fnv"
	"math"
	"math/bits"
	"sort"
	"strconv"

	"logdir/internal/core"
)

const (
	ldAggExact   = 128 // different values counted exactly (and sent as they are); past it a sketch
	ldHLLBits    = 12  // 2^12 = 4096 registers: a standard error of 1.04/64 = 1.6%
	ldHLLM       = 1 << ldHLLBits
	ldAggSendMax = 200 // per node, the values (of the field) with the most different values: their sets or sketches are sent
)

// ldFieldOf is the value of the field name in the message: one of its fields (ldFields, fs), or else host, program, sender (the
// address it came from) or severity, so the values card can also count by those.
func ldFieldOf(m *core.LdMsg, fs []core.LdKV, name string) string {
	for _, kv := range fs {
		if kv.Key == name {
			return kv.Val
		}
	}
	switch name {
	case "host":
		return m.Host
	case "program":
		return m.Prog
	case "sender":
		return m.Src
	case "severity":
		return sevName(m.Sev)
	}
	return ""
}

// ldAgg is what one value of the field adds up to.
type ldAgg struct {
	Sum  float64  `json:"s,omitempty"`
	Nums uint64   `json:"n,omitempty"` // messages whose sum field is a number
	Vals []string `json:"v,omitempty"`
	HLL  string   `json:"h,omitempty"` // the sketch, encoded (hllEncode)
	set  map[string]struct{}
	reg  []uint8
}

func (a *ldAgg) add(v string) {
	if a.reg != nil {
		hllAdd(a.reg, v)
		return
	}
	if a.set == nil {
		a.set = map[string]struct{}{}
	}
	a.set[v] = struct{}{}
	if len(a.set) > ldAggExact {
		a.reg = make([]uint8, ldHLLM)
		for x := range a.set {
			hllAdd(a.reg, x)
		}
		a.set = nil
	}
}

// distinct is how many different values it has seen: exact up to ldAggExact, estimated past it.
func (a *ldAgg) distinct() uint64 {
	if a.reg != nil {
		return hllEstimate(a.reg)
	}
	return uint64(len(a.set))
}

// seal puts what was counted into the fields that are sent.
func (a *ldAgg) seal() {
	a.Vals, a.HLL = nil, ""
	if a.reg != nil {
		a.HLL = hllEncode(a.reg)
		return
	}
	for v := range a.set {
		a.Vals = append(a.Vals, v)
	}
	sort.Strings(a.Vals)
}

// merge adds another node's answer (sealed) to this one.
func (a *ldAgg) merge(o *ldAgg) error {
	a.Sum += o.Sum
	a.Nums += o.Nums
	for _, v := range o.Vals {
		a.add(v)
	}
	if o.HLL != "" {
		r, err := hllDecode(o.HLL)
		if err != nil {
			return err
		}
		if a.reg == nil {
			a.reg = make([]uint8, ldHLLM)
			for x := range a.set {
				hllAdd(a.reg, x)
			}
			a.set = nil
		}
		for i, x := range r {
			a.reg[i] = max(a.reg[i], x)
		}
	}
	return nil
}

// mergeLive adds another part's counts (not sealed: over other hours of the same node) to this one.
func (a *ldAgg) mergeLive(o *ldAgg) {
	a.Sum += o.Sum
	a.Nums += o.Nums
	if o.reg == nil {
		for v := range o.set {
			a.add(v)
		}
		return
	}
	if a.reg == nil {
		a.reg = make([]uint8, ldHLLM)
		for x := range a.set {
			hllAdd(a.reg, x)
		}
		a.set = nil
	}
	for i, x := range o.reg {
		a.reg[i] = max(a.reg[i], x)
	}
}

// measure adds message m (with fields fs) to a, for a measure of kind over field mf; n is how many such messages.
func (a *ldAgg) measure(kind, mf string, m *core.LdMsg, fs []core.LdKV, n uint64) {
	v := ldFieldOf(m, fs, mf)
	if v == "" {
		return
	}
	switch kind {
	case "distinct":
		a.add(v)
	case "sum":
		if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
			a.Sum += f * float64(n)
			a.Nums += n
		}
	}
}

// ldAggTrim keeps, of one node's counts, what is sent: every sum (a number each), and the sets or sketches of the ldAggSendMax values
// with the most different values (the merge counts those; a value that is small on every node may come out lower than it is).
func ldAggTrim(m map[string]*ldAgg, kind string) {
	if kind == "distinct" && len(m) > ldAggSendMax {
		type kd struct {
			k string
			d uint64
		}
		all := make([]kd, 0, len(m))
		for k, a := range m {
			all = append(all, kd{k, a.distinct()})
		}
		sort.Slice(all, func(i, j int) bool {
			if all[i].d != all[j].d {
				return all[i].d > all[j].d
			}
			return all[i].k < all[j].k
		})
		for _, x := range all[ldAggSendMax:] {
			delete(m, x.k)
		}
	}
	for _, a := range m {
		a.seal()
	}
}

// ── HyperLogLog ──

func hllHash(v string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(v))
	x := h.Sum64()
	// splitmix64's finalizer: FNV's low and high bits are not spread well enough on their own for short, similar strings (ports)
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

func hllAdd(reg []uint8, v string) {
	x := hllHash(v)
	i := x >> (64 - ldHLLBits)
	w := x<<ldHLLBits | 1<<(ldHLLBits-1) // the sentinel bit bounds the run of zeros
	if r := uint8(bits.LeadingZeros64(w) + 1); r > reg[i] {
		reg[i] = r
	}
}

func hllEstimate(reg []uint8) uint64 {
	m := float64(len(reg))
	var sum float64
	zeros := 0
	for _, r := range reg {
		sum += math.Ldexp(1, -int(r))
		if r == 0 {
			zeros++
		}
	}
	e := 0.7213 / (1 + 1.079/m) * m * m / sum
	if e <= 2.5*m && zeros > 0 {
		e = m * math.Log(m/float64(zeros)) // linear counting for small numbers
	}
	return uint64(math.Round(e))
}

// hllEncode: 'd' and the registers, or (shorter while few are set) 's' and the set ones as (index high, index low, value) triples; base64.
func hllEncode(reg []uint8) string {
	var sp []byte
	for i, r := range reg {
		if r != 0 {
			sp = append(sp, byte(i>>8), byte(i), r)
		}
	}
	if len(sp) < len(reg) {
		return base64.RawStdEncoding.EncodeToString(append([]byte{'s'}, sp...))
	}
	return base64.RawStdEncoding.EncodeToString(append([]byte{'d'}, reg...))
}

func hllDecode(s string) ([]uint8, error) {
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil || len(b) == 0 {
		return nil, errors.New("hll: bad encoding")
	}
	reg := make([]uint8, ldHLLM)
	switch b[0] {
	case 'd':
		if len(b)-1 != ldHLLM {
			return nil, errors.New("hll: wrong size")
		}
		copy(reg, b[1:])
	case 's':
		b = b[1:]
		if len(b)%3 != 0 {
			return nil, errors.New("hll: wrong size")
		}
		for i := 0; i < len(b); i += 3 {
			j := int(b[i])<<8 | int(b[i+1])
			if j >= ldHLLM || b[i+2] > 64 {
				return nil, errors.New("hll: out of range")
			}
			reg[j] = b[i+2]
		}
	default:
		return nil, errors.New("hll: unknown encoding")
	}
	for _, r := range reg {
		if r > 64-ldHLLBits+1 {
			return nil, errors.New("hll: out of range")
		}
	}
	return reg, nil
}
