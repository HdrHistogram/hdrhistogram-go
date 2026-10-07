package hdrhistogram

// Every helper the packed fuzzers use lives in this file and takes no
// *testing.T: the ClusterFuzzLite build (go-118-fuzz-build) compiles only the
// fuzz target's own file, with a shimmed testing package.

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

// buildPackedV2Stream builds a base64 V2 compressed stream around a raw zig-zag
// payload. payloadLen, normOff and cookieXor let callers write header fields
// that disagree with the payload.
func buildPackedV2Stream(low, high int64, sig, payloadLen, normOff int32, cookieXor uint32, payload []byte) []byte {
	hdr := make([]byte, ENCODING_HEADER_SIZE)
	binary.BigEndian.PutUint32(hdr[0:], uint32(V2EncodingCookieBase|0x10)^cookieXor)
	binary.BigEndian.PutUint32(hdr[4:], uint32(payloadLen))
	binary.BigEndian.PutUint32(hdr[8:], uint32(normOff))
	binary.BigEndian.PutUint32(hdr[12:], uint32(sig))
	binary.BigEndian.PutUint64(hdr[16:], uint64(low))
	binary.BigEndian.PutUint64(hdr[24:], uint64(high))
	binary.BigEndian.PutUint64(hdr[32:], math.Float64bits(1.0))
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

// packedFuzzGeom is a histogram geometry the fuzzers can select by index.
type packedFuzzGeom struct {
	low, high int64
	sig       int32
}

// packedDecodeGeoms are valid geometries for the decode fuzzer. The MaxInt64
// entries make the top bucket's highest-equivalent value saturate.
var packedDecodeGeoms = []packedFuzzGeom{
	{1, 3600000000, 3},
	{1, 1000, 1},
	{1, 1000000, 2},
	{1000, 1 << 40, 4},
	{1, 1 << 62, 5},
	{1, math.MaxInt64, 1},
	{1 << 20, math.MaxInt64, 2},
}

// packedDiffGeoms are kept small (countsLen of a few thousand at most) so the
// differential fuzzer can compare every bucket after each run cheaply.
var packedDiffGeoms = []packedFuzzGeom{
	{1, 1000000, 2},
	{1, 1000, 1},
	{1, math.MaxInt64, 1},
	{1 << 20, math.MaxInt64, 1},
	{7, 123456789, 2},
	{1, 2, 1},
}

// packedCheckState verifies the structural invariants of a packed histogram
// and returns a description of the first violation, or "".
func packedCheckState(p *PackedHistogram) string {
	if int(p.size) != len(p.idx) || len(p.cnt) != int(p.size)*int(p.width) {
		return "size/len mismatch"
	}
	var sum int64
	saturated := false
	for i := int32(0); i < p.size; i++ {
		if p.idx[i] < 0 || p.idx[i] >= p.geom.countsLen {
			return "index outside countsLen"
		}
		if i > 0 && p.idx[i] <= p.idx[i-1] {
			return "indices not strictly ascending"
		}
		c := p.slotGet(i)
		if c <= 0 {
			return "non-positive stored count"
		}
		if c > math.MaxInt64-sum {
			saturated = true
		} else {
			sum += c
		}
	}
	if saturated {
		if p.totalCount != math.MaxInt64 {
			return "bucket sum overflows but total is not saturated"
		}
	} else if sum != p.totalCount {
		return "bucket sum != totalCount"
	}
	return ""
}

// referenceMerge records every bucket of from into a copy-equivalent of p,
// in value order, which is what Merge must match.
func referenceMerge(p, from *PackedHistogram) (*PackedHistogram, int64) {
	ref := NewPacked(p.geom.lowestDiscernibleValue, p.geom.highestTrackableValue, int(p.geom.significantFigures))
	p.ForEachBucket(func(v, c int64) bool {
		if ref.RecordValues(v, c) != nil {
			panic("reference: cannot copy p")
		}
		return true
	})
	var dropped int64
	from.ForEachBucket(func(v, c int64) bool {
		if ref.RecordValues(v, c) != nil {
			dropped += c
		}
		return true
	})
	return ref, dropped
}

// packedSameBuckets reports whether two packed histograms hold the same
// populated buckets and counts.
func packedSameBuckets(a, b *PackedHistogram) bool {
	if a.size != b.size {
		return false
	}
	for i := int32(0); i < a.size; i++ {
		if a.idx[i] != b.idx[i] || a.slotGet(i) != b.slotGet(i) {
			return false
		}
	}
	return true
}

// packedRefPercentile is an independent reference for ValueAtPercentile: a
// plain saturating walk over the populated buckets, without the blocked scan.
func packedRefPercentile(p *PackedHistogram, pct float64) int64 {
	if p.totalCount == 0 {
		return 0
	}
	if pct > 100 {
		pct = 100
	} else if pct < 0 {
		pct = 0
	}
	target := p.countAtPercentile(pct)
	var running, vfi int64
	for i := int32(0); i < p.size; i++ {
		c := p.slotGet(i)
		if c > math.MaxInt64-running {
			running = math.MaxInt64
		} else {
			running += c
		}
		if running >= target {
			vfi = p.geom.valueFromFlatIndex(p.idx[i])
			break
		}
	}
	if pct == 0 {
		return p.geom.lowestEquivalentValue(vfi)
	}
	return p.highestEquivalent(vfi)
}

// packedCheckPercentiles checks ValueAtPercentile and ValueAtPercentilesSlice
// against each other and against packedRefPercentile, returning a description
// of the first mismatch, or "".
func packedCheckPercentiles(p *PackedHistogram, pcts []float64) string {
	slice := p.ValueAtPercentilesSlice(pcts)
	for i, pct := range pcts {
		ref := packedRefPercentile(p, pct)
		if got := p.ValueAtPercentile(pct); got != ref {
			return fmt.Sprintf("ValueAtPercentile(%v) = %d, reference %d", pct, got, ref)
		}
		if slice[i] != ref {
			return fmt.Sprintf("ValueAtPercentilesSlice[%d] (p%v) = %d, reference %d", i, pct, slice[i], ref)
		}
	}
	return ""
}

// FuzzPackedDecodeHostile: DecodePacked must never panic on an arbitrary
// payload or inconsistent header, and any histogram it accepts must be
// structurally sound and survive re-encode + re-decode unchanged. The fuzzer
// mutates the raw zig-zag payload plus selected header fields; the stream is
// wrapped in valid zlib and base64 so mutations reach the decoder itself.
//
// flags: bit 0 offsets payloadLen by lenDelta, bit 1 writes lenDelta as the
// normalizingIndexOffset, bit 2 flips low bits of the inner cookie.
func FuzzPackedDecodeHostile(f *testing.F) {
	f.Add(uint8(0), int8(0), uint8(0), []byte{0x14, 0x14})                                                 // two counts of 10
	f.Add(uint8(0), int8(0), uint8(0), []byte{0x01, 0x14})                                                 // zero-run of 1, then a count
	f.Add(uint8(1), int8(0), uint8(0), []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x0a}) // MinInt64 zero-run
	f.Add(uint8(2), int8(0), uint8(0), []byte{0xfe, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})       // MaxInt64 count
	f.Add(uint8(3), int8(0), uint8(0), []byte{})                                                           // empty histogram
	f.Add(uint8(4), int8(0), uint8(0), []byte{0x81, 0x80, 0x01, 0x02})                                     // multi-byte zero-run
	f.Add(uint8(5), int8(0), uint8(0), []byte{0xfd, 0x0e, 0x02})                                           // zero-run of 959, then the top bucket (wraps without saturation)
	f.Add(uint8(0), int8(3), uint8(1), []byte{0x14, 0x14})                                                 // payloadLen too long
	f.Add(uint8(0), int8(-1), uint8(1), []byte{0x14, 0x14})                                                // payloadLen too short
	f.Add(uint8(0), int8(2), uint8(2), []byte{0x14})                                                       // normalizingIndexOffset 2 (ignored)
	f.Add(uint8(0), int8(1), uint8(2), []byte{0x14})                                                       // legacy Go offset 1 (ignored)
	f.Add(uint8(0), int8(0), uint8(0), []byte{0xfe, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}) // MaxInt64 then 1: saturated total
	f.Add(uint8(0), int8(0), uint8(0), []byte{0x02, 0xfe, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}) // 1 then MaxInt64

	f.Fuzz(func(t *testing.T, geom uint8, lenDelta int8, flags uint8, payload []byte) {
		g := packedDecodeGeoms[int(geom)%len(packedDecodeGeoms)]
		payloadLen := int32(len(payload))
		var normOff int32
		var cookieXor uint32
		if flags&1 != 0 {
			payloadLen += int32(lenDelta)
		}
		if flags&2 != 0 {
			normOff = int32(lenDelta)
		}
		if flags&4 != 0 {
			cookieXor = uint32(lenDelta) & 0x0f
		}
		hp, err := DecodePacked(buildPackedV2Stream(g.low, g.high, g.sig, payloadLen, normOff, cookieXor, payload))
		if err != nil {
			return
		}
		if payloadLen != int32(len(payload)) || cookieXor != 0 {
			t.Fatalf("accepted an inconsistent header: payloadLen %d (actual %d), normOff %d, cookieXor %#x",
				payloadLen, len(payload), normOff, cookieXor)
		}
		// normalizingIndexOffset only describes a writer's in-memory layout, so
		// it must never change what is decoded.
		if normOff != 0 {
			plain, err := DecodePacked(buildPackedV2Stream(g.low, g.high, g.sig, payloadLen, 0, 0, payload))
			if err != nil || !packedSameBuckets(hp, plain) || hp.TotalCount() != plain.TotalCount() {
				t.Fatalf("offset %d changed the decoded histogram (offset-0 decode err %v)", normOff, err)
			}
		}
		if msg := packedCheckState(hp); msg != "" {
			t.Fatalf("decoded state invalid: %s", msg)
		}
		saturated := hp.TotalCount() == math.MaxInt64
		if hp.Populated() > 0 {
			if hp.Max() < hp.Min() {
				t.Fatalf("Max %d < Min %d", hp.Max(), hp.Min())
			}
			if hp.CountAtValue(hp.Max()) == 0 || hp.CountAtValue(hp.Min()) == 0 {
				t.Fatalf("CountAtValue is 0 at Min %d or Max %d", hp.Min(), hp.Max())
			}
			if !saturated && hp.ValueAtPercentile(100) != hp.Max() {
				t.Fatalf("p100 %d != Max %d", hp.ValueAtPercentile(100), hp.Max())
			}
		}
		if msg := packedCheckPercentiles(hp, []float64{100, 0, 50, 99, 99.9, -1, 50, 150}); msg != "" {
			t.Fatal(msg)
		}
		re, err := hp.Encode()
		if err != nil {
			t.Fatalf("re-encode of a decoded histogram failed: %v", err)
		}
		hp2, err := DecodePacked(re)
		if err != nil {
			t.Fatalf("re-decode of our own stream failed: %v", err)
		}
		if hp2.TotalCount() != hp.TotalCount() || !packedSameBuckets(hp, hp2) {
			t.Fatalf("state drifted across re-encode: total %d -> %d, populated %d -> %d",
				hp.TotalCount(), hp2.TotalCount(), hp.Populated(), hp2.Populated())
		}
	})
}

