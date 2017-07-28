package hdrhistogram_test

import (
	"math/rand"
	"testing"

	"github.com/codahale/hdrhistogram"
)

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

func BenchmarkWindowedHistogramRotateAndMerge(b *testing.B) {
	rnd := rand.NewSource(32)
	minValue := int64(0)
	maxValue := int64(10000000)
	valuesPerHistogram := 100000
	valueRange := maxValue - minValue
	windowSize := 100
	w := hdrhistogram.NewWindowed(windowSize, minValue, maxValue, 2)

	for i := 0; i < windowSize; i++ {
		for j := 0; j < valuesPerHistogram; j++ {
			err := w.Current.RecordValue(rnd.Int63()%valueRange + minValue)
			if err != nil {
				b.Fatal(err)
			}
		}
		w.Rotate()
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StartTimer()
		w.Rotate()
		b.StopTimer()
		for j := 0; j < valuesPerHistogram; j++ {
			err := w.Current.RecordValue(rnd.Int63()%valueRange + minValue)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
