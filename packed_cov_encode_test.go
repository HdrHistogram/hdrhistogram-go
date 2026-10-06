package hdrhistogram

import (
	"bytes"
	"testing"
)

// This file deeply covers PackedHistogram.Encode and fillBufferFromSparse:
// empty histograms, every count width (1/2/4/8), byte-identity vs the dense
// encoder across multiple geometries, zero-run coalescing, and the countsLimit
// edges (single populated bucket, top bucket). All exact-parity assertions
// compare against the dense reference, which is authoritative for the V2 wire
// format; where packed intentionally streams from the sparse backing, the
// payload bytes must still be identical.

type cov_encode_pair struct {
	v, n int64
}

// cov_encode_build records the same (value,count) pairs into a fresh dense and
// packed histogram sharing the same geometry, returning both.
func cov_encode_build(low, high int64, sig int, pairs []cov_encode_pair) (*Histogram, *PackedHistogram, error) {
	d := New(low, high, sig)
	p := NewPacked(low, high, sig)
	for _, pr := range pairs {
		if err := d.RecordValues(pr.v, pr.n); err != nil {
			return nil, nil, err
		}
		if err := p.RecordValues(pr.v, pr.n); err != nil {
			return nil, nil, err
		}
	}
	return d, p, nil
}

// cov_encode_assertParity asserts the packed payload and full Encode() stream
// are byte-identical to the dense encoder.
func cov_encode_assertParity(t *testing.T, tag string, d *Histogram, p *PackedHistogram) {
	t.Helper()

	densePayload, err := d.fillBufferFromCountsArray()
	if err != nil {
		t.Fatalf("%s: dense fillBufferFromCountsArray: %v", tag, err)
	}
	packedPayload := p.fillBufferFromSparse()
	if !bytes.Equal(densePayload, packedPayload) {
		t.Fatalf("%s: sparse payload != dense payload\n dense (%d): %v\npacked (%d): %v",
			tag, len(densePayload), densePayload, len(packedPayload), packedPayload)
	}

	denseEnc, err := d.Encode(V2CompressedEncodingCookieBase)
	if err != nil {
		t.Fatalf("%s: dense Encode: %v", tag, err)
	}
	packedEnc, err := p.Encode()
	if err != nil {
		t.Fatalf("%s: packed Encode: %v", tag, err)
	}
	if !bytes.Equal(denseEnc, packedEnc) {
		t.Fatalf("%s: packed stream != dense stream (len %d vs %d)", tag, len(packedEnc), len(denseEnc))
	}
}

// Every configuration below is exercised empty (no records): the empty encode
// must still match dense, and the empty payload/countsLimit path is the one
// where Max()==highestEquivalent(0).
func TestCov_encodeEmptyParityAcrossConfigs(t *testing.T) {
	configs := []struct {
		name      string
		low, high int64
		sig       int
	}{
		{"1_1e9_2", 1, 1_000_000_000, 2},
		{"100_3p6e9_3", 100, 3_600_000_000, 3},
		{"1_maxint64_5", 1, 9223372036854775807, 5},
		{"1_3p6e9_3", 1, 3_600_000_000, 3},
	}
	for _, c := range configs {
		d, p, err := cov_encode_build(c.low, c.high, c.sig, nil)
		if err != nil {
			t.Fatalf("%s: build: %v", c.name, err)
		}
		if p.Populated() != 0 || p.TotalCount() != 0 {
			t.Fatalf("%s: expected empty, got size=%d total=%d", c.name, p.Populated(), p.TotalCount())
		}
		// An empty packed histogram keeps its initial width of 1.
		if p.CountWidth() != 1 {
			t.Fatalf("%s: empty width = %d, want 1", c.name, p.CountWidth())
		}
		cov_encode_assertParity(t, "empty/"+c.name, d, p)
	}
}

// Drive every count width (1/2/4/8) and confirm both the width transition and
// byte-identity with dense (whose int64 counts hold the same values).
func TestCov_encodeCountWidthParity(t *testing.T) {
	cases := []struct {
		name      string
		count     int64
		wantWidth int
	}{
		{"width1", 200, 1},              // <= 0xFF
		{"width1_boundary", 255, 1},     // == 0xFF, still width 1
		{"width2_boundary_low", 256, 2}, // 0xFF+1 forces width 2
		{"width2", 60000, 2},            // <= 0xFFFF
		{"width2_boundary_hi", 65535, 2},
		{"width4_boundary_low", 65536, 4}, // forces width 4
		{"width4", 3_000_000_000, 4},      // <= 0xFFFFFFFF
		{"width4_boundary_hi", 4294967295, 4},
		{"width8_boundary_low", 4294967296, 8}, // 0xFFFFFFFF+1 forces width 8
		{"width8", 5_000_000_000_000, 8},
	}
	for _, c := range cases {
		// Use two populated buckets so the widen re-pack loop runs over >1 slot.
		pairs := []cov_encode_pair{
			{v: 1000, n: c.count},
			{v: 50_000_000, n: c.count},
		}
		d, p, err := cov_encode_build(1, 1_000_000_000, 3, pairs)
		if err != nil {
			t.Fatalf("%s: build: %v", c.name, err)
		}
		if p.CountWidth() != c.wantWidth {
			t.Fatalf("%s: width = %d, want %d", c.name, p.CountWidth(), c.wantWidth)
		}
		if p.Populated() != 2 {
			t.Fatalf("%s: populated = %d, want 2", c.name, p.Populated())
		}
		cov_encode_assertParity(t, "width/"+c.name, d, p)
	}
}

