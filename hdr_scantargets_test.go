package hdrhistogram_test

import (
	"math/rand"
	"testing"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
)

// ValueAtPercentiles resolves all percentiles in one blocked pass; each result must equal
// the independent single-percentile query on the same histogram.
func TestValueAtPercentilesMatchesSingle(t *testing.T) {
	geometries := []struct {
		low, high int64
		sig       int
	}{{1, 1_000_000, 3}, {1, 1_000_000_000, 2}, {100, 100_000_000, 3}, {1, 10, 1}, {1, 3_600_000_000, 5}}
	rng := rand.New(rand.NewSource(1))
	for _, g := range geometries {
		for round := 0; round < 40; round++ {
			h := hdr.New(g.low, g.high, g.sig)
			switch round % 4 {
			case 0: // single value, including the last trackable one
				_ = h.RecordValue(g.high)
			case 1: // few values spread over the range
				for i := 0; i < 5; i++ {
					_ = h.RecordValue(rng.Int63n(g.high) + 1)
				}
			default: // many values, skewed low
				for i := 0; i < 20000; i++ {
					_ = h.RecordValues(rng.Int63n(rng.Int63n(g.high)+1)+1, rng.Int63n(4)+1)
				}
			}
			ps := []float64{0, 100, 100, 150, -3, 50, 99.9999}
			for i := rng.Intn(6); i > 0; i-- {
				ps = append(ps, rng.Float64()*100)
			}
			rng.Shuffle(len(ps), func(a, b int) { ps[a], ps[b] = ps[b], ps[a] })
			in := append([]float64(nil), ps...)
			got := h.ValueAtPercentiles(in)
			for _, p := range ps {
				if want := h.ValueAtPercentile(p); got[p] != want {
					t.Fatalf("geometry %+v round %d: ValueAtPercentiles[%v] = %d, ValueAtPercentile = %d", g, round, p, got[p], want)
				}
			}
		}
	}
}

func TestValueAtPercentilesEmptyInputs(t *testing.T) {
	h := hdr.New(1, 1_000_000, 3)
	if got := h.ValueAtPercentiles(nil); len(got) != 0 {
		t.Fatalf("nil percentiles on empty histogram: %v", got)
	}
	_ = h.RecordValue(42)
	if got := h.ValueAtPercentiles([]float64{}); len(got) != 0 {
		t.Fatalf("empty percentiles: %v", got)
	}
}
