package hdrhistogram

// Deep coverage of PackedHistogram.valueFromIdxAtCount: the saturating running-sum
// add, the unreachable / empty return-0 path, and crossings at the first and last
// populated bucket. Whitebox (package-internal) so we can read unexported fields and
// call the geometry oracle directly for exact parity checks.
//
// All top-level test funcs are prefixed TestCov_scan and helpers cov_scan_ so this
// file compiles alongside the other coverage-agent files with no name collisions.

import (
	"math"
	"testing"
)

// cov_scan_twoBuckets builds a packed histogram with exactly two populated buckets,
// at ascending flat indices, with the given raw slot counts (bypassing the public
// RecordValues total-count overflow guard via sparseAdd). It returns the histogram
// and the two flat indices in scan order (ciLo < ciHi).
func cov_scan_twoBuckets(t *testing.T, vLo, vHi, cLo, cHi int64) (*PackedHistogram, int32, int32) {
	t.Helper()
	p := NewPacked(1, 100000, 3)
	ciLo := int32(p.geom.countsIndexFor(vLo))
	ciHi := int32(p.geom.countsIndexFor(vHi))
	if ciLo >= ciHi {
		t.Fatalf("cov_scan_twoBuckets: need ciLo < ciHi, got %d >= %d (vLo=%d vHi=%d)", ciLo, ciHi, vLo, vHi)
	}
	// Insert low bucket first, then high; sparseAdd keeps idx[] ascending regardless.
	p.sparseAdd(ciLo, cLo)
	p.sparseAdd(ciHi, cHi)
	if p.size != 2 {
		t.Fatalf("cov_scan_twoBuckets: expected size 2, got %d", p.size)
	}
	// Sanity: the slots hold exactly what we asked for (width may have widened).
	if got := p.slotGet(p.lowerBound(ciLo)); got != cLo {
		t.Fatalf("cov_scan_twoBuckets: low slot = %d, want %d", got, cLo)
	}
	if got := p.slotGet(p.lowerBound(ciHi)); got != cHi {
		t.Fatalf("cov_scan_twoBuckets: high slot = %d, want %d", got, cHi)
	}
	return p, ciLo, ciHi
}

// TestCov_scan_ValueFromIdxAtCount_EmptyUnreachable: an empty histogram (size 0) can
// never satisfy any positive target, so the scan loop never runs and returns 0.
func TestCov_scan_ValueFromIdxAtCount_EmptyUnreachable(t *testing.T) {
	p := NewPacked(1, 100000, 3)
	if p.size != 0 {
		t.Fatalf("fresh packed histogram should have size 0, got %d", p.size)
	}
	for _, target := range []int64{1, 2, 1000, math.MaxInt64} {
		if got := p.valueFromIdxAtCount(target); got != 0 {
			t.Errorf("valueFromIdxAtCount(%d) on empty = %d, want 0", target, got)
		}
	}
	// The public wrapper on an empty histogram short-circuits on totalCount==0.
	if got := p.ValueAtPercentile(50); got != 0 {
		t.Errorf("ValueAtPercentile(50) on empty = %d, want 0", got)
	}
}

// TestCov_scan_ValueFromIdxAtCount_CrossFirstBucket: with a positive count in the
// first populated bucket, any target <= that count crosses at the first bucket and
// returns the first bucket's flat value.
func TestCov_scan_ValueFromIdxAtCount_CrossFirstBucket(t *testing.T) {
	p, ciLo, ciHi := cov_scan_twoBuckets(t, 10, 5000, 7, 9)
	wantLo := p.geom.valueFromFlatIndex(ciLo)
	_ = ciHi
	for _, target := range []int64{1, 2, 7} { // all <= low count (7)
		if got := p.valueFromIdxAtCount(target); got != wantLo {
			t.Errorf("valueFromIdxAtCount(%d) = %d, want first-bucket value %d", target, got, wantLo)
		}
	}
}

// TestCov_scan_ValueFromIdxAtCount_CrossLastBucket: a target that exceeds the first
// bucket's count but is <= the running total crosses at the last populated bucket.
func TestCov_scan_ValueFromIdxAtCount_CrossLastBucket(t *testing.T) {
	p, ciLo, ciHi := cov_scan_twoBuckets(t, 10, 5000, 7, 9)
	_ = ciLo
	wantHi := p.geom.valueFromFlatIndex(ciHi)
	for _, target := range []int64{8, 10, 16} { // > 7, <= 7+9==16
		if got := p.valueFromIdxAtCount(target); got != wantHi {
			t.Errorf("valueFromIdxAtCount(%d) = %d, want last-bucket value %d", target, got, wantHi)
		}
	}
}

// TestCov_scan_ValueFromIdxAtCount_NonEmptyUnreachable: with a populated but finite
// running total and NO saturation, a target above the total is unreachable and the
// scan falls through the loop to return 0.
func TestCov_scan_ValueFromIdxAtCount_NonEmptyUnreachable(t *testing.T) {
	p, _, _ := cov_scan_twoBuckets(t, 10, 5000, 7, 9) // total 16, no saturation
	for _, target := range []int64{17, 100, math.MaxInt64} {
		if got := p.valueFromIdxAtCount(target); got != 0 {
			t.Errorf("valueFromIdxAtCount(%d) with total 16 = %d, want 0 (unreachable)", target, got)
		}
	}
}

