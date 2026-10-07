package hdrhistogram

import (
	"encoding/binary"
	"math"
)

// Reuse, iteration and merge APIs for PackedHistogram, aimed at rolling
// windows: record into a dense Histogram, move each completed slice into a
// reused PackedHistogram slot, and merge the retained slices into a dense
// aggregate when percentiles are needed.

// Reset empties the histogram for reuse. Like the dense Histogram.Reset and
// HdrHistogram_c's hdr_packed_reset, it keeps the allocated backing arrays,
// and it also keeps the current count width, so a record/merge/reset cycle
// does not reallocate. The width only grows; to give the memory back (say,
// after a burst widened a ring slot to 8-byte counts), call Compact.
func (p *PackedHistogram) Reset() {
	p.idx = p.idx[:0]
	p.cnt = p.cnt[:0]
	p.size = 0
	p.totalCount = 0
}

// ForEachBucket calls fn once per populated bucket, in ascending value order,
// with the bucket's value (the lowest value equivalent to it, as dense
// iteration reports it) and its count. It stops early if fn returns false.
// fn must not modify p. The work is proportional to the number of populated
// buckets and it does not allocate.
//
// Its signature is an iter.Seq2, so it also works with range:
//
//	for value, count := range p.ForEachBucket { ... }
func (p *PackedHistogram) ForEachBucket(fn func(value, count int64) bool) {
	for i := int32(0); i < p.size; i++ {
		if !fn(p.geom.valueFromFlatIndex(p.idx[i]), p.slotGet(i)) {
			return
		}
	}
}

// sameIndexing reports whether values map to the same flat counts index in
// both geometries (the counts length may still differ). Every other part of
// the index math derives from these two magnitudes.
func sameIndexing(a, b *Histogram) bool {
	return a.unitMagnitude == b.unitMagnitude && a.subBucketHalfCountMagnitude == b.subBucketHalfCountMagnitude
}

// MergeInto adds this histogram's counts to dst and returns the total count
// that dst could not hold. The result is exactly that of dst.Merge applied to
// the dense equivalent of p: each populated bucket is recorded at its value,
// and buckets whose value is out of dst's range are dropped. As with
// Histogram.Merge, counts are added without overflow checks.
//
// When dst indexes values the same way (the same significant digits and the
// same unit magnitude, floor(log2(lowestDiscernibleValue))), counts are added
// index to index, with work proportional to the number of populated buckets.
// Otherwise each bucket is re-recorded at its value. Neither path allocates.
func (p *PackedHistogram) MergeInto(dst *Histogram) (dropped int64) {
	if sameIndexing(p.geom, dst) {
		for i := int32(0); i < p.size; i++ {
			c := p.slotGet(i)
			if idx := int(p.idx[i]); idx < len(dst.counts) {
				dst.setCountAtIndex(idx, c)
			} else {
				dropped += c
			}
		}
		return dropped
	}
	for i := int32(0); i < p.size; i++ {
		c := p.slotGet(i)
		// The same bound dst.RecordValues applies, without building an error
		// for every dropped bucket.
		if idx := dst.countsIndexFor(p.geom.valueFromFlatIndex(p.idx[i])); uint(idx) < uint(len(dst.counts)) {
			dst.setCountAtIndex(idx, c)
		} else {
			dropped += c
		}
	}
	return dropped
}

// MergeFrom adds the counts of a dense histogram to p and returns the total
// count that p could not hold: buckets whose value is out of p's range, and
// counts that would overflow p's total (PackedHistogram rejects those rather
// than wrapping). Non-positive dense counts, which only a corrupted dense
// histogram can hold, are skipped and not counted as dropped. It scans src's
// counts once and builds no temporary; with p empty, as after Reset, each
// bucket is appended in order, and into a slot with enough capacity it does
// not allocate.
//
// To move a completed dense slice into a reused slot:
//
//	slot.Reset()
//	slot.MergeFrom(active)
//	active.Reset()
func (p *PackedHistogram) MergeFrom(src *Histogram) (dropped int64) {
	same := sameIndexing(p.geom, src)
	for idx, c := range src.counts {
		if c <= 0 {
			continue
		}
		ci := int32(idx)
		if !same {
			// Re-record at the bucket's value, with the bound RecordValues
			// applies but without building an error for every dropped bucket.
			ci = int32(p.geom.countsIndexFor(src.valueFromFlatIndex(ci)))
		}
		if uint32(ci) >= uint32(p.geom.countsLen) || c > math.MaxInt64-p.totalCount {
			dropped += c
			continue
		}
		p.sparseAdd(ci, c)
		p.totalCount += c
	}
	return dropped
}

