package hdrhistogram

// Deep coverage for PackedHistogram.Min / PackedHistogram.Max and the
// overflow-safe PackedHistogram.highestEquivalent helper they rely on.
//
// The dense *Histogram Min()/Max() are the parity oracle wherever the dense
// result is well-defined (i.e. no signed overflow). The one place the packed
// path intentionally diverges is the very top bucket of a MaxInt64-tracking
// histogram, where highestEquivalent must SATURATE at MaxInt64 rather than let
// leq+size overflow int64. That branch is exercised explicitly.
//
// All exported names are prefixed TestCov_minmax and all helpers cov_minmax_
// so this file can compile alongside the other coverage files.

import (
	"math"
	"testing"
)

// cov_minmax_recordAll records the same values into a fresh packed and dense
// histogram built with identical geometry and returns both.
func cov_minmax_recordAll(t *testing.T, ldv, htv int64, sig int, values []int64) (*PackedHistogram, *Histogram) {
	t.Helper()
	p := NewPacked(ldv, htv, sig)
	d := New(ldv, htv, sig)
	for _, v := range values {
		if err := p.RecordValue(v); err != nil {
			t.Fatalf("packed RecordValue(%d): %v", v, err)
		}
		if err := d.RecordValue(v); err != nil {
			t.Fatalf("dense RecordValue(%d): %v", v, err)
		}
	}
	return p, d
}

// cov_minmax_assertParity asserts packed Min/Max exactly match the dense oracle.
func cov_minmax_assertParity(t *testing.T, name string, p *PackedHistogram, d *Histogram) {
	t.Helper()
	if got, want := p.Min(), d.Min(); got != want {
		t.Fatalf("%s: Min packed=%d dense=%d", name, got, want)
	}
	if got, want := p.Max(), d.Max(); got != want {
		t.Fatalf("%s: Max packed=%d dense=%d", name, got, want)
	}
}

// TestCov_minmaxEmptyParity covers the size==0 branch of both Min and Max and
// checks parity with the dense empty histogram across several geometries. With
// ldv=1 the equivalent range of 0 has size 1 so empty Max==0; with a larger ldv
// the range widens and empty Max is > 0 (normal, non-saturating highestEquivalent).
func TestCov_minmaxEmptyParity(t *testing.T) {
	cases := []struct {
		name       string
		ldv, htv   int64
		sig        int
		wantMax    int64 // documented empty-Max value
		wantMaxPos bool  // whether we expect a strictly positive empty Max
	}{
		{"unit-res", 1, 3600000000, 3, 0, false},
		{"coarse-res", 1000, 3600000000, 3, 511, true},
		{"maxint64-track", 1, math.MaxInt64, 3, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPacked(tc.ldv, tc.htv, tc.sig)
			d := New(tc.ldv, tc.htv, tc.sig)

			if p.Populated() != 0 {
				t.Fatalf("expected 0 populated buckets, got %d", p.Populated())
			}
			// Min on an empty histogram is lowestEquivalentValue(0) == 0.
			if got := p.Min(); got != 0 {
				t.Fatalf("empty Min packed=%d, want 0", got)
			}
			// Max on empty is highestEquivalent(0).
			if got := p.Max(); got != tc.wantMax {
				t.Fatalf("empty Max packed=%d, want %d", got, tc.wantMax)
			}
			if tc.wantMaxPos && p.Max() <= 0 {
				t.Fatalf("expected strictly positive empty Max, got %d", p.Max())
			}
			// Parity with dense empty histogram.
			cov_minmax_assertParity(t, "empty-"+tc.name, p, d)
			// Empty Max must equal the explicit highestEquivalent(0) call.
			if p.Max() != p.highestEquivalent(0) {
				t.Fatalf("empty Max %d != highestEquivalent(0) %d", p.Max(), p.highestEquivalent(0))
			}
		})
	}
}

