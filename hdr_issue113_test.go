package hdrhistogram

import (
	"math"
	"strings"
	"testing"
)

// #113: dense recording and merging must reject counts that would push the
// total past MaxInt64, as PackedHistogram does, instead of wrapping it.

func checkDenseConsistent(t *testing.T, name string, h *Histogram) {
	t.Helper()
	if h.TotalCount() < 0 || h.TotalCount() != countsSum(h) {
		t.Fatalf("%s: total %d, counts sum %d", name, h.TotalCount(), countsSum(h))
	}
}

func denseCountAt(h *Histogram, v int64) int64 { return h.counts[h.countsIndexFor(v)] }

func TestRecordValuesRejectsTotalOverflow(t *testing.T) {
	h := New(1, 10_000_000, 3)
	if err := h.RecordValues(1000, math.MaxInt64-10); err != nil {
		t.Fatal(err)
	}
	// Exactly reaching MaxInt64 is allowed.
	if err := h.RecordValues(5000, 10); err != nil {
		t.Fatalf("total of exactly MaxInt64: %v", err)
	}
	before := h.Export()
	for _, n := range []int64{1, math.MaxInt64} {
		if err := h.RecordValues(5000, n); err == nil || !strings.Contains(err.Error(), "overflow the total count") {
			t.Fatalf("RecordValues(5000, %d) = %v, want overflow error", n, err)
		}
	}
	if err := h.RecordValue(7); err == nil {
		t.Fatal("RecordValue past MaxInt64 accepted")
	}
	// n == 0 at the limit is still a no-op success.
	if err := h.RecordValues(5000, 0); err != nil {
		t.Fatalf("RecordValues(_, 0) at the limit: %v", err)
	}
	if !h.Equals(Import(before)) {
		t.Fatal("rejected records changed the histogram")
	}
	checkDenseConsistent(t, "record", h)
	if h.Max() < 5000 || h.ValueAtQuantile(100) < 5000 {
		t.Fatalf("max %d p100 %d, want >= 5000", h.Max(), h.ValueAtQuantile(100))
	}
}

func TestRecordCorrectedValueStopsAtTotalOverflow(t *testing.T) {
	h := New(1, 10_000_000, 3)
	p := NewPacked(1, 10_000_000, 3)
	for _, r := range []interface{ RecordValues(int64, int64) error }{h, p} {
		if err := r.RecordValues(1, math.MaxInt64-2); err != nil {
			t.Fatal(err)
		}
	}
	// 1000 with interval 100 records 1000, 900, 800, ...: only two fit.
	errD := h.RecordCorrectedValue(1000, 100)
	errP := p.RecordCorrectedValue(1000, 100)
	if errD == nil || errP == nil {
		t.Fatalf("corrected overflow not reported: dense %v packed %v", errD, errP)
	}
	if h.TotalCount() != math.MaxInt64 || p.TotalCount() != math.MaxInt64 {
		t.Fatalf("totals: dense %d packed %d, want MaxInt64", h.TotalCount(), p.TotalCount())
	}
	for _, v := range []int64{1000, 900, 800} {
		if denseCountAt(h, v) != p.CountAtValue(v) {
			t.Fatalf("value %d: dense %d packed %d", v, denseCountAt(h, v), p.CountAtValue(v))
		}
	}
	checkDenseConsistent(t, "corrected", h)
}

func TestMergeDropsTotalOverflow(t *testing.T) {
	a := New(1, 10_000_000, 3)
	b := New(1, 10_000_000, 3)
	if err := a.RecordValues(1000, math.MaxInt64-5); err != nil {
		t.Fatal(err)
	}
	_ = b.RecordValues(2000, 5) // fits exactly
	_ = b.RecordValues(5000, 10)
	if d := a.Merge(b); d != 10 {
		t.Fatalf("Merge dropped %d, want 10", d)
	}
	checkDenseConsistent(t, "merge", a)
	if a.TotalCount() != math.MaxInt64 || denseCountAt(a, 2000) != 5 || denseCountAt(a, 5000) != 0 {
		t.Fatalf("total %d, count(2000) %d, count(5000) %d", a.TotalCount(), denseCountAt(a, 2000), denseCountAt(a, 5000))
	}

	// Dense/packed parity for the same merge.
	pa := NewPacked(1, 10_000_000, 3)
	pb := NewPacked(1, 10_000_000, 3)
	_ = pa.RecordValues(1000, math.MaxInt64-5)
	_ = pb.RecordValues(2000, 5)
	_ = pb.RecordValues(5000, 10)
	if d := pa.Merge(pb); d != 10 || pa.TotalCount() != math.MaxInt64 {
		t.Fatalf("packed Merge dropped %d total %d", d, pa.TotalCount())
	}

	// Self-merge at more than half of MaxInt64 drops the whole second copy.
	s := New(1, 10_000_000, 3)
	_ = s.RecordValues(1000, math.MaxInt64/2+1)
	if d := s.Merge(s); d != math.MaxInt64/2+1 {
		t.Fatalf("self-merge dropped %d", d)
	}
	checkDenseConsistent(t, "self-merge", s)
}

func TestWindowedMergeDoesNotWrapTotal(t *testing.T) {
	w := NewWindowed(2, 1, 10_000_000, 3)
	_ = w.Current.RecordValues(1000, math.MaxInt64)
	w.Rotate()
	_ = w.Current.RecordValues(5000, 10)
	m := w.Merge()
	checkDenseConsistent(t, "windowed", m)
	if m.TotalCount() != math.MaxInt64 {
		t.Fatalf("windowed total %d", m.TotalCount())
	}
}

func TestPackedMergeIntoDenseDropsTotalOverflow(t *testing.T) {
	for _, digits := range []int{3, 2} { // same indexing, then re-record path
		dst := New(1, 10_000_000, digits)
		if err := dst.RecordValues(1000, math.MaxInt64-3); err != nil {
			t.Fatal(err)
		}
		p := NewPacked(1, 10_000_000, 3)
		_ = p.RecordValues(10, 3)
		_ = p.RecordValues(5000, 7)
		if d := p.MergeInto(dst); d != 7 {
			t.Fatalf("digits %d: MergeInto dropped %d, want 7", digits, d)
		}
		checkDenseConsistent(t, "MergeInto", dst)
		if dst.TotalCount() != math.MaxInt64 {
			t.Fatalf("digits %d: total %d", digits, dst.TotalCount())
		}
	}
}
