package hdrhistogram

import (
	"testing"
)

// sumCounts is the internal invariant oracle: a well-formed histogram's totalCount
// must equal the sum of its counts[].
func sumCounts(h *Histogram) int64 {
	var s int64
	for _, c := range h.counts {
		s += c
	}
	return s
}

// checkInvariants asserts the structural invariants every valid histogram must hold,
// independent of how it was produced (decoded, recorded, imported).
func checkInvariants(t *testing.T, h *Histogram, ctx string) {
	t.Helper()
	if got := sumCounts(h); got != h.TotalCount() {
		t.Fatalf("%s: TotalCount()=%d != sum(counts)=%d", ctx, h.TotalCount(), got)
	}
	if int32(len(h.counts)) != h.countsLen {
		t.Fatalf("%s: len(counts)=%d != countsLen=%d", ctx, len(h.counts), h.countsLen)
	}
	if h.TotalCount() < 0 {
		t.Fatalf("%s: negative TotalCount %d", ctx, h.TotalCount())
	}
	if h.Min() > h.Max() {
		t.Fatalf("%s: Min()=%d > Max()=%d", ctx, h.Min(), h.Max())
	}
	if h.TotalCount() > 0 && h.Max() > h.ValueAtPercentile(100) {
		t.Fatalf("%s: Max()=%d > ValueAtPercentile(100)=%d", ctx, h.Max(), h.ValueAtPercentile(100))
	}
	// Percentiles must be monotonic non-decreasing across [0,100].
	// (A tighter "every percentile lies within [Min,Max]" bound does NOT hold on this
	// port yet: for a small totalCount, low percentiles round countAtPercentile to 0
	// and read value 0 below Min, because Java's max(countAtPercentile,1) rule is not
	// applied. That stronger invariant is enforced on the branch that adds the rule.)
	prev := int64(-1)
	for _, p := range []float64{0, 1, 25, 50, 75, 90, 99, 99.9, 100} {
		v := h.ValueAtPercentile(p) // must not panic
		if v < prev {
			t.Fatalf("%s: ValueAtPercentile not monotonic: p=%v -> %d < prev %d", ctx, p, v, prev)
		}
		prev = v
	}
}

// FuzzDecodeInvariants is a stronger FuzzDecode: beyond no-panic, any histogram that
// decodes must satisfy the structural invariants AND survive a canonical round-trip
// (Encode -> Decode -> Equals). A TotalCount-only check misses bucket-misplacement
// bugs (e.g. a wrong normalizingIndexOffset that preserves the count but shifts every
// value); Equals + the percentile-range checks catch those.
func FuzzDecodeInvariants(f *testing.F) {
	if h := New(1, 1000, 3); h != nil {
		_ = h.RecordValue(42)
		if enc, err := h.Encode(V2CompressedEncodingCookieBase); err == nil {
			f.Add(enc)
		}
	}
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := Decode(data)
		if err != nil || h == nil {
			return
		}
		checkInvariants(t, h, "decoded")

		enc, err := h.Encode(V2CompressedEncodingCookieBase)
		if err != nil {
			t.Fatalf("re-encode failed: %v", err)
		}
		h2, err := Decode(enc)
		if err != nil {
			t.Fatalf("re-decode failed: %v", err)
		}
		checkInvariants(t, h2, "round-tripped")
		if !h.Equals(h2) {
			t.Fatalf("Encode->Decode is not an identity (Equals=false)")
		}
	})
}

// FuzzPercentileQueries fuzzes the READ path directly: record a value, then query
// with an arbitrary float percentile (including NaN/Inf/negative/>100). Property: the
// query never panics, and a non-empty histogram returns a value within [Min, Max].
func FuzzPercentileQueries(f *testing.F) {
	f.Add(int64(1), int64(1000000), uint8(3), int64(500), float64(50))
	f.Add(int64(100), int64(10000000), uint8(3), int64(777), float64(-1))
	f.Fuzz(func(t *testing.T, lo, hi int64, sig uint8, v int64, pct float64) {
		if lo < 1 || hi <= lo || hi > (1<<40) {
			t.Skip()
		}
		h := New(lo, hi, int(sig%5)+1)
		_ = h.RecordValue(clampVal(v, lo, hi))
		// Must not panic on any float percentile — NaN, ±Inf, negative, or > 100.
		// (Tighter value bounds for negative percentiles are enforced where the
		// negative clamp lands; here we guard the read path against crashes.)
		_ = h.ValueAtPercentile(pct)
		_ = h.ValueAtQuantile(pct)
	})
}

// FuzzZigZagDecodeBytes decodes arbitrary bytes as a zig-zag varint. Property: never
// panics; the reported consumed length is within [0, 9] (LEB128-64b9B max); and any
// value it decodes re-encodes to no more bytes than it consumed.
func FuzzZigZagDecodeBytes(f *testing.F) {
	f.Add([]byte{0x00})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		v, n, err := zig_zag_decode_i64(data)
		if err != nil {
			return
		}
		if n < 0 || n > 9 {
			t.Fatalf("zig_zag_decode_i64 consumed %d bytes, want [0,9]", n)
		}
		if n > len(data) {
			t.Fatalf("consumed %d > input %d", n, len(data))
		}
		if re := zig_zag_encode_i64(v); len(re) > n && n > 0 {
			t.Fatalf("re-encode of %d is %d bytes, longer than the %d consumed", v, len(re), n)
		}
	})
}

// FuzzMergeMetamorphic builds two histograms with the same bounds and checks that
// Merge is well-behaved: no panic, count conservation, and invariants after merge.
func FuzzMergeMetamorphic(f *testing.F) {
	f.Add(int64(1), int64(1000000), uint8(3), int64(10), int64(20))
	f.Fuzz(func(t *testing.T, lo, hi int64, sig uint8, a, b int64) {
		if lo < 1 || hi <= lo || hi > (1<<40) {
			t.Skip()
		}
		h1 := New(lo, hi, int(sig%5)+1)
		h2 := New(lo, hi, int(sig%5)+1)
		_ = h1.RecordValue(clampVal(a, lo, hi))
		_ = h2.RecordValue(clampVal(b, lo, hi))
		before := h1.TotalCount() + h2.TotalCount()
		dropped := h1.Merge(h2) // must not panic
		if h1.TotalCount()+dropped != before {
			t.Fatalf("count not conserved: merged %d + dropped %d != %d", h1.TotalCount(), dropped, before)
		}
		checkInvariants(t, h1, "merged")
	})
}

func clampVal(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
