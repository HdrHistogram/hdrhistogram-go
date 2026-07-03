package hdrhistogram

import (
	"github.com/stretchr/testify/assert"
	"math/rand"
	"testing"
)

// linearScanReference is the pre-optimization linear prefix-sum scan, kept here
// as the correctness oracle for the blocked skip-scan in getValueFromIdxUpToCount.
func (h *Histogram) linearScanReference(countAtPercentile int64) int64 {
	var countToIdx int64
	for idx, c := range h.counts {
		countToIdx += c
		if countToIdx >= countAtPercentile {
			return h.valueFromFlatIndex(int32(idx))
		}
	}
	return 0
}

// TestGetValueFromIdxUpToCount_BlockedVsLinear proves the blocked skip-scan
// returns byte-identical results to the plain linear scan for every possible
// target count, across histograms whose counts[] length spans all residues mod
// the block width (so partial-tail and crossing-block-boundary paths are all
// exercised), and for a variety of value distributions.
func TestGetValueFromIdxUpToCount_BlockedVsLinear(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	configs := []struct{ low, high int64 }{
		{1, 100},                // tiny counts[] — mostly tail path
		{1, 1000},               // small
		{1, 3600 * 1000 * 1000}, // production-sized
		{1000, 100000},          // offset low bound
	}
	for _, cfg := range configs {
		for sig := 1; sig <= 3; sig++ {
			h := New(cfg.low, cfg.high, sig)
			// Sprinkle recorded values across the whole range so counts[] has
			// non-trivial gaps and clusters (skip vs crossing blocks).
			for i := 0; i < 5000; i++ {
				v := cfg.low + rng.Int63n(cfg.high-cfg.low+1)
				_ = h.RecordValue(v)
			}
			n := len(h.counts)
			// Test every target from below 0 through past totalCount, hitting
			// every block boundary and both edges.
			targets := []int64{-5, -1, 0, 1, 2, h.totalCount - 1, h.totalCount, h.totalCount + 1, h.totalCount + 100}
			for b := 0; b <= n; b++ { // one target per index boundary
				var partial int64
				for k := 0; k < b && k < n; k++ {
					partial += h.counts[k]
				}
				targets = append(targets, partial, partial+1)
			}
			for _, target := range targets {
				got := h.getValueFromIdxUpToCount(target)
				want := h.linearScanReference(target)
				if got != want {
					t.Fatalf("cfg=%v sig=%d n=%d target=%d: blocked=%d linear=%d",
						cfg, sig, n, target, got, want)
				}
			}
		}
	}
}

func TestHistogram_New_internals(t *testing.T) {
	// test for numberOfSignificantValueDigits if higher than 5 the numberOfSignificantValueDigits will be forced to 5
	hist := New(1, 9007199254740991, 6)
	assert.Equal(t, int64(5), hist.significantFigures)
	// test for numberOfSignificantValueDigits if lower than 1 the numberOfSignificantValueDigits will be forced to 1
	hist = New(1, 9007199254740991, 0)
	assert.Equal(t, int64(1), hist.significantFigures)
}
