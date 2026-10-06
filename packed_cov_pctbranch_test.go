package hdrhistogram

// Deep branch coverage for the sparse (packed) percentile read path:
//   PackedHistogram.countAtPercentile
//   PackedHistogram.valueFromIdxAtCount
//   PackedHistogram.ValueAtPercentile / ValueAtPercentilesSlice
//
// Every reachable branch of these functions is exercised with edge percentiles
// (NaN, +Inf, -Inf, negative, >100, exactly 0.0, exactly 100.0), the
// clamp-to-total path, an empty histogram, and a totalCount > 2^52 case. Where
// the dense *Histogram defines the same query the packed result is asserted
// bit-for-bit against it; where packed intentionally (or, for one case,
// unintentionally) diverges the exact packed behavior is pinned and the reason
// documented inline.
//
// All exported symbols in this file are prefixed TestCov_pctbranch; helpers use
// the cov_pctbranch_ prefix so this file compiles alongside the other coverage
// agents' files with no name collisions.

import (
	"errors"
	"math"
	"testing"
)

// cov_pctbranch_pair builds a dense Histogram and a PackedHistogram with the
// identical geometry and identical recordings, so their queries can be compared.
func cov_pctbranch_pair(t *testing.T, low, high int64, sig int, rec [][2]int64) (*Histogram, *PackedHistogram) {
	t.Helper()
	d := New(low, high, sig)
	p := NewPacked(low, high, sig)
	for _, r := range rec {
		if err := d.RecordValues(r[0], r[1]); err != nil {
			t.Fatalf("dense RecordValues(%d,%d): %v", r[0], r[1], err)
		}
		if err := p.RecordValues(r[0], r[1]); err != nil {
			t.Fatalf("packed RecordValues(%d,%d): %v", r[0], r[1], err)
		}
	}
	if d.TotalCount() != p.TotalCount() {
		t.Fatalf("total mismatch after build: dense=%d packed=%d", d.TotalCount(), p.TotalCount())
	}
	return d, p
}

// cov_pctbranch_inRange asserts v lies within [Min, Max] of a non-empty packed
// histogram (the value the percentile resolves to must be a real bucket value).
func cov_pctbranch_inRange(t *testing.T, p *PackedHistogram, pc float64, v int64) {
	t.Helper()
	if p.TotalCount() == 0 {
		return
	}
	if v < p.Min() || v > p.Max() {
		t.Errorf("pc=%v: packed value %d out of range [Min=%d, Max=%d]", pc, v, p.Min(), p.Max())
	}
}

// --- countAtPercentile: every branch, exact returns ---

func TestCov_pctbranch_countAtPercentile_branches(t *testing.T) {
	// total = 8 populated across a few buckets.
	_, p := cov_pctbranch_pair(t, 1, 100000, 3, [][2]int64{
		{5, 3}, {20, 2}, {300, 1}, {4000, 1}, {90000, 1},
	})
	total := p.TotalCount()
	if total != 8 {
		t.Fatalf("precondition: total=%d, want 8", total)
	}

	tests := []struct {
		name string
		pc   float64
		want int64
	}{
		// NaN: cc is NaN, !(cc>=1.0) is true -> floor to 1.
		{"NaN", math.NaN(), 1},
		// +Inf: req>100 clamps req=100, cc=total+0.5 >= total -> total.
		{"+Inf", math.Inf(1), total},
		// -Inf: cc = -Inf, !(cc>=1.0) true -> 1.
		{"-Inf", math.Inf(-1), 1},
		// negative finite: cc = -0.4+0.5 = 0.1 < 1 -> 1.
		{"neg", -5, 1},
		// exactly 0.0: cc = 0.5 < 1 -> 1.
		{"zero", 0.0, 1},
		// >100: req clamped to 100 -> cc>=total -> total.
		{"gt100", 150, total},
		// exactly 100.0: cc = total+0.5 >= total -> total (clamp-to-total path).
		{"hundred", 100.0, total},
		// mid: cc = 0.5*8+0.5 = 4.5, not >= 8 -> int64(4.5) = 4 (truncating cast).
		{"fifty", 50, 4},
		// low but reachable: 12.5% of 8 = 1.0+0.5 = 1.5 -> int64 = 1.
		{"twelvehalf", 12.5, 1},
		// just under 100: 99.99% * 8 = 7.9992+0.5 = 8.4992 >= 8 -> clamp to total.
		{"almost100", 99.99, total},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.countAtPercentile(tc.pc); got != tc.want {
				t.Errorf("countAtPercentile(%v) = %d, want %d", tc.pc, got, tc.want)
			}
		})
	}
}

