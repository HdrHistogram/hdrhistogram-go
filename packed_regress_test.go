package hdrhistogram

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
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
func wrapV2Payload(t *testing.T, payload []byte) []byte {
	t.Helper()
	hdr := make([]byte, ENCODING_HEADER_SIZE)
	binary.BigEndian.PutUint32(hdr[0:], uint32(V2EncodingCookieBase|0x10))
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(payload)))
	binary.BigEndian.PutUint32(hdr[8:], 0)
	binary.BigEndian.PutUint32(hdr[12:], 3)
	binary.BigEndian.PutUint64(hdr[16:], 1)
	binary.BigEndian.PutUint64(hdr[24:], 3600000000)
	binary.BigEndian.PutUint64(hdr[32:], 0x3ff0000000000000) // 1.0
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	_, _ = w.Write(append(hdr, payload...))
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
