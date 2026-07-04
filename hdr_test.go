package hdrhistogram_test

import (
	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"github.com/stretchr/testify/assert"
	"math"
	"reflect"
	"testing"
)

// nolint
func TestHighSigFig(t *testing.T) {
	input := []int64{
		459876, 669187, 711612, 816326, 931423, 1033197, 1131895, 2477317,
		3964974, 12718782,
	}

	hist := hdrhistogram.New(459876, 12718782, 5)
	for _, sample := range input {
		hist.RecordValue(sample)
	}

	if v, want := hist.ValueAtQuantile(50), int64(1048575); v != want {
		t.Errorf("Median was %v, but expected %v", v, want)
	}
}

func TestValueAtQuantile(t *testing.T) {
	h := hdrhistogram.New(1, 10000000, 3)

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	data := []struct {
		q float64
		v int64
	}{
		{q: 50, v: 500223},
		{q: 75, v: 750079},
		{q: 90, v: 900095},
		{q: 95, v: 950271},
		{q: 99, v: 990207},
		{q: 99.9, v: 999423},
		{q: 99.99, v: 999935},
	}

	for _, d := range data {
		if v := h.ValueAtQuantile(d.q); v != d.v {
			t.Errorf("P%v was %v, but expected %v", d.q, v, d.v)
		}
	}
}

func TestMean(t *testing.T) {
	h := hdrhistogram.New(1, 10000000, 3)
	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	assert.InDelta(t, 500000, h.Mean(), 500000*0.001)
}

func TestStdDev(t *testing.T) {
	h := hdrhistogram.New(1, 10000000, 3)
	total := 0.0
	for i := 0; i < 1000000; i++ {
		total += math.Pow(float64(i-500000.0), 2)
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	variance := total / float64(1000000-1)
	stdDev := math.Sqrt(variance)
	assert.InDelta(t, stdDev, h.StdDev(), stdDev*0.001)
}

func TestTotalCount(t *testing.T) {
	h := hdrhistogram.New(1, 10000000, 3)

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
		if v, want := h.TotalCount(), int64(i+1); v != want {
			t.Errorf("TotalCount was %v, but expected %v", v, want)
		}
	}
}

func TestMax(t *testing.T) {
	h := hdrhistogram.New(1, 10000000, 3)

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	if v, want := h.Max(), int64(1000447); v != want {
		t.Errorf("Max was %v, but expected %v", v, want)
	}
}

func TestReset(t *testing.T) {
	h := hdrhistogram.New(1, 10000000, 3)

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	h.Reset()

	if v, want := h.Max(), int64(0); v != want {
		t.Errorf("Max was %v, but expected %v", v, want)
	}
}

