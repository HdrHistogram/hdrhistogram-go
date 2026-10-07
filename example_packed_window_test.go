package hdrhistogram_test

import (
	"fmt"

	"github.com/HdrHistogram/hdrhistogram-go"
)

// A rolling window: record into one dense histogram, move each completed
// one-second slice into a reused packed slot of a ring, and merge the retained
// slices into a dense aggregate when percentiles are needed.
func ExamplePackedHistogram_MergeInto() {
	const slots = 3
	active := hdrhistogram.New(1, 30000000, 3)
	ring := make([]*hdrhistogram.PackedHistogram, slots)
	for i := range ring {
		ring[i] = hdrhistogram.NewPacked(1, 30000000, 3)
	}

	for second := 0; second < 5; second++ {
		for v := int64(1); v <= 100; v++ {
			_ = active.RecordValue(v * int64(second+1) * 100)
		}
		// The second is complete: reuse the oldest slot for it.
		slot := ring[second%slots]
		slot.Reset()
		slot.MergeFrom(active)
		active.Reset()
	}

	// Aggregate the last three seconds (seconds 2, 3 and 4).
	window := hdrhistogram.New(1, 30000000, 3)
	for _, slot := range ring {
		slot.MergeInto(window)
	}
	fmt.Println("count:", window.TotalCount())
	fmt.Println("p50:", window.ValueAtQuantile(50))
	fmt.Println("max:", window.Max())
	// Output:
	// count: 300
	// p50: 19215
	// max: 50015
}
