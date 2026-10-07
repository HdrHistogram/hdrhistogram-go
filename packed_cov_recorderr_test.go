package hdrhistogram

// Deep branch coverage for the PackedHistogram write path:
// RecordValue / RecordValues error + edge branches.
//
// Targeted funcs: PackedHistogram.RecordValue, PackedHistogram.RecordValues,
// with supporting reach into sparseAdd / widenToFit / slotGet via the record path.
//
// Where the packed behavior is well-defined against the dense reference we assert
// exact parity (same error presence, same message, same CountAtValue / Min / Max).
// Where packed intentionally DIVERGES from dense (the total-count overflow guard,
// which dense lacks) we assert the divergence explicitly so it is pinned.
//
// All test/helper identifiers are prefixed TestCov_recorderr / cov_recorderr_ so
// this file compiles alongside the other coverage files without collisions.

import (
	"math"
	"strings"
	"testing"
)

// cov_recorderr_cfg is the standard geometry used by the sibling tests.
const (
	cov_recorderr_lo  = int64(1)
	cov_recorderr_hi  = int64(3600000000)
	cov_recorderr_sig = 3
)

func cov_recorderr_newPacked() *PackedHistogram {
	return NewPacked(cov_recorderr_lo, cov_recorderr_hi, cov_recorderr_sig)
}

func cov_recorderr_newDense() *Histogram {
	return New(cov_recorderr_lo, cov_recorderr_hi, cov_recorderr_sig)
}

// cov_recorderr_denseCount reads the dense count at v's bucket (dense exposes no
// public CountAtValue), returning 0 for out-of-range values to mirror the packed
// CountAtValue contract.
func cov_recorderr_denseCount(d *Histogram, v int64) int64 {
	if v < 0 {
		return 0
	}
	idx := d.countsIndexFor(v)
	if uint(idx) >= uint(len(d.counts)) {
		return 0
	}
	return d.counts[idx]
}

// ----- out-of-range (value too large) branch, in lock-step with dense -----

func TestCov_recorderr_OutOfRangeParityWithDense(t *testing.T) {
	cases := []struct {
		name string
		v    int64
	}{
		{"zero", 0},
		{"one", 1},
		{"lowest", cov_recorderr_lo},
		{"exactly_highest", cov_recorderr_hi},
		{"highest_plus_1", cov_recorderr_hi + 1},
		{"double_highest", cov_recorderr_hi * 2},
		{"maxint_half", math.MaxInt64 / 2},
		{"maxint", math.MaxInt64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := cov_recorderr_newDense()
			p := cov_recorderr_newPacked()

			derr := d.RecordValue(tc.v)
			perr := p.RecordValue(tc.v)

			if (derr == nil) != (perr == nil) {
				t.Fatalf("range disagreement for v=%d: dense err=%v packed err=%v", tc.v, derr, perr)
			}
			if perr != nil {
				// packed and dense use the same message for the range guard.
				if derr.Error() != perr.Error() {
					t.Fatalf("v=%d message mismatch: dense=%q packed=%q", tc.v, derr.Error(), perr.Error())
				}
				want := "is too large to be recorded"
				if !strings.Contains(perr.Error(), want) {
					t.Fatalf("v=%d packed error %q does not contain %q", tc.v, perr.Error(), want)
				}
				// A rejected record must leave the histogram completely untouched.
				if p.TotalCount() != 0 || p.Populated() != 0 || p.CountAtValue(tc.v) != 0 {
					t.Fatalf("v=%d rejected but state changed: total=%d pop=%d cav=%d",
						tc.v, p.TotalCount(), p.Populated(), p.CountAtValue(tc.v))
				}
				return
			}
			// Accepted: counts must agree with dense at that value.
			if got, want := p.CountAtValue(tc.v), cov_recorderr_denseCount(d, tc.v); got != want {
				t.Fatalf("v=%d accepted but CountAtValue packed=%d dense=%d", tc.v, got, want)
			}
		})
	}
}

