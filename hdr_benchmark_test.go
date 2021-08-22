package hdrhistogram_test

import (
	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"gonum.org/v1/gonum/stat/distuv"
	"math"
	"math/rand"
	"testing"
)

// nolint
func BenchmarkHistogramRecordValue(b *testing.B) {
	h := hdrhistogram.New(1, 10000000, 3)
	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		h.RecordValue(100)
	}
}

func BenchmarkNew(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		hdrhistogram.New(1, 120000, 3) // this could track 1ms-2min
	}
}

// nolint
func BenchmarkHistogramValueAtPercentile(b *testing.B) {
	rand.Seed(12345)
	var highestTrackableValue int64 = 1000000
	var lowestDiscernibleValue int64 = 1
	var sigfigs = 3
	var totalDatapoints = 1000000
	h, data := populateHistogramLogNormalDist(b, lowestDiscernibleValue, highestTrackableValue, sigfigs, totalDatapoints)
	quantiles := make([]float64, totalDatapoints)
	for i := range quantiles {
		data[i] = rand.Float64() * 100.0
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		h.ValueAtPercentile(data[i%totalDatapoints])
	}
}

// nolint
func BenchmarkHistogramValueAtPercentileGivenPercentileSlice(b *testing.B) {
	rand.Seed(12345)
	var highestTrackableValue int64 = 1000000
	var lowestDiscernibleValue int64 = 1
	var sigfigs = 3
	var totalDatapoints = 1000000
	h, data := populateHistogramLogNormalDist(b, lowestDiscernibleValue, highestTrackableValue, sigfigs, totalDatapoints)
	quantiles := make([]float64, b.N)
	for i := range quantiles {
		data[i] = rand.Float64() * 100.0
	}
	percentilesOfInterest := []float64{50.0, 95.0, 99.0, 99.9}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, percentile := range percentilesOfInterest {
			h.ValueAtPercentile(percentile)
		}
	}
}

// nolint
func BenchmarkHistogramValueAtPercentilesGivenPercentileSlice(b *testing.B) {
	rand.Seed(12345)
	var highestTrackableValue int64 = 1000000
	var lowestDiscernibleValue int64 = 1
	var sigfigs = 3
	var totalDatapoints = 1000000
	h, data := populateHistogramLogNormalDist(b, lowestDiscernibleValue, highestTrackableValue, sigfigs, totalDatapoints)
	quantiles := make([]float64, b.N)
	for i := range quantiles {
		data[i] = rand.Float64() * 100.0
	}
	percentilesOfInterest := []float64{50.0, 95.0, 99.0, 99.9}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		h.ValueAtPercentiles(percentilesOfInterest)
	}
}

func BenchmarkWindowedHistogramRecordAndRotate(b *testing.B) {
	w := hdrhistogram.NewWindowed(3, 1, 10000000, 3)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := w.Current.RecordValue(100); err != nil {
			b.Fatal(err)
		}

		if i%100000 == 1 {
			w.Rotate()
		}
	}
}

func BenchmarkWindowedHistogramMerge(b *testing.B) {
	w := hdrhistogram.NewWindowed(3, 1, 10000000, 3)
	for i := 0; i < 10000000; i++ {
		if err := w.Current.RecordValue(100); err != nil {
			b.Fatal(err)
		}

		if i%100000 == 1 {
			w.Rotate()
		}
	}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		w.Merge()
	}
}

func populateHistogramLogNormalDist(b *testing.B, lowestDiscernibleValue int64, highestTrackableValue int64, sigfigs int, totalDatapoints int) (*hdrhistogram.Histogram, []float64) {
	dist := distuv.LogNormal{Mu: 0.0, Sigma: 0.5}
	h := hdrhistogram.New(lowestDiscernibleValue, highestTrackableValue, sigfigs)
	data := make([]float64, totalDatapoints)

	// Draw some random values from the lognormal distribution
	min := math.MaxFloat64
	max := 0.0
	for i := range data {
		data[i] = dist.Rand()
		if data[i] < min {
			min = data[i]
		}
		if data[i] > max {
			max = data[i]
		}
	}
	k := float64(highestTrackableValue) / (max - min)
	for i := range data {
		v := k * data[i]
		if err := h.RecordValue(int64(v)); err != nil {
			b.Fatal(err)
		}
	}
	return h, data
}
