package hdrhistogram_test

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"io"
	"math"
	"reflect"
	"testing"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
)

// The expected values capture the public Go policy rather than calculating
// expectations with the implementation's rank or bucket-index helpers. C agrees
// on ordinary ranks; Java returns 2047 and 3007 at 40 and 66.67 respectively.
func TestPackedCompatibilityPercentilePolicy(t *testing.T) {
	p := hdr.NewPacked(100, 100000, 3)
	for _, value := range []int64{1000, 2000, 3000} {
		if err := p.RecordValue(value); err != nil {
			t.Fatal(err)
		}
	}
	percentiles := []float64{66.67, math.NaN(), -1, 100, 40, math.Inf(-1), 0, math.Inf(1), 150}
	want := []int64{2047, 1023, 960, 3007, 1023, 960, 960, 3007, 3007}
	if got := p.ValueAtPercentilesSlice(percentiles); !reflect.DeepEqual(got, want) {
		t.Fatalf("unsorted batch = %v, want %v", got, want)
	}
	for i, percentile := range percentiles {
		if got := p.ValueAtPercentile(percentile); got != want[i] {
			t.Errorf("percentile %v = %d, want %d", percentile, got, want[i])
		}
	}
}

func TestPackedCompatibilityCoarseExtremaRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []int64
	}{
		{name: "empty"},
		{name: "zero", values: []int64{0}},
		{name: "positive_in_zero_bucket", values: []int64{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := hdr.NewPacked(1000, 1000000, 3)
			for _, value := range tc.values {
				if err := p.RecordValue(value); err != nil {
					t.Fatal(err)
				}
			}
			encoded, err := p.Encode()
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := hdr.DecodePacked(encoded)
			if err != nil {
				t.Fatal(err)
			}
			for _, histogram := range []*hdr.PackedHistogram{p, decoded} {
				if histogram.Min() != 0 || histogram.Max() != 511 || histogram.TotalCount() != int64(len(tc.values)) {
					t.Fatalf("min/max/count = %d/%d/%d", histogram.Min(), histogram.Max(), histogram.TotalCount())
				}
			}
		})
	}
}

func TestPackedCompatibilityCountQueryDomain(t *testing.T) {
	p := hdr.NewPacked(1, 2047, 3)
	if err := p.RecordValue(2047); err != nil {
		t.Fatal(err)
	}
	for _, value := range []int64{-1, math.MinInt64, 2048, math.MaxInt64} {
		if got := p.CountAtValue(value); got != 0 {
			t.Errorf("CountAtValue(%d) = %d, want 0", value, got)
		}
	}
	if got := p.CountAtValue(2047); got != 1 {
		t.Fatalf("last bucket count = %d, want 1", got)
	}
}

func TestPackedCompatibilityConstructorPolicy(t *testing.T) {
	p := hdr.NewPacked(1, 1000, 3)
	if err := p.RecordValue(1001); err != nil {
		t.Fatalf("record above configured maximum within allocated buckets: %v", err)
	}
	if p.CountAtValue(1001) != 1 {
		t.Fatal("missing count above configured maximum")
	}
	q := hdr.NewPacked(0, -1, 6)
	if err := q.RecordValue(1000); err != nil {
		t.Fatalf("record into normalized constructor geometry: %v", err)
	}
	if q.LowestTrackableValue() != 1 || q.HighestTrackableValue() != -1 || q.SignificantFigures() != 5 {
		t.Fatal("constructor normalization changed")
	}
}