// countAtPercentile on an EMPTY histogram: total=0 so cc = 0.5 -> !(>=1) -> 1
// for every percentile. (ValueAtPercentile short-circuits before this matters.)
func TestCov_pctbranch_countAtPercentile_empty(t *testing.T) {
	p := NewPacked(1, 100000, 3)
	if p.TotalCount() != 0 {
		t.Fatalf("precondition: expected empty histogram")
	}
	for _, pc := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1, 0, 50, 100, 150} {
		if got := p.countAtPercentile(pc); got != 1 {
			t.Errorf("empty countAtPercentile(%v) = %d, want 1", pc, got)
		}
	}
}

// --- ValueAtPercentile: dense parity on the well-defined range ---

func TestCov_pctbranch_ValueAtPercentile_denseParity(t *testing.T) {
	d, p := cov_pctbranch_pair(t, 1, 100000, 3, [][2]int64{
		{5, 3}, {20, 2}, {300, 1}, {4000, 1}, {90000, 1},
	})
	// Non-negative in-range percentiles are documented bit-for-bit dense.
	for _, pc := range []float64{0.0, 1, 12.5, 25, 37.5, 50, 62.5, 75, 90, 99, 99.99, 100} {
		dv := d.ValueAtPercentile(pc)
		pv := p.ValueAtPercentile(pc)
		if dv != pv {
			t.Errorf("pc=%v: packed=%d != dense=%d", pc, pv, dv)
		}
		cov_pctbranch_inRange(t, p, pc, pv)
	}
}

// exactly 0.0 -> lowestEquivalent branch -> equals Min() and dense.
func TestCov_pctbranch_ValueAtPercentile_zeroIsLowestEquivalent(t *testing.T) {
	// Coarse first bucket (low sig figs, large min value) so lowestEquivalent !=
	// highestEquivalent and the 0.0 branch is meaningfully distinct.
	d, p := cov_pctbranch_pair(t, 1, 100000000, 1, [][2]int64{
		{50000, 2}, {60000, 1}, {9000000, 1},
	})
	pv := p.ValueAtPercentile(0.0)
	if pv != p.Min() {
		t.Errorf("ValueAtPercentile(0.0)=%d, want Min()=%d", pv, p.Min())
	}
	if dv := d.ValueAtPercentile(0.0); dv != pv {
		t.Errorf("ValueAtPercentile(0.0): packed=%d != dense=%d", pv, dv)
	}
	// lowestEquivalent of the first populated bucket, spelled out.
	wantLE := p.geom.lowestEquivalentValue(p.geom.valueFromFlatIndex(p.idx[0]))
	if pv != wantLE {
		t.Errorf("ValueAtPercentile(0.0)=%d, want lowestEquivalent=%d", pv, wantLE)
	}
}

// exactly 100.0 -> highestEquivalent of the last populated bucket -> equals
// Max() and dense (for totalCount <= 2^52).
func TestCov_pctbranch_ValueAtPercentile_hundredIsMax(t *testing.T) {
	d, p := cov_pctbranch_pair(t, 1, 100000, 3, [][2]int64{
		{5, 3}, {20, 2}, {90000, 4},
	})
	pv := p.ValueAtPercentile(100.0)
	if pv != p.Max() {
		t.Errorf("ValueAtPercentile(100.0)=%d, want Max()=%d", pv, p.Max())
	}
	if dv := d.ValueAtPercentile(100.0); dv != pv {
		t.Errorf("ValueAtPercentile(100.0): packed=%d != dense=%d", pv, dv)
	}
	wantHE := p.highestEquivalent(p.geom.valueFromFlatIndex(p.idx[p.size-1]))
	if pv != wantHE {
		t.Errorf("ValueAtPercentile(100.0)=%d, want highestEquivalent=%d", pv, wantHE)
	}
}

