package hdrhistogram_test

import (
	"math"
	"testing"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
)

// New with a near-MaxInt64 highest value exercises getBucketsNeededToCoverValue's
// overflow guard (the shift that would exceed MaxInt64).
func TestNewMaxRangeOverflowGuard(t *testing.T) {
	h := hdrhistogram.New(1, math.MaxInt64, 3)
	if h == nil {
		t.Fatal("New returned nil for MaxInt64 range")
	}
	// The largest trackable value must be recordable without error or panic.
	if err := h.RecordValue(math.MaxInt64 / 2); err != nil {
		t.Errorf("RecordValue(MaxInt64/2) unexpected error: %v", err)
	}
	if h.TotalCount() != 1 {
		t.Errorf("TotalCount = %d, want 1", h.TotalCount())
	}
}

// RecordCorrectedValue must return an error when the value is out of range.
func TestRecordCorrectedValueOutOfRange(t *testing.T) {
	h := hdrhistogram.New(1, 1000, 3)
	if err := h.RecordCorrectedValue(1_000_000_000, 10); err == nil {
		t.Error("RecordCorrectedValue with out-of-range value: expected error, got nil")
	}
}

// Merge must report the count that could not be recorded because the destination
// range is narrower than the source's.
func TestMergeReportsDropped(t *testing.T) {
	dst := hdrhistogram.New(1, 1000, 3)       // narrow
	src := hdrhistogram.New(1, 10_000_000, 3) // wide
	for i := 0; i < 50; i++ {
		if err := src.RecordValue(5_000_000); err != nil { // out of dst range
			t.Fatal(err)
		}
	}
	for i := 0; i < 100; i++ {
		if err := src.RecordValue(500); err != nil { // in dst range
			t.Fatal(err)
		}
	}
	dropped := dst.Merge(src)
	if dropped != 50 {
		t.Errorf("Merge dropped = %d, want 50", dropped)
	}
	if dst.TotalCount() != 100 {
		t.Errorf("dst TotalCount = %d, want 100", dst.TotalCount())
	}
}
