package hdrhistogram

import (
	"testing"
)

// This file deeply covers PackedHistogram.widenToFit and its callers
// (sparseAdd / slotGet / slotSet / packedWidthMax) driven through the public
// RecordValues path with crafted counts. It exercises every width transition —
// including the SKIPS that jump past intermediate widths (1->4, 1->8, 2->8) —
// the no-op path (need already fits the current width), and verifies that every
// previously recorded count survives the re-pack into the wider layout, checked
// bit-for-bit against a dense reference histogram.

// cov_widen params: a wide, high-resolution geometry so our crafted values land
// in distinct, well-separated buckets and huge counts are representable.
const (
	cov_widen_low  = int64(1)
	cov_widen_high = int64(9223372036854775807)
	cov_widen_sig  = 3
)

// packedWidthMax thresholds, restated locally so the tests assert against fixed
// numbers rather than re-deriving them from the code under test.
const (
	cov_widen_max1 = int64(0xFF)       // 255
	cov_widen_max2 = int64(0xFFFF)     // 65535
	cov_widen_max4 = int64(0xFFFFFFFF) // 4294967295
)

// cov_widen_newPair returns a fresh dense+packed pair with identical geometry.
func cov_widen_newPair(t *testing.T) (*Histogram, *PackedHistogram) {
	t.Helper()
	return New(cov_widen_low, cov_widen_high, cov_widen_sig),
		NewPacked(cov_widen_low, cov_widen_high, cov_widen_sig)
}

// cov_widen_rec records (v,n) into both dense and packed and fails on any error
// or on any divergence in TotalCount after the record.
func cov_widen_rec(t *testing.T, d *Histogram, p *PackedHistogram, v, n int64) {
	t.Helper()
	if err := d.RecordValues(v, n); err != nil {
		t.Fatalf("dense RecordValues(%d,%d): %v", v, n, err)
	}
	if err := p.RecordValues(v, n); err != nil {
		t.Fatalf("packed RecordValues(%d,%d): %v", v, n, err)
	}
	if d.TotalCount() != p.TotalCount() {
		t.Fatalf("total drift after (%d,%d): dense %d != packed %d", v, n, d.TotalCount(), p.TotalCount())
	}
}

// cov_widen_assertCountsSurvive walks every dense bucket and asserts the packed
// count equals the dense count at that bucket's value. This is the "all existing
// counts survive the re-pack" invariant, checked against a well-defined oracle.
func cov_widen_assertCountsSurvive(t *testing.T, d *Histogram, p *PackedHistogram, tag string) {
	t.Helper()
	for i := int32(0); i < d.countsLen; i++ {
		want := d.counts[i]
		got := p.CountAtValue(d.valueFromFlatIndex(i))
		if want != got {
			t.Fatalf("%s: count[%d] (value %d) dense %d != packed %d",
				tag, i, d.valueFromFlatIndex(i), want, got)
		}
	}
	if d.TotalCount() != p.TotalCount() {
		t.Fatalf("%s: total dense %d != packed %d", tag, d.TotalCount(), p.TotalCount())
	}
}

// cov_widen_distinctValues returns values guaranteed to map to distinct flat
// indices under the test geometry, so each sits in its own populated bucket.
func cov_widen_distinctValues(t *testing.T, p *PackedHistogram, vals ...int64) {
	t.Helper()
	seen := map[int]bool{}
	for _, v := range vals {
		ci := p.geom.countsIndexFor(v)
		if ci < 0 || int32(ci) >= p.geom.countsLen {
			t.Fatalf("value %d out of range (ci=%d)", v, ci)
		}
		if seen[ci] {
			t.Fatalf("value %d collides on flat index %d — pick a different probe value", v, ci)
		}
		seen[ci] = true
	}
}

// -------------------------------------------------------------------------
// Every width transition through a FRESH (new-bucket) insert, incl. skips.
// -------------------------------------------------------------------------