// TestCov_minmaxOnlyZero covers recording only the value 0. Min()/Max() derive
// from idx[0], so Min()==0 (bucket 0 is populated) and Max()==highestEquivalent(0),
// matching the dense oracle.
func TestCov_minmaxOnlyZero(t *testing.T) {
	p, d := cov_minmax_recordAll(t, 1, 100000, 3, []int64{0})

	if p.Populated() != 1 {
		t.Fatalf("expected 1 populated bucket, got %d", p.Populated())
	}
	if got := p.Min(); got != 0 {
		t.Fatalf("Min after recording only 0 = %d, want 0", got)
	}
	// Max is highestEquivalent(0), same as dense.
	if got, want := p.Max(), p.highestEquivalent(0); got != want {
		t.Fatalf("Max=%d, want highestEquivalent(0)=%d", got, want)
	}
	cov_minmax_assertParity(t, "only-zero", p, d)

	// Recording 0 alongside a larger value keeps Min at 0 (bucket 0 is first).
	p2, d2 := cov_minmax_recordAll(t, 1, 100000, 3, []int64{0, 5000})
	if got := p2.Min(); got != 0 {
		t.Fatalf("Min with {0,5000} = %d, want 0", got)
	}
	cov_minmax_assertParity(t, "zero-plus-large", p2, d2)
}

// TestCov_minmaxSingleBucket covers a single populated non-zero bucket across a
// range of values and geometries, asserting full parity with dense. This hits
// the size!=0 branch of both Min and Max and the normal (non-saturating) branch
// of highestEquivalent.
func TestCov_minmaxSingleBucket(t *testing.T) {
	values := []int64{1, 2, 42, 1000, 999999, 123456789, 1 << 40}
	for _, v := range values {
		p, d := cov_minmax_recordAll(t, 1, math.MaxInt64, 3, []int64{v})
		if p.Populated() != 1 {
			t.Fatalf("v=%d expected 1 populated bucket, got %d", v, p.Populated())
		}
		// Min == lowestEquivalent of the bucket; Max == highestEquivalent.
		if got, want := p.Min(), p.geom.lowestEquivalentValue(v); got != want {
			t.Fatalf("v=%d Min=%d, want lowestEquivalent=%d", v, got, want)
		}
		// For non-overflow buckets Min <= v <= Max must hold.
		if p.Min() > v || p.Max() < v {
			t.Fatalf("v=%d not bracketed: Min=%d Max=%d", v, p.Min(), p.Max())
		}
		cov_minmax_assertParity(t, "single", p, d)
	}
}

// TestCov_minmaxMultiBucket covers many populated buckets, including insertion
// out of ascending order (idx[] is kept sorted so Min uses idx[0] and Max uses
// idx[size-1] regardless of record order). Full parity with dense.
func TestCov_minmaxMultiBucket(t *testing.T) {
	cases := []struct {
		name   string
		values []int64
	}{
		{"ascending", []int64{5, 42, 1000, 999999, 123456789}},
		{"descending", []int64{123456789, 999999, 1000, 42, 5}},
		{"shuffled", []int64{1000, 123456789, 5, 999999, 42}},
		{"with-zero", []int64{0, 5, 42, 1000, 999999, 123456789}},
		{"dupes", []int64{7, 7, 7, 4242, 4242, 100000000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, d := cov_minmax_recordAll(t, 1, 3600000000, 3, tc.values)
			if p.Populated() < 1 {
				t.Fatalf("%s: expected populated buckets", tc.name)
			}
			// Min/Max independent of record order. Min derives from the SMALLEST
			// populated flat index, which includes a recorded 0 (bucket 0), so the
			// expected floor is the true minimum over all recorded values.
			var lo, hi int64 = math.MaxInt64, 0
			for _, v := range tc.values {
				if v < lo {
					lo = v
				}
				if v > hi {
					hi = v
				}
			}
			if got, want := p.Min(), p.geom.lowestEquivalentValue(lo); got != want {
				t.Fatalf("%s: Min=%d want %d", tc.name, got, want)
			}
			if p.Max() < hi {
				t.Fatalf("%s: Max=%d < recorded max %d", tc.name, p.Max(), hi)
			}
			cov_minmax_assertParity(t, tc.name, p, d)
		})
	}
}