// NaN and >100 both resolve through the clamp/floor paths and stay in range.
// NaN matches dense (both take the highestEquivalent branch off a count of 1);
// >100 clamps to 100 == Max.
func TestCov_pctbranch_ValueAtPercentile_nanAndOver(t *testing.T) {
	d, p := cov_pctbranch_pair(t, 1, 100000, 3, [][2]int64{
		{5, 3}, {20, 2}, {90000, 4},
	})
	if pv, dv := p.ValueAtPercentile(math.NaN()), d.ValueAtPercentile(math.NaN()); pv != dv {
		t.Errorf("NaN: packed=%d != dense=%d", pv, dv)
	} else {
		cov_pctbranch_inRange(t, p, math.NaN(), pv)
	}
	if pv, dv := p.ValueAtPercentile(150), d.ValueAtPercentile(150); pv != dv {
		t.Errorf(">100: packed=%d != dense=%d", pv, dv)
	} else if pv != p.Max() {
		t.Errorf(">100: packed=%d, want Max()=%d", pv, p.Max())
	}
	// +Inf is also clamped to 100 -> Max.
	if pv := p.ValueAtPercentile(math.Inf(1)); pv != p.Max() {
		t.Errorf("+Inf: packed=%d, want Max()=%d", pv, p.Max())
	}
}

// FIXED: a negative (finite or -Inf) percentile is now clamped to 0 by the packed
// path, matching dense.
//
// dense.ValueAtPercentile clamps percentile<0 to 0 and returns the 0th-percentile
// lowestEquivalent (== Min). packed.ValueAtPercentile now performs the same clamp
// (percentile < 0 -> 0) before dispatch, so for a negative input it takes the
// `percentile == 0.0` lowestEquivalent branch and returns Min() -- bit-for-bit
// equal to dense, matching dense's TestPercentiles_NegativeClampAcrossAPIs.
//
// The assertions below pin dense parity (lowestEquivalent == Min == dense) with a
// coarse first bucket, the exact geometry that previously exposed the divergence.
func TestCov_pctbranch_ValueAtPercentile_negativeMatchesDense(t *testing.T) {
	d, p := cov_pctbranch_pair(t, 1, 100000000, 1, [][2]int64{
		{50000, 2}, {60000, 1}, {9000000, 1},
	})
	firstVal := p.geom.valueFromFlatIndex(p.idx[0])
	wantLE := p.geom.lowestEquivalentValue(firstVal)
	// Coarse first bucket: highestEquivalent != lowestEquivalent, so returning the
	// clamped lowestEquivalent (not the first bucket's highestEquivalent) is a
	// meaningful, non-degenerate assertion.
	if p.highestEquivalent(firstVal) == wantLE {
		t.Fatalf("precondition: need a coarse first bucket (HE=%d LE=%d)", p.highestEquivalent(firstVal), wantLE)
	}
	for _, pc := range []float64{-5, -0.0001, math.Inf(-1)} {
		pv := p.ValueAtPercentile(pc)
		dv := d.ValueAtPercentile(pc)
		// Packed now clamps negative -> 0 -> lowestEquivalent == Min.
		if pv != wantLE || pv != p.Min() {
			t.Errorf("pc=%v: packed=%d, want lowestEquivalent=%d (Min=%d)", pc, pv, wantLE, p.Min())
		}
		// Dense clamps to 0th percentile == lowestEquivalent == Min.
		if dv != wantLE || dv != d.Min() {
			t.Errorf("pc=%v: dense=%d, want lowestEquivalent=%d (Min=%d)", pc, dv, wantLE, d.Min())
		}
		// Parity restored: packed == dense for negative percentiles now.
		if pv != dv {
			t.Errorf("pc=%v: expected packed(%d) to match dense(%d)", pc, pv, dv)
		}
		cov_pctbranch_inRange(t, p, pc, pv)
	}
	// -0.0 is treated as 0.0 by ==, so it takes the lowestEquivalent branch and
	// DOES match dense (negative sign bit does not make it < 0).
	if pv, dv := p.ValueAtPercentile(math.Copysign(0, -1)), d.ValueAtPercentile(math.Copysign(0, -1)); pv != dv || pv != p.Min() {
		t.Errorf("-0.0: packed=%d dense=%d, want Min()=%d", pv, dv, p.Min())
	}
}

