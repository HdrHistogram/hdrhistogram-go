package hdrhistogram

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// packedFuzzGeoms are the geometries the decode fuzzer picks from. They are kept
// to valid configurations so the fuzzer exercises the payload decoder rather
// than header validation.
var packedFuzzGeoms = []struct {
	low, high int64
	sig       int32
}{
	{1, 3600000000, 3},
	{1, 1000, 1},
	{1, 1000000, 2},
	{1000, 1 << 40, 4},
	{1, 1 << 62, 5},
}

// FuzzPackedDecodeHostile: DecodePacked must never panic on an arbitrary
// payload, and any successfully-decoded histogram must survive query +
// re-encode + re-decode. The fuzzer mutates the raw zig-zag payload and the
// test wraps it in a valid header, zlib and base64, so mutations reach
// fillSparseFromPayload instead of failing at the outer layers.
func FuzzPackedDecodeHostile(f *testing.F) {
	f.Add(uint8(0), []byte{0x14, 0x14})                                                 // two counts of 10
	f.Add(uint8(0), []byte{0x01, 0x14})                                                 // zero-run of 1, then a count
	f.Add(uint8(1), []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x0a}) // MinInt64 zero-run
	f.Add(uint8(2), []byte{0xfe, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})       // MaxInt64 count
	f.Add(uint8(3), []byte{})                                                           // empty histogram
	f.Add(uint8(4), []byte{0x81, 0x80, 0x01, 0x02})                                     // multi-byte zero-run

	f.Fuzz(func(t *testing.T, geom uint8, payload []byte) {
		g := packedFuzzGeoms[int(geom)%len(packedFuzzGeoms)]
		hp, err := DecodePacked(wrapV2PayloadGeom(t, g.low, g.high, g.sig, int32(len(payload)), payload))
		if err != nil {
			return
		}
		_ = hp.TotalCount()
		_ = hp.Min()
		_ = hp.Max()
		_ = hp.ValueAtPercentile(0)
		_ = hp.ValueAtPercentile(50)
		_ = hp.ValueAtPercentile(100)
		_ = hp.ValueAtPercentilesSlice([]float64{0, 99, 99.9, 100})
		if hp.Populated() > 0 && hp.CountAtValue(hp.Max()) == 0 {
			t.Fatalf("CountAtValue(Max()=%d) == 0 on a populated histogram", hp.Max())
		}
		re, err := hp.Encode()
		if err != nil {
			t.Fatalf("re-encode of a decoded histogram failed: %v", err)
		}
		hp2, err := DecodePacked(re)
		if err != nil {
			t.Fatalf("re-decode of our own stream failed: %v", err)
		}
		if hp2.TotalCount() != hp.TotalCount() {
			t.Fatalf("total drifted across re-encode: %d != %d", hp2.TotalCount(), hp.TotalCount())
		}
	})
}

// FuzzPackedDifferential: interpret the input as 16-byte (value, count) records,
// apply the same stream to dense and packed, and assert parity at every
// recorded bucket, the populated-bucket count, min/max/total, percentiles and a
// byte-identical V2 encode. The geometry is small so each execution is cheap.
func FuzzPackedDifferential(f *testing.F) {
	const high = 1000000
	seed := make([]byte, 0, 32)
	seed = binary.BigEndian.AppendUint64(seed, 1000)
	seed = binary.BigEndian.AppendUint64(seed, 1)
	seed = binary.BigEndian.AppendUint64(seed, 6147)
	seed = binary.BigEndian.AppendUint64(seed, 70000)
	f.Add(seed)

	f.Fuzz(func(t *testing.T, data []byte) {
		d := New(1, high, 2)
		p := NewPacked(1, high, 2)
		touched := make(map[int]int64) // counts index -> one value recorded there
		for i := 0; i+16 <= len(data); i += 16 {
			v := int64(binary.BigEndian.Uint64(data[i:]))
			c := int64(binary.BigEndian.Uint64(data[i+8:]))
			if v < 0 {
				v = -v
			}
			v = v%high + 1
			if c < 0 {
				c = -c
			}
			c = c%100000 + 1
			if err := d.RecordValues(v, c); err != nil {
				continue
			}
			if err := p.RecordValues(v, c); err != nil {
				t.Fatalf("packed rejected a value dense accepted: v=%d c=%d: %v", v, c, err)
			}
			touched[d.countsIndexFor(v)] = v
		}
		if int(p.Populated()) != len(touched) {
			t.Fatalf("populated %d != %d distinct recorded buckets", p.Populated(), len(touched))
		}
		for idx, v := range touched {
			if got, want := p.CountAtValue(v), d.counts[idx]; got != want {
				t.Fatalf("CountAtValue(%d) packed %d != dense %d", v, got, want)
			}
		}
		if d.TotalCount() != p.TotalCount() {
			t.Fatalf("total %d != %d", d.TotalCount(), p.TotalCount())
		}
		if d.Min() != p.Min() || d.Max() != p.Max() {
			t.Fatalf("min/max mismatch: dense (%d,%d) packed (%d,%d)", d.Min(), d.Max(), p.Min(), p.Max())
		}
		for _, pc := range []float64{0, 25, 50, 90, 99, 99.9, 100} {
			if d.ValueAtPercentile(pc) != p.ValueAtPercentile(pc) {
				t.Fatalf("p%.4g dense %d != packed %d", pc, d.ValueAtPercentile(pc), p.ValueAtPercentile(pc))
			}
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
	})
}