// Populated data across the four geometries, including lowestDiscernible>1 and
// the MaxInt64 range, must encode byte-identically to dense.
func TestCov_encodePopulatedParityAcrossConfigs(t *testing.T) {
	configs := []struct {
		name      string
		low, high int64
		sig       int
		pairs     []cov_encode_pair
	}{
		{
			name: "1_1e9_2",
			low:  1, high: 1_000_000_000, sig: 2,
			pairs: []cov_encode_pair{{1, 3}, {1000, 7}, {12345, 2}, {999_999_999, 1}},
		},
		{
			name: "100_3p6e9_3_lowGt1",
			low:  100, high: 3_600_000_000, sig: 3,
			// values below lowestDiscernible collapse into low buckets; spread wide.
			pairs: []cov_encode_pair{{50, 4}, {100, 5}, {250, 9}, {1_000_000, 3}, {3_599_999_999, 1}},
		},
		{
			name: "1_maxint64_5",
			low:  1, high: 9223372036854775807, sig: 5,
			pairs: []cov_encode_pair{{1, 2}, {123456, 6}, {1 << 40, 3}, {1 << 61, 1}},
		},
		{
			name: "1_3p6e9_3",
			low:  1, high: 3_600_000_000, sig: 3,
			pairs: []cov_encode_pair{{7, 8}, {42, 1}, {4096, 300}, {2_000_000_000, 5}},
		},
	}
	for _, c := range configs {
		d, p, err := cov_encode_build(c.low, c.high, c.sig, c.pairs)
		if err != nil {
			t.Fatalf("%s: build: %v", c.name, err)
		}
		cov_encode_assertParity(t, "pop/"+c.name, d, p)
	}
}