func TestCov_widenTransitionsFreshBucket(t *testing.T) {
	// Distinct probe values, one per populated bucket.
	vSmallA := int64(3)
	vSmallB := int64(50)
	vTrigger := int64(1_000_000)

	cases := []struct {
		name      string
		fromWidth int   // width established before the trigger record
		setupBig  int64 // count on a helper bucket to force `fromWidth` (0 => none)
		trigger   int64 // count recorded into a NEW bucket (vTrigger)
		wantWidth int
	}{
		// From width 1 (no setupBig needed; small counts keep width 1).
		{"1->2", 1, 0, cov_widen_max1 + 1, 2},               // 256, just over 1-byte max
		{"1->2_boundaryMax1_noop", 1, 0, cov_widen_max1, 1}, // 255 exactly => stays width 1
		{"1->4_skip", 1, 0, cov_widen_max2 + 1, 4},          // 65536, skips width 2
		{"1->8_skip", 1, 0, cov_widen_max4 + 1, 8},          // 4294967296, skips widths 2 and 4
		// From width 2 (setupBig=300 forces width 2 first).
		{"2->2_noop", 2, 300, 100, 2},                // 100 fits width 2 => widenToFit no-op
		{"2->4", 2, 300, cov_widen_max2 + 1, 4},      // 65536
		{"2->8_skip", 2, 300, cov_widen_max4 + 1, 8}, // skips width 4
		// From width 4 (setupBig=70000 forces width 4 first).
		{"4->4_noop", 4, 70000, 5000, 4},          // fits width 4 => no-op
		{"4->8", 4, 70000, cov_widen_max4 + 1, 8}, // 4294967296
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, p := cov_widen_newPair(t)
			cov_widen_distinctValues(t, p, vSmallA, vSmallB, vTrigger)

			// Small buckets that must survive every re-pack.
			cov_widen_rec(t, d, p, vSmallA, 7)
			cov_widen_rec(t, d, p, vSmallB, 42)

			if tc.setupBig != 0 {
				// Establish the "from" width on its own dedicated bucket.
				cov_widen_rec(t, d, p, vSmallB, tc.setupBig-42) // vSmallB now holds setupBig
				if p.CountWidth() != tc.fromWidth {
					t.Fatalf("setup: from-width got %d want %d", p.CountWidth(), tc.fromWidth)
				}
			} else if p.CountWidth() != tc.fromWidth {
				t.Fatalf("setup: from-width got %d want %d", p.CountWidth(), tc.fromWidth)
			}

			// The trigger record into a fresh bucket.
			cov_widen_rec(t, d, p, vTrigger, tc.trigger)

			if p.CountWidth() != tc.wantWidth {
				t.Fatalf("after trigger: width got %d want %d", p.CountWidth(), tc.wantWidth)
			}
			cov_widen_assertCountsSurvive(t, d, p, tc.name)

			// Explicitly re-check the trigger bucket's own value survived.
			if got := p.CountAtValue(vTrigger); got != tc.trigger {
				t.Fatalf("%s: trigger bucket count got %d want %d", tc.name, got, tc.trigger)
			}
		})
	}
}

// -------------------------------------------------------------------------
// Every width transition through an EXISTING bucket (accumulate then widen),
// incl. skips — this drives the sparseAdd found-branch widenToFit(nv) call.
// -------------------------------------------------------------------------

func TestCov_widenTransitionsExistingBucket(t *testing.T) {
	v := int64(6147)

	cases := []struct {
		name      string
		base      int64 // first record (establishes a starting count/width)
		add       int64 // second record into the SAME bucket
		wantWidth int
	}{
		{"1->1_noop", 10, 5, 1},               // 15 fits width 1
		{"1->2", 10, cov_widen_max1, 2},       // 265 > 255
		{"1->4_skip", 10, cov_widen_max2, 4},  // 65545 > 65535, skips 2
		{"1->8_skip", 10, cov_widen_max4, 8},  // > 4294967295, skips 2 and 4
		{"2->4", 300, cov_widen_max2, 4},      // start width2, 65835 > 65535
		{"2->8_skip", 300, cov_widen_max4, 8}, // start width2, skips 4
		{"4->8", 70000, cov_widen_max4, 8},    // start width4
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, p := cov_widen_newPair(t)
			// A neighbour small bucket that must survive.
			cov_widen_rec(t, d, p, 3, 9)

			cov_widen_rec(t, d, p, v, tc.base)
			cov_widen_rec(t, d, p, v, tc.add)

			if p.CountWidth() != tc.wantWidth {
				t.Fatalf("%s: width got %d want %d", tc.name, p.CountWidth(), tc.wantWidth)
			}
			want := tc.base + tc.add
			if got := p.CountAtValue(v); got != want {
				t.Fatalf("%s: accumulated count got %d want %d", tc.name, got, want)
			}
			if got := p.CountAtValue(3); got != 9 {
				t.Fatalf("%s: neighbour count got %d want 9", tc.name, got)
			}
			cov_widen_assertCountsSurvive(t, d, p, tc.name)
		})
	}
}

