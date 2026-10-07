package hdrhistogram

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"unsafe"
)

// PackedHistogram is a memory-optimised sparse variant of Histogram. Create it
// with NewPacked; the zero value is not ready for use. It keeps the same bucket
// geometry as Histogram without allocating a dense counts array. Its storage
// grows with the number of populated buckets, using sorted indexes and counts
// of a uniform adaptive width (1, 2, 4 or 8 bytes).
//
// Recording into an existing bucket uses a binary search. Populating a new
// bucket also shifts the following entries and costs O(populated buckets).
// Filling many distinct buckets can therefore take quadratic time; prefer
// Histogram for hot recording paths or when most buckets will be populated.
//
// A PackedHistogram must not be copied after initialization: a struct copy
// shares backing arrays but has separate totals and other bookkeeping. Use Clone
// for an independent copy preserving exact geometry, bucket counts (including
// decoded distributions with saturated totals), and log metadata. Clone followed
// by Reset makes an empty sibling with exactly the same geometry. Reconstructing
// from geometry getters with NewPacked applies constructor normalization, which
// cannot preserve decoded zero-digit precision. Encode followed by DecodePacked
// also creates independent storage, but requires valid wire geometry, normalizes
// conversion-ratio metadata to 1, and does not retain log metadata.
//
// PackedHistogram provides no internal synchronization. Concurrent read-only
// calls are safe only while no goroutine mutates the histogram. Callers must
// synchronize all mutation, including recording, Reset, Compact, merges and
// metadata setters, against reads and other mutations. A merge also requires
// its source to remain unchanged for the duration of the call.
type PackedHistogram struct {
	geom       *Histogram // geometry oracle; geom.counts is nil and never indexed
	idx        []int32    // populated flat counts indices, ascending, len == size
	cnt        []byte     // counts, len == size*width, little-endian
	width      uint8      // count byte width: 1, 2, 4, 8
	size       int32      // populated buckets (== len(idx))
	totalCount int64
}

// NewPacked creates a sparse histogram with the same arguments as New. Its
// per-instance geometry oracle never allocates a counts array.
func NewPacked(lowestDiscernibleValue, highestTrackableValue int64, numberOfSignificantValueDigits int) *PackedHistogram {
	return &PackedHistogram{
		geom:  newGeometry(lowestDiscernibleValue, highestTrackableValue, numberOfSignificantValueDigits),
		width: 1,
	}
}

// ---- count-width helpers ----

func packedWidthMax(w uint8) int64 {
	switch w {
	case 1:
		return 0xFF
	case 2:
		return 0xFFFF
	case 4:
		return 0xFFFFFFFF
	default:
		return math.MaxInt64
	}
}

func (p *PackedHistogram) slotGet(i int32) int64 {
	off := int(i) * int(p.width)
	switch p.width {
	case 1:
		return int64(p.cnt[off])
	case 2:
		return int64(binary.LittleEndian.Uint16(p.cnt[off:]))
	case 4:
		return int64(binary.LittleEndian.Uint32(p.cnt[off:]))
	default:
		return int64(binary.LittleEndian.Uint64(p.cnt[off:]))
	}
}

func (p *PackedHistogram) slotSet(i int32, val int64) {
	off := int(i) * int(p.width)
	switch p.width {
	case 1:
		p.cnt[off] = byte(val)
	case 2:
		binary.LittleEndian.PutUint16(p.cnt[off:], uint16(val))
	case 4:
		binary.LittleEndian.PutUint32(p.cnt[off:], uint32(val))
	default:
		binary.LittleEndian.PutUint64(p.cnt[off:], uint64(val))
	}
}

// widenToFit grows the uniform count width so a slot can hold need, re-packing
// existing counts into the wider layout.
func (p *PackedHistogram) widenToFit(need int64) {
	nw := p.width
	for need > packedWidthMax(nw) && nw < 8 {
		nw *= 2
	}
	if nw == p.width {
		return
	}
	// Keep the slot capacity the old buffer had (e.g. retained by Reset), at the
	// new width, so widening does not throw it away.
	nb := make([]byte, int(p.size)*int(nw), cap(p.cnt)/int(p.width)*int(nw))
	for i := int32(0); i < p.size; i++ {
		v := p.slotGet(i) // reads at old width from p.cnt
		off := int(i) * int(nw)
		switch nw {
		case 2:
			binary.LittleEndian.PutUint16(nb[off:], uint16(v))
		case 4:
			binary.LittleEndian.PutUint32(nb[off:], uint32(v))
		default:
			binary.LittleEndian.PutUint64(nb[off:], uint64(v))
		}
	}
	p.cnt = nb
	p.width = nw
}

