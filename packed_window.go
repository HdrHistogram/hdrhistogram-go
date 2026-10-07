package hdrhistogram

import "math"

// Reuse, iteration and merge APIs for PackedHistogram, aimed at rolling
// windows: record into a dense Histogram, move each completed slice into a
// reused PackedHistogram slot, and merge the retained slices into a dense
// aggregate when percentiles are needed.

// Reset empties the histogram for reuse. Like the dense Histogram.Reset and
// HdrHistogram_c's hdr_packed_reset, it keeps the allocated backing arrays,
// and it also keeps the current count width, so a record/merge/reset cycle
// does not reallocate. The width only grows; a new PackedHistogram starts
// again at one byte per count.
func (p *PackedHistogram) Reset() {
	p.idx = p.idx[:0]
	p.cnt = p.cnt[:0]
	p.size = 0
	p.totalCount = 0
}

// ForEachBucket calls fn once per populated bucket, in ascending value order,
// with the bucket's value (the lowest value equivalent to it, as dense
// iteration reports it) and its count. It stops early if fn returns false.
// The work is proportional to the number of populated buckets and it does
// not allocate.
func (p *PackedHistogram) ForEachBucket(fn func(value, count int64) bool) {
	for i := int32(0); i < p.size; i++ {
		if !fn(p.geom.valueFromFlatIndex(p.idx[i]), p.slotGet(i)) {
			return
		}
	}
}

// sameIndexing reports whether values map to the same flat counts index in
// both geometries (the counts length may still differ).
func sameIndexing(a, b *Histogram) bool {
	return a.unitMagnitude == b.unitMagnitude && a.subBucketHalfCountMagnitude == b.subBucketHalfCountMagnitude
}

// MergeInto adds this histogram's counts to dst and returns the total count
// that dst could not hold. The result is exactly that of dst.Merge applied to
// the dense equivalent of p: each populated bucket is recorded at its value,
// and buckets whose value is out of dst's range are dropped. As with
// Histogram.Merge, counts are added without overflow checks.
//
// When dst indexes values the same way (same lowestDiscernibleValue and
// significant digits), counts are added index to index: the work is
// proportional to the number of populated buckets and it does not allocate.
// Otherwise each bucket is re-recorded through dst.RecordValues.
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
		if dst.RecordValues(p.geom.valueFromFlatIndex(p.idx[i]), c) != nil {
			dropped += c
		}
	}
	return dropped
}

// MergeFrom adds the counts of a dense histogram to p and returns the total
// count that p could not hold: buckets whose value is out of p's range, and
// counts that would overflow p's total (PackedHistogram rejects those rather
// than wrapping). It scans src's counts once, without a temporary
// allocation; with p empty, as after Reset, each bucket is appended in order.
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
			// Only positive counts are mergeable; a negative count can only come
			// from a corrupted dense histogram and RecordValues rejects it.
			continue
		}
		if same {
			if int32(idx) >= p.geom.countsLen {
				dropped += c
				continue
			}
			if c > math.MaxInt64-p.totalCount {
				dropped += c
				continue
			}
			p.sparseAdd(int32(idx), c)
			p.totalCount += c
			continue
		}
		if p.RecordValues(src.valueFromFlatIndex(int32(idx)), c) != nil {
			dropped += c
		}
	}
	return dropped
}
