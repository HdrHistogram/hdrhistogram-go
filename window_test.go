package hdrhistogram_test

import (
	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"testing"
)

// nolint
func TestWindowedHistogram(t *testing.T) {
	w := hdrhistogram.NewWindowed(2, 1, 1000, 3)

	for i := 0; i < 100; i++ {
		w.Current.RecordValue(int64(i))
	}
	w.Rotate()

	for i := 100; i < 200; i++ {
		w.Current.RecordValue(int64(i))
	}
	w.Rotate()

	for i := 200; i < 300; i++ {
		w.Current.RecordValue(int64(i))
	}

	if v, want := w.Merge().ValueAtQuantile(50), int64(199); v != want {
		t.Errorf("Median was %v, but expected %v", v, want)
	}
}