func TestMerge(t *testing.T) {
	h1 := hdrhistogram.New(1, 1000, 3)
	h2 := hdrhistogram.New(1, 1000, 3)

	for i := 0; i < 100; i++ {
		if err := h1.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	for i := 100; i < 200; i++ {
		if err := h2.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	h1.Merge(h2)

	if v, want := h1.ValueAtQuantile(50), int64(99); v != want {
		t.Errorf("Median was %v, but expected %v", v, want)
	}
}

func TestMin(t *testing.T) {
	h := hdrhistogram.New(1, 10000000, 3)

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	if v, want := h.Min(), int64(0); v != want {
		t.Errorf("Min was %v, but expected %v", v, want)
	}
}

func TestHistogram_ValueAtPercentiles(t *testing.T) {
	h := hdrhistogram.New(1, 3600*1000*1000, 3)

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	// Ensure calculating the percentiles altogether returns the same values
	// multiple calls to ValueAtQuantile()
	values := h.ValueAtPercentiles([]float64{0.0, 50.0, 95.0, 99.0, 100.0})
	assert.Equal(t, h.ValueAtQuantile(0.0), values[0.0])
	assert.Equal(t, h.ValueAtQuantile(50.0), values[50.0])
	assert.Equal(t, h.ValueAtQuantile(95.0), values[95.0])
	assert.Equal(t, h.ValueAtQuantile(99.0), values[99.0])
	assert.Equal(t, h.ValueAtQuantile(100.0), values[100.0])

	// negative test using out of bounds percentiles
	assert.Equal(t, h.ValueAtQuantile(110.0), h.ValueAtPercentiles([]float64{110.0})[110.0])
	// assert upper bound is enforced
	assert.Equal(t, h.ValueAtQuantile(100.0), h.ValueAtPercentiles([]float64{110.0})[110.0])
	assert.Equal(t, h.ValueAtQuantile(-1.0), h.ValueAtPercentiles([]float64{-1.0})[-1.0])
	// assert lower bound is enforced
	assert.Equal(t, h.ValueAtQuantile(0.0), h.ValueAtPercentiles([]float64{-1.0})[-1.0])
	assert.Equal(t, int64(0), h.ValueAtPercentiles([]float64{-1.0})[-1.0])
	h.Reset()
	for i := 0; i < 10000; i++ {
		if err := h.RecordValue(int64(1000)); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.RecordValue(int64(100000000)); err != nil {
		t.Fatal(err)
	}
	// ensure that percentiles that are calculated using the count number will be properly computed
	values = h.ValueAtPercentiles([]float64{30.0, 99.0, 99.99, 99.999, 100.0})
	assert.Equal(t, h.ValueAtQuantile(30.0), values[30.0])
	assert.Equal(t, h.ValueAtQuantile(99.0), values[99.0])
	assert.Equal(t, h.ValueAtQuantile(99.99), values[99.99])
	assert.Equal(t, h.ValueAtQuantile(99.999), values[99.999])
	assert.Equal(t, h.ValueAtQuantile(100.0), values[100.0])
}

// Regression: an empty histogram with lowestDiscernibleValue > 1 (unitMagnitude > 0)
// must return zeros for every percentile from ValueAtPercentiles, per its documented
// contract ("Returns a map of 0's if no recorded values exist").
func TestValueAtPercentiles_EmptyHistogram(t *testing.T) {
	h := hdrhistogram.New(100, 10000000, 3) // unitMagnitude = floor(log2(100)) = 6
	pcts := []float64{0.0, 50.0, 90.0, 99.0, 100.0}
	got := h.ValueAtPercentiles(pcts)
	for _, p := range pcts {
		assert.Equal(t, int64(0), got[p], "empty histogram, percentile %v", p)
	}
}

func TestByteSize(t *testing.T) {
	h := hdrhistogram.New(1, 100000, 3)

	if v, want := h.ByteSize(), 65604; v != want {
		t.Errorf("ByteSize was %v, but expected %d", v, want)
	}
}

func TestRecordCorrectedValue(t *testing.T) {
	h := hdrhistogram.New(1, 100000, 3)

	if err := h.RecordCorrectedValue(10, 100); err != nil {
		t.Fatal(err)
	}

	if v, want := h.ValueAtQuantile(75), int64(10); v != want {
		t.Errorf("Corrected value was %v, but expected %v", v, want)
	}
}

func TestRecordCorrectedValueStall(t *testing.T) {
	h := hdrhistogram.New(1, 100000, 3)

	if err := h.RecordCorrectedValue(1000, 100); err != nil {
		t.Fatal(err)
	}

	if v, want := h.ValueAtQuantile(75), int64(800); v != want {
		t.Errorf("Corrected value was %v, but expected %v", v, want)
	}
}

func TestRecordValuesRejectsNegativeCount(t *testing.T) {
	h := hdrhistogram.New(1, 100000, 3)
	if err := h.RecordValue(50); err != nil {
		t.Fatal(err)
	}
	before := h.TotalCount()

	// A negative count must be rejected, not silently subtracted (which would
	// drive counts[idx] and TotalCount negative and corrupt every query).
	if err := h.RecordValues(50, -5); err == nil {
		t.Fatal("RecordValues with a negative count should return an error")
	}
	if got := h.TotalCount(); got != before {
		t.Errorf("TotalCount changed after a rejected negative record: got %d, want %d", got, before)
	}

	// A negative count on an otherwise-empty histogram must not go negative.
	empty := hdrhistogram.New(1, 100000, 3)
	if err := empty.RecordValues(50, -3); err == nil {
		t.Fatal("RecordValues with a negative count should return an error")
	}
	if got := empty.TotalCount(); got != 0 {
		t.Errorf("TotalCount went negative: got %d, want 0", got)
	}

	// n == 0 is a legal no-op.
	if err := empty.RecordValues(50, 0); err != nil {
		t.Errorf("RecordValues with count 0 should be a no-op, got error: %v", err)
	}
	if got := empty.TotalCount(); got != 0 {
		t.Errorf("TotalCount after a zero-count record: got %d, want 0", got)
	}
}

func TestCumulativeDistribution(t *testing.T) {
	h := hdrhistogram.New(1, 100000000, 3)

	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	actual := h.CumulativeDistribution()
	expected := []hdrhistogram.Bracket{
		hdrhistogram.Bracket{Quantile: 0, Count: 1, ValueAt: 0},
		hdrhistogram.Bracket{Quantile: 50, Count: 500224, ValueAt: 500223},
		hdrhistogram.Bracket{Quantile: 75, Count: 750080, ValueAt: 750079},
		hdrhistogram.Bracket{Quantile: 87.5, Count: 875008, ValueAt: 875007},
		hdrhistogram.Bracket{Quantile: 93.75, Count: 937984, ValueAt: 937983},
		hdrhistogram.Bracket{Quantile: 96.875, Count: 969216, ValueAt: 969215},
		hdrhistogram.Bracket{Quantile: 98.4375, Count: 984576, ValueAt: 984575},
		hdrhistogram.Bracket{Quantile: 99.21875, Count: 992256, ValueAt: 992255},
		hdrhistogram.Bracket{Quantile: 99.609375, Count: 996352, ValueAt: 996351},
		hdrhistogram.Bracket{Quantile: 99.8046875, Count: 998400, ValueAt: 998399},
		hdrhistogram.Bracket{Quantile: 99.90234375, Count: 999424, ValueAt: 999423},
		hdrhistogram.Bracket{Quantile: 99.951171875, Count: 999936, ValueAt: 999935},
		hdrhistogram.Bracket{Quantile: 99.9755859375, Count: 999936, ValueAt: 999935},
		hdrhistogram.Bracket{Quantile: 99.98779296875, Count: 999936, ValueAt: 999935},
		hdrhistogram.Bracket{Quantile: 99.993896484375, Count: 1000000, ValueAt: 1000447},
		hdrhistogram.Bracket{Quantile: 100, Count: 1000000, ValueAt: 1000447},
	}

	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("CF was %#v, but expected %#v", actual, expected)
	}
}

func TestDistribution(t *testing.T) {
	h := hdrhistogram.New(8, 1024, 3)

	for i := 0; i < 1024; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	actual := h.Distribution()
	if len(actual) != 128 {
		t.Errorf("Number of bars seen was %v, expected was 128", len(actual))
	}
	for _, b := range actual {
		if b.Count != 8 {
			t.Errorf("Count per bar seen was %v, expected was 8", b.Count)
		}
	}
}

func TestNaN(t *testing.T) {
	h := hdrhistogram.New(1, 100000, 3)
	if math.IsNaN(h.Mean()) {
		t.Error("mean is NaN")
	}
	if math.IsNaN(h.StdDev()) {
		t.Error("stddev is NaN")
	}
}

func TestSignificantFigures(t *testing.T) {
	const sigFigs = 4
	h := hdrhistogram.New(1, 10, sigFigs)
	if h.SignificantFigures() != sigFigs {
		t.Errorf("Significant figures was %v, expected %d", h.SignificantFigures(), sigFigs)
	}
}

func TestLowestTrackableValue(t *testing.T) {
	const minVal = 2
	h := hdrhistogram.New(minVal, 10, 3)
	if h.LowestTrackableValue() != minVal {
		t.Errorf("LowestTrackableValue figures was %v, expected %d", h.LowestTrackableValue(), minVal)
	}
}

func TestHighestTrackableValue(t *testing.T) {
	const maxVal = 11
	h := hdrhistogram.New(1, maxVal, 3)
	if h.HighestTrackableValue() != maxVal {
		t.Errorf("HighestTrackableValue figures was %v, expected %d", h.HighestTrackableValue(), maxVal)
	}
}

func TestUnitMagnitudeOverflow(t *testing.T) {
	h := hdrhistogram.New(0, 200, 4)
	if err := h.RecordValue(11); err != nil {
		t.Fatal(err)
	}
}

// nolint
func TestSubBucketMaskOverflow(t *testing.T) {
	hist := hdrhistogram.New(2e7, 1e8, 5)
	for _, sample := range [...]int64{1e8, 2e7, 3e7} {
		hist.RecordValue(sample)
	}

	for q, want := range map[float64]int64{
		50:    33554431,
		83.33: 33554431,
		83.34: 100663295,
		99:    100663295,
	} {
		if got := hist.ValueAtQuantile(q); got != want {
			t.Errorf("got %d for %fth percentile. want: %d", got, q, want)
		}
	}
}

func TestExportImport(t *testing.T) {
	min := int64(1)
	max := int64(10000000)
	sigfigs := 3
	h := hdrhistogram.New(min, max, sigfigs)
	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	s := h.Export()

	if v := s.LowestTrackableValue; v != min {
		t.Errorf("LowestTrackableValue was %v, but expected %v", v, min)
	}

	if v := s.HighestTrackableValue; v != max {
		t.Errorf("HighestTrackableValue was %v, but expected %v", v, max)
	}

	if v := int(s.SignificantFigures); v != sigfigs {
		t.Errorf("SignificantFigures was %v, but expected %v", v, sigfigs)
	}

	if imported := hdrhistogram.Import(s); !imported.Equals(h) {
		t.Error("Expected Histograms to be equivalent")
	}

}

// TestImportLengthMismatch ensures Import tolerates a Snapshot whose Counts
// length does not match the histogram geometry: a longer slice is truncated to
// the geometry, a shorter one leaves the trailing buckets zero — neither panics.
func TestImportLengthMismatch(t *testing.T) {
	min, max, sig := int64(1), int64(10000000), 3
	ref := hdrhistogram.New(min, max, sig)
	for i := int64(1); i <= 100000; i++ {
		if err := ref.RecordValue(i); err != nil {
			t.Fatal(err)
		}
	}
	full := ref.Export()

	// Longer Counts: append surplus buckets that must be ignored.
	longer := &hdrhistogram.Snapshot{
		LowestTrackableValue:  full.LowestTrackableValue,
		HighestTrackableValue: full.HighestTrackableValue,
		SignificantFigures:    full.SignificantFigures,
		Counts:                append(append([]int64(nil), full.Counts...), 7, 7, 7),
	}
	if got := hdrhistogram.Import(longer); !got.Equals(ref) {
		t.Error("oversized Snapshot should import to the same histogram (surplus truncated)")
	}

	// Shorter Counts: truncate; Import must not panic and must sum what's present.
	shortLen := len(full.Counts) / 2
	shorter := &hdrhistogram.Snapshot{
		LowestTrackableValue:  full.LowestTrackableValue,
		HighestTrackableValue: full.HighestTrackableValue,
		SignificantFigures:    full.SignificantFigures,
		Counts:                append([]int64(nil), full.Counts[:shortLen]...),
	}
	var wantTotal int64
	for _, c := range full.Counts[:shortLen] {
		if c > 0 {
			wantTotal += c
		}
	}
	got := hdrhistogram.Import(shorter) // must not panic
	if got.TotalCount() != wantTotal {
		t.Errorf("truncated Snapshot TotalCount = %d, want %d", got.TotalCount(), wantTotal)
	}
}

func TestEquals(t *testing.T) {
	h1 := hdrhistogram.New(1, 10000000, 3)
	for i := 0; i < 1000000; i++ {
		if err := h1.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	h2 := hdrhistogram.New(1, 10000000, 3)
	for i := 0; i < 10000; i++ {
		if err := h1.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	if h1.Equals(h2) {
		t.Error("Expected Histograms to not be equivalent")
	}

	h1.Reset()
	h2.Reset()

	if !h1.Equals(h2) {
		t.Error("Expected Histograms to be equivalent")
	}
}

// nolint
func TestHistogram_ValuesAreEquivalent(t *testing.T) {
	hist := hdrhistogram.New(1476573605, 1476593605, 3)
	assert.True(t, hist.ValuesAreEquivalent(1476583605, 2147483647))

	// test large histograms
	hist = hdrhistogram.New(20000000, 100000000, 5)
	hist.RecordValue(100000000)
	hist.RecordValue(20000000)
	hist.RecordValue(30000000)
	assert.True(t, hist.ValuesAreEquivalent(20000000, hist.ValueAtQuantile(50.0)))
	assert.True(t, hist.ValuesAreEquivalent(100000000, hist.ValueAtQuantile(83.34)))
	assert.True(t, hist.ValuesAreEquivalent(100000000, hist.ValueAtQuantile(99.0)))
}

// ValueAtPercentilesSlice must equal ValueAtPercentiles (map) and the singular
// ValueAtPercentile, in input order, for unsorted + duplicate + edge inputs.
func TestValueAtPercentilesSlice(t *testing.T) {
	h := hdrhistogram.New(1, 3600*1000*1000, 3)
	for i := 0; i < 1000000; i++ {
		if err := h.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	pcts := []float64{99.0, 0.0, 50.0, 99.0, 100.0, 99.99, 150.0, 50.0}
	// copy because ValueAtPercentiles sorts in place
	pctsCopy := append([]float64(nil), pcts...)
	slice := h.ValueAtPercentilesSlice(pcts)
	m := h.ValueAtPercentiles(pctsCopy)
	for i, p := range pcts {
		if p > 100 {
			p = 100
		}
		assert.Equal(t, h.ValueAtPercentile(p), slice[i], "slice vs singular p=%v idx=%d", p, i)
		assert.Equal(t, m[p], slice[i], "slice vs map p=%v idx=%d", p, i)
	}
	assert.Empty(t, h.ValueAtPercentilesSlice(nil))

	// empty histogram -> all zeros
	e := hdrhistogram.New(100, 10000000, 3)
	for _, v := range e.ValueAtPercentilesSlice([]float64{0, 50, 99, 100}) {
		assert.Equal(t, int64(0), v)
	}

	// Negative percentiles clamp to the 0th percentile (documented contract). With
	// unitMagnitude > 0 (New(100, ...)) the 0th percentile is the lowest equivalent
	// value, NOT highestEquivalentValue(0); a negative input must not leak the latter.
	hc := hdrhistogram.New(100, 10000000, 3)
	for i := int64(100); i <= 100000; i += 100 {
		if err := hc.RecordValue(i); err != nil {
			t.Fatal(err)
		}
	}
	neg := hc.ValueAtPercentilesSlice([]float64{-1, -0.0001, 0.0, -100})
	want := hc.ValueAtPercentile(0)
	for i, got := range neg {
		assert.Equal(t, want, got, "negative/zero percentile must equal ValueAtPercentile(0), idx=%d", i)
	}
}

// Regression for the percentile clamping contracts (C5/C6):
//   - ValueAtPercentiles must key the result map only by the caller's percentiles,
//     with no phantom key from clamping >100 inputs.
//   - Negative percentiles clamp to the 0th percentile (lowest equivalent), not
//     highestEquivalentValue(0), which leaks e.g. 63 for New(100, ...).
func TestValueAtPercentiles_ClampingContracts(t *testing.T) {
	// Phantom-key: input {150} must yield exactly {150: ...}, never a stray 100 key.
	h := hdrhistogram.New(1, 3600*1000*1000, 3)
	for i := int64(0); i < 100000; i++ {
		if err := h.RecordValue(i); err != nil {
			t.Fatal(err)
		}
	}
	m := h.ValueAtPercentiles([]float64{150})
	if len(m) != 1 {
		t.Fatalf("ValueAtPercentiles([150]) returned %d keys %v, want exactly 1 (150)", len(m), m)
	}
	if _, ok := m[150]; !ok {
		t.Fatalf("ValueAtPercentiles([150]) missing key 150: %v", m)
	}
	if _, ok := m[100]; ok {
		t.Fatalf("ValueAtPercentiles([150]) has phantom key 100: %v", m)
	}
	// A clamped-high input returns the p100 value.
	if m[150] != h.ValueAtPercentile(100) {
		t.Fatalf("ValueAtPercentiles([150])[150] = %d, want p100 %d", m[150], h.ValueAtPercentile(100))
	}

	// Negative clamp with unitMagnitude > 0 (New(100, ...)): the 63 leak case.
	hc := hdrhistogram.New(100, 10000000, 3)
	for i := int64(100); i <= 100000; i += 100 {
		if err := hc.RecordValue(i); err != nil {
			t.Fatal(err)
		}
	}
	want := hc.ValueAtPercentile(0)
	if got := hc.ValueAtPercentile(-1); got != want {
		t.Fatalf("ValueAtPercentile(-1) = %d, want 0th-percentile %d (highestEquivalentValue(0) leak?)", got, want)
	}
	if got := hc.ValueAtPercentiles([]float64{-5})[-5]; got != want {
		t.Fatalf("ValueAtPercentiles([-5])[-5] = %d, want 0th-percentile %d", got, want)
	}
}

// Regression for issue #60: an empty histogram with lowestDiscernibleValue > 1
// (unitMagnitude > 0) must return 0 for every percentile, not highestEquivalentValue(0).
func TestValueAtPercentile_EmptyHistogramReturnsZero(t *testing.T) {
	h := hdrhistogram.New(100, 10000000, 3)
	if h.TotalCount() != 0 {
		t.Fatalf("precondition: TotalCount = %d, want 0", h.TotalCount())
	}
	for _, p := range []float64{0, 25, 50, 90, 99, 100} {
		if got := h.ValueAtPercentile(p); got != 0 {
			t.Errorf("empty ValueAtPercentile(%v) = %d, want 0", p, got)
		}
	}
}

// Reset must restore the histogram to its original state, including the metadata
// (tag, start/end time), not just the recorded counts.
func TestResetClearsMetadata(t *testing.T) {
	h := hdrhistogram.New(1, 1000000, 3)
	h.SetTag("svcA")
	h.SetStartTimeMs(111)
	h.SetEndTimeMs(222)
	if err := h.RecordValue(500); err != nil {
		t.Fatal(err)
	}
	h.Reset()
	if h.Tag() != "" {
		t.Errorf("Reset did not clear tag: %q", h.Tag())
	}
	if h.StartTimeMs() != 0 {
		t.Errorf("Reset did not clear startTimeMs: %d", h.StartTimeMs())
	}
	if h.EndTimeMs() != 0 {
		t.Errorf("Reset did not clear endTimeMs: %d", h.EndTimeMs())
	}
	if h.TotalCount() != 0 {
		t.Errorf("Reset did not clear totalCount: %d", h.TotalCount())
	}
}
