package hdrhistogram

// Coverage-focused tests for the sparse PackedHistogram "plural / misc" query
// surface: ValueAtPercentilesSlice parity vs the singular ValueAtPercentile,
// Populated, CountWidth, GetMemorySize, and CountAtValue edge cases.
//
// All exported names are prefixed TestCov_pluralmisc and all helpers
// cov_pluralmisc_ so this file compiles alongside the other coverage agents'
// files with no collisions.

import (
	"math"
	"reflect"
	"testing"
)

// cov_pluralmisc_newPopulated builds a packed histogram over a fixed geometry
// and records the supplied (value,count) pairs. It returns the histogram so the
// caller can inspect unexported fields for exact parity checks.
func cov_pluralmisc_newPopulated(t *testing.T, pairs [][2]int64) *PackedHistogram {
	t.Helper()
	p := NewPacked(1, 3600*1000*1000, 3)
	for _, pc := range pairs {
		if err := p.RecordValues(pc[0], pc[1]); err != nil {
			t.Fatalf("RecordValues(%d,%d): %v", pc[0], pc[1], err)
		}
	}
	return p
}

// TestCov_pluralmisc_ValueAtPercentilesSliceParity asserts, for a battery of
// percentile-slice shapes (ordered, unordered, duplicate, single, length-0),
// that ValueAtPercentilesSlice returns exactly the element-wise application of
// the singular ValueAtPercentile, and that the output length always matches the
// input length. This is the documented contract of the plural helper.
func TestCov_pluralmisc_ValueAtPercentilesSliceParity(t *testing.T) {
	p := cov_pluralmisc_newPopulated(t, [][2]int64{
		{1, 100},
		{5, 40},
		{42, 25},
		{100, 10},
		{1000, 5},
		{250000, 3},
		{3000000, 1},
	})

	cases := []struct {
		name string
		pcts []float64
	}{
		{"ordered", []float64{0, 10, 25, 50, 75, 90, 99, 99.9, 100}},
		{"unordered", []float64{99, 50, 0, 100, 25, 90}},
		{"duplicate", []float64{50, 50, 50, 90, 90, 99, 99}},
		{"single", []float64{50}},
		{"boundaries", []float64{0, 100}},
		{"overshoot", []float64{100, 150, 250}}, // >100 clamps to 100 inside ValueAtPercentile
		{"lengthzero", []float64{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := p.ValueAtPercentilesSlice(tc.pcts)
			if len(got) != len(tc.pcts) {
				t.Fatalf("len(got)=%d want %d", len(got), len(tc.pcts))
			}
			want := make([]int64, len(tc.pcts))
			for i, pc := range tc.pcts {
				want[i] = p.ValueAtPercentile(pc)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("slice parity mismatch for %v:\n got=%v\nwant=%v", tc.pcts, got, want)
			}
		})
	}
}