// lowerBound returns the first index into idx[] whose value is >= key.
func (p *PackedHistogram) lowerBound(key int32) int32 {
	a := p.idx[:p.size]
	base, n := int32(0), p.size
	for n > 0 {
		half := n >> 1
		mid := base + half
		if a[mid] < key {
			base = mid + 1
			n -= half + 1
		} else {
			n = half
		}
	}
	return base
}

// sparseAdd adds delta (>= 0) to the count at flat index ci, inserting a new
// populated bucket if needed.
func (p *PackedHistogram) sparseAdd(ci int32, delta int64) {
	pos := p.lowerBound(ci)
	if pos < p.size && p.idx[pos] == ci {
		// Hit fast path: increment in place at the current width, only falling
		// back to widen+set when the value crosses the width boundary.
		off := int(pos) * int(p.width)
		switch p.width {
		case 1:
			if nv := int64(p.cnt[off]) + delta; nv <= 0xFF {
				p.cnt[off] = byte(nv)
			} else {
				p.widenToFit(nv)
				p.slotSet(pos, nv)
			}
		case 2:
			if nv := int64(binary.LittleEndian.Uint16(p.cnt[off:])) + delta; nv <= 0xFFFF {
				binary.LittleEndian.PutUint16(p.cnt[off:], uint16(nv))
			} else {
				p.widenToFit(nv)
				p.slotSet(pos, nv)
			}
		case 4:
			if nv := int64(binary.LittleEndian.Uint32(p.cnt[off:])) + delta; nv <= 0xFFFFFFFF {
				binary.LittleEndian.PutUint32(p.cnt[off:], uint32(nv))
			} else {
				p.widenToFit(nv)
				p.slotSet(pos, nv)
			}
		default:
			binary.LittleEndian.PutUint64(p.cnt[off:], uint64(int64(binary.LittleEndian.Uint64(p.cnt[off:]))+delta))
		}
		return
	}
	// insert new bucket at pos
	p.widenToFit(delta)
	w := int(p.width)
	// idx insert
	p.idx = append(p.idx, 0)
	copy(p.idx[pos+1:], p.idx[pos:])
	p.idx[pos] = ci
	// cnt insert: open a width-sized gap at pos*w, growing capacity geometrically
	// to avoid a fresh allocation on every insert (copy has memmove semantics).
	oldLen := len(p.cnt)
	newLen := oldLen + w
	gapLo := int(pos) * w
	if newLen <= cap(p.cnt) {
		p.cnt = p.cnt[:newLen]
		copy(p.cnt[gapLo+w:], p.cnt[gapLo:oldLen])
	} else {
		ncap := 2 * cap(p.cnt)
		if ncap < newLen {
			ncap = newLen
		}
		nb := make([]byte, newLen, ncap)
		copy(nb, p.cnt[:gapLo])
		copy(nb[gapLo+w:], p.cnt[gapLo:oldLen])
		p.cnt = nb
	}
	gap := p.cnt[gapLo : gapLo+w]
	for b := range gap {
		gap[b] = 0
	}
	p.size++
	p.slotSet(pos, delta)
}

// ---- record ----

// RecordValue records a single occurrence of v.
func (p *PackedHistogram) RecordValue(v int64) error {
	return p.RecordValues(v, 1)
}

// RecordValues records n occurrences of v, returning an error if v is out of
// range, n is negative, or the total would overflow int64.
func (p *PackedHistogram) RecordValues(v, n int64) error {
	g := p.geom
	ci := int32(g.countsIndexFor(v))
	if uint32(ci) >= uint32(g.countsLen) {
		return fmt.Errorf("value %d is too large to be recorded", v)
	}
	if n < 0 {
		return fmt.Errorf("cannot record a negative count %d", n)
	}
	if n > math.MaxInt64-p.totalCount {
		return fmt.Errorf("recording %d would overflow the total count", n)
	}
	if n != 0 {
		p.sparseAdd(ci, n)
	}
	p.totalCount += n
	return nil
}

// ---- basic queries ----

// TotalCount returns the number of recorded values.
func (p *PackedHistogram) TotalCount() int64 { return p.totalCount }

