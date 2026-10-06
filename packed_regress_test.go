package hdrhistogram

import (
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
	return buildPackedV2Stream(low, high, sig, payloadLen, 0, 0, payload)
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
	const bomb = 16 << 20 // 16 MB of zero bytes compresses to ~16 KB
	zeros := make([]byte, bomb)

	t.Run("payloadLen above geometry maximum", func(t *testing.T) {
		enc := wrapV2PayloadGeom(t, 1, 3600000000, 3, bomb, zeros)
		alloc, err := decodeAllocBytes(t, enc)
		if err == nil || !strings.Contains(err.Error(), "exceeds the maximum") {
			t.Fatalf("err=%v, want PayloadLength rejection", err)
		}
		if alloc > 4<<20 {
			t.Fatalf("decode allocated %d bytes before rejecting a %d-byte compressed stream", alloc, len(enc))
		}
	})

	t.Run("stream longer than payloadLen", func(t *testing.T) {
		enc := wrapV2PayloadGeom(t, 1, 3600000000, 3, 4, zeros)
		alloc, err := decodeAllocBytes(t, enc)
		if err == nil || !strings.Contains(err.Error(), "PayloadLength should have the same size") {
			t.Fatalf("err=%v, want payload length mismatch", err)
		}
		if alloc > 4<<20 {
			t.Fatalf("decode allocated %d bytes before rejecting a %d-byte compressed stream", alloc, len(enc))
		}
	})

	t.Run("negative payloadLen", func(t *testing.T) {
		if _, err := DecodePacked(wrapV2PayloadGeom(t, 1, 3600000000, 3, -1, nil)); err == nil {
			t.Fatal("DecodePacked accepted a negative PayloadLength")
		}
	})
}

// ValueAtPercentilesSlice must agree with ValueAtPercentile when a target lands
// exactly on the edge of an 8-bucket scan block.
func TestPackedPercentilesSliceBlockEdge(t *testing.T) {
	for _, n := range []int64{8, 9, 16} {
		p := NewPacked(1, 1000, 1)
		for v := int64(1); v <= n; v++ {
			if err := p.RecordValue(v); err != nil {
				t.Fatal(err)
			}
		}
		pcts := []float64{100, 50, 0, 87.5, 100 * 7 / float64(n), 99.9}
		got := p.ValueAtPercentilesSlice(pcts)
		for i, pct := range pcts {
			if want := p.ValueAtPercentile(pct); got[i] != want {
				t.Fatalf("n=%d p%v: slice %d, singular %d", n, pct, got[i], want)
			}
		}
		if got[0] != n {
			t.Fatalf("n=%d: p100 = %d, want %d", n, got[0], n)
		}
	}
}