// TestCov_pluralmisc_ValueAtPercentilesSliceLengthZero pins the length-0 branch:
// the result is a non-nil, empty slice (make([]int64, 0)), never nil.
func TestCov_pluralmisc_ValueAtPercentilesSliceLengthZero(t *testing.T) {
	p := cov_pluralmisc_newPopulated(t, [][2]int64{{7, 3}})
	got := p.ValueAtPercentilesSlice([]float64{})
	if got == nil {
		t.Fatalf("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
	// And for a nil input the loop runs zero times, still yielding an empty slice.
	gotNil := p.ValueAtPercentilesSlice(nil)
	if len(gotNil) != 0 {
		t.Fatalf("nil input: expected empty slice, got %v", gotNil)
	}
}

// TestCov_pluralmisc_ValueAtPercentilesSliceEmptyHist covers the empty-histogram
// path: totalCount==0 makes every ValueAtPercentile return 0, so the plural
// helper returns an all-zero slice of matching length.
func TestCov_pluralmisc_ValueAtPercentilesSliceEmptyHist(t *testing.T) {
	p := NewPacked(1, 100000, 3)
	if p.TotalCount() != 0 {
		t.Fatalf("fresh histogram TotalCount=%d want 0", p.TotalCount())
	}
	pcts := []float64{0, 25, 50, 99, 100}
	got := p.ValueAtPercentilesSlice(pcts)
	want := []int64{0, 0, 0, 0, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("empty hist slice = %v want %v", got, want)
	}
	// Singular agrees element-wise.
	for i, pc := range pcts {
		if v := p.ValueAtPercentile(pc); v != got[i] {
			t.Fatalf("singular/plural disagree at empty pc=%v: %d vs %d", pc, v, got[i])
		}
	}
}

// TestCov_pluralmisc_ValueAtPercentileZeroBranch exercises the percentile==0.0
// branch of ValueAtPercentile (lowestEquivalentValue) distinctly from p>0
// (highestEquivalent), and confirms the plural helper threads both branches.
func TestCov_pluralmisc_ValueAtPercentileZeroBranch(t *testing.T) {
	p := cov_pluralmisc_newPopulated(t, [][2]int64{{10, 5}, {500, 5}, {9000, 5}})
	// p0 uses lowestEquivalentValue of the first populated bucket's value.
	p0 := p.ValueAtPercentile(0.0)
	if p0 > p.Min() {
		// p0 is the lowest-equivalent of the bucket the running count first hits;
		// it must not exceed Min (lowest-equivalent of the first populated bucket).
		t.Fatalf("p0=%d should be <= Min=%d", p0, p.Min())
	}
	// p100 must equal the packed Max (clamp-to-total then highestEquivalent).
	if p100 := p.ValueAtPercentile(100.0); p100 != p.Max() {
		t.Fatalf("p100=%d want Max=%d", p100, p.Max())
	}
	// Plural threads both.
	slice := p.ValueAtPercentilesSlice([]float64{0.0, 100.0})
	if slice[0] != p0 || slice[1] != p.Max() {
		t.Fatalf("plural mismatch: %v (p0=%d max=%d)", slice, p0, p.Max())
	}
}

// TestCov_pluralmisc_PopulatedAndCountWidth tracks Populated (count of distinct
// occupied buckets) and CountWidth (adaptive byte width) as records arrive.
func TestCov_pluralmisc_PopulatedAndCountWidth(t *testing.T) {
	p := NewPacked(1, 1<<40, 3)

	// Fresh: no buckets, width starts at 1 byte.
	if got := p.Populated(); got != 0 {
		t.Fatalf("fresh Populated=%d want 0", got)
	}
	if got := p.CountWidth(); got != 1 {
		t.Fatalf("fresh CountWidth=%d want 1", got)
	}

	// One value -> one populated bucket, still width 1 (count fits in a byte).
	if err := p.RecordValues(123, 1); err != nil {
		t.Fatal(err)
	}
	if got := p.Populated(); got != 1 {
		t.Fatalf("Populated=%d want 1", got)
	}
	if got := p.CountWidth(); got != 1 {
		t.Fatalf("CountWidth=%d want 1", got)
	}

	// Recording the same value again must NOT add a bucket.
	if err := p.RecordValues(123, 1); err != nil {
		t.Fatal(err)
	}
	if got := p.Populated(); got != 1 {
		t.Fatalf("after dup Populated=%d want 1", got)
	}

	// A distinct value adds a second bucket.
	if err := p.RecordValues(9999, 1); err != nil {
		t.Fatal(err)
	}
	if got := p.Populated(); got != 2 {
		t.Fatalf("Populated=%d want 2", got)
	}

	// Drive a count over 0xFF to force a widen to 2 bytes.
	if err := p.RecordValues(123, 0x1000); err != nil {
		t.Fatal(err)
	}
	if got := p.CountWidth(); got != 2 {
		t.Fatalf("after >255 count CountWidth=%d want 2", got)
	}
	// Populated unchanged by the widen (same bucket, just a wider slot).
	if got := p.Populated(); got != 2 {
		t.Fatalf("after widen Populated=%d want 2", got)
	}

	// Drive over 0xFFFF -> width 4.
	if err := p.RecordValues(123, 0x20000); err != nil {
		t.Fatal(err)
	}
	if got := p.CountWidth(); got != 4 {
		t.Fatalf("CountWidth=%d want 4", got)
	}

	// Drive over 0xFFFFFFFF -> width 8.
	if err := p.RecordValues(123, 0x100000000); err != nil {
		t.Fatal(err)
	}
	if got := p.CountWidth(); got != 8 {
		t.Fatalf("CountWidth=%d want 8", got)
	}
	// The widened count must round-trip exactly through CountAtValue.
	wantCount := int64(1 + 1 + 0x1000 + 0x20000 + 0x100000000)
	if got := p.CountAtValue(123); got != wantCount {
		t.Fatalf("CountAtValue(123)=%d want %d", got, wantCount)
	}
}

// TestCov_pluralmisc_GetMemorySize checks GetMemorySize is the fixed overhead on
// a fresh histogram (no backing allocated) and grows once at least one bucket is
// populated, and that it is consistent with the idx/cnt capacities.
func TestCov_pluralmisc_GetMemorySize(t *testing.T) {
	p := NewPacked(1, 100000, 3)
	if got := p.GetMemorySize(); got != packedFixedSize {
		t.Fatalf("fresh GetMemorySize=%d want %d", got, packedFixedSize)
	}
	if err := p.RecordValue(42); err != nil {
		t.Fatal(err)
	}
	got := p.GetMemorySize()
	if got <= packedFixedSize {
		t.Fatalf("populated GetMemorySize=%d want >%d", got, packedFixedSize)
	}
	// Must equal fixed + cap(idx)*4 + cap(cnt) exactly (white-box, package-internal).
	want := packedFixedSize + cap(p.idx)*4 + cap(p.cnt)
	if got != want {
		t.Fatalf("GetMemorySize=%d want cap-derived %d", got, want)
	}
	// Adding more distinct buckets never shrinks the footprint.
	prev := got
	for v := int64(1); v < 200; v++ {
		if err := p.RecordValue(v * 7); err != nil {
			t.Fatal(err)
		}
	}
	if now := p.GetMemorySize(); now < prev {
		t.Fatalf("GetMemorySize shrank: %d < %d", now, prev)
	}
}

// TestCov_pluralmisc_CountAtValueOutOfRange covers both out-of-range guards of
// CountAtValue: a negative value and a value beyond the last bucket both
// return 0 without touching the backing store.
func TestCov_pluralmisc_CountAtValueOutOfRange(t *testing.T) {
	high := int64(100000)
	p := NewPacked(1, high, 3)
	if err := p.RecordValues(50, 3); err != nil {
		t.Fatal(err)
	}
	if err := p.RecordValues(5000, 2); err != nil {
		t.Fatal(err)
	}

	if got := p.CountAtValue(-1); got != 0 {
		t.Fatalf("CountAtValue(-1)=%d want 0", got)
	}
	if got := p.CountAtValue(math.MinInt64); got != 0 {
		t.Fatalf("CountAtValue(MinInt64)=%d want 0", got)
	}
	// Above highestTrackableValue (unrecorded) and beyond the last bucket.
	if got := p.CountAtValue(high + 1); got != 0 {
		t.Fatalf("CountAtValue(high+1)=%d want 0", got)
	}
	if got := p.CountAtValue(math.MaxInt64); got != 0 {
		t.Fatalf("CountAtValue(MaxInt64)=%d want 0", got)
	}
	// Sanity: geometry high bound is what we expect.
	if p.geom.highestTrackableValue != high {
		t.Fatalf("test setup: highestTrackableValue=%d want %d", p.geom.highestTrackableValue, high)
	}
}

// TestCov_pluralmisc_CountAtValueInRangeNeverRecorded covers the in-range but
// never-populated branch of CountAtValue: the lowerBound lands on a bucket that
// either doesn't exist or holds a different flat index, so 0 is returned. It
// also confirms a recorded value reads back its exact count.
func TestCov_pluralmisc_CountAtValueInRangeNeverRecorded(t *testing.T) {
	p := cov_pluralmisc_newPopulated(t, [][2]int64{{50, 3}, {5000, 7}})

	// In-range value below the smallest populated bucket -> lowerBound==0 but
	// idx[0] != ci -> 0.
	if got := p.CountAtValue(1); got != 0 {
		t.Fatalf("CountAtValue(1) never-recorded=%d want 0", got)
	}
	// In-range value between the two populated buckets -> 0.
	if got := p.CountAtValue(500); got != 0 {
		t.Fatalf("CountAtValue(500) never-recorded=%d want 0", got)
	}
	// In-range value above the largest populated bucket but within range ->
	// lowerBound==size -> 0.
	if got := p.CountAtValue(90000); got != 0 {
		t.Fatalf("CountAtValue(90000) never-recorded=%d want 0", got)
	}
	// Recorded values read back exactly.
	if got := p.CountAtValue(50); got != 3 {
		t.Fatalf("CountAtValue(50)=%d want 3", got)
	}
	if got := p.CountAtValue(5000); got != 7 {
		t.Fatalf("CountAtValue(5000)=%d want 7", got)
	}
	// A value in the SAME bucket as a recorded value shares its count.
	// countsIndexFor(50) == countsIndexFor of any value in 50's equivalent range.
	ci50 := p.geom.countsIndexFor(50)
	for v := int64(0); v <= 200; v++ {
		if p.geom.countsIndexFor(v) == ci50 {
			if got := p.CountAtValue(v); got != 3 {
				t.Fatalf("CountAtValue(%d) in bucket of 50 = %d want 3", v, got)
			}
		}
	}
}

// TestCov_pluralmisc_ValueAtZeroValueRecorded exercises RecordValue(0): the min
// guard skips 0, so CountAtValue(0) is still readable and Populated reflects the
// zero bucket. This keeps the CountAtValue v==0 in-range boundary covered.
func TestCov_pluralmisc_CountAtValueZero(t *testing.T) {
	p := NewPacked(1, 100000, 3)
	if err := p.RecordValues(0, 4); err != nil {
		t.Fatal(err)
	}
	if got := p.CountAtValue(0); got != 4 {
		t.Fatalf("CountAtValue(0)=%d want 4", got)
	}
	if got := p.Populated(); got != 1 {
		t.Fatalf("Populated=%d want 1", got)
	}
}