// Max mirrors dense: the highest-equivalent of the last populated bucket
// (overflow-safe), matching Histogram.Max bit-for-bit.
func (p *PackedHistogram) Max() int64 {
	if p.size == 0 {
		return p.highestEquivalent(0)
	}
	return p.highestEquivalent(p.geom.valueFromFlatIndex(p.idx[p.size-1]))
}

// Min mirrors dense: the lowest-equivalent of the first populated bucket.
func (p *PackedHistogram) Min() int64 {
	if p.size == 0 {
		return p.geom.lowestEquivalentValue(0)
	}
	return p.geom.lowestEquivalentValue(p.geom.valueFromFlatIndex(p.idx[0]))
}

// CountAtValue returns the recorded count at v's bucket (0 if out of range).
// The range is the same one RecordValues accepts, which extends past
// highestTrackableValue up to the top of the last bucket.
func (p *PackedHistogram) CountAtValue(v int64) int64 {
	g := p.geom
	if v < 0 {
		return 0
	}
	idx := g.countsIndexFor(v)
	if idx < 0 || idx >= int(g.countsLen) {
		return 0
	}
	ci := int32(idx)
	pos := p.lowerBound(ci)
	if pos < p.size && p.idx[pos] == ci {
		return p.slotGet(pos)
	}
	return 0
}

// Populated returns the number of populated buckets.
func (p *PackedHistogram) Populated() int32 { return p.size }

// CountWidth returns the current per-count byte width (1/2/4/8).
func (p *PackedHistogram) CountWidth() int { return int(p.width) }

// highestEquivalent is the overflow-safe highest-equivalent value: it saturates
// at MaxInt64 for the top bucket instead of wrapping.
func (p *PackedHistogram) highestEquivalent(v int64) int64 {
	g := p.geom
	leq := g.lowestEquivalentValue(v)
	size := g.sizeOfEquivalentValueRange(v)
	if leq > math.MaxInt64-size {
		return math.MaxInt64
	}
	return leq + size - 1
}

// countAtPercentile resolves a percentile to a target cumulative count in
// [1, totalCount], guarding the float->int cast and clamping to totalCount so
// p100 == max holds. Bit-for-bit dense for totalCount <= 2^52.
func (p *PackedHistogram) countAtPercentile(percentile float64) int64 {
	req := percentile
	if req > 100 {
		req = 100
	}
	cc := (req/100.0)*float64(p.totalCount) + 0.5
	if !(cc >= 1.0) { // NaN or < 1 (incl. negative / -Inf)
		return 1
	}
	if cc >= float64(p.totalCount) {
		return p.totalCount
	}
	return int64(cc)
}

// packedScanBlock is the number of populated buckets summed per block in the
// blocked prefix-sum below (mirrors the dense getValueFromIdxUpToCount).
const packedScanBlock = 8

