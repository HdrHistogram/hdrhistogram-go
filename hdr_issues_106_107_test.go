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

// Java PackedHistogram.shiftValuesLeft(2) produced this stream
// (normalizingIndexOffset 2048).
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

// Java writes the V2 payload in logical index order (its encoder reads through
// the in-memory rotation), so normalizingIndexOffset only describes the
// writer's layout and both decoders ignore it. Values are Java's own decode.
func TestDecodersIgnoreNormalizingIndexOffset(t *testing.T) {
	for _, tc := range []struct {
		wire              string
		total, min, max   int64
		value, valueCount int64
	}{
		// PackedHistogram.shiftValuesLeft(2): offset 2048.
		{javaShiftedStream, 7, 4936, 4939, 4936, 7},
		// Histogram(1, 3600000000, 3) recording 100, 12345, 1000000, then
		// shiftValuesLeft(2): offset 2048.
		{"HISTFAAAAC14nJNpmSzMwMDAycDAAaQYmBkggBFEXJu8hMH+A0RgPhvT60SmjWlMAIxwB6Y=", 3, 400, 4001791, 49376, 1},
	} {
		d, err := Decode([]byte(tc.wire))
		if err != nil {
			t.Fatalf("dense: %v", err)
		}
		if d.TotalCount() != tc.total || d.Min() != tc.min || d.Max() != tc.max || d.counts[d.countsIndexFor(tc.value)] != tc.valueCount {
			t.Fatalf("dense: total %d min %d max %d, want Java's %d %d %d", d.TotalCount(), d.Min(), d.Max(), tc.total, tc.min, tc.max)
		}
		p, err := DecodePacked([]byte(tc.wire))
		if err != nil {
			t.Fatalf("packed: %v", err)
		}
		if p.TotalCount() != tc.total || p.Min() != tc.min || p.Max() != tc.max || p.CountAtValue(tc.value) != tc.valueCount {
			t.Fatalf("packed: total %d min %d max %d, want Java's %d %d %d", p.TotalCount(), p.Min(), p.Max(), tc.total, tc.min, tc.max)
		}
	}
	// Any offset decodes to exactly the buckets of the same payload with offset 0,
	// including zero-digit geometry.
	for _, sig := range []int32{0, 1, 3, 5} {
		base := buildPackedV2Stream(1, 1000, sig, 3, 0, 0, []byte{0x14, 0x01, 0x06})
		want, err := DecodePacked(base)
		if err != nil {
			t.Fatal(err)
		}
		for _, off := range []int32{1, 2, -1, 1024, 2048, math.MaxInt32, math.MinInt32} {
			enc := buildPackedV2Stream(1, 1000, sig, 3, off, 0, []byte{0x14, 0x01, 0x06})
			p, err := DecodePacked(enc)
			if err != nil || !packedSameBuckets(p, want) || p.TotalCount() != want.TotalCount() {
				t.Fatalf("sig %d offset %d: packed err %v or buckets differ", sig, off, err)
			}
			d, err := Decode(enc)
			if err != nil || d.TotalCount() != want.TotalCount() {
				t.Fatalf("sig %d offset %d: dense err %v", sig, off, err)
			}
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
