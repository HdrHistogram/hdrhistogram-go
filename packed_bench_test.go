package hdrhistogram

import (
	"math"
	"math/rand"
	"testing"
)

// realistic skewed latency sample producing a sparse population.
func packedSamples(n int, spread float64) []int64 {
	r := rand.New(rand.NewSource(99))
	s := make([]int64, n)
	for i := range s {
		u := r.Float64()
		v := 200.0 * math.Exp(spread*u)
		if v > 1e9 {
			v = 1e9
		}
		if v < 1 {
			v = 1
		}
		s[i] = int64(v)
	}
	return s
}

func newPopulatedPacked(spread float64) *PackedHistogram {
	p := NewPacked(1, 1000000000, 2)
	for _, v := range packedSamples(500000, spread) {
		_ = p.RecordValue(v)
	}
	return p
}

func BenchmarkPackedRecord(b *testing.B) {
	p := newPopulatedPacked(2.2)
	s := packedSamples(1<<20, 2.2)
	mask := len(s) - 1
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.RecordValue(s[i&mask])
	}
}

func BenchmarkPackedValueAtPercentile(b *testing.B) {
	p := newPopulatedPacked(2.2)
	pcts := []float64{50, 99, 99.9}
	var sink int64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink += p.ValueAtPercentile(pcts[i%3])
	}
	_ = sink
}

func BenchmarkPackedValueAtPercentilesSlice(b *testing.B) {
	p := newPopulatedPacked(2.2)
	pcts := []float64{50, 99, 99.9}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.ValueAtPercentilesSlice(pcts)
	}
}

func BenchmarkPackedEncode(b *testing.B) {
	p := newPopulatedPacked(2.2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.Encode()
	}
}