// valueFromIdxAtCount scans the populated buckets and returns the flat index at
// which the running count first reaches target (0 if unreachable/empty).
//
// For the narrow uniform widths (1/2/4 bytes) a block cannot overflow int64:
// there are at most p.size <= counts-length buckets and each count is < 2^32, so
// the running total stays far below math.MaxInt64 and needs no per-bucket
// saturation. That lets us sum a fixed block of packedScanBlock counts with no
// loop-carried dependency, skip the whole block with a single compare, and
// re-scan only the crossing block element by element. Width 8 (where one count
// can be up to math.MaxInt64) keeps the saturating scalar walk.
func (p *PackedHistogram) valueFromIdxAtCount(target int64) int64 {
	n := p.size
	cnt := p.cnt
	var running int64
	i := int32(0)
	switch p.width {
	case 1:
		for ; i+packedScanBlock <= n; i += packedScanBlock {
			b := cnt[i : i+packedScanBlock : i+packedScanBlock]
			s := int64(b[0]) + int64(b[1]) + int64(b[2]) + int64(b[3]) +
				int64(b[4]) + int64(b[5]) + int64(b[6]) + int64(b[7])
			if running+s >= target {
				for j := int32(0); j < packedScanBlock; j++ {
					running += int64(b[j])
					if running >= target {
						return p.geom.valueFromFlatIndex(p.idx[i+j])
					}
				}
			}
			running += s
		}
	case 2:
		for ; i+packedScanBlock <= n; i += packedScanBlock {
			base := int(i) * 2
			b := cnt[base : base+2*packedScanBlock : base+2*packedScanBlock]
			s := int64(binary.LittleEndian.Uint16(b[0:])) + int64(binary.LittleEndian.Uint16(b[2:])) +
				int64(binary.LittleEndian.Uint16(b[4:])) + int64(binary.LittleEndian.Uint16(b[6:])) +
				int64(binary.LittleEndian.Uint16(b[8:])) + int64(binary.LittleEndian.Uint16(b[10:])) +
				int64(binary.LittleEndian.Uint16(b[12:])) + int64(binary.LittleEndian.Uint16(b[14:]))
			if running+s >= target {
				for j := int32(0); j < packedScanBlock; j++ {
					running += int64(binary.LittleEndian.Uint16(b[2*j:]))
					if running >= target {
						return p.geom.valueFromFlatIndex(p.idx[i+j])
					}
				}
			}
			running += s
		}
	case 4:
		for ; i+packedScanBlock <= n; i += packedScanBlock {
			base := int(i) * 4
			b := cnt[base : base+4*packedScanBlock : base+4*packedScanBlock]
			s := int64(binary.LittleEndian.Uint32(b[0:])) + int64(binary.LittleEndian.Uint32(b[4:])) +
				int64(binary.LittleEndian.Uint32(b[8:])) + int64(binary.LittleEndian.Uint32(b[12:])) +
				int64(binary.LittleEndian.Uint32(b[16:])) + int64(binary.LittleEndian.Uint32(b[20:])) +
				int64(binary.LittleEndian.Uint32(b[24:])) + int64(binary.LittleEndian.Uint32(b[28:]))
			if running+s >= target {
				for j := int32(0); j < packedScanBlock; j++ {
					running += int64(binary.LittleEndian.Uint32(b[4*j:]))
					if running >= target {
						return p.geom.valueFromFlatIndex(p.idx[i+j])
					}
				}
			}
			running += s
		}
	}
	// Tail (and the entire width-8 path): saturating scalar walk.
	for ; i < n; i++ {
		c := p.slotGet(i)
		if c > math.MaxInt64-running {
			running = math.MaxInt64
		} else {
			running += c
		}
		if running >= target {
			return p.geom.valueFromFlatIndex(p.idx[i])
		}
	}
	return 0
}

// ValueAtPercentile returns the largest value that (100%-percentile) of recorded
// entries are >= to. Returns 0 for an empty histogram.
func (p *PackedHistogram) ValueAtPercentile(percentile float64) int64 {
	if p.totalCount == 0 {
		return 0
	}
	// Clamp to [0,100] exactly as dense Histogram.ValueAtPercentile: a negative
	// percentile is the 0th percentile (lowest-equivalent == Min), not the
	// highest-equivalent of the first bucket.
	if percentile > 100 {
		percentile = 100
	} else if percentile < 0 {
		percentile = 0
	}
	vfi := p.valueFromIdxAtCount(p.countAtPercentile(percentile))
	if percentile == 0.0 {
		return p.geom.lowestEquivalentValue(vfi)
	}
	return p.highestEquivalent(vfi)
}

