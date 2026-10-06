package hdrhistogram

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"runtime"
	"strings"
	"testing"
)

// Values above highestTrackableValue that still land in the last bucket are
// accepted by RecordValue, so CountAtValue must report them too.
func TestPackedCountAtValueAboveHighestTrackable(t *testing.T) {
	p := NewPacked(1, 1000, 1)
	d := New(1, 1000, 1)
	if err := p.RecordValue(1020); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordValue(1020); err != nil {
		t.Fatal(err)
	}
	if got := p.CountAtValue(1020); got != 1 {
		t.Fatalf("CountAtValue(1020)=%d want 1", got)
	}
	if got := p.CountAtValue(p.Max()); got != 1 {
		t.Fatalf("CountAtValue(Max())=%d want 1", got)
	}
	if got, want := p.CountAtValue(1020), d.counts[d.countsIndexFor(1020)]; got != want {
		t.Fatalf("CountAtValue(1020)=%d dense=%d", got, want)
	}
	if got := p.CountAtValue(1 << 40); got != 0 {
		t.Fatalf("CountAtValue(out of range)=%d want 0", got)
	}
}

// NewPacked must not allocate the dense counts array.
func TestPackedNewDoesNotAllocateDenseCounts(t *testing.T) {
	var p *PackedHistogram
	allocs := testing.AllocsPerRun(10, func() { p = NewPacked(1, 9e18, 5) })
	if allocs > 2 {
		t.Fatalf("NewPacked allocs=%v want <=2", allocs)
	}
	if p.geom.counts != nil {
		t.Fatal("geometry oracle holds a counts array")
	}
}

// wrapV2Payload builds a V2 compressed stream for (1, 3600000000, 3) around a
// raw zig-zag payload.
func wrapV2Payload(t testing.TB, payload []byte) []byte {
	t.Helper()
	return wrapV2PayloadGeom(t, 1, 3600000000, 3, int32(len(payload)), payload)
}

// wrapV2PayloadGeom is wrapV2Payload with an explicit geometry and a
// payloadLen header field that may disagree with len(payload).
func wrapV2PayloadGeom(t testing.TB, low, high int64, sig, payloadLen int32, payload []byte) []byte {
	t.Helper()
	hdr := make([]byte, ENCODING_HEADER_SIZE)
	binary.BigEndian.PutUint32(hdr[0:], uint32(V2EncodingCookieBase|0x10))
	binary.BigEndian.PutUint32(hdr[4:], uint32(payloadLen))
	binary.BigEndian.PutUint32(hdr[8:], 0)
	binary.BigEndian.PutUint32(hdr[12:], uint32(sig))
	binary.BigEndian.PutUint64(hdr[16:], uint64(low))
	binary.BigEndian.PutUint64(hdr[24:], uint64(high))
	binary.BigEndian.PutUint64(hdr[32:], 0x3ff0000000000000) // 1.0
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	_, _ = w.Write(hdr)
	_, _ = w.Write(payload)
	_ = w.Close()
	out := make([]byte, 8, 8+z.Len())
	binary.BigEndian.PutUint32(out[0:], uint32(V2CompressedEncodingCookieBase|0x10))
	binary.BigEndian.PutUint32(out[4:], uint32(z.Len()))
	out = append(out, z.Bytes()...)
	enc := make([]byte, base64.StdEncoding.EncodedLen(len(out)))
	base64.StdEncoding.Encode(enc, out)
	return enc
}

// A zero-run of MinInt64 (whose negation overflows) must be rejected rather
// than wrapping dstIndex and silently writing counts to arbitrary buckets.
func TestPackedDecodeRejectsMinInt64ZeroRun(t *testing.T) {
	minRun := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	payload := append([]byte{0x14, 0x14}, minRun...)
	payload = append(payload, 0x0a, 0x0a, 0x0a)
	if _, err := DecodePacked(wrapV2Payload(t, payload)); err == nil {
		t.Fatal("DecodePacked accepted a MinInt64 zero-run")
	}
	// Sanity: the same wrapper around a valid payload decodes.
	if _, err := DecodePacked(wrapV2Payload(t, []byte{0x14, 0x14})); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
}

// decodeAllocBytes reports the bytes allocated by one DecodePacked call.
func decodeAllocBytes(t *testing.T, enc []byte) (uint64, error) {
	t.Helper()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := DecodePacked(enc)
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc, err
}

// A small compressed stream must not be able to force a large allocation:
// the payload is bounded by the geometry before it is inflated.
func TestPackedDecodeBoundsDecompression(t *testing.T) {
	const bomb = 64 << 20 // 64 MB of zero bytes compresses to ~64 KB
	zeros := make([]byte, bomb)

	t.Run("payloadLen above geometry maximum", func(t *testing.T) {
		enc := wrapV2PayloadGeom(t, 1, 3600000000, 3, bomb, zeros)
		alloc, err := decodeAllocBytes(t, enc)
		if err == nil || !strings.Contains(err.Error(), "exceeds the maximum") {
			t.Fatalf("err=%v, want PayloadLength rejection", err)
		}
		if alloc > 8<<20 {
			t.Fatalf("decode allocated %d bytes before rejecting a %d-byte compressed stream", alloc, len(enc))
		}
	})

	t.Run("stream longer than payloadLen", func(t *testing.T) {
		enc := wrapV2PayloadGeom(t, 1, 3600000000, 3, 4, zeros)
		alloc, err := decodeAllocBytes(t, enc)
		if err == nil || !strings.Contains(err.Error(), "PayloadLength should have the same size") {
			t.Fatalf("err=%v, want payload length mismatch", err)
		}
		if alloc > 8<<20 {
			t.Fatalf("decode allocated %d bytes before rejecting a %d-byte compressed stream", alloc, len(enc))
		}
	})

	t.Run("negative payloadLen", func(t *testing.T) {
		if _, err := DecodePacked(wrapV2PayloadGeom(t, 1, 3600000000, 3, -1, nil)); err == nil {
			t.Fatal("DecodePacked accepted a negative PayloadLength")
		}
	})
}