// --- empty histogram: ValueAtPercentile short-circuits to 0 for every input ---

func TestCov_pctbranch_ValueAtPercentile_empty(t *testing.T) {
	p := NewPacked(1, 100000, 3)
	for _, pc := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -10, 0.0, 50, 100, 150} {
		if got := p.ValueAtPercentile(pc); got != 0 {
			t.Errorf("empty ValueAtPercentile(%v) = %d, want 0", pc, got)
		}
	}
	// Slice variant on empty: all zeros.
	out := p.ValueAtPercentilesSlice([]float64{0, 50, 100})
	for i, v := range out {
		if v != 0 {
			t.Errorf("empty slice[%d] = %d, want 0", i, v)
		}
	}
}

// --- totalCount > 2^52: clamp-to-total keeps p100 == Max where dense rounds off ---

func TestCov_pctbranch_ValueAtPercentile_hugeTotal(t *testing.T) {
	d, p := cov_pctbranch_pair(t, 1, 100000, 3, [][2]int64{
		{10, 5_000_000_000_000_000}, // 5e15 > 2^52 (4.503e15)
		{90000, 3},
	})
	if p.TotalCount() <= (1 << 52) {
		t.Fatalf("precondition: total=%d must exceed 2^52", p.TotalCount())
	}
	if p.CountWidth() != 8 {
		t.Errorf("expected 8-byte counts for a 5e15 bucket, got width %d", p.CountWidth())
	}

	// Low/mid percentiles still agree with dense (same float formula).
	for _, pc := range []float64{0.0, 25, 50, 90, 99.9} {
		if pv, dv := p.ValueAtPercentile(pc), d.ValueAtPercentile(pc); pv != dv {
			t.Errorf("hugeTotal pc=%v: packed=%d != dense=%d", pc, pv, dv)
		} else {
			cov_pctbranch_inRange(t, p, pc, pv)
		}
	}

	// p100: packed's clamp-to-total path returns Max. Dense, by contrast, rounds
	// its float target ABOVE the true total (5000000000000003 -> +0.5 rounds to
	// 5000000000000004 at float64 spacing 1 in [2^52,2^53)), never reaches it, and
	// returns 0. This is exactly the case packed.countAtPercentile's clamp exists
	// to fix ("clamping to totalCount so p100 == max holds"), so packed is the
	// correct one here.
	pv := p.ValueAtPercentile(100.0)
	if pv != p.Max() {
		t.Errorf("hugeTotal p100: packed=%d, want Max()=%d", pv, p.Max())
	}
	if dv := d.ValueAtPercentile(100.0); dv != 0 {
		t.Logf("note: dense p100 at total>2^52 returns %d (float-rounding limitation); packed correctly returns Max=%d", dv, pv)
	}
	// countAtPercentile takes the clamp-to-total branch and returns exactly total.
	if got := p.countAtPercentile(100.0); got != p.TotalCount() {
		t.Errorf("hugeTotal countAtPercentile(100)=%d, want total=%d", got, p.TotalCount())
	}
	cov_pctbranch_inRange(t, p, 100.0, pv)
}

// --- valueFromIdxAtCount: target reached, target unreachable (returns 0), and
// the running-sum saturation guard ---

func TestCov_pctbranch_valueFromIdxAtCount_reachAndMiss(t *testing.T) {
	_, p := cov_pctbranch_pair(t, 1, 100000, 3, [][2]int64{
		{5, 3}, {20, 2}, {90000, 1},
	})
	// target 1 -> first populated bucket's flat value.
	if got, want := p.valueFromIdxAtCount(1), p.geom.valueFromFlatIndex(p.idx[0]); got != want {
		t.Errorf("valueFromIdxAtCount(1)=%d, want %d", got, want)
	}
	// target == total -> last populated bucket's flat value.
	if got, want := p.valueFromIdxAtCount(p.TotalCount()), p.geom.valueFromFlatIndex(p.idx[p.size-1]); got != want {
		t.Errorf("valueFromIdxAtCount(total)=%d, want %d", got, want)
	}
	// target beyond total -> unreachable -> 0.
	if got := p.valueFromIdxAtCount(p.TotalCount() + 1); got != 0 {
		t.Errorf("valueFromIdxAtCount(total+1)=%d, want 0", got)
	}
	// empty histogram -> loop body never runs -> 0.
	empty := NewPacked(1, 100000, 3)
	if got := empty.valueFromIdxAtCount(1); got != 0 {
		t.Errorf("empty valueFromIdxAtCount(1)=%d, want 0", got)
	}
}