// -------------------------------------------------------------------------
// packedWidthMax: exact table of the documented per-width caps, and the
// monotonic doubling behaviour observed through widenToFit at each boundary.
// -------------------------------------------------------------------------

func TestCov_widenPackedWidthMaxTable(t *testing.T) {
	cases := []struct {
		w    uint8
		want int64
	}{
		{1, 0xFF},
		{2, 0xFFFF},
		{4, 0xFFFFFFFF},
		{8, int64(^uint64(0) >> 1)}, // math.MaxInt64 (default branch)
		{3, int64(^uint64(0) >> 1)}, // any non-1/2/4 hits the default branch too
		{16, int64(^uint64(0) >> 1)},
	}
	for _, tc := range cases {
		if got := packedWidthMax(tc.w); got != tc.want {
			t.Fatalf("packedWidthMax(%d) = %d, want %d", tc.w, got, tc.want)
		}
	}
}

// widenToFit is a no-op whenever `need` already fits the current width — verify
// the width is untouched at each cap boundary (need == max => no widen).
func TestCov_widenNoOpAtEachBoundary(t *testing.T) {
	cases := []struct {
		name      string
		setup     int64 // establishes current width
		wantWidth int
		need      int64 // fits current width exactly => no-op
	}{
		{"width1_needMax1", 1, 1, cov_widen_max1},     // 255 fits width 1
		{"width2_needMax2", 300, 2, cov_widen_max2},   // 65535 fits width 2
		{"width4_needMax4", 70000, 4, cov_widen_max4}, // 4294967295 fits width 4
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, p := cov_widen_newPair(t)
			// Establish current width on bucket for value 100.
			cov_widen_rec(t, d, p, 100, tc.setup)
			if p.CountWidth() != tc.wantWidth {
				t.Fatalf("setup width got %d want %d", p.CountWidth(), tc.wantWidth)
			}
			wBefore := p.CountWidth()
			cntPtrBefore := &p.cnt[0]

			// Record `need - setup` more into the SAME bucket so nv == need exactly,
			// which reaches widenToFit(need) with need == packedWidthMax(width): no-op.
			cov_widen_rec(t, d, p, 100, tc.need-tc.setup)

			if p.CountWidth() != wBefore {
				t.Fatalf("%s: width changed on no-op path: %d -> %d", tc.name, wBefore, p.CountWidth())
			}
			// The backing byte slice must not have been reallocated on the no-op path.
			if &p.cnt[0] != cntPtrBefore {
				t.Fatalf("%s: cnt buffer reallocated on no-op widen", tc.name)
			}
			if got := p.CountAtValue(100); got != tc.need {
				t.Fatalf("%s: count got %d want %d", tc.name, got, tc.need)
			}
			cov_widen_assertCountsSurvive(t, d, p, tc.name)
		})
	}
}

// One extra past-the-boundary case per width: need == max+1 forces exactly one
// doubling step, confirming the boundary is inclusive on the low side.
func TestCov_widenOneStepPastBoundary(t *testing.T) {
	cases := []struct {
		name      string
		setup     int64
		fromWidth int
		need      int64
		toWidth   int
	}{
		{"1->2_at256", 1, 1, cov_widen_max1 + 1, 2},
		{"2->4_at65536", 300, 2, cov_widen_max2 + 1, 4},
		{"4->8_at4Gplus1", 70000, 4, cov_widen_max4 + 1, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, p := cov_widen_newPair(t)
			cov_widen_rec(t, d, p, 100, tc.setup)
			if p.CountWidth() != tc.fromWidth {
				t.Fatalf("from width got %d want %d", p.CountWidth(), tc.fromWidth)
			}
			cov_widen_rec(t, d, p, 100, tc.need-tc.setup)
			if p.CountWidth() != tc.toWidth {
				t.Fatalf("%s: width got %d want %d", tc.name, p.CountWidth(), tc.toWidth)
			}
			if got := p.CountAtValue(100); got != tc.need {
				t.Fatalf("%s: count got %d want %d", tc.name, got, tc.need)
			}
		})
	}
}

// -------------------------------------------------------------------------
// Re-pack fidelity across MANY buckets at each destination width: after a widen
// every slot must round-trip through slotGet at the new width. Compares the full
// count vector to a dense oracle after each escalation, exercising slotGet /
// slotSet for widths 2, 4 and 8 within a single re-packed buffer.
// -------------------------------------------------------------------------