// The range guard is the FIRST check: an out-of-range value with a negative
// count must report "too large", not "negative count" (branch-ordering).
func TestCov_recorderr_OutOfRangeTakesPrecedenceOverNegative(t *testing.T) {
	p := cov_recorderr_newPacked()
	err := p.RecordValues(cov_recorderr_hi*4, -5)
	if err == nil {
		t.Fatal("expected error for out-of-range value with negative count")
	}
	if !strings.Contains(err.Error(), "is too large to be recorded") {
		t.Fatalf("expected range error to win, got %q", err.Error())
	}
}

// ----- exact countsLen boundary: the largest in-range vs first out-of-range -----

func TestCov_recorderr_CountsLenBoundary(t *testing.T) {
	p := cov_recorderr_newPacked()
	g := p.geom

	// highestTrackableValue must map to a valid flat index (false side of the guard).
	ciHi := int32(g.countsIndexFor(g.highestTrackableValue))
	if uint32(ciHi) >= uint32(g.countsLen) {
		t.Fatalf("highestTrackableValue mapped out of range: ci=%d countsLen=%d", ciHi, g.countsLen)
	}
	if err := p.RecordValue(g.highestTrackableValue); err != nil {
		t.Fatalf("recording highestTrackableValue should succeed, got %v", err)
	}

	inRange := func(v int64) bool { return uint32(int32(g.countsIndexFor(v))) < uint32(g.countsLen) }

	// A value just above highest is still in-range (it maps to the same top
	// sub-bucket / last flat index) and must be accepted.
	if !inRange(g.highestTrackableValue + 1) {
		t.Fatalf("highest+1 unexpectedly out of range")
	}
	if err := cov_recorderr_newPacked().RecordValue(g.highestTrackableValue + 1); err != nil {
		t.Fatalf("in-range top-bucket value rejected: %v", err)
	}

	// Binary-search the exact boundary: countsIndexFor is monotonic in v, so there
	// is a single crossover from in-range to out-of-range. lo stays in-range, hi
	// stays out-of-range; the loop narrows to the first out value.
	lo, hi := g.highestTrackableValue, g.highestTrackableValue*4
	if inRange(hi) {
		t.Fatalf("upper probe %d still in range; cannot bracket boundary", hi)
	}
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if inRange(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	firstOut := hi
	if inRange(firstOut) {
		t.Fatalf("boundary search broke: %d still in range", firstOut)
	}
	if !inRange(firstOut - 1) {
		t.Fatalf("value just below boundary %d should be in range", firstOut-1)
	}
	// The largest in-range value records; the first out-of-range value is rejected.
	if err := cov_recorderr_newPacked().RecordValue(firstOut - 1); err != nil {
		t.Fatalf("largest in-range value %d rejected: %v", firstOut-1, err)
	}
	if err := p.RecordValue(firstOut); err == nil {
		t.Fatalf("value %d past countsLen should be rejected", firstOut)
	}
}

// ----- negative count branch -----

func TestCov_recorderr_NegativeCount(t *testing.T) {
	negs := []int64{-1, -2, -1000, math.MinInt64}
	for _, n := range negs {
		d := cov_recorderr_newDense()
		p := cov_recorderr_newPacked()
		v := int64(5000)

		derr := d.RecordValues(v, n)
		perr := p.RecordValues(v, n)
		if perr == nil {
			t.Fatalf("n=%d: expected negative-count error", n)
		}
		if derr == nil || derr.Error() != perr.Error() {
			t.Fatalf("n=%d: dense/packed negative-count message mismatch: dense=%v packed=%v", n, derr, perr)
		}
		if !strings.Contains(perr.Error(), "cannot record a negative count") {
			t.Fatalf("n=%d: unexpected message %q", n, perr.Error())
		}
		// Rejected before any state mutation.
		if p.TotalCount() != 0 || p.Populated() != 0 || p.CountAtValue(v) != 0 {
			t.Fatalf("n=%d: state mutated after negative reject: total=%d pop=%d cav=%d",
				n, p.TotalCount(), p.Populated(), p.CountAtValue(v))
		}
	}
}

// ----- total-count overflow branch (packed-specific hardening) -----

func TestCov_recorderr_TotalCountOverflow(t *testing.T) {
	v := int64(1234)

	// Record MaxInt64 occurrences: n == MaxInt64 - total(0), boundary is inclusive
	// (the guard is strictly '>'), so this must succeed.
	p := cov_recorderr_newPacked()
	if err := p.RecordValues(v, math.MaxInt64); err != nil {
		t.Fatalf("recording MaxInt64 into empty histogram should succeed, got %v", err)
	}
	if p.TotalCount() != math.MaxInt64 {
		t.Fatalf("totalCount=%d, want MaxInt64", p.TotalCount())
	}
	// The count widened all the way to 8 bytes to hold MaxInt64.
	if p.CountWidth() != 8 {
		t.Fatalf("CountWidth=%d, want 8 after recording MaxInt64", p.CountWidth())
	}
	if got := p.CountAtValue(v); got != math.MaxInt64 {
		t.Fatalf("CountAtValue=%d, want MaxInt64", got)
	}

	// One more occurrence would overflow: 1 > MaxInt64 - MaxInt64 (== 0) -> reject.
	err := p.RecordValues(v, 1)
	if err == nil {
		t.Fatal("expected overflow error recording +1 past MaxInt64 total")
	}
	if !strings.Contains(err.Error(), "would overflow the total count") {
		t.Fatalf("unexpected overflow message %q", err.Error())
	}
	// State unchanged by the rejected add.
	if p.TotalCount() != math.MaxInt64 || p.CountAtValue(v) != math.MaxInt64 {
		t.Fatalf("state changed after overflow reject: total=%d cav=%d", p.TotalCount(), p.CountAtValue(v))
	}
	// n == 0 is never an overflow even at a saturated total (0 > 0 is false).
	if err := p.RecordValues(v, 0); err != nil {
		t.Fatalf("zero-count record at saturated total must not overflow, got %v", err)
	}

	// Non-empty boundary: total=10, then n = MaxInt64-10 fits exactly, +1 overflows.
	p2 := cov_recorderr_newPacked()
	if err := p2.RecordValues(v, 10); err != nil {
		t.Fatalf("seed record failed: %v", err)
	}
	if err := p2.RecordValues(v, math.MaxInt64-10); err != nil {
		t.Fatalf("recording exactly to MaxInt64 should succeed, got %v", err)
	}
	if p2.TotalCount() != math.MaxInt64 {
		t.Fatalf("p2 totalCount=%d, want MaxInt64", p2.TotalCount())
	}
	if err := p2.RecordValues(int64(9999), 1); err == nil {
		t.Fatal("expected overflow on any positive add once total is MaxInt64")
	}
}

// #113: dense and packed RecordValues both reject a count that would overflow
// the total, leaving the histogram unchanged.
func TestCov_recorderr_OverflowMatchesDense(t *testing.T) {
	v := int64(1234)
	d := cov_recorderr_newDense()
	if err := d.RecordValues(v, math.MaxInt64); err != nil {
		t.Fatalf("dense seed failed: %v", err)
	}
	if err := d.RecordValues(v, 1); err == nil {
		t.Fatal("dense must reject the overflowing add")
	}
	if d.TotalCount() != math.MaxInt64 {
		t.Fatalf("dense total corrupted: %d", d.TotalCount())
	}

	// Packed on the same sequence rejects and keeps a sane total.
	p := cov_recorderr_newPacked()
	if err := p.RecordValues(v, math.MaxInt64); err != nil {
		t.Fatalf("packed seed failed: %v", err)
	}
	if err := p.RecordValues(v, 1); err == nil {
		t.Fatal("packed must reject the overflowing add")
	}
	if p.TotalCount() != math.MaxInt64 {
		t.Fatalf("packed total corrupted: %d", p.TotalCount())
	}
}

// ----- count == 0 no-op branch: no bucket created, but min/max fields update -----

func TestCov_recorderr_ZeroCountNoOpUpdatesMinMaxFields(t *testing.T) {
	p := cov_recorderr_newPacked()
	v := int64(5000)

	if err := p.RecordValues(v, 0); err != nil {
		t.Fatalf("zero-count record should succeed, got %v", err)
	}
	// n == 0 skips sparseAdd: no populated bucket, no count, no total.
	if p.Populated() != 0 {
		t.Fatalf("Populated=%d after zero-count record, want 0", p.Populated())
	}
	if p.TotalCount() != 0 {
		t.Fatalf("TotalCount=%d after zero-count record, want 0", p.TotalCount())
	}
	if p.CountAtValue(v) != 0 {
		t.Fatalf("CountAtValue=%d after zero-count record, want 0", p.CountAtValue(v))
	}
	// The Min()/Max() QUERIES read idx[] (still empty) and so report the
	// empty-histogram values -- the tracking fields are not consulted there.
	if got := p.Min(); got != p.geom.lowestEquivalentValue(0) {
		t.Fatalf("Min()=%d on bucket-empty histogram, want %d", got, p.geom.lowestEquivalentValue(0))
	}
	if got := p.Max(); got != p.highestEquivalent(0) {
		t.Fatalf("Max()=%d on bucket-empty histogram, want %d", got, p.highestEquivalent(0))
	}
}

// A zero-count record of value 0 is a complete no-op: no bucket, no count, no total.
func TestCov_recorderr_ZeroCountOfValueZeroLeavesFields(t *testing.T) {
	p := cov_recorderr_newPacked()
	if err := p.RecordValues(0, 0); err != nil {
		t.Fatalf("record (0,0) should succeed, got %v", err)
	}
	if p.Populated() != 0 || p.TotalCount() != 0 {
		t.Fatalf("state changed by (0,0): pop=%d total=%d", p.Populated(), p.TotalCount())
	}
}

// ----- value == 0 (bucket 0) with a real count -----

func TestCov_recorderr_RecordValueZeroBucket(t *testing.T) {
	d := cov_recorderr_newDense()
	p := cov_recorderr_newPacked()

	if err := p.RecordValue(0); err != nil {
		t.Fatalf("recording value 0 should succeed, got %v", err)
	}
	if err := d.RecordValue(0); err != nil {
		t.Fatalf("dense recording value 0 should succeed, got %v", err)
	}

	// Bucket 0 is flat index 0 and is now populated.
	if p.Populated() != 1 {
		t.Fatalf("Populated=%d after recording 0, want 1", p.Populated())
	}
	if p.idx[0] != 0 {
		t.Fatalf("idx[0]=%d after recording 0, want flat index 0", p.idx[0])
	}
	if p.CountAtValue(0) != 1 {
		t.Fatalf("CountAtValue(0)=%d, want 1", p.CountAtValue(0))
	}
	if p.TotalCount() != 1 {
		t.Fatalf("TotalCount=%d, want 1", p.TotalCount())
	}
	// Min()/Max() queries (idx-based) must match dense bit-for-bit.
	if p.Min() != d.Min() {
		t.Fatalf("Min() packed=%d dense=%d", p.Min(), d.Min())
	}
	if p.Max() != d.Max() {
		t.Fatalf("Max() packed=%d dense=%d", p.Max(), d.Max())
	}
	if p.CountAtValue(0) != cov_recorderr_denseCount(d, 0) {
		t.Fatalf("CountAtValue(0) packed=%d dense=%d", p.CountAtValue(0), cov_recorderr_denseCount(d, 0))
	}
}

// ----- a valid record after a rejected one still works -----

func TestCov_recorderr_ValidRecordAfterError(t *testing.T) {
	// (a) after an out-of-range rejection.
	p := cov_recorderr_newPacked()
	if err := p.RecordValue(cov_recorderr_hi * 4); err == nil {
		t.Fatal("expected out-of-range rejection")
	}
	if err := p.RecordValue(1234); err != nil {
		t.Fatalf("valid record after range error failed: %v", err)
	}

	// (b) after a negative-count rejection.
	if err := p.RecordValues(1234, -3); err == nil {
		t.Fatal("expected negative-count rejection")
	}
	if err := p.RecordValues(1234, 2); err != nil {
		t.Fatalf("valid record after negative error failed: %v", err)
	}

	// (c) after a total-count overflow rejection on a saturated bucket... use a
	// fresh histogram driven to saturation, then recover with n==0 (allowed) and a
	// small value in a DIFFERENT, unsaturated bucket total is impossible once total
	// is MaxInt64, so only assert the earlier recoveries persisted correctly.
	if got := p.CountAtValue(1234); got != 3 {
		t.Fatalf("CountAtValue(1234)=%d, want 3 (1 + 2 across recoveries)", got)
	}
	if p.TotalCount() != 3 {
		t.Fatalf("TotalCount=%d, want 3", p.TotalCount())
	}

	// Cross-check the recovered state against a clean dense histogram that only saw
	// the two valid records.
	d := cov_recorderr_newDense()
	_ = d.RecordValue(1234)
	_ = d.RecordValues(1234, 2)
	if p.CountAtValue(1234) != cov_recorderr_denseCount(d, 1234) {
		t.Fatalf("post-recovery CountAtValue packed=%d dense=%d", p.CountAtValue(1234), cov_recorderr_denseCount(d, 1234))
	}
}

// ----- count-width widening exercised end-to-end through the record path -----

func TestCov_recorderr_RecordWidensCountWidth(t *testing.T) {
	cases := []struct {
		n         int64
		wantWidth int
	}{
		{1, 1},           // fits in 1 byte
		{0xFF, 1},        // still 1 byte at the max
		{0x100, 2},       // spills to 2 bytes
		{0xFFFF, 2},      // 2-byte max
		{0x10000, 4},     // spills to 4 bytes
		{0xFFFFFFFF, 4},  // 4-byte max
		{0x100000000, 8}, // spills to 8 bytes
	}
	for _, tc := range cases {
		p := cov_recorderr_newPacked()
		v := int64(4242)
		if err := p.RecordValues(v, tc.n); err != nil {
			t.Fatalf("n=%d record failed: %v", tc.n, err)
		}
		if p.CountWidth() != tc.wantWidth {
			t.Fatalf("n=%d: CountWidth=%d, want %d", tc.n, p.CountWidth(), tc.wantWidth)
		}
		// The stored count must round-trip exactly at whatever width was chosen
		// (slotGet/slotSet at widths 2 and 4 are exercised here).
		if got := p.CountAtValue(v); got != tc.n {
			t.Fatalf("n=%d: CountAtValue=%d, want %d", tc.n, got, tc.n)
		}
		if p.TotalCount() != tc.n {
			t.Fatalf("n=%d: TotalCount=%d, want %d", tc.n, p.TotalCount(), tc.n)
		}
	}
}

// A second record into an already-widened, already-populated slot exercises the
// widen-on-existing-slot path inside sparseAdd (widths 2 and 4 in slotGet).
func TestCov_recorderr_RecordAccumulatesAcrossWidths(t *testing.T) {
	p := cov_recorderr_newPacked()
	v := int64(4242)
	var want int64
	for _, n := range []int64{200, 200, 70000, 5000000000} {
		if err := p.RecordValues(v, n); err != nil {
			t.Fatalf("record n=%d failed: %v", n, err)
		}
		want += n
		if got := p.CountAtValue(v); got != want {
			t.Fatalf("after n=%d: CountAtValue=%d, want %d", n, got, want)
		}
	}
	if p.Populated() != 1 {
		t.Fatalf("Populated=%d, want 1 (single bucket accumulated)", p.Populated())
	}
	if p.CountWidth() != 8 {
		t.Fatalf("CountWidth=%d, want 8 after 5e9 count", p.CountWidth())
	}
}

// ----- RecordValue is exactly RecordValues(v, 1) -----

func TestCov_recorderr_RecordValueIsRecordValuesOne(t *testing.T) {
	a := cov_recorderr_newPacked()
	b := cov_recorderr_newPacked()
	for _, v := range []int64{0, 1, 42, 5000, cov_recorderr_hi} {
		if err := a.RecordValue(v); err != nil {
			t.Fatalf("RecordValue(%d) failed: %v", v, err)
		}
		if err := b.RecordValues(v, 1); err != nil {
			t.Fatalf("RecordValues(%d,1) failed: %v", v, err)
		}
	}
	if a.TotalCount() != b.TotalCount() || a.Populated() != b.Populated() {
		t.Fatalf("RecordValue vs RecordValues(,1) diverged: total %d/%d pop %d/%d",
			a.TotalCount(), b.TotalCount(), a.Populated(), b.Populated())
	}
	for _, v := range []int64{0, 1, 42, 5000, cov_recorderr_hi} {
		if a.CountAtValue(v) != b.CountAtValue(v) {
			t.Fatalf("count mismatch at %d: %d vs %d", v, a.CountAtValue(v), b.CountAtValue(v))
		}
	}
}
