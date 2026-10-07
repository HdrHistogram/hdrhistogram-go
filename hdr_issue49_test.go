package hdrhistogram_test

import (
	"fmt"
	"math"
	"testing"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
)

// This single-threaded example reproduces #49's exact out-of-bounds panic on
// v1.1.2. Later bounded scans avoid the panic but return zero instead of 1000.
func TestIssue49WindowedPercentile(t *testing.T) {
	w := hdr.NewWindowed(3, 1, 10_000_000_000, 3)
	if err := w.Current.RecordValues(1000, (1<<53)+3); err != nil {
		t.Fatal(err)
	}
	w.Rotate()
	h := w.Merge()
	if got := h.TotalCount(); got != (1<<53)+3 {
		t.Fatalf("TotalCount = %d, want %d", got, (1<<53)+3)
	}
	if got := h.ValueAtQuantile(100); got != 1000 {
		t.Fatalf("P100 = %d, want 1000", got)
	}
}

func TestPercentilesLargeCounts(t *testing.T) {
	// Around 2^52, adding 0.5 can round up past an integer total; above 2^53,
	// converting the total itself can round either way. Near MaxInt64, the
	// rounded float can be outside int64's range entirely.
	for _, total := range []int64{
		(1 << 52) - 1, 1 << 52, (1 << 52) + 1, (1 << 52) + 3,
		(1 << 53) - 1, 1 << 53, (1 << 53) + 1, (1 << 53) + 3,
		math.MaxInt64 - 1024, math.MaxInt64 - 1, math.MaxInt64,
	} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			h := hdr.New(1, 10000, 3)
			p := hdr.NewPacked(1, 10000, 3)
			for _, histogram := range []interface {
				RecordValues(int64, int64) error
			}{h, p} {
				if err := histogram.RecordValues(1000, total-1); err != nil {
					t.Fatal(err)
				}
				if err := histogram.RecordValues(2000, 1); err != nil {
					t.Fatal(err)
				}
			}
			// Unsorted inputs and duplicates also exercise the batch query order.
			percentiles := []float64{100, 0, 99.9999, 150, 50, math.Inf(1), -1, math.Inf(-1), 100}
			want := []int64{2000, 1000, 1000, 2000, 1000, 2000, 1000, 1000, 2000}
			mapped := h.ValueAtPercentiles(append([]float64(nil), percentiles...))
			denseSlice := h.ValueAtPercentilesSlice(percentiles)
			packedSlice := p.ValueAtPercentilesSlice(percentiles)
			for i, percentile := range percentiles {
				for api, got := range map[string]int64{
					"scalar":        h.ValueAtPercentile(percentile),
					"quantile":      h.ValueAtQuantile(percentile),
					"map":           mapped[percentile],
					"slice":         denseSlice[i],
					"packed scalar": p.ValueAtPercentile(percentile),
					"packed slice":  packedSlice[i],
				} {
					if got != want[i] {
						t.Errorf("%s(%v) = %d, want %d", api, percentile, got, want[i])
					}
				}
			}
			// NaN has no usable map key; scalar and slice queries select the first rank.
			if got := h.ValueAtPercentile(math.NaN()); got != 1000 {
				t.Errorf("scalar(NaN) = %d, want 1000", got)
			}
			if got := h.ValueAtPercentilesSlice([]float64{math.NaN()})[0]; got != 1000 {
				t.Errorf("slice(NaN) = %d, want 1000", got)
			}
		})
	}
}