// packedFuzzValue maps a raw fuzz word to a value, biased towards the regions
// where packed and dense are most likely to diverge.
func packedFuzzValue(raw uint64, g packedFuzzGeom, topEnd int64) int64 {
	x := raw >> 3
	switch raw & 7 {
	case 0:
		return int64(x % uint64(g.high+1)) // anywhere in [0, high]
	case 1:
		return 0
	case 2: // around a power of two
		v := int64(1) << (x % 63)
		switch (x >> 6) % 3 {
		case 0:
			return v - 1
		case 2:
			if v < math.MaxInt64 {
				return v + 1
			}
		}
		return v
	case 3: // above highestTrackableValue but inside the last bucket
		return g.high + int64(x%uint64(topEnd-g.high+1))
	case 4: // just out of range, or negative when the top bucket ends at MaxInt64
		if topEnd < math.MaxInt64-1000 {
			return topEnd + 1 + int64(x%1000)
		}
		return -1 - int64(x%1000)
	case 5: // the lowest discernible region
		return int64(x % uint64(4*g.low))
	default:
		return int64(raw)
	}
}

// FuzzPackedDifferential runs a fuzzed sequence of operations against a dense
// Histogram and a PackedHistogram in lockstep and requires identical results:
// record errors, per-bucket counts, totals, min/max, percentiles, byte-identical
// V2 encodes, and continued recording after decoding each side's stream into
// the other type. Each operation is 17 bytes: an opcode, a value word and an
// argument word. Opcodes: 0 record, 1 record a small count, 2 record a large
// or width-edge count, 3 query, 4 encode and cross-decode, 5 min/max, 6 record
// a run of distinct buckets, 7 reset (and maybe compact), 8 merges (MergeInto,
// MergeFrom, ForEachBucket and packed Merge), 9 compact.
func FuzzPackedDifferential(f *testing.F) {
	op := func(code byte, v, arg uint64) []byte {
		b := []byte{code}
		b = binary.BigEndian.AppendUint64(b, v)
		return binary.BigEndian.AppendUint64(b, arg)
	}
	var seed []byte
	seed = append(seed, op(0, 1000<<3, 0)...)      // record 1000
	seed = append(seed, op(1, 6147<<3, 70000)...)  // record 6147 x 70001
	seed = append(seed, op(2, 3, 1<<40)...)        // a large count in the top bucket
	seed = append(seed, op(3, 1000<<3, 99900)...)  // query
	seed = append(seed, op(4, 0, 0)...)            // round-trip through both decoders
	seed = append(seed, op(0, (1<<12)<<3|2, 0)...) // around a power of two
	f.Add(uint8(0), seed)
	f.Add(uint8(2), seed)
	f.Add(uint8(5), seed)
	// Exactly 8 and 16 populated buckets, so percentile targets fall on the
	// edges of the 8-bucket scan blocks.
	for _, k := range []uint64{8, 16} {
		var s []byte
		for v := uint64(1); v <= k; v++ {
			s = append(s, op(0, v<<3, 0)...)
		}
		s = append(s, op(3, 0, 100000)...)
		f.Add(uint8(1), s)
	}
	// Bucket counts landing exactly on each count-width edge.
	for _, edge := range []uint64{0xFF, 0xFFFF, 0xFFFFFFFF} {
		var s []byte
		s = append(s, op(2, 1000<<3, edge-2)...) // count edge-1
		s = append(s, op(0, 1000<<3, 0)...)      // count edge
		s = append(s, op(0, 1000<<3, 0)...)      // count edge+1
		s = append(s, op(3, 1000<<3, 50000)...)
		f.Add(uint8(0), s)
	}
	f.Add(uint8(4), op(6, 7<<3, 39|(37<<8))) // bulk: 40 buckets with stride 38
	var window []byte                        // fill, merge across geometries, reset, refill, merge
	window = append(window, op(6, 7<<3, 39|(37<<8))...)
	window = append(window, op(2, 3, 1<<33)...)
	for k := uint64(0); k < 6; k++ {
		window = append(window, op(8, 0, k)...)
	}
	window = append(window, op(7, 0, 0)...)
	window = append(window, op(1, 500<<3, 99)...)
	window = append(window, op(8, 0, 1)...)
	f.Add(uint8(0), window)

	f.Fuzz(func(t *testing.T, geom uint8, ops []byte) {
		g := packedDiffGeoms[int(geom)%len(packedDiffGeoms)]
		d := New(g.low, g.high, int(g.sig))
		p := NewPacked(g.low, g.high, int(g.sig))
		// Highest value the counts array can hold, which can be well above high.
		topEnd := p.highestEquivalent(p.geom.valueFromFlatIndex(p.geom.countsLen - 1))
		record := func(v, n int64) {
			if n > math.MaxInt64-d.TotalCount() {
				// Intentional difference: packed rejects a total overflow, dense wraps.
				if err := p.RecordValues(v, n); err == nil {
					t.Fatalf("packed accepted RecordValues(%d, %d) overflowing total %d", v, n, p.TotalCount())
				}
				return
			}
			derr := d.RecordValues(v, n)
			perr := p.RecordValues(v, n)
			if (derr == nil) != (perr == nil) {
				t.Fatalf("RecordValues(%d, %d): dense err %v, packed err %v", v, n, derr, perr)
			}
		}
		checkPercentiles := func(pcts []float64) {
			if msg := packedCheckPercentiles(p, pcts); msg != "" {
				t.Fatal(msg)
			}
			if d.TotalCount() <= 1<<52 {
				for _, pct := range pcts {
					if dv, pv := d.ValueAtPercentile(pct), p.ValueAtPercentile(pct); dv != pv {
						t.Fatalf("p%v: dense %d, packed %d", pct, dv, pv)
					}
				}
			}
		}

		// Bound the work per input so CI fuzzing time is spent on many inputs.
		const maxOps, maxRoundTrips = 256, 4
		roundTrips := 0
		for nops := 0; len(ops) >= 17 && nops < maxOps; nops++ {
			code := ops[0] % 10
			v := packedFuzzValue(binary.BigEndian.Uint64(ops[1:]), g, topEnd)
			arg := binary.BigEndian.Uint64(ops[9:])
			ops = ops[17:]

			switch code {
			case 0, 1, 2:
				n := int64(1)
				switch code {
				case 1:
					n = int64(arg%1000) + 1
				case 2:
					n = int64(arg%(1<<40)) + 1 // large enough to widen counts to 8 bytes
					if arg>>40&1 == 1 && v >= 0 {
						// Land the bucket on a count-width edge: max-1, max or max+1.
						w := [3]uint8{1, 2, 4}[(arg>>41)%3]
						n = packedWidthMax(w) - p.CountAtValue(v) + int64(arg%3) - 1
						if n < 1 {
							n = 1
						}
					}
				}
				record(v, n)
			case 3:
				if v >= 0 {
					want := int64(0)
					if i := d.countsIndexFor(v); i >= 0 && i < len(d.counts) {
						want = d.counts[i]
					}
					if got := p.CountAtValue(v); got != want {
						t.Fatalf("CountAtValue(%d): packed %d, dense %d", v, got, want)
					}
				}
				pct := float64(arg%100001) / 1000
				checkPercentiles([]float64{pct, 0, 100, -1, pct, 150})
			case 4:
				if roundTrips++; roundTrips > maxRoundTrips {
					continue
				}
				de, err := d.Encode(V2CompressedEncodingCookieBase)
				if err != nil {
					t.Fatal(err)
				}
				pe, err := p.Encode()
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(de, pe) {
					t.Fatalf("encode mismatch (dense %d bytes, packed %d bytes)", len(de), len(pe))
				}
				// Swap: continue with each side decoded from the other's stream.
				if p, err = DecodePacked(de); err != nil {
					t.Fatalf("DecodePacked(dense stream): %v", err)
				}
				if d, err = Decode(pe); err != nil {
					t.Fatalf("Decode(packed stream): %v", err)
				}
			case 5:
				if d.Min() != p.Min() || d.Max() != p.Max() {
					t.Fatalf("min/max: dense (%d,%d), packed (%d,%d)", d.Min(), d.Max(), p.Min(), p.Max())
				}
			case 6: // bulk: up to 40 distinct buckets in one op
				k := int64(arg%40) + 1
				stride := int64((arg>>8)%1000) + 1
				for i := int64(0); i < k && v <= math.MaxInt64-i*stride; i++ {
					record(v+i*stride, 1)
				}
			case 7: // reset both, as a rolling-window slot would be reused
				d.Reset()
				p.Reset()
				if arg&1 == 1 {
					p.Compact()
				}
			case 9: // Compact must not change the contents
				before, _ := p.Encode()
				p.Compact()
				if after, _ := p.Encode(); !bytes.Equal(before, after) {
					t.Fatal("Compact changed the encoding")
				}
				if msg := packedCheckState(p); msg != "" {
					t.Fatalf("after Compact: %s", msg)
				}
			case 8: // merges: MergeInto must equal dense Merge; MergeFrom must rebuild p
				dg := packedDiffGeoms[int(arg%uint64(len(packedDiffGeoms)))]
				want := New(dg.low, dg.high, int(dg.sig))
				got := New(dg.low, dg.high, int(dg.sig))
				if wd, gd := want.Merge(d), p.MergeInto(got); wd != gd {
					t.Fatalf("MergeInto dropped %d, dense Merge dropped %d", gd, wd)
				}
				if want.TotalCount() != got.TotalCount() {
					t.Fatalf("MergeInto total %d, dense Merge total %d", got.TotalCount(), want.TotalCount())
				}
				for i := range want.counts {
					if want.counts[i] != got.counts[i] {
						t.Fatalf("MergeInto counts[%d] = %d, dense Merge %d", i, got.counts[i], want.counts[i])
					}
				}
				q := NewPacked(g.low, g.high, int(g.sig))
				if dropped := q.MergeFrom(d); dropped != 0 || !packedSameBuckets(p, q) || q.TotalCount() != p.TotalCount() {
					t.Fatalf("MergeFrom(dense twin) differs: dropped %d, total %d vs %d", dropped, q.TotalCount(), p.TotalCount())
				}
				// MergeFrom into the fuzzed geometry must equal recording every
				// dense bucket there (exercising the re-recording and drop paths).
				mq, rq := NewPacked(dg.low, dg.high, int(dg.sig)), NewPacked(dg.low, dg.high, int(dg.sig))
				var wantDropped int64
				it := d.rIterator()
				for it.next() {
					if rq.RecordValues(it.valueFromIdx, it.countAtIdx) != nil {
						wantDropped += it.countAtIdx
					}
				}
				if dropped := mq.MergeFrom(d); dropped != wantDropped || !packedSameBuckets(mq, rq) || mq.TotalCount() != rq.TotalCount() {
					t.Fatalf("MergeFrom into %v: dropped %d (want %d), total %d (want %d)", dg, dropped, wantDropped, mq.TotalCount(), rq.TotalCount())
				}
				// ForEachBucket must visit exactly the dense buckets, in order.
				it = d.rIterator()
				p.ForEachBucket(func(v, c int64) bool {
					if !it.next() || v != it.valueFromIdx || c != it.countAtIdx {
						t.Fatalf("ForEachBucket (%d,%d) differs from dense iteration", v, c)
					}
					return true
				})
				if it.next() {
					t.Fatal("ForEachBucket stopped before dense iteration did")
				}
				// Packed-to-packed Merge must equal recording. Destinations: an
				// empty histogram, one holding every other bucket of the source
				// (so the fast path both inserts new buckets and adds to existing
				// ones), and one holding all of them; sources: p and mq.
				for _, src := range []*PackedHistogram{p, mq} {
					empty := NewPacked(dg.low, dg.high, int(dg.sig))
					half := NewPacked(dg.low, dg.high, int(dg.sig))
					k := 0
					mq.ForEachBucket(func(v, c int64) bool {
						if k%2 == 0 {
							_ = half.RecordValues(v, c)
						}
						k++
						return true
					})
					for _, dst := range []*PackedHistogram{empty, half, rq} {
						ref, wantDropped := referenceMerge(dst, src)
						if dropped := dst.Merge(src); dropped != wantDropped || !packedSameBuckets(dst, ref) || dst.TotalCount() != ref.TotalCount() {
							t.Fatalf("Merge into %v: dropped %d (want %d), total %d (want %d)", dg, dropped, wantDropped, dst.TotalCount(), ref.TotalCount())
						}
						if msg := packedCheckState(dst); msg != "" {
							t.Fatalf("after Merge: %s", msg)
						}
					}
				}
			}
			if d.TotalCount() != p.TotalCount() {
				t.Fatalf("total: dense %d, packed %d", d.TotalCount(), p.TotalCount())
			}
		}

		if msg := packedCheckState(p); msg != "" {
			t.Fatalf("packed state invalid: %s", msg)
		}
		j := int32(0)
		for i, c := range d.counts {
			var pc int64
			if j < p.size && int(p.idx[j]) == i {
				pc = p.slotGet(j)
				j++
			}
			if pc != c {
				t.Fatalf("counts[%d]: dense %d, packed %d", i, c, pc)
			}
		}
		if d.Min() != p.Min() || d.Max() != p.Max() {
			t.Fatalf("min/max: dense (%d,%d), packed (%d,%d)", d.Min(), d.Max(), p.Min(), p.Max())
		}
		checkPercentiles([]float64{100, 0, 25, 50, 90, 99, 99.9, 50})
	})
}