// Covers the `c > math.MaxInt64-running` saturation branch of valueFromIdxAtCount.
// This is a defensive guard that the public API cannot reach (RecordValues caps
// totalCount at MaxInt64, so the running sum can never overflow); it is driven
// here white-box via sparseAdd to place two near-MaxInt64 slot counts, forcing
// the second accumulation to saturate at MaxInt64 rather than wrap negative.
func TestCov_pctbranch_valueFromIdxAtCount_saturation(t *testing.T) {
	p := NewPacked(1, 100000, 3)
	ci1 := int32(p.geom.countsIndexFor(10))
	ci2 := int32(p.geom.countsIndexFor(90000))
	if ci1 >= ci2 {
		t.Fatalf("precondition: need ci1(%d) < ci2(%d)", ci1, ci2)
	}
	p.sparseAdd(ci1, math.MaxInt64-10) // running after slot 0 = MaxInt64-10 < target
	p.sparseAdd(ci2, 20)               // 20 > MaxInt64-(MaxInt64-10)=10 -> saturates
	if p.CountWidth() != 8 {
		t.Fatalf("precondition: expected 8-byte width, got %d", p.CountWidth())
	}
	// With target == MaxInt64, slot 0 alone (MaxInt64-10) does not reach it; the
	// second slot triggers the saturation branch and the running sum clamps to
	// MaxInt64 >= target, returning slot 1's flat value.
	got := p.valueFromIdxAtCount(math.MaxInt64)
	if want := p.geom.valueFromFlatIndex(ci2); got != want {
		t.Errorf("saturation valueFromIdxAtCount(MaxInt64)=%d, want %d", got, want)
	}
}

// --- ValueAtPercentilesSlice parity with per-call ValueAtPercentile ---

func TestCov_pctbranch_ValueAtPercentilesSlice_matchesSingular(t *testing.T) {
	_, p := cov_pctbranch_pair(t, 1, 100000, 3, [][2]int64{
		{5, 3}, {20, 2}, {300, 1}, {4000, 1}, {90000, 1},
	})
	pcs := []float64{0.0, 25, 50, 75, 90, 99, 100, 150, math.NaN()}
	out := p.ValueAtPercentilesSlice(pcs)
	if len(out) != len(pcs) {
		t.Fatalf("slice len=%d, want %d", len(out), len(pcs))
	}
	for i, pc := range pcs {
		if want := p.ValueAtPercentile(pc); out[i] != want {
			t.Errorf("slice[%d] (pc=%v) = %d, want %d", i, pc, out[i], want)
		}
	}
}

// --- sanity: RecordValues errors are the documented ones (used to build states
// above); confirms error wrapping so callers can errors.Is / test != nil ---

func TestCov_pctbranch_recordErrorsForContext(t *testing.T) {
	p := NewPacked(1, 100000, 3)
	if err := p.RecordValues(10, -1); err == nil {
		t.Errorf("expected error recording negative count")
	}
	if err := p.RecordValues(1<<62, 1); err == nil {
		t.Errorf("expected out-of-range error for oversized value")
	}
	// overflow guard: totalCount + n would exceed MaxInt64.
	if err := p.RecordValues(10, math.MaxInt64); err != nil {
		t.Fatalf("first large record should succeed: %v", err)
	}
	err := p.RecordValues(10, 1)
	if err == nil {
		t.Errorf("expected overflow error on second record")
	}
	// The errors are plain fmt.Errorf values (not sentinels); ensure non-nil and
	// that errors.Is against a nil target behaves (no wrapped sentinel expected).
	if err != nil && errors.Is(err, nil) {
		t.Errorf("unexpected errors.Is(err, nil) == true")
	}
}
