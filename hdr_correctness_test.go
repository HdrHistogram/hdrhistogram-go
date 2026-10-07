package hdrhistogram

import (
	"math"
	"strings"
	"testing"
)

// Real foreign-writer fixtures (HdrHistogram Java de84b0a, HdrHistogram_c
// 8885476), with the values those implementations report when reading them.
const (
	// Java PackedHistogram(1, 1000, 0) and (1, 10000, 0), recordValue(100).
	javaZeroDigits1000  = "HISTFAAAAB14nJNpmSzMwMDAxIAKGCEU8wv7DxAWLxMASToDcw=="
	javaZeroDigits10000 = "HISTFAAAAB14nJNpmSzMwMDAxIAKGCGUuoD9BwiLlwkAQaICvw=="
	// Java and C (1, 2048, 3) with recordValue(2048): byte-identical streams.
	foreignBoundary2048 = "HISTFAAAACB4nJNpmSzMwMDAzAABMJoRQnEw2H+AsP7LMwEARZgDpQ=="
)

// #84: zero significant digits are valid on the wire (Java accepts 0-5). The
// decoders must use the serialized geometry instead of clamping it to 1,
// which silently moved a recorded 100 to 7.
func TestDecodeJavaZeroDigitStreams(t *testing.T) {
	for _, wire := range []string{javaZeroDigits1000, javaZeroDigits10000} {
		p, err := DecodePacked([]byte(wire))
		if err != nil {
			t.Fatal(err)
		}
		// Java reads these as total 1, min 64, max 127, count at 100 == 1.
		if p.TotalCount() != 1 || p.Min() != 64 || p.Max() != 127 || p.CountAtValue(100) != 1 {
			t.Fatalf("packed: total %d min %d max %d count(100) %d, want 1 64 127 1",
				p.TotalCount(), p.Min(), p.Max(), p.CountAtValue(100))
		}
		if p.geom.significantFigures != 0 {
			t.Fatalf("decoded geometry has %d significant digits, want 0", p.geom.significantFigures)
		}
		d, err := Decode([]byte(wire))
		if err != nil {
			t.Fatal(err)
		}
		if d.TotalCount() != 1 || d.Min() != 64 || d.Max() != 127 || d.counts[d.countsIndexFor(100)] != 1 {
			t.Fatalf("dense: total %d min %d max %d, want 1 64 127", d.TotalCount(), d.Min(), d.Max())
		}
		// Re-encoding keeps 0 digits, and both decoders read it back unchanged.
		pe, _ := p.Encode()
		de, _ := d.Encode(V2CompressedEncodingCookieBase)
		if string(pe) != string(de) {
			t.Fatal("packed and dense re-encodings differ")
		}
		again, err := DecodePacked(pe)
		if err != nil || again.geom.significantFigures != 0 || again.Max() != 127 || again.CountAtValue(100) != 1 {
			t.Fatalf("re-decode: %v", err)
		}
	}
	// The public constructors keep their documented clamping to 1-5 digits.
	if g := New(1, 1000, 0); g.significantFigures != 1 {
		t.Fatalf("New clamps digits to 1, got %d", g.significantFigures)
	}
	if p := NewPacked(1, 1000, 0); p.geom.significantFigures != 1 {
		t.Fatalf("NewPacked clamps digits to 1, got %d", p.geom.significantFigures)
	}
}

// Serialized geometry outside the format's range is rejected, not clamped.
func TestDecodeRejectsInvalidWireDigitsAndLowest(t *testing.T) {
	for _, tc := range []struct {
		low int64
		sig int32
	}{{1, -1}, {1, 6}, {0, 3}, {-5, 3}} {
		enc := buildPackedV2Stream(tc.low, 1000, tc.sig, 0, 0, 0, nil)
		if _, err := Decode(enc); err == nil || !strings.Contains(err.Error(), "corrupt histogram header") {
			t.Errorf("Decode(low %d, sig %d) err = %v", tc.low, tc.sig, err)
		}
		if _, err := DecodePacked(enc); err == nil || !strings.Contains(err.Error(), "corrupt histogram header") {
			t.Errorf("DecodePacked(low %d, sig %d) err = %v", tc.low, tc.sig, err)
		}
	}
}

// #85: negative values must be rejected before mutation, even where the
// bucket math would map them to a valid index (the widest ranges).
func TestRecordRejectsNegativeValues(t *testing.T) {
	for _, high := range []int64{1000, 3600000000, math.MaxInt64} {
		d := New(1, high, 3)
		p := NewPacked(1, high, 3)
		for _, v := range []int64{-1, -1000, math.MinInt64} {
			if err := d.RecordValues(v, 1); err == nil || !strings.Contains(err.Error(), "negative") {
				t.Fatalf("dense high %d: RecordValues(%d) err = %v", high, v, err)
			}
			if err := p.RecordValues(v, 1); err == nil || !strings.Contains(err.Error(), "negative") {
				t.Fatalf("packed high %d: RecordValues(%d) err = %v", high, v, err)
			}
			if err := d.RecordCorrectedValue(v, 10); err == nil {
				t.Fatalf("dense high %d: RecordCorrectedValue(%d) accepted", high, v)
			}
		}
		if d.TotalCount() != 0 || p.TotalCount() != 0 || p.Populated() != 0 {
			t.Fatalf("high %d: rejected values changed state", high)
		}
		for _, h := range d.counts {
			if h != 0 {
				t.Fatalf("high %d: rejected value left a dense count", high)
			}
		}
		// Zero and the configured maximum stay recordable.
		if d.RecordValue(0) != nil || p.RecordValue(0) != nil || d.RecordValue(high) != nil || p.RecordValue(high) != nil {
			t.Fatalf("high %d: 0 or the configured maximum was rejected", high)
		}
	}
}