// Produced by Java PackedHistogram after shiftValuesLeft(2), with a serialized
// normalizingIndexOffset of 2048. Java writes the payload in logical order, so
// both Go decoders read it as Java does: 7 counts in the bucket at 4936.
func TestPackedCompatibilityReadsShiftedJavaFixture(t *testing.T) {
	fixture := []byte("HISTFAAAACR4nJNpmSzMwMDAzMDAwQChwYARTPI7Odh/gAgsNuYDAEyEA/o=")
	p, err := hdr.DecodePacked(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if p.TotalCount() != 7 || p.Min() != 4936 || p.Max() != 4939 || p.CountAtValue(4936) != 7 {
		t.Fatalf("packed: total %d min %d max %d, want Java's 7 4936 4939", p.TotalCount(), p.Min(), p.Max())
	}
	d, err := hdr.Decode(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if d.TotalCount() != 7 || d.Min() != 4936 || d.Max() != 4939 {
		t.Fatalf("dense: total %d min %d max %d, want Java's 7 4936 4939", d.TotalCount(), d.Min(), d.Max())
	}
}

// Produced by C with conversion_ratio=2.5.
// The V2 conversion ratio is metadata: counts stay integer bucket counts, and
// both encoders write back the ratio a stream was decoded with.
func TestCompatibilityPreservesCConversionRatio(t *testing.T) {
	fixture := []byte("HISTFAAAACF4nJNpmSzMwMDAzAABMJoRTPI7OTiwQAQWC/MBAEIPAuc=")
	if got := packedFixtureRatio(t, fixture); got != 2.5 {
		t.Fatalf("foreign fixture ratio = %v, want 2.5", got)
	}
	p, err := hdr.DecodePacked(fixture)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if got := packedFixtureRatio(t, encoded); got != 2.5 {
		t.Fatalf("packed re-encoded ratio = %v, want 2.5", got)
	}
	if clone, _ := p.Clone().Encode(); packedFixtureRatio(t, clone) != 2.5 {
		t.Fatal("Clone dropped the conversion ratio")
	}
	q, err := hdr.DecodePacked(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if p.TotalCount() == 0 || p.TotalCount() != q.TotalCount() || !reflect.DeepEqual(packedCompatibilityBuckets(p), packedCompatibilityBuckets(q)) {
		t.Fatal("re-encoding changed integer counts")
	}

	d, err := hdr.Decode(fixture)
	if err != nil {
		t.Fatal(err)
	}
	dense, err := d.Encode(hdr.V2CompressedEncodingCookieBase)
	if err != nil {
		t.Fatal(err)
	}
	if got := packedFixtureRatio(t, dense); got != 2.5 {
		t.Fatalf("dense re-encoded ratio = %v, want 2.5", got)
	}
	if string(dense) != string(encoded) {
		t.Fatal("dense and packed re-encodings differ")
	}
	if again, _ := hdr.Import(d.Export()).Encode(hdr.V2CompressedEncodingCookieBase); packedFixtureRatio(t, again) != 2.5 {
		t.Fatal("Export/Import dropped the conversion ratio")
	}

	// Histograms built by the constructors keep writing the default of 1.
	if fresh, _ := hdr.NewPacked(1, 1000, 3).Encode(); packedFixtureRatio(t, fresh) != 1 {
		t.Fatal("NewPacked does not encode a ratio of 1")
	}
	if fresh, _ := hdr.New(1, 1000, 3).Encode(hdr.V2CompressedEncodingCookieBase); packedFixtureRatio(t, fresh) != 1 {
		t.Fatal("New does not encode a ratio of 1")
	}
}

func TestPackedCompatibilitySaturatedSourcePreservesBuckets(t *testing.T) {
	// V2 buckets [MaxInt64, 1] have a true sum outside int64's range.
	fixture := []byte("HISTFAAAACJ4nJNpmSzMwMDAxQABzFCaEUKpC9h/gLD+/YcCJgCOiQuz")
	p, err := hdr.DecodePacked(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if p.TotalCount() != math.MaxInt64 || p.CountAtValue(0) != math.MaxInt64 || p.CountAtValue(1) != 1 {
		t.Fatal("decode failed to retain saturated source buckets")
	}
	dst := hdr.New(1, 10000, 3)
	if dropped := p.MergeInto(dst); dropped != 0 {
		t.Fatalf("MergeInto dropped %d", dropped)
	}
	if dst.TotalCount() != math.MinInt64 {
		t.Fatalf("unchecked destination total = %d, want MinInt64", dst.TotalCount())
	}
	counts := dst.Export().Counts
	if counts[0] != math.MaxInt64 || counts[1] != 1 {
		t.Fatal("MergeInto skipped positive source buckets")
	}
	encoded, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	copy, err := hdr.DecodePacked(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(packedCompatibilityBuckets(p), packedCompatibilityBuckets(copy)) {
		t.Fatal("encode/decode lost saturated source buckets")
	}
	copy.Reset()
	if err := copy.RecordValue(0); err != nil {
		t.Fatal(err)
	}
	if p.CountAtValue(0) != math.MaxInt64 || p.CountAtValue(1) != 1 {
		t.Fatal("decoded copy shares mutable storage")
	}
}

func TestPackedCompatibilityIndependentMergeCopy(t *testing.T) {
	p := hdr.NewPacked(100, 100000, 3)
	if err := p.RecordValues(1000, 3); err != nil {
		t.Fatal(err)
	}
	copy := hdr.NewPacked(p.LowestTrackableValue(), p.HighestTrackableValue(), int(p.SignificantFigures()))
	if dropped := copy.Merge(p); dropped != 0 {
		t.Fatalf("copy dropped %d", dropped)
	}
	if !reflect.DeepEqual(packedCompatibilityBuckets(p), packedCompatibilityBuckets(copy)) {
		t.Fatal("copy differs from original")
	}
	// Mutate the existing count to detect aliases even when no growth occurs.
	if err := copy.RecordValue(1000); err != nil {
		t.Fatal(err)
	}
	if p.CountAtValue(1000) != 3 || copy.CountAtValue(1000) != 4 {
		t.Fatal("merged copy shares mutable counts")
	}
}

func packedCompatibilityBuckets(p *hdr.PackedHistogram) [][2]int64 {
	var buckets [][2]int64
	p.ForEachBucket(func(value, count int64) bool {
		buckets = append(buckets, [2]int64{value, count})
		return true
	})
	return buckets
}

func packedFixtureRatio(t *testing.T, encoded []byte) float64 {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 8 {
		t.Fatal("truncated compressed fixture")
	}
	z, err := zlib.NewReader(bytes.NewReader(raw[8:]))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := z.Close(); err != nil {
			t.Error(err)
		}
	}()
	var header [40]byte
	if _, err := io.ReadFull(z, header[:]); err != nil {
		t.Fatal(err)
	}
	return math.Float64frombits(binary.BigEndian.Uint64(header[32:40]))
}