// ValueAtPercentilesSlice returns ValueAtPercentile for each percentile.
//
// Instead of calling ValueAtPercentile once per percentile (each re-walking the
// populated buckets from the start), it resolves every percentile to a
// cumulative-count target, orders those targets ascending, and walks the
// populated buckets a single time, emitting each value as the running count
// crosses its target. Results are bit-identical to the per-percentile calls
// (including the negative->0 clamp and the p==0 lowest-equivalent vs p>0
// highest-equivalent distinction) and order-agnostic (it sorts internally and
// emits into the caller's original slot order).
func (p *PackedHistogram) ValueAtPercentilesSlice(percentiles []float64) []int64 {
	out := make([]int64, len(percentiles))
	n := len(percentiles)
	if n == 0 || p.totalCount == 0 {
		return out
	}
	type ptarget struct {
		orig   int
		isZero bool
		target int64
	}
	order := make([]ptarget, n)
	for i, pc := range percentiles {
		if pc > 100 {
			pc = 100
		} else if pc < 0 {
			pc = 0
		}
		order[i] = ptarget{orig: i, isZero: pc == 0.0, target: p.countAtPercentile(pc)}
	}
	sort.Slice(order, func(a, b int) bool { return order[a].target < order[b].target })

	sz := p.size
	cnt := p.cnt
	var running int64
	j := 0
	i := int32(0)
	// emit drains every target the running count has reached at bucket flat-index fi.
	emit := func(fi int32) {
		vfi := p.geom.valueFromFlatIndex(fi)
		for j < n && running >= order[j].target {
			if order[j].isZero {
				out[order[j].orig] = p.geom.lowestEquivalentValue(vfi)
			} else {
				out[order[j].orig] = p.highestEquivalent(vfi)
			}
			j++
		}
	}
	// Blocked single pass for widths 1/2/4 (no int64 overflow across a block, as
	// in valueFromIdxAtCount): skip a block with one compare when it crosses no
	// pending target; scan only crossing blocks element by element.
	switch p.width {
	case 1:
		for ; j < n && i+packedScanBlock <= sz; i += packedScanBlock {
			b := cnt[i : i+packedScanBlock : i+packedScanBlock]
			s := int64(b[0]) + int64(b[1]) + int64(b[2]) + int64(b[3]) +
				int64(b[4]) + int64(b[5]) + int64(b[6]) + int64(b[7])
			if running+s < order[j].target {
				running += s
				continue
			}
			for k := int32(0); k < packedScanBlock; k++ {
				running += int64(b[k])
				if running >= order[j].target {
					emit(p.idx[i+k])
					if j >= n {
						return out
					}
				}
			}
		}
	case 2:
		for ; j < n && i+packedScanBlock <= sz; i += packedScanBlock {
			base := int(i) * 2
			b := cnt[base : base+2*packedScanBlock : base+2*packedScanBlock]
			s := int64(binary.LittleEndian.Uint16(b[0:])) + int64(binary.LittleEndian.Uint16(b[2:])) +
				int64(binary.LittleEndian.Uint16(b[4:])) + int64(binary.LittleEndian.Uint16(b[6:])) +
				int64(binary.LittleEndian.Uint16(b[8:])) + int64(binary.LittleEndian.Uint16(b[10:])) +
				int64(binary.LittleEndian.Uint16(b[12:])) + int64(binary.LittleEndian.Uint16(b[14:]))
			if running+s < order[j].target {
				running += s
				continue
			}
			for k := int32(0); k < packedScanBlock; k++ {
				running += int64(binary.LittleEndian.Uint16(b[2*k:]))
				if running >= order[j].target {
					emit(p.idx[i+k])
					if j >= n {
						return out
					}
				}
			}
		}
	case 4:
		for ; j < n && i+packedScanBlock <= sz; i += packedScanBlock {
			base := int(i) * 4
			b := cnt[base : base+4*packedScanBlock : base+4*packedScanBlock]
			s := int64(binary.LittleEndian.Uint32(b[0:])) + int64(binary.LittleEndian.Uint32(b[4:])) +
				int64(binary.LittleEndian.Uint32(b[8:])) + int64(binary.LittleEndian.Uint32(b[12:])) +
				int64(binary.LittleEndian.Uint32(b[16:])) + int64(binary.LittleEndian.Uint32(b[20:])) +
				int64(binary.LittleEndian.Uint32(b[24:])) + int64(binary.LittleEndian.Uint32(b[28:]))
			if running+s < order[j].target {
				running += s
				continue
			}
			for k := int32(0); k < packedScanBlock; k++ {
				running += int64(binary.LittleEndian.Uint32(b[4*k:]))
				if running >= order[j].target {
					emit(p.idx[i+k])
					if j >= n {
						return out
					}
				}
			}
		}
	}
	// Tail (and the entire width-8 path): saturating scalar walk.
	for ; j < n && i < sz; i++ {
		c := p.slotGet(i)
		if c > math.MaxInt64-running {
			running = math.MaxInt64
		} else {
			running += c
		}
		if running >= order[j].target {
			emit(p.idx[i])
		}
	}
	return out
}

// GetMemorySize returns the approximate bytes held by this histogram: the
// struct, its per-instance geometry oracle, and the idx + cnt capacities.
func (p *PackedHistogram) GetMemorySize() int {
	return packedFixedSize + cap(p.idx)*4 + cap(p.cnt)
}

// packedFixedSize is the per-instance overhead independent of population.
const packedFixedSize = int(unsafe.Sizeof(PackedHistogram{})) + int(unsafe.Sizeof(Histogram{}))
