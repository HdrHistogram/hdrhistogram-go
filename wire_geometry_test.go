package hdrhistogram

import (
	"math"
	"strings"
	"testing"
)

func TestDecodersRejectInvalidWireGeometry(t *testing.T) {
	for _, tc := range []struct {
		name      string
		low, high int64
		sig       int32
	}{
		{"negative precision", 1, 1000, -1},
		{"excess precision", 1, 1000, 6},
		{"unrepresentable geometry", math.MaxInt64/2 + 1, math.MaxInt64, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Correct framing and checksum, with one observation at index zero.
			encoded := buildPackedV2Stream(tc.low, tc.high, tc.sig, 1, 0, 0, []byte{2})
			if h, err := Decode(encoded); h != nil || err == nil || !strings.Contains(err.Error(), "corrupt histogram header") {
				t.Fatalf("dense decode: histogram=%v error=%v", h, err)
			}
			if h, err := DecodePacked(encoded); h != nil || err == nil || !strings.Contains(err.Error(), "corrupt histogram header") {
				t.Fatalf("packed decode: histogram=%v error=%v", h, err)
			}
		})
	}
}

// Released hdrhistogram-go v1.0.0 wrote lowest values below 1 with the
// geometry of 1 (unit magnitude 0); both decoders read them as 1.
func TestDecodersPreserveLegacyLowest(t *testing.T) {
	for _, low := range []int64{0, -1, math.MinInt64} {
		encoded := buildPackedV2Stream(low, 1000, 3, 1, 0, 0, []byte{2})
		d, err := Decode(encoded)
		if err != nil || d == nil {
			t.Fatalf("legacy dense low=%d: %v", low, err)
		}
		p, err := DecodePacked(encoded)
		if err != nil || p == nil {
			t.Fatalf("legacy packed low=%d: %v", low, err)
		}
		if d.LowestTrackableValue() != 1 || p.LowestTrackableValue() != 1 || d.TotalCount() != 1 || p.CountAtValue(0) != 1 {
			t.Fatalf("legacy low=%d changed geometry or counts", low)
		}
	}
}

// Every range New and NewPacked accept must round-trip through both decoders:
// Go has always written these headers, including highest values below twice
// the lowest value, zero or negative. Rejecting them would make existing Go
// streams and interval logs unreadable.
func TestDecodersAcceptGoWritableRanges(t *testing.T) {
	for _, r := range []struct{ low, high int64 }{
		{1, 1}, {1, 0}, {1, -1}, {10, 15}, {100, 100}, {100, 199}, {1000, 1500},
	} {
		h := New(r.low, r.high, 3)
		p := NewPacked(r.low, r.high, 3)
		if err := h.RecordValue(r.low); err != nil {
			t.Fatalf("New(%d, %d, 3): %v", r.low, r.high, err)
		}
		if err := p.RecordValue(r.low); err != nil {
			t.Fatalf("NewPacked(%d, %d, 3): %v", r.low, r.high, err)
		}
		enc, err := h.Encode(V2CompressedEncodingCookieBase)
		if err != nil {
			t.Fatal(err)
		}
		penc, err := p.Encode()
		if err != nil {
			t.Fatal(err)
		}
		d, err := Decode(enc)
		if err != nil {
			t.Fatalf("Decode of New(%d, %d, 3): %v", r.low, r.high, err)
		}
		if !d.Equals(h) {
			t.Fatalf("Decode of New(%d, %d, 3) changed the histogram", r.low, r.high)
		}
		q, err := DecodePacked(penc)
		if err != nil {
			t.Fatalf("DecodePacked of NewPacked(%d, %d, 3): %v", r.low, r.high, err)
		}
		if q.TotalCount() != 1 || q.CountAtValue(r.low) != 1 || q.HighestTrackableValue() != r.high {
			t.Fatalf("DecodePacked of NewPacked(%d, %d, 3) changed the histogram", r.low, r.high)
		}
	}
}

func TestDecodersPreserveValidWireGeometry(t *testing.T) {
	for _, low := range []int64{1, 100, 1000, 1 << 40} {
		for sig := 1; sig <= 5; sig++ {
			encoded := buildPackedV2Stream(low, 2*low, int32(sig), 1, 0, 0, []byte{2})
			d, err := Decode(encoded)
			if err != nil {
				t.Fatal(err)
			}
			p, err := DecodePacked(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if d.TotalCount() != 1 || p.TotalCount() != 1 || d.lowestDiscernibleValue != low || p.geom.lowestDiscernibleValue != low ||
				d.highestTrackableValue != 2*low || p.geom.highestTrackableValue != 2*low || d.significantFigures != int64(sig) || p.geom.significantFigures != int64(sig) {
				t.Fatalf("serialized geometry was changed for low=%d sig=%d", low, sig)
			}
		}
	}
}
