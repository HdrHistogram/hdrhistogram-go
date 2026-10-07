package hdrhistogram

import (
	"math"
	"strings"
	"testing"
)

// Released hdrhistogram-go v1.2.0 (and v1.0.0, byte-identical) wrote a
// normalizingIndexOffset of 1 into every stream: New(1, 3600000000, 3)
// recording 1, 100, 12345 and 3600000000.
const goV120LegacyOffset = "HISTFAAAADV42pJpmSzMwMDAw8DAwMjAwMDMAAEgNsO1yUsY7D9ARZgOMzIddmO6voiJCRAAAP//nYsH5A=="

// Java shiftValuesLeft produced this stream (normalizingIndexOffset 2048).
const javaShiftedStream = "HISTFAAAACR4nJNpmSzMwMDAzMDAwQChwYARTPI7Odh/gAgsNuYDAEyEA/o="

// #106: DecodePacked must read streams from Go releases before #66, which
// carry the meaningless legacy offset 1, exactly as dense Decode does.
func TestDecodeLegacyGoOffsetStreams(t *testing.T) {
	streams := append([]string{goV120LegacyOffset}, goV100LowestBelowOne...)
	for _, wire := range streams {
		d, err := Decode([]byte(wire))
		if err != nil {
			t.Fatalf("dense: %v", err)
		}
		p, err := DecodePacked([]byte(wire))
		if err != nil {
			t.Fatalf("packed: %v", err)
		}
		if p.TotalCount() != d.TotalCount() || p.Max() != d.Max() || p.Min() != d.Min() {
			t.Fatalf("packed total/min/max %d/%d/%d, dense %d/%d/%d",
				p.TotalCount(), p.Min(), p.Max(), d.TotalCount(), d.Min(), d.Max())
		}
		n := 0
		p.ForEachBucket(func(v, c int64) bool {
			if d.counts[d.countsIndexFor(v)] != c {
				t.Fatalf("bucket %d: packed %d, dense %d", v, c, d.counts[d.countsIndexFor(v)])
			}
			n++
			return true
		})
		populated := 0
		for _, c := range d.counts {
			if c != 0 {
				populated++
			}
		}
		if n != populated {
			t.Fatalf("packed has %d buckets, dense %d", n, populated)
		}
		// Re-encoding writes the current offset of 0.
		enc, _ := p.Encode()
		if again, err := DecodePacked(enc); err != nil || again.TotalCount() != p.TotalCount() {
			t.Fatalf("re-decode: %v", err)
		}
	}
	if d, _ := Decode([]byte(goV120LegacyOffset)); d.TotalCount() != 4 || d.ValueAtQuantile(50) != 100 {
		t.Fatalf("v1.2.0 fixture: total %d p50 %d, want 4 100", d.TotalCount(), d.ValueAtQuantile(50))
	}
}

// Offsets other than 0 and the legacy 1 come from shifted histograms; both
// decoders reject them instead of mis-indexing the payload. Offset 1 is only
// read as the legacy value where a shift could not produce it.
func TestDecodeRejectsShiftedOffsets(t *testing.T) {
	for _, wire := range []string{javaShiftedStream} {
		if _, err := Decode([]byte(wire)); err == nil || !strings.Contains(err.Error(), "normalizingIndexOffset 2048") {
			t.Fatalf("dense Decode of a shifted Java stream: err = %v", err)
		}
		if _, err := DecodePacked([]byte(wire)); err == nil || !strings.Contains(err.Error(), "normalizingIndexOffset 2048") {
			t.Fatalf("DecodePacked of a shifted Java stream: err = %v", err)
		}
	}
	for _, tc := range []struct {
		sig, offset int32
		ok          bool
	}{
		{3, 0, true}, {3, 1, true}, {1, 1, true}, {5, 1, true},
		{3, 2, false}, {3, -1, false}, {3, 1024, false}, {3, math.MinInt32, false},
		{0, 1, false}, // zero digits: subBucketHalfCount is 1, so 1 could be a real shift
	} {
		enc := buildPackedV2Stream(1, 1000, tc.sig, 1, tc.offset, 0, []byte{2})
		_, derr := Decode(enc)
		_, perr := DecodePacked(enc)
		if (derr == nil) != tc.ok || (perr == nil) != tc.ok {
			t.Fatalf("sig %d offset %d: dense err %v, packed err %v, want ok=%v", tc.sig, tc.offset, derr, perr, tc.ok)
		}
	}
}

// #107: a stream whose bucket counts sum past int64 used to decode densely as
// an empty histogram (the total wrapped to 0). Dense Decode now rejects it;
// DecodePacked keeps the buckets and saturates, as documented.
func TestDenseDecodeRejectsCountSumOverflow(t *testing.T) {
	const wrapped = "HISTFAAAACh4nJJpmSzMwMAAwiDADKUZIZS6gP0HCOvffyiAM1gAAQAA//8vZxS0" // MaxInt64, MaxInt64, 2
	if h, err := Decode([]byte(wrapped)); err == nil || h != nil || !strings.Contains(err.Error(), "sum past MaxInt64") {
		t.Fatalf("Decode: histogram %v, err %v", h, err)
	}
	p, err := DecodePacked([]byte(wrapped))
	if err != nil {
		t.Fatalf("DecodePacked: %v", err)
	}
	if p.TotalCount() != math.MaxInt64 || p.Max() != 2 {
		t.Fatalf("DecodePacked: total %d max %d", p.TotalCount(), p.Max())
	}
	// Exactly MaxInt64 still fits and decodes.
	maxCount := []byte{0xfe, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	exact := buildPackedV2Stream(1, 10000, 3, 9, 0, 0, maxCount)
	d, err := Decode(exact)
	if err != nil {
		t.Fatalf("exact MaxInt64: %v", err)
	}
	if d.TotalCount() != math.MaxInt64 {
		t.Fatalf("exact MaxInt64: total %d", d.TotalCount())
	}
	one := append(append([]byte{}, maxCount...), 2)
	if _, err := Decode(buildPackedV2Stream(1, 10000, 3, int32(len(one)), 0, 0, one)); err == nil {
		t.Fatal("MaxInt64 + 1 decoded without error")
	}
}