// #86: the configured highest value must be recordable when it falls exactly
// on a bucket boundary (C and Java size the geometry with <=, not <).
func TestConfiguredHighestValueAtBucketBoundaries(t *testing.T) {
	for sig := 1; sig <= 5; sig++ {
		for _, low := range []int64{1, 3, 1000, 1024} {
			unit, half := geometryMagnitudes(low, sig)
			first := int64(1) << uint(int(unit)+int(half)+1) // smallest untrackable value of bucket 0
			for k := 0; k < 4 && first<<uint(k) > 0; k++ {
				boundary := first << uint(k)
				for _, high := range []int64{boundary - 1, boundary, boundary + 1} {
					d := New(low, high, sig)
					p := NewPacked(low, high, sig)
					if err := d.RecordValue(high); err != nil {
						t.Fatalf("dense New(%d, %d, %d).RecordValue(high): %v", low, high, sig, err)
					}
					if err := p.RecordValue(high); err != nil {
						t.Fatalf("packed NewPacked(%d, %d, %d).RecordValue(high): %v", low, high, sig, err)
					}
				}
			}
		}
	}
	// The overflow guard still terminates for the widest range.
	if err := New(1, math.MaxInt64, 3).RecordValue(math.MaxInt64); err != nil {
		t.Fatal(err)
	}
}

// #86: Java and C streams for (1, 2048, 3) holding 2048 decode in both
// decoders with the values Java and C report.
func TestDecodeForeignBoundaryStream(t *testing.T) {
	p, err := DecodePacked([]byte(foreignBoundary2048))
	if err != nil {
		t.Fatal(err)
	}
	if p.TotalCount() != 1 || p.Min() != 2048 || p.Max() != 2049 || p.CountAtValue(2048) != 1 {
		t.Fatalf("packed: total %d min %d max %d, want 1 2048 2049", p.TotalCount(), p.Min(), p.Max())
	}
	d, err := Decode([]byte(foreignBoundary2048))
	if err != nil {
		t.Fatal(err)
	}
	if d.TotalCount() != 1 || d.Min() != 2048 || d.Max() != 2049 {
		t.Fatalf("dense: total %d min %d max %d, want 1 2048 2049", d.TotalCount(), d.Min(), d.Max())
	}
}

// #90: a decoded source whose buckets sum past MaxInt64, merged into a full
// destination, must report a saturated dropped total, never 0.
func TestPackedMergeDroppedSaturates(t *testing.T) {
	maxCount := []byte{0xfe, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	payload := append(append(append([]byte{}, maxCount...), maxCount...), 4) // MaxInt64, MaxInt64, 2
	stream := buildPackedV2Stream(1, 10000, 3, int32(len(payload)), 0, 0, payload)
	full := func() *PackedHistogram {
		p := NewPacked(1, 10000, 3)
		_ = p.RecordValues(5, math.MaxInt64)
		return p
	}
	src, err := DecodePacked(stream)
	if err != nil {
		t.Fatal(err)
	}
	if d := full().Merge(src); d != math.MaxInt64 {
		t.Fatalf("Merge dropped %d, want saturated MaxInt64", d)
	}
	self, _ := DecodePacked(stream)
	if d := self.Merge(self); d != math.MaxInt64 {
		t.Fatalf("self Merge dropped %d, want saturated MaxInt64", d)
	}
	dense := New(1, 10000, 3)
	_ = dense.RecordValues(0, math.MaxInt64)
	_ = dense.RecordValues(1, math.MaxInt64)
	_ = dense.RecordValues(2, 2)
	if d := full().MergeFrom(dense); d != math.MaxInt64 {
		t.Fatalf("MergeFrom dropped %d, want saturated MaxInt64", d)
	}
	// MergeInto: buckets at indexes 4000-4002 (sum past MaxInt64) are all out
	// of a (1, 2048, 3) destination's 3072 counts, on the same-indexing path.
	farPayload := append(zig_zag_encode_i64(-4000), payload...)
	far, err := DecodePacked(buildPackedV2Stream(1, 10000, 3, int32(len(farPayload)), 0, 0, farPayload))
	if err != nil {
		t.Fatal(err)
	}
	narrow := New(1, 2048, 3)
	if !sameIndexing(far.geom, narrow) || int(far.idx[0]) < len(narrow.counts) {
		t.Fatal("setup: expected same indexing and out-of-range buckets")
	}
	if d := far.MergeInto(narrow); d != math.MaxInt64 {
		t.Fatalf("MergeInto dropped %d, want saturated MaxInt64", d)
	}
	if narrow.TotalCount() != 0 {
		t.Fatalf("MergeInto added %d to the destination", narrow.TotalCount())
	}
}