// Zero-run coalescing: two populated buckets separated by a large empty gap
// must produce the same coalesced negative run as dense, and the payload must
// be far shorter than one entry per flat index (proving the RLE ran).
func TestCov_encodeZeroRunCoalescing(t *testing.T) {
	// bucket at value 1 and a bucket near the top leave a huge zero gap.
	pairs := []cov_encode_pair{{1, 5}, {900_000_000, 5}}
	d, p, err := cov_encode_build(1, 1_000_000_000, 3, pairs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cov_encode_assertParity(t, "zerorun", d, p)

	payload := p.fillBufferFromSparse()
	countsLimit := int32(p.geom.countsIndexFor(p.Max()) + 1)
	// If coalescing failed we'd emit ~countsLimit entries (>= countsLimit bytes).
	// With coalescing the payload is a tiny handful of bytes.
	if int32(len(payload)) >= countsLimit {
		t.Fatalf("zero-run not coalesced: payload len %d >= countsLimit %d", len(payload), countsLimit)
	}
	if len(payload) > 32 {
		t.Fatalf("zero-run payload unexpectedly large: %d bytes", len(payload))
	}

	// Decode the coalesced payload back through the sparse decoder and confirm
	// only the two buckets survive with the right counts.
	enc, err := p.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rt, err := DecodePacked(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rt.Populated() != 2 {
		t.Fatalf("roundtrip populated = %d, want 2", rt.Populated())
	}
	if rt.TotalCount() != 10 {
		t.Fatalf("roundtrip total = %d, want 10", rt.TotalCount())
	}
	if rt.CountAtValue(1) != 5 || rt.CountAtValue(900_000_000) != 5 {
		t.Fatalf("roundtrip counts wrong: %d %d", rt.CountAtValue(1), rt.CountAtValue(900_000_000))
	}
}

// A single count of 1 in the middle emits exactly one leading zero-run (>1),
// then a single positive, then a trailing coalesced run is elided because
// countsLimit stops right after the last populated bucket.
func TestCov_encodeSingleValueParity(t *testing.T) {
	pairs := []cov_encode_pair{{5000, 1}}
	d, p, err := cov_encode_build(1, 1_000_000_000, 3, pairs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cov_encode_assertParity(t, "single", d, p)
	if p.CountWidth() != 1 {
		t.Fatalf("single width = %d, want 1", p.CountWidth())
	}
}

// countsLimit edges: (a) a single populated bucket at flat index 0 yields the
// smallest possible countsLimit, and (b) a value in the top bucket drives
// countsLimit to the full counts length. Both must match dense exactly.
func TestCov_encodeCountsLimitEdges(t *testing.T) {
	// (a) bucket 0: value 0 lands at flat index 0.
	{
		d, p, err := cov_encode_build(1, 1_000_000_000, 3, []cov_encode_pair{{0, 42}})
		if err != nil {
			t.Fatalf("bucket0 build: %v", err)
		}
		// idx[0] must be flat index 0 -> countsLimit == 1.
		if p.Populated() != 1 || p.idx[0] != 0 {
			t.Fatalf("bucket0: populated=%d idx0=%d, want 1 and 0", p.Populated(), p.idx[0])
		}
		countsLimit := int32(p.geom.countsIndexFor(p.Max()) + 1)
		if countsLimit != 1 {
			t.Fatalf("bucket0: countsLimit = %d, want 1", countsLimit)
		}
		cov_encode_assertParity(t, "countsLimit/bucket0", d, p)
	}

	// (b) top bucket: record highestTrackableValue so countsLimit reaches the
	// final populated index (== dense's own countsLimit for the same value).
	{
		const high int64 = 1_000_000_000
		d, p, err := cov_encode_build(1, high, 3, []cov_encode_pair{{high, 1}})
		if err != nil {
			t.Fatalf("topbucket build: %v", err)
		}
		lastIdx := p.idx[p.Populated()-1]
		countsLimit := int32(p.geom.countsIndexFor(p.Max()) + 1)
		if countsLimit != lastIdx+1 {
			t.Fatalf("topbucket: countsLimit=%d, want lastIdx+1=%d", countsLimit, lastIdx+1)
		}
		// dense agrees on the flat index for the top value.
		denseLimit := int32(d.countsIndexFor(d.Max()) + 1)
		if denseLimit != countsLimit {
			t.Fatalf("topbucket: dense countsLimit %d != packed %d", denseLimit, countsLimit)
		}
		cov_encode_assertParity(t, "countsLimit/topbucket", d, p)
	}

	// (c) top bucket of the MaxInt64-range histogram: exercises the overflow-safe
	// highestEquivalent saturation feeding countsLimit.
	{
		const high int64 = 9223372036854775807
		d, p, err := cov_encode_build(1, high, 5, []cov_encode_pair{{high, 1}})
		if err != nil {
			t.Fatalf("maxint top build: %v", err)
		}
		cov_encode_assertParity(t, "countsLimit/maxint-top", d, p)
	}
}

// Mixed widths + gaps + multiple configs in one table, asserting both payload
// and full stream identity, to sweep the remaining branch combinations of
// fillBufferFromSparse (leading zeros, interior single zeros vs runs, trailing
// truncation) together with slotGet at each width.
func TestCov_encodeMixedTable(t *testing.T) {
	type tc struct {
		name      string
		low, high int64
		sig       int
		pairs     []cov_encode_pair
		wantWidth int
	}
	cases := []tc{
		{
			name: "adjacent_no_gap", low: 1, high: 1_000_000_000, sig: 2,
			// adjacent flat indices -> no interior zero run
			pairs:     []cov_encode_pair{{1, 1}, {2, 1}, {3, 1}},
			wantWidth: 1,
		},
		{
			name: "single_interior_zero", low: 1, high: 1_000_000_000, sig: 3,
			pairs:     []cov_encode_pair{{1000, 2}, {1002, 2}}, // exactly one zero between (zeros==1 path)
			wantWidth: 1,
		},
		{
			name: "wide_and_gappy", low: 100, high: 3_600_000_000, sig: 3,
			pairs:     []cov_encode_pair{{100, 70000}, {5_000_000, 3}, {3_500_000_000, 1}},
			wantWidth: 4,
		},
		{
			name: "maxint_wide", low: 1, high: 9223372036854775807, sig: 4,
			pairs:     []cov_encode_pair{{10, 5_000_000_000}, {1 << 50, 2}},
			wantWidth: 8,
		},
	}
	for _, c := range cases {
		d, p, err := cov_encode_build(c.low, c.high, c.sig, c.pairs)
		if err != nil {
			t.Fatalf("%s: build: %v", c.name, err)
		}
		if p.CountWidth() != c.wantWidth {
			t.Fatalf("%s: width = %d, want %d", c.name, p.CountWidth(), c.wantWidth)
		}
		cov_encode_assertParity(t, "mixed/"+c.name, d, p)
	}
}
