package hdrhistogram

import (
	"math"
	"testing"
)

// plural: negative percentile is clamped to 0 (438-440), and the early-return
// when every target has emitted before a block finishes (479-481).
func TestCov_perfpaths2_PluralClampAndEarlyReturn(t *testing.T) {
	// width 2, >8 buckets so the blocked plural path runs.
	p := NewPacked(1, 1000000000, 3)
	d := New(1, 1000000000, 3)
	for b := 0; b < 12; b++ {
		c := int64(1)
		if b == 0 {
			c = 300 // force width 2
		}
		_ = p.RecordValues(int64(1000+b*1000), c)
		_ = d.RecordValues(int64(1000+b*1000), c)
	}
	if p.CountWidth() != 2 {
		t.Fatalf("want width 2, got %d", p.CountWidth())
	}
	// negative percentile in a slice -> clamped to 0 == Min, matching dense.
	got := p.ValueAtPercentilesSlice([]float64{-5, 50})
	if got[0] != p.Min() || got[0] != d.ValueAtPercentile(-5) {
		t.Fatalf("plural neg clamp: got %d, Min %d, dense %d", got[0], p.Min(), d.ValueAtPercentile(-5))
	}
	// single low percentile: target crosses in the first block element, j reaches
	// n mid-block -> early return (width-2 case).
	if v := p.ValueAtPercentilesSlice([]float64{0})[0]; v != p.ValueAtPercentile(0) {
		t.Fatalf("plural early-return p0 (w2): %d != %d", v, p.ValueAtPercentile(0))
	}
	// width-1 early return (a distinct code path/line from width 2).
	p1 := NewPacked(1, 1000000000, 3)
	for b := 0; b < 12; b++ {
		_ = p1.RecordValue(int64(1000 + b*1000))
	}
	if p1.CountWidth() != 1 {
		t.Fatalf("want width 1, got %d", p1.CountWidth())
	}
	if v := p1.ValueAtPercentilesSlice([]float64{0})[0]; v != p1.ValueAtPercentile(0) {
		t.Fatalf("plural early-return p0 (w1): %d != %d", v, p1.ValueAtPercentile(0))
	}
}

// plural (ValueAtPercentilesSlice) width-8 tail saturating add: counts summing
// past MaxInt64. Built whitebox (the total-count guard blocks this via records).
func TestCov_perfpaths2_PluralWidth8Saturation(t *testing.T) {
	p := NewPacked(1, 1000000000, 3)
	p.width = 8
	p.size = 2
	p.idx = []int32{0, 1}
	p.cnt = make([]byte, 16)
	p.slotSet(0, math.MaxInt64-5)
	p.slotSet(1, 10)
	p.totalCount = math.MaxInt64 // so the p100 target == MaxInt64 is reachable only past the saturating pin
	got := p.ValueAtPercentilesSlice([]float64{100})
	if want := p.highestEquivalent(p.geom.valueFromFlatIndex(1)); got[0] != want {
		t.Fatalf("plural width-8 saturation: got %d want %d", got[0], want)
	}
}

// width-8 tail saturating add in valueFromIdxAtCount (533-535): counts summing
// past MaxInt64 before the target. Built whitebox (the public total-count guard
// prevents this state through RecordValues).
func TestCov_perfpaths2_Width8ScanSaturation(t *testing.T) {
	p := NewPacked(1, 1000000000, 3)
	p.width = 8
	p.size = 2
	p.idx = []int32{0, 1}
	p.cnt = make([]byte, 16)
	p.slotSet(0, math.MaxInt64-5)
	p.slotSet(1, 10)
	// target reachable only after the saturating pin at bucket 1.
	got := p.valueFromIdxAtCount(math.MaxInt64)
	if want := p.geom.valueFromFlatIndex(1); got != want {
		t.Fatalf("width-8 saturating scan: got %d want %d", got, want)
	}
}

// decode total-count saturation (198-200): two positive counts summing past
// MaxInt64 in the payload.
func TestCov_perfpaths2_DecodeTotalSaturation(t *testing.T) {
	p := NewPacked(1, 1000000000, 3)
	payload := append(zig_zag_encode_i64(math.MaxInt64), zig_zag_encode_i64(math.MaxInt64)...)
	if err := fillSparseFromPayload(payload, p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.totalCount != math.MaxInt64 {
		t.Fatalf("total should saturate to MaxInt64, got %d", p.totalCount)
	}
	if p.CountWidth() != 8 {
		t.Fatalf("counts of MaxInt64 should widen to 8, got %d", p.CountWidth())
	}
}

// decode zlib errors: NewReader failure on a non-zlib body (147-149) and
// ReadAll failure on a valid header with a corrupt deflate body (154-156).
func TestCov_perfpaths2_DecodeZlibErrors(t *testing.T) {
	// non-zlib body -> zlib.NewReader errors.
	if _, err := decodePackedCompressed([]byte{0x00, 0x00, 0x00, 0x00}); err == nil {
		t.Fatal("expected NewReader error on non-zlib body")
	}
	// valid 2-byte zlib header (0x78 0x9c) + an invalid deflate block (BTYPE=11
	// reserved) -> NewReader ok, io.ReadAll errors.
	if _, err := decodePackedCompressed([]byte{0x78, 0x9c, 0xff, 0xff, 0xff, 0xff}); err == nil {
		t.Fatal("expected ReadAll error on invalid deflate body")
	}
}
