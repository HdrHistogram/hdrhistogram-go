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
		{"negative highest", 1, -1, 3},
		{"zero highest", 1, 0, 3},
		{"range too small", 100, 100, 3},
		{"just below double lowest", 100, 199, 3},
		{"negative precision", 1, 1000, -1},
		{"excess precision", 1, 1000, 6},
		{"multiplication would overflow", math.MaxInt64/2 + 1, math.MaxInt64, 1},
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

func TestDecodersPreserveLegacyLowestWhileValidatingRange(t *testing.T) {
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
		invalid := buildPackedV2Stream(low, 1, 3, 1, 0, 0, []byte{2})
		if _, err := Decode(invalid); err == nil {
			t.Fatalf("legacy dense low=%d bypassed range validation", low)
		}
		if _, err := DecodePacked(invalid); err == nil {
			t.Fatalf("legacy packed low=%d bypassed range validation", low)
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
