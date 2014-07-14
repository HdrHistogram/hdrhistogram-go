package hdrhistogram

import (
	"reflect"
	"testing"
)

func TestValueAtQuantile(t *testing.T) {
	h, err := NewHDRHistogram(1, 10000000, 3)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	if v := h.ValueAtQuantile(50); v != 500223 {
		t.Errorf("P50 was %v, but expected 500223", v)
	}

	if v := h.ValueAtQuantile(90); v != 900095 {
		t.Errorf("P90 was %v, but expected 900095", v)
	}

	if v := h.ValueAtQuantile(95); v != 950271 {
		t.Errorf("P95 was %v, but expected 950271", v)
	}

	if v := h.ValueAtQuantile(99); v != 990207 {
		t.Errorf("P99 was %v, but expected 990207", v)
	}

	if v := h.ValueAtQuantile(99.9); v != 999423 {
		t.Errorf("P99.9 was %v, but expected 999423", v)
	}

	if v := h.ValueAtQuantile(99.99); v != 999935 {
		t.Errorf("P99.99 was %v, but expected 999935", v)
	}

	if v := h.ValueAtQuantile(99.999); v != 1000447 {
		t.Errorf("P99.999 was %v, but expected 1000447", v)
	}
}

func TestMean(t *testing.T) {
	h, err := NewHDRHistogram(1, 10000000, 3)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	if v := h.Mean(); v != 500000.013312 {
		t.Errorf("Mean was %v, but expected ~500000", v)
	}
}

func TestStdDev(t *testing.T) {
	h, err := NewHDRHistogram(1, 10000000, 3)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	if v := h.StdDev(); v != 288675.1403682715 {
		t.Errorf("StdDev was %v, but expected ~288675", v)
	}
}

func TestMax(t *testing.T) {
	h, err := NewHDRHistogram(1, 10000000, 3)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	if v := h.Max(); v != 999936 {
		t.Errorf("Max was %v, but expected 999936", v)
	}
}

func TestReset(t *testing.T) {
	h, err := NewHDRHistogram(1, 10000000, 3)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	h.Reset()

	if v := h.Max(); v != 0 {
		t.Errorf("Max was %v, but expected 0", v)
	}
}

func TestMerge(t *testing.T) {
	h1, err := NewHDRHistogram(1, 10000000, 3)
	if err != nil {
		t.Fatal(err)
	}

	h2, err := NewHDRHistogram(1, 10000000, 3)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 1000000; i++ {
		if i%2 == 0 {
			if err := h1.RecordValue(int64(i)); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := h2.RecordValue(int64(i)); err != nil {
				t.Fatal(err)
			}
		}
	}

	h1.Merge(h2)

	if v := h1.StdDev(); v != 288675.1421382609 {
		t.Errorf("StdDev was %v, but expected ~288675", v)
	}
}

func TestMin(t *testing.T) {
	h, err := NewHDRHistogram(1, 10000000, 3)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	if v := h.Min(); v != 0 {
		t.Errorf("Min was %v, but expected 0", v)
	}
}

func TestCumulativeDistribution(t *testing.T) {
	h, err := NewHDRHistogram(1, 100000000, 3)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	actual := h.CumulativeDistribution()
	expected := []Bracket{
		Bracket{Quantile: 0, Count: 1},
		Bracket{Quantile: 50, Count: 500224},
		Bracket{Quantile: 75, Count: 750080},
		Bracket{Quantile: 87.5, Count: 875008},
		Bracket{Quantile: 93.75, Count: 937984},
		Bracket{Quantile: 96.875, Count: 969216},
		Bracket{Quantile: 98.4375, Count: 984576},
		Bracket{Quantile: 99.21875, Count: 992256},
		Bracket{Quantile: 99.609375, Count: 996352},
		Bracket{Quantile: 99.8046875, Count: 998400},
		Bracket{Quantile: 99.90234375, Count: 999424},
		Bracket{Quantile: 99.951171875, Count: 999936},
		Bracket{Quantile: 99.9755859375, Count: 999936},
		Bracket{Quantile: 99.98779296875, Count: 999936},
		Bracket{Quantile: 99.993896484375, Count: 1000000},
		Bracket{Quantile: 100, Count: 1000000},
	}

	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("CF was %#v, but expected %#v", actual, expected)
	}
}

func BenchmarkRecordValue(b *testing.B) {
	h, err := NewHDRHistogram(1, 10000000, 3)
	if err != nil {
		b.Fatal(err)
	}
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
