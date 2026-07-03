package hdrhistogram

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"io"
	"testing"
)

// C1: Mean must not overflow the int64 count*value product. Recording a large
// value many times makes count*value exceed int64 max (~9.2e18); the pre-fix code
// multiplied in int64 and wrapped, silently corrupting the mean.
func TestMeanNoInt64Overflow(t *testing.T) {
	const v = int64(1_000_000_000_000_000) // 1e15
	const n = int64(100_000)               // 1e5 -> product 1e20 wraps int64
	h := New(1, v, 3)
	if err := h.RecordValues(v, n); err != nil {
		t.Fatal(err)
	}
	mean := h.Mean()
	// The true mean is ~1e15 (all samples equal v). A wrapped int64 product would
	// land far off (tiny or negative). Assert we are within the histogram's
	// precision of v.
	if mean < float64(v)*0.99 || mean > float64(v)*1.01 {
		t.Fatalf("Mean() = %g, want ~%d (int64 overflow in count*value?)", mean, v)
	}
}

// C2: the serialized normalizing index offset must be 0, since this port stores
// counts[] unrotated. A non-zero value makes the C/Java readers misplace buckets.
func TestSerializedNormalizingIndexOffsetIsZero(t *testing.T) {
	h := New(1, 1_000_000, 3)
	_ = h.RecordValue(42)
	enc, err := h.Encode(V2CompressedEncodingCookieBase)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(enc))
	if err != nil {
		t.Fatal(err)
	}
	z, err := zlib.NewReader(bytes.NewReader(decoded[8:]))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	_, _, offset, _, _, _, _, err := decodeDeCompressedHeaderFormat(raw[0:ENCODING_HEADER_SIZE])
	if err != nil {
		t.Fatal(err)
	}
	if offset != 0 {
		t.Fatalf("serialized normalizingIndexOffset = %d, want 0", offset)
	}
}

// C3: OutputBaseTime must emit the canonical "#[BaseTime:" header (capital T) that
// the reader's regex expects; the lowercase form was silently unreadable, losing
// the base time on round-trip.
func TestOutputBaseTimeRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	lw := NewHistogramLogWriter(&buf)
	if err := lw.OutputBaseTime(3_600_000); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("#[BaseTime:")) {
		t.Fatalf("writer emitted non-canonical base-time header: %q", buf.String())
	}
	reader := NewHistogramLogReader(&buf)
	reader.NextIntervalHistogram() // drives header parsing
	if !reader.observedBaseTime || reader.baseTimeSec != 3600 {
		t.Fatalf("base time not round-tripped: baseTimeSec=%v observed=%v (want 3600/true)",
			reader.baseTimeSec, reader.observedBaseTime)
	}
}