// TestCov_minmaxTopBucketSaturates covers the overflow-safe saturating branch of
// highestEquivalent: for the very top bucket of a MaxInt64-tracking histogram,
// leq+size overflows int64, so highestEquivalent must return MaxInt64 without
// signed overflow. We assert the saturating branch condition actually holds and
// that Max()==MaxInt64.
func TestCov_minmaxTopBucketSaturates(t *testing.T) {
	p := NewPacked(1, math.MaxInt64, 3)
	d := New(1, math.MaxInt64, 3)

	if err := p.RecordValue(math.MaxInt64); err != nil {
		t.Fatalf("RecordValue(MaxInt64): %v", err)
	}
	if err := d.RecordValue(math.MaxInt64); err != nil {
		t.Fatalf("dense RecordValue(MaxInt64): %v", err)
	}

	// The top bucket's flat index must be the last valid index.
	topCi := p.geom.countsIndexFor(math.MaxInt64)
	if int32(topCi) != p.geom.countsLen-1 {
		t.Fatalf("expected top ci == countsLen-1 (%d), got %d", p.geom.countsLen-1, topCi)
	}

	topVal := p.geom.valueFromFlatIndex(int32(topCi))
	leq := p.geom.lowestEquivalentValue(topVal)
	size := p.geom.sizeOfEquivalentValueRange(topVal)

	// Prove the SATURATING branch is the one taken: leq + size would overflow.
	if leq <= math.MaxInt64-size {
		t.Fatalf("expected saturating condition leq(%d) > MaxInt64-size(%d)", leq, math.MaxInt64-size)
	}
	if got := p.highestEquivalent(topVal); got != math.MaxInt64 {
		t.Fatalf("highestEquivalent(top)=%d, want MaxInt64 (saturated)", got)
	}
	if got := p.Max(); got != math.MaxInt64 {
		t.Fatalf("Max()=%d, want MaxInt64", got)
	}
	// Min of a single top-bucket entry is that bucket's lowest-equivalent.
	if got := p.Min(); got != leq {
		t.Fatalf("Min()=%d, want lowestEquivalent(top)=%d", got, leq)
	}
	// Dense reaches the same MaxInt64 here (its leq+size double-wrap coincides),
	// so parity still holds at the very top even though packed gets there safely.
	if got, want := p.Max(), d.Max(); got != want {
		t.Fatalf("top-bucket Max packed=%d dense=%d (expected coincident)", got, want)
	}
	if got, want := p.Min(), d.Min(); got != want {
		t.Fatalf("top-bucket Min packed=%d dense=%d", got, want)
	}
}

// TestCov_minmaxNormalHighestEquivalent covers the NON-saturating branch of
// highestEquivalent for a scattering of top-region flat indices: each must equal
// dense highestEquivalentValue and must not saturate (leq+size does not overflow).
func TestCov_minmaxNormalHighestEquivalent(t *testing.T) {
	p := NewPacked(1, math.MaxInt64, 3)
	d := New(1, math.MaxInt64, 3)
	// A spread of flat indices below the final overflowing one.
	for _, ci := range []int32{0, 1, 512, 30000, p.geom.countsLen - 2} {
		v := p.geom.valueFromFlatIndex(ci)
		leq := p.geom.lowestEquivalentValue(v)
		size := p.geom.sizeOfEquivalentValueRange(v)
		if leq > math.MaxInt64-size {
			// Skip any index that legitimately saturates; that's covered elsewhere.
			continue
		}
		if got, want := p.highestEquivalent(v), d.highestEquivalentValue(v); got != want {
			t.Fatalf("ci=%d highestEquivalent packed=%d dense=%d", ci, got, want)
		}
	}
}