// Compact shrinks the histogram's storage to fit its contents: the count
// width becomes the narrowest that holds the largest current count, and
// spare capacity is released. After Reset it returns the histogram to the
// footprint of a new one, so a ring slot widened by a burst can be shrunk
// in place. It allocates exactly-sized arrays (none when empty).
func (p *PackedHistogram) Compact() {
	if p.size == 0 {
		p.idx, p.cnt, p.width = nil, nil, 1
		return
	}
	var largest int64
	for i := int32(0); i < p.size; i++ {
		if c := p.slotGet(i); c > largest {
			largest = c
		}
	}
	w := uint8(1)
	for largest > packedWidthMax(w) && w < 8 {
		w *= 2
	}
	idx := make([]int32, p.size)
	copy(idx, p.idx[:p.size])
	cnt := make([]byte, int(p.size)*int(w))
	for i := int32(0); i < p.size; i++ {
		putPackedCount(cnt, int(i)*int(w), w, p.slotGet(i))
	}
	p.idx, p.cnt, p.width = idx, cnt, w
}

// putPackedCount writes v at byte offset off of buf at width w.
func putPackedCount(buf []byte, off int, w uint8, v int64) {
	switch w {
	case 1:
		buf[off] = byte(v)
	case 2:
		binary.LittleEndian.PutUint16(buf[off:], uint16(v))
	case 4:
		binary.LittleEndian.PutUint32(buf[off:], uint32(v))
	default:
		binary.LittleEndian.PutUint64(buf[off:], uint64(v))
	}
}

// Merge adds the counts of another packed histogram to p and returns the
// total count that p could not hold. The result is that of recording every
// populated bucket of from into p in value order: buckets whose value is out
// of p's range are dropped, and so are counts that would overflow p's total.
// from must not share backing storage with p (a struct copy of p does);
// p.Merge(p) itself is allowed and doubles every count.
//
// When both index values the same way (see MergeInto) and the totals cannot
// overflow, the two sorted bucket lists are merged in place from the end in
// a single pass, linear in both sizes; it allocates only when p needs more
// capacity. Otherwise each bucket is re-recorded at its value.
func (p *PackedHistogram) Merge(from *PackedHistogram) (dropped int64) {
	if from.size == 0 {
		return 0
	}
	same := sameIndexing(p.geom, from.geom)
	if from == p || !same || from.totalCount == math.MaxInt64 || from.totalCount > math.MaxInt64-p.totalCount {
		// Re-record bucket by bucket, reading each count before adding it, which
		// also makes p.Merge(p) add a snapshot of p. from.totalCount is only a
		// lower bound of its bucket sum when saturated by a decode.
		for i := int32(0); i < from.size; i++ {
			c := from.slotGet(i)
			ci := from.idx[i]
			if !same {
				ci = int32(p.geom.countsIndexFor(from.geom.valueFromFlatIndex(ci)))
			}
			if uint32(ci) >= uint32(p.geom.countsLen) || c > math.MaxInt64-p.totalCount {
				dropped += c
				continue
			}
			p.sparseAdd(ci, c)
			p.totalCount += c
		}
		return dropped
	}

	// Same indexing and no overflow possible. Buckets past p's counts are a
	// suffix of from (its indices ascend) and are dropped.
	n := from.size
	for n > 0 && from.idx[n-1] >= p.geom.countsLen {
		n--
		dropped += from.slotGet(n)
	}
	// Pass 1: how many buckets are new to p, and the largest resulting count.
	var added int32
	var largest int64
	for i, j := int32(0), int32(0); j < n; {
		switch {
		case i < p.size && p.idx[i] < from.idx[j]:
			i++
		case i < p.size && p.idx[i] == from.idx[j]:
			if c := p.slotGet(i) + from.slotGet(j); c > largest {
				largest = c
			}
			i++
			j++
		default:
			if c := from.slotGet(j); c > largest {
				largest = c
			}
			added++
			j++
		}
	}
	p.widenToFit(largest)
	newSize := p.size + added
	w := int(p.width)
	if int(newSize) <= cap(p.idx) {
		p.idx = p.idx[:newSize]
	} else {
		idx := make([]int32, newSize, 2*int(newSize))
		copy(idx, p.idx[:p.size])
		p.idx = idx
	}
	if int(newSize)*w <= cap(p.cnt) {
		p.cnt = p.cnt[:int(newSize)*w]
	} else {
		cnt := make([]byte, int(newSize)*w, 2*int(newSize)*w)
		copy(cnt, p.cnt[:int(p.size)*w])
		p.cnt = cnt
	}
	// Pass 2: merge from the end, so every slot of p is read before the write
	// position (never behind it) reaches it.
	i, j, out := p.size-1, n-1, newSize-1
	for j >= 0 {
		switch {
		case i >= 0 && p.idx[i] > from.idx[j]:
			p.idx[out] = p.idx[i]
			p.slotSet(out, p.slotGet(i))
			i--
		case i >= 0 && p.idx[i] == from.idx[j]:
			p.idx[out] = p.idx[i]
			p.slotSet(out, p.slotGet(i)+from.slotGet(j))
			i--
			j--
		default:
			p.idx[out] = from.idx[j]
			p.slotSet(out, from.slotGet(j))
			j--
		}
		out--
	}
	p.size = newSize
	p.totalCount += from.totalCount - dropped
	return dropped
}