func TestCov_widenManyBucketsRepackFidelity(t *testing.T) {
	d, p := cov_widen_newPair(t)

	// A spread of distinct probe values, each in its own bucket.
	vals := []int64{1, 7, 40, 500, 9000, 130000, 2_500_000, 77_000_000, 1_500_000_000}
	cov_widen_distinctValues(t, p, vals...)

	// Seed each bucket with a small, distinct count (all fit width 1).
	for i, v := range vals {
		cov_widen_rec(t, d, p, v, int64(i+1))
	}
	if p.CountWidth() != 1 {
		t.Fatalf("expected width 1 after small seeds, got %d", p.CountWidth())
	}
	cov_widen_assertCountsSurvive(t, d, p, "width1-seed")

	// Escalate the first bucket through 2, 4, 8 and confirm ALL buckets survive
	// each re-pack (not just the one we grew).
	escalate := []struct {
		to    int64
		width int
	}{
		{cov_widen_max1 + 1, 2}, // 256
		{cov_widen_max2 + 1, 4}, // 65536
		{cov_widen_max4 + 1, 8}, // 4294967296
	}
	prev := int64(1) // current count on vals[0]
	for _, e := range escalate {
		cov_widen_rec(t, d, p, vals[0], e.to-prev)
		prev = e.to
		if p.CountWidth() != e.width {
			t.Fatalf("escalate to %d: width got %d want %d", e.to, p.CountWidth(), e.width)
		}
		cov_widen_assertCountsSurvive(t, d, p, "escalated")
		// Every seeded neighbour still holds its original tiny count.
		for i, v := range vals[1:] {
			if got := p.CountAtValue(v); got != int64(i+2) {
				t.Fatalf("neighbour value %d: got %d want %d after escalate", v, got, i+2)
			}
		}
	}
}

// -------------------------------------------------------------------------
// Direct unit test of widenToFit against slotGet at width 8 for the full
// unsigned range representable per width, ensuring the little-endian re-pack in
// widenToFit preserves values that use the top bytes of each source width.
// -------------------------------------------------------------------------

func TestCov_widenPreservesHighBytes(t *testing.T) {
	// Values chosen to exercise every byte lane at each source width.
	// width1: 0xAB ; width2: 0xABCD ; width4: 0xABCDEF01
	d, p := cov_widen_newPair(t)
	vA, vB, vC := int64(3), int64(50), int64(9000)
	cov_widen_distinctValues(t, p, vA, vB, vC)

	cov_widen_rec(t, d, p, vA, 0xAB)   // stays width 1
	cov_widen_rec(t, d, p, vB, 0xABCD) // -> width 2, re-packs vA
	if p.CountWidth() != 2 {
		t.Fatalf("expected width 2, got %d", p.CountWidth())
	}
	if got := p.CountAtValue(vA); got != 0xAB {
		t.Fatalf("width2 re-pack: vA got %#x want 0xAB", got)
	}

	cov_widen_rec(t, d, p, vC, 0xABCDEF01) // -> width 4, re-packs vA,vB
	if p.CountWidth() != 4 {
		t.Fatalf("expected width 4, got %d", p.CountWidth())
	}
	if got := p.CountAtValue(vA); got != 0xAB {
		t.Fatalf("width4 re-pack: vA got %#x want 0xAB", got)
	}
	if got := p.CountAtValue(vB); got != 0xABCD {
		t.Fatalf("width4 re-pack: vB got %#x want 0xABCD", got)
	}

	// Push vC over the 4-byte cap to force width 8 and re-pack all three.
	cov_widen_rec(t, d, p, vC, cov_widen_max4+1-0xABCDEF01)
	if p.CountWidth() != 8 {
		t.Fatalf("expected width 8, got %d", p.CountWidth())
	}
	if got := p.CountAtValue(vA); got != 0xAB {
		t.Fatalf("width8 re-pack: vA got %#x want 0xAB", got)
	}
	if got := p.CountAtValue(vB); got != 0xABCD {
		t.Fatalf("width8 re-pack: vB got %#x want 0xABCD", got)
	}
	if got := p.CountAtValue(vC); got != cov_widen_max4+1 {
		t.Fatalf("width8 re-pack: vC got %d want %d", got, cov_widen_max4+1)
	}
	cov_widen_assertCountsSurvive(t, d, p, "highbytes-width8")
}