// TestCov_scan_ValueFromIdxAtCount_SaturatingAdd: two buckets whose counts sum PAST
// math.MaxInt64. The first bucket's running total lands just below MaxInt64 so the
// second bucket's count triggers the saturating branch (c > MaxInt64-running), the
// running total pins to MaxInt64, and a MaxInt64 target crosses at the LAST bucket.
func TestCov_scan_ValueFromIdxAtCount_SaturatingAdd(t *testing.T) {
	const near = math.MaxInt64 - 5
	p, ciLo, ciHi := cov_scan_twoBuckets(t, 10, 5000, near, 10)
	if p.width != 8 {
		t.Fatalf("counts near MaxInt64 must widen to 8-byte slots, got width %d", p.width)
	}

	// Target == MaxInt64: first bucket (near) alone does NOT reach it, but the
	// saturating add pins running to MaxInt64 at the second bucket, which then
	// satisfies the target. Crossing is the last bucket.
	wantHi := p.geom.valueFromFlatIndex(ciHi)
	if got := p.valueFromIdxAtCount(math.MaxInt64); got != wantHi {
		t.Errorf("valueFromIdxAtCount(MaxInt64) with saturation = %d, want last-bucket value %d", got, wantHi)
	}

	// A target strictly inside the first bucket's own count still crosses at the
	// first bucket (no saturation needed to reach it): running=near >= target.
	wantLo := p.geom.valueFromFlatIndex(ciLo)
	if got := p.valueFromIdxAtCount(near); got != wantLo {
		t.Errorf("valueFromIdxAtCount(near) = %d, want first-bucket value %d", got, wantLo)
	}
	// One above the first bucket's count: not reached at bucket 0, reached (via the
	// saturating pin to MaxInt64) at bucket 1.
	if got := p.valueFromIdxAtCount(near + 1); got != wantHi {
		t.Errorf("valueFromIdxAtCount(near+1) = %d, want last-bucket value %d", got, wantHi)
	}
}

// TestCov_scan_ValueFromIdxAtCount_SaturateBothBucketsMax: an even more extreme case
// where BOTH buckets hold MaxInt64. The saturating branch fires on the second bucket
// (running is already MaxInt64 from the first), and every reachable target resolves.
func TestCov_scan_ValueFromIdxAtCount_SaturateBothBucketsMax(t *testing.T) {
	p, ciLo, ciHi := cov_scan_twoBuckets(t, 10, 5000, math.MaxInt64, math.MaxInt64)
	wantLo := p.geom.valueFromFlatIndex(ciLo)
	wantHi := p.geom.valueFromFlatIndex(ciHi)

	// First bucket already saturates the target: crossing at bucket 0.
	if got := p.valueFromIdxAtCount(math.MaxInt64); got != wantLo {
		t.Errorf("valueFromIdxAtCount(MaxInt64) both-max = %d, want first-bucket value %d", got, wantLo)
	}
	// A target only the (saturating) second bucket can matter for is impossible since
	// the first already pins to MaxInt64; confirm the low target still hits bucket 0.
	if got := p.valueFromIdxAtCount(1); got != wantLo {
		t.Errorf("valueFromIdxAtCount(1) both-max = %d, want first-bucket value %d", got, wantLo)
	}
	_ = wantHi // documented: second bucket is unreachable once bucket 0 saturates.
}

// TestCov_scan_ValueAtPercentile_ParityDense: for modest counts (no overflow, no
// saturation) the packed value-at-percentile must match the dense reference exactly,
// exercising valueFromIdxAtCount's normal crossing path against getValueFromIdxUpToCount.
func TestCov_scan_ValueAtPercentile_ParityDense(t *testing.T) {
	dense := New(1, 100000, 3)
	packed := NewPacked(1, 100000, 3)
	// A spread of values with varied multiplicities.
	samples := []struct{ v, n int64 }{
		{1, 3}, {2, 1}, {5, 10}, {37, 4}, {100, 25},
		{999, 2}, {4096, 7}, {50000, 1}, {99999, 6},
	}
	for _, s := range samples {
		if err := dense.RecordValues(s.v, s.n); err != nil {
			t.Fatalf("dense.RecordValues(%d,%d): %v", s.v, s.n, err)
		}
		if err := packed.RecordValues(s.v, s.n); err != nil {
			t.Fatalf("packed.RecordValues(%d,%d): %v", s.v, s.n, err)
		}
	}
	if dense.TotalCount() != packed.TotalCount() {
		t.Fatalf("total count mismatch: dense %d packed %d", dense.TotalCount(), packed.TotalCount())
	}
	percentiles := []float64{0, 0.5, 1, 10, 25, 50, 75, 90, 99, 99.9, 100, 150, -5}
	for _, pc := range percentiles {
		want := dense.ValueAtPercentile(pc)
		got := packed.ValueAtPercentile(pc)
		if got != want {
			t.Errorf("ValueAtPercentile(%g): packed %d != dense %d", pc, got, want)
		}
	}
}

// TestCov_scan_ValueAtPercentile_Zeroth exercises the percentile==0.0 wrapper branch
// (lowestEquivalentValue of the crossing index) distinctly from the highestEquivalent
// branch, and confirms parity with dense at the boundary.
func TestCov_scan_ValueAtPercentile_Zeroth(t *testing.T) {
	dense := New(1, 100000, 3)
	packed := NewPacked(1, 100000, 3)
	for _, v := range []int64{7, 7, 42, 42, 42, 9001} {
		if err := dense.RecordValue(v); err != nil {
			t.Fatal(err)
		}
		if err := packed.RecordValue(v); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := packed.ValueAtPercentile(0), dense.ValueAtPercentile(0); got != want {
		t.Errorf("ValueAtPercentile(0): packed %d != dense %d", got, want)
	}
	// p0 resolves to the lowest-equivalent of the first populated bucket == Min-ish.
	if got := packed.ValueAtPercentile(0); got != packed.geom.lowestEquivalentValue(packed.geom.valueFromFlatIndex(packed.idx[0])) {
		t.Errorf("ValueAtPercentile(0) = %d, not lowest-equivalent of first bucket", got)
	}
}
