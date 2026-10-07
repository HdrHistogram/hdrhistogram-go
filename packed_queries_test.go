package hdrhistogram

import (
	"fmt"
	"math"
	"testing"
)

func assertPackedStatistic(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > 1e-12*math.Max(1, math.Abs(want)) {
		t.Fatalf("%s = %.17g, want %.17g", name, got, want)
	}
}

func TestPackedStatisticsMatchDense(t *testing.T) {
	for _, low := range []int64{1, 1024} {
		for _, count := range []int64{1, 256, 65536, 1 << 32} {
			t.Run(fmt.Sprintf("low=%d/count=%d", low, count), func(t *testing.T) {
				p, d := NewPacked(low, math.MaxInt64, 2), New(low, math.MaxInt64, 2)
				for i, v := range []int64{0, 100, 2000, 1000000, 1 << 60, math.MaxInt64} {
					n := count + int64(i)
					if err := p.RecordValues(v, n); err != nil {
						t.Fatal(err)
					}
					if err := d.RecordValues(v, n); err != nil {
						t.Fatal(err)
					}
				}
				assertPackedStatistic(t, "Mean", p.Mean(), d.Mean())
				assertPackedStatistic(t, "StdDev", p.StdDev(), d.StdDev())
			})
		}
	}
}

func TestPackedStatisticsEmptyAndBucketMedians(t *testing.T) {
	p := NewPacked(1024, math.MaxInt64, 2)
	assertPackedStatistic(t, "empty Mean", p.Mean(), 0)
	assertPackedStatistic(t, "empty StdDev", p.StdDev(), 0)
	if err := p.RecordValue(0); err != nil {
		t.Fatal(err)
	}
	assertPackedStatistic(t, "coarse bucket Mean", p.Mean(), 512)
	assertPackedStatistic(t, "single bucket StdDev", p.StdDev(), 0)
	if err := p.RecordValue(2048); err != nil {
		t.Fatal(err)
	}
	assertPackedStatistic(t, "coarse Mean", p.Mean(), 1536)
	assertPackedStatistic(t, "coarse StdDev", p.StdDev(), 1024)
	p.Reset()
	assertPackedStatistic(t, "reset Mean", p.Mean(), 0)
	assertPackedStatistic(t, "reset StdDev", p.StdDev(), 0)

	// With one significant digit, the top bucket spans 2^58 values. Its
	// midpoint is MaxInt64 + 1 - 2^57, without an overflowing endpoint sum.
	p = NewPacked(1, math.MaxInt64, 1)
	if err := p.RecordValues(math.MaxInt64, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	assertPackedStatistic(t, "top bucket Mean", p.Mean(), float64(math.MaxInt64-(1<<57)+1))
	assertPackedStatistic(t, "top bucket StdDev", p.StdDev(), 0)
}

func TestPackedStatisticsSaturatedDecodedCounts(t *testing.T) {
	// Equal counts at values 0 and 100 exceed int64 in aggregate. Normalizing
	// by the saturated TotalCount would incorrectly give mean=100, stddev=100.
	payload := zig_zag_encode_i64(math.MaxInt64)
	payload = append(payload, zig_zag_encode_i64(-99)...)
	payload = append(payload, zig_zag_encode_i64(math.MaxInt64)...)
	p, err := DecodePacked(wrapV2Payload(t, payload))
	if err != nil {
		t.Fatal(err)
	}
	if p.TotalCount() != math.MaxInt64 {
		t.Fatalf("TotalCount = %d, want saturation", p.TotalCount())
	}
	assertPackedStatistic(t, "decoded Mean", p.Mean(), 50)
	assertPackedStatistic(t, "decoded StdDev", p.StdDev(), 50)
}

func TestPackedStatisticsDoNotAllocate(t *testing.T) {
	p := NewPacked(1, math.MaxInt64, 5)
	for _, v := range []int64{10, 10000, 1 << 60} {
		if err := p.RecordValue(v); err != nil {
			t.Fatal(err)
		}
	}
	var mean, stddev float64
	if allocs := testing.AllocsPerRun(100, func() { mean, stddev = p.Mean(), p.StdDev() }); allocs != 0 {
		t.Fatalf("statistics allocated %g times", allocs)
	}
	if mean <= 0 || stddev <= 0 || p.geom.counts != nil {
		t.Fatal("statistics did not preserve sparse storage and positive results")
	}
}

func TestPackedGeometryGetters(t *testing.T) {
	for _, cfg := range []struct {
		low, high int64
		sig       int
	}{{0, 1000000, -1}, {1, 1000000, 6}, {1000, 1 << 40, 3}} {
		p := NewPacked(cfg.low, cfg.high, cfg.sig)
		d := New(cfg.low, cfg.high, cfg.sig)
		if p.LowestTrackableValue() != d.LowestTrackableValue() || p.HighestTrackableValue() != d.HighestTrackableValue() || p.SignificantFigures() != d.SignificantFigures() {
			t.Fatalf("packed getters differ from dense for %+v", cfg)
		}
		if err := p.RecordValues(12345, 10); err != nil {
			t.Fatal(err)
		}
		encoded, err := p.Encode()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodePacked(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.LowestTrackableValue() != p.LowestTrackableValue() || decoded.HighestTrackableValue() != p.HighestTrackableValue() || decoded.SignificantFigures() != p.SignificantFigures() {
			t.Fatal("decoded geometry getters differ from original")
		}
		sibling := NewPacked(decoded.LowestTrackableValue(), decoded.HighestTrackableValue(), int(decoded.SignificantFigures()))
		if dropped := sibling.Merge(decoded); dropped != 0 {
			t.Fatalf("sibling merge dropped %d", dropped)
		}
		if sibling.TotalCount() != 10 || sibling.CountAtValue(12345) != 10 || sibling.geom.counts != nil {
			t.Fatal("sibling did not preserve counts and sparse storage")
		}
	}
}

func TestPackedClonePreservesJavaZeroDigitGeometry(t *testing.T) {
	p, err := DecodePacked([]byte(javaZeroDigits1000))
	if err != nil {
		t.Fatal(err)
	}
	p.SetTag("foreign")
	clone := p.Clone()
	if clone.SignificantFigures() != 0 || clone.LowestTrackableValue() != 1 || clone.HighestTrackableValue() != 1000 || clone.Tag() != "foreign" {
		t.Fatal("clone changed the decoded geometry or metadata")
	}
	assertPackedStatistic(t, "zero-digit Mean", clone.Mean(), 96)
	assertPackedStatistic(t, "zero-digit StdDev", clone.StdDev(), 0)
	clone.Reset()
	if err := clone.RecordValue(100); err != nil {
		t.Fatal(err)
	}
	if clone.SignificantFigures() != 0 || clone.Min() != 64 || clone.Max() != 127 || clone.CountAtValue(100) != 1 {
		t.Fatal("empty sibling failed to preserve zero-digit bucket mapping")
	}
	if p.TotalCount() != 1 || p.CountAtValue(100) != 1 || p.Tag() != "foreign" {
		t.Fatal("reset/record on clone changed original")
	}
}

func TestPackedRecordCorrectedValueMatchesDense(t *testing.T) {
	for _, tc := range []struct{ value, interval int64 }{
		{100, 10}, {105, 10}, {100, 0}, {100, -10}, {100, math.MinInt64}, {100, 100}, {100, 101}, {0, 10},
	} {
		t.Run(fmt.Sprintf("%d/%d", tc.value, tc.interval), func(t *testing.T) {
			p, d := NewPacked(1, 1000, 3), New(1, 1000, 3)
			if err := p.RecordCorrectedValue(tc.value, tc.interval); err != nil {
				t.Fatal(err)
			}
			if err := d.RecordCorrectedValue(tc.value, tc.interval); err != nil {
				t.Fatal(err)
			}
			if p.TotalCount() != d.TotalCount() {
				t.Fatalf("TotalCount = %d, want %d", p.TotalCount(), d.TotalCount())
			}
			for v := int64(0); v <= tc.value; v++ {
				if got, want := p.CountAtValue(v), d.counts[d.countsIndexFor(v)]; got != want {
					t.Fatalf("count at %d = %d, want %d", v, got, want)
				}
			}
			if p.geom.counts != nil {
				t.Fatal("correction allocated dense counts")
			}
		})
	}
}

func TestPackedRecordCorrectedValueErrors(t *testing.T) {
	p := NewPacked(1, 1000, 3)
	if err := p.RecordCorrectedValue(1<<40, 10); err == nil {
		t.Fatal("accepted out-of-range original value")
	}
	if p.TotalCount() != 0 || p.Populated() != 0 {
		t.Fatal("range error changed histogram")
	}
	if err := p.RecordValues(1, math.MaxInt64-2); err != nil {
		t.Fatal(err)
	}
	if err := p.RecordCorrectedValue(100, 10); err == nil {
		t.Fatal("accepted correction beyond total-count limit")
	}
	if p.TotalCount() != math.MaxInt64 || p.CountAtValue(100) != 1 || p.CountAtValue(90) != 1 || p.CountAtValue(80) != 0 {
		t.Fatal("failed correction did not retain exactly the original and successful synthetic observation")
	}
	if err := p.RecordCorrectedValue(200, 0); err == nil || p.CountAtValue(200) != 0 {
		t.Fatal("failed original observation changed saturated histogram")
	}
}

func TestPackedCloneIndependent(t *testing.T) {
	for _, low := range []int64{1, 1024} {
		t.Run(fmt.Sprintf("low=%d", low), func(t *testing.T) {
			p := NewPacked(low, math.MaxInt64, 3)
			for _, v := range []int64{12345, 1 << 60} {
				if err := p.RecordValues(v, 1<<32); err != nil {
					t.Fatal(err)
				}
			}
			p.SetTag("source")
			p.SetStartTimeMs(100)
			p.SetEndTimeMs(200)
			clone := p.Clone()
			if clone.geom == p.geom || clone.geom.counts != nil {
				t.Fatal("clone must have independent sparse geometry")
			}
			if clone.LowestTrackableValue() != p.LowestTrackableValue() || clone.HighestTrackableValue() != p.HighestTrackableValue() || clone.SignificantFigures() != p.SignificantFigures() {
				t.Fatal("clone changed geometry")
			}
			if clone.TotalCount() != p.TotalCount() || clone.Populated() != p.Populated() || clone.CountWidth() != p.CountWidth() {
				t.Fatal("clone changed sparse state")
			}
			if clone.Tag() != "source" || clone.StartTimeMs() != 100 || clone.EndTimeMs() != 200 {
				t.Fatal("clone lost metadata")
			}
			if err := clone.RecordValue(12345); err != nil {
				t.Fatal(err)
			}
			if err := clone.RecordValue(0); err != nil {
				t.Fatal(err)
			}
			clone.SetTag("clone")
			clone.SetStartTimeMs(300)
			clone.SetEndTimeMs(400)
			if p.CountAtValue(12345) != 1<<32 || p.CountAtValue(0) != 0 || p.CountAtValue(1<<60) != 1<<32 || p.TotalCount() != 1<<33 {
				t.Fatal("clone mutation changed source counts")
			}
			if p.Tag() != "source" || p.StartTimeMs() != 100 || p.EndTimeMs() != 200 {
				t.Fatal("clone mutation changed source metadata")
			}
			p.Reset()
			if err := p.RecordValue(99999); err != nil {
				t.Fatal(err)
			}
			if clone.CountAtValue(12345) != (1<<32)+1 || clone.CountAtValue(1<<60) != 1<<32 || clone.CountAtValue(0) != 1 || clone.TotalCount() != (1<<33)+2 {
				t.Fatal("source reset/reuse changed clone counts")
			}
			if clone.Tag() != "clone" || clone.StartTimeMs() != 300 || clone.EndTimeMs() != 400 {
				t.Fatal("source reset changed clone metadata")
			}
			clone.Reset()
			if p.CountAtValue(99999) != 1 || p.TotalCount() != 1 {
				t.Fatal("clone reset changed source")
			}
			if clone.CountWidth() != 8 || clone.LowestTrackableValue() != low {
				t.Fatal("reset clone did not preserve width and geometry")
			}
		})
	}
}

func TestPackedCloneSaturatedDecodedCounts(t *testing.T) {
	payload := zig_zag_encode_i64(math.MaxInt64)
	payload = append(payload, zig_zag_encode_i64(-99)...)
	payload = append(payload, zig_zag_encode_i64(math.MaxInt64)...)
	p, err := DecodePacked(wrapV2Payload(t, payload))
	if err != nil {
		t.Fatal(err)
	}
	clone := p.Clone()
	p.Reset()
	if clone.TotalCount() != math.MaxInt64 || clone.CountAtValue(0) != math.MaxInt64 || clone.CountAtValue(100) != math.MaxInt64 || clone.CountWidth() != 8 {
		t.Fatal("clone lost overflowing decoded distribution")
	}
	assertPackedStatistic(t, "cloned Mean", clone.Mean(), 50)
	assertPackedStatistic(t, "cloned StdDev", clone.StdDev(), 50)
}

func TestPackedCloneEmpty(t *testing.T) {
	p := NewPacked(1, math.MaxInt64, 5)
	if err := p.RecordValues(10, 1<<32); err != nil {
		t.Fatal(err)
	}
	p.Reset()
	clone := p.Clone()
	if clone.TotalCount() != 0 || clone.Populated() != 0 || clone.CountWidth() != 8 || clone.geom.counts != nil {
		t.Fatal("empty clone lost width or sparse geometry")
	}
	if err := clone.RecordValue(20); err != nil {
		t.Fatal(err)
	}
	if p.TotalCount() != 0 || p.Populated() != 0 {
		t.Fatal("recording to empty clone changed source")
	}
}
