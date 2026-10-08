package hdrhistogram

import (
	"math"
	"math/rand"
	"testing"
)

// linearPercentiles is a plain prefix-sum reference for ValueAtPercentiles, independent of the
// blocked scans: the first index whose cumulative count reaches each percentile's rank.
func linearPercentiles(h *Histogram, ps []float64) map[float64]int64 {
	out := make(map[float64]int64, len(ps))
	for _, p := range ps {
		out[p] = 0
		target := percentileRank(p, h.totalCount)
		var total int64
		for idx, c := range h.counts {
			total += c
			if total >= target {
				v := h.valueFromFlatIndex(int32(idx))
				if p <= 0 {
					out[p] = h.lowestEquivalentValue(v)
				} else {
					out[p] = h.highestEquivalentValue(v)
				}
				break
			}
		}
	}
	return out
}

// Zero-digit geometries, reachable only through Import and decoding, have counts arrays whose
// length is not a multiple of the scan block, so they exercise scanTargets' scalar tail.
func TestValueAtPercentilesZeroDigitTail(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, high := range []int64{2, 3, 10, 1000, 1 << 40} {
		h := Import(&Snapshot{LowestTrackableValue: 1, HighestTrackableValue: high, SignificantFigures: 0})
		n := len(h.counts)
		if n%scanBlock == 0 {
			t.Fatalf("high %d: counts length %d is a multiple of %d; the tail is not exercised", high, n, scanBlock)
		}
		for round := 0; round < 200; round++ {
			s := h.Export()
			for i := range s.Counts {
				s.Counts[i] = 0
			}
			switch round % 4 {
			case 0: // only the last count, which always sits in the tail
				s.Counts[n-1] = rng.Int63n(5) + 1
			case 1: // the last two counts
				s.Counts[n-1], s.Counts[n-2] = rng.Int63n(5)+1, rng.Int63n(5)+1
			default:
				for i := range s.Counts {
					if rng.Intn(3) == 0 {
						s.Counts[i] = rng.Int63n(100)
					}
				}
			}
			g := Import(s)
			if g.TotalCount() == 0 {
				continue
			}
			ps := []float64{0, 100, 100, 99.99, 50, 50, 150, -1}
			for i := 0; i < 6; i++ {
				ps = append(ps, rng.Float64()*100)
			}
			want := linearPercentiles(g, ps)
			got := g.ValueAtPercentiles(append([]float64(nil), ps...))
			for _, p := range ps {
				if got[p] != want[p] {
					t.Fatalf("high %d round %d: ValueAtPercentiles[%v] = %d, linear reference %d", high, round, p, got[p], want[p])
				}
			}
		}
	}
}

// Import of a snapshot that fails Validate can wrap the total; the map variant then agrees
// with ValueAtPercentile, which makes the same non-negative, non-overflowing assumption.
func TestValueAtPercentilesWrappedImportMatchesSingle(t *testing.T) {
	s := New(1, 1000, 1).Export()
	s.Counts[0], s.Counts[1], s.Counts[2], s.Counts[9] = math.MaxInt64, math.MaxInt64, 2, 1
	if s.Validate() == nil {
		t.Fatal("setup: snapshot should fail Validate")
	}
	h := Import(s)
	got := h.ValueAtPercentiles([]float64{50, 99, 100})
	for _, p := range []float64{50, 99, 100} {
		if want := h.ValueAtPercentile(p); got[p] != want {
			t.Fatalf("p%v: ValueAtPercentiles %d, ValueAtPercentile %d", p, got[p], want)
		}
	}
}
