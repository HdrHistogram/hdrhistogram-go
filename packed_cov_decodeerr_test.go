package hdrhistogram

// Deep coverage of DecodePacked / decodePackedCompressed / fillSparseFromPayload
// error paths. Every crafted input must return a non-nil error and must NEVER
// panic. Valid inputs (including a width-8 wide-count round trip) are checked
// for exact parity against the dense decoder where dense is well-defined.
//
// All exported symbols in this file are prefixed TestCov_decodeerr and all
// helpers cov_decodeerr_ so the file compiles alongside the other coverage
// files with no name collisions.

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
)

// ---- crafting helpers -------------------------------------------------------

// cov_decodeerr_buildInner assembles a 40-byte V2 inner header followed by the
// supplied payload, exactly matching the on-wire layout the decoder parses.
func cov_decodeerr_buildInner(cookie, payloadLen, normOff, sig int32, low, high int64, ratio float64, payload []byte) []byte {
	b := new(bytes.Buffer)
	_ = binary.Write(b, binary.BigEndian, cookie)     // 0-3
	_ = binary.Write(b, binary.BigEndian, payloadLen) // 4-7
	_ = binary.Write(b, binary.BigEndian, normOff)    // 8-11
	_ = binary.Write(b, binary.BigEndian, sig)        // 12-15
	_ = binary.Write(b, binary.BigEndian, low)        // 16-23
	_ = binary.Write(b, binary.BigEndian, high)       // 24-31
	_ = binary.Write(b, binary.BigEndian, ratio)      // 32-39
	b.Write(payload)
	return b.Bytes()
}

// cov_decodeerr_zlib zlib-compresses raw bytes the same way the encoder does.
func cov_decodeerr_zlib(t *testing.T, raw []byte) []byte {
	t.Helper()
	var zb bytes.Buffer
	w, err := zlib.NewWriterLevel(&zb, zlib.BestCompression)
	if err != nil {
		t.Fatalf("zlib writer: %v", err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	return zb.Bytes()
}

// cov_decodeerr_wrap wraps a compressed blob in the 8-byte outer header and
// base64-encodes it, honoring explicit cookie / length overrides so individual
// outer fields can be corrupted in isolation.
func cov_decodeerr_wrap(outerCookie, clen int32, compressed []byte) []byte {
	out := new(bytes.Buffer)
	_ = binary.Write(out, binary.BigEndian, outerCookie)
	_ = binary.Write(out, binary.BigEndian, clen)
	out.Write(compressed)
	return []byte(base64.StdEncoding.EncodeToString(out.Bytes()))
}

// cov_decodeerr_stream is the common path: build inner header+payload, compress,
// wrap with a correct outer cookie and matching compressed length.
func cov_decodeerr_stream(t *testing.T, cookie, payloadLen, normOff, sig int32, low, high int64, ratio float64, payload []byte) []byte {
	t.Helper()
	inner := cov_decodeerr_buildInner(cookie, payloadLen, normOff, sig, low, high, ratio, payload)
	comp := cov_decodeerr_zlib(t, inner)
	return cov_decodeerr_wrap(compressedEncodingCookie, int32(len(comp)), comp)
}

// cov_decodeerr_decodeNoPanic runs DecodePacked and converts any panic into a
// test failure so the "never panic" contract is asserted explicitly.
func cov_decodeerr_decodeNoPanic(t *testing.T, encoded []byte) (rp *PackedHistogram, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DecodePacked panicked on crafted input: %v", r)
		}
	}()
	rp, err = DecodePacked(encoded)
	return
}

// cov_decodeerr_wantErr asserts a non-nil error, a nil histogram, and that the
// message contains want.
func cov_decodeerr_wantErr(t *testing.T, name string, rp *PackedHistogram, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error, got nil (rp=%v)", name, rp)
	}
	if rp != nil {
		t.Fatalf("%s: expected nil histogram on error, got %v", name, rp)
	}
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: error %q does not contain %q", name, err.Error(), want)
	}
}

const (
	cov_decodeerr_low  int64 = 1
	cov_decodeerr_high int64 = 3600000000
	cov_decodeerr_sig  int32 = 3
)

// cov_decodeerr_countsLen returns the flat counts length for the standard test
// geometry, needed to craft overflow payloads before any decode happens.
func cov_decodeerr_countsLen() int32 {
	return New(cov_decodeerr_low, cov_decodeerr_high, int(cov_decodeerr_sig)).countsLen
}

// ---- outer-layer error paths ------------------------------------------------

func TestCov_decodeerr_OuterErrors(t *testing.T) {
	goodInner := cov_decodeerr_buildInner(encodingCookie, 0, 0, cov_decodeerr_sig,
		cov_decodeerr_low, cov_decodeerr_high, 1.0, nil)
	goodComp := cov_decodeerr_zlib(t, goodInner)

	cases := []struct {
		name    string
		encoded []byte
		want    string
	}{
		{
			name:    "invalid base64 bad chars",
			encoded: []byte("!!!!not-valid-base64!!!!"),
			want:    "", // base64 package error text; just require non-nil
		},
		{
			name:    "invalid base64 bad length",
			encoded: []byte("QUJD="), // 5 chars, malformed padding
			want:    "",
		},
		{
			name:    "buffer under 8 bytes",
			encoded: []byte(base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})),
			want:    "encoded histogram too short",
		},
		{
			name:    "buffer exactly 7 bytes",
			encoded: []byte(base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 3, 4, 5, 6})),
			want:    "encoded histogram too short",
		},
		{
			name: "wrong outer cookie",
			// masked cookie 0x11111101 != V2CompressedEncodingCookieBase
			encoded: cov_decodeerr_wrap(int32(0x11111111), 0, nil),
			want:    "encoding not supported",
		},
		{
			name:    "negative compressed length",
			encoded: cov_decodeerr_wrap(compressedEncodingCookie, -1, nil),
			want:    "negative lengthOfCompressedContents",
		},
		{
			name:    "compressed length exceeds buffer",
			encoded: cov_decodeerr_wrap(compressedEncodingCookie, 100, goodComp), // claims 100, has len(goodComp)
			want:    "compressed contents buffer is smaller",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rp, err := cov_decodeerr_decodeNoPanic(t, tc.encoded)
			cov_decodeerr_wantErr(t, tc.name, rp, err, tc.want)
		})
	}
}

// ---- inner-layer error paths ------------------------------------------------

func TestCov_decodeerr_NotZlib(t *testing.T) {
	// Outer header valid, cookie valid, clen matches, but the "compressed"
	// bytes are not a zlib stream: zlib.NewReader must fail.
	garbage := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05}
	enc := cov_decodeerr_wrap(compressedEncodingCookie, int32(len(garbage)), garbage)
	rp, err := cov_decodeerr_decodeNoPanic(t, enc)
	cov_decodeerr_wantErr(t, "not-zlib", rp, err, "")
}

func TestCov_decodeerr_InnerTruncated(t *testing.T) {
	// Decompresses to fewer than ENCODING_HEADER_SIZE (40) bytes.
	short := make([]byte, ENCODING_HEADER_SIZE-1) // 39 bytes
	comp := cov_decodeerr_zlib(t, short)
	enc := cov_decodeerr_wrap(compressedEncodingCookie, int32(len(comp)), comp)
	rp, err := cov_decodeerr_decodeNoPanic(t, enc)
	cov_decodeerr_wantErr(t, "inner-truncated", rp, err, "decompressed histogram truncated")
}

func TestCov_decodeerr_WrongInnerCookie(t *testing.T) {
	// Valid 40-byte header but a bogus inner cookie (masked != V2EncodingCookieBase).
	enc := cov_decodeerr_stream(t, int32(0x1c849300), 0, 0, cov_decodeerr_sig,
		cov_decodeerr_low, cov_decodeerr_high, 1.0, nil)
	rp, err := cov_decodeerr_decodeNoPanic(t, enc)
	cov_decodeerr_wantErr(t, "wrong-inner-cookie", rp, err, "encoding not supported")
}

func TestCov_decodeerr_NonZeroNormalizingOffset(t *testing.T) {
	// Craft by binary-editing a genuine dense stream: decode the outer wrapper,
	// inflate the inner buffer, overwrite bytes 8..11 (normalizingIndexOffset)
	// with a non-zero value, re-deflate and re-wrap.
	d := New(cov_decodeerr_low, cov_decodeerr_high, int(cov_decodeerr_sig))
	if err := d.RecordValues(12345, 7); err != nil {
		t.Fatal(err)
	}
	denseEnc, err := d.Encode(V2CompressedEncodingCookieBase)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(string(denseEnc))
	if err != nil {
		t.Fatal(err)
	}
	// Inflate inner.
	zr, err := zlib.NewReader(bytes.NewReader(raw[8:]))
	if err != nil {
		t.Fatal(err)
	}
	inner := new(bytes.Buffer)
	if _, err := inner.ReadFrom(zr); err != nil {
		t.Fatal(err)
	}
	_ = zr.Close()
	innerBytes := inner.Bytes()
	// Sanity: the original offset is zero before we corrupt it.
	if got := binary.BigEndian.Uint32(innerBytes[8:12]); got != 0 {
		t.Fatalf("precondition: dense normalizingIndexOffset = %d, want 0", got)
	}
	binary.BigEndian.PutUint32(innerBytes[8:12], 7) // non-zero offset
	comp := cov_decodeerr_zlib(t, innerBytes)
	enc := cov_decodeerr_wrap(compressedEncodingCookie, int32(len(comp)), comp)

	rp, err := cov_decodeerr_decodeNoPanic(t, enc)
	cov_decodeerr_wantErr(t, "nonzero-normoff", rp, err, "non-zero normalizingIndexOffset")
}

func TestCov_decodeerr_PayloadLenMismatch(t *testing.T) {
	// Header advertises a payload length that does not match the trailing bytes.
	payload := zig_zag_encode_i64(-5) // 1 real payload byte's worth
	enc := cov_decodeerr_stream(t, encodingCookie, int32(len(payload)+10), 0, cov_decodeerr_sig,
		cov_decodeerr_low, cov_decodeerr_high, 1.0, payload)
	rp, err := cov_decodeerr_decodeNoPanic(t, enc)
	cov_decodeerr_wantErr(t, "payloadlen-mismatch", rp, err, "PayloadLength should have the same size")
}

// ---- payload (fillSparseFromPayload) error paths ----------------------------

func TestCov_decodeerr_MalformedVarint(t *testing.T) {
	// A single 0x80 byte: continuation bit set but no following byte -> the
	// zig-zag decoder must report a truncation error rather than panic.
	payload := []byte{0x80}
	enc := cov_decodeerr_stream(t, encodingCookie, int32(len(payload)), 0, cov_decodeerr_sig,
		cov_decodeerr_low, cov_decodeerr_high, 1.0, payload)
	rp, err := cov_decodeerr_decodeNoPanic(t, enc)
	cov_decodeerr_wantErr(t, "malformed-varint", rp, err, "truncated compressed histogram decode")
}

func TestCov_decodeerr_ZeroRunOverflow(t *testing.T) {
	countsLen := cov_decodeerr_countsLen()
	// Negative count => zero run; make it one longer than the whole counts space.
	payload := zig_zag_encode_i64(-int64(countsLen) - 1)
	enc := cov_decodeerr_stream(t, encodingCookie, int32(len(payload)), 0, cov_decodeerr_sig,
		cov_decodeerr_low, cov_decodeerr_high, 1.0, payload)
	rp, err := cov_decodeerr_decodeNoPanic(t, enc)
	cov_decodeerr_wantErr(t, "zero-run-overflow", rp, err, "overflows counts length")
}

func TestCov_decodeerr_PositiveIndexOverflow(t *testing.T) {
	countsLen := cov_decodeerr_countsLen()
	// A zero run of exactly countsLen leaves dstIndex == countsLen (still legal),
	// then a positive count tries to write one past the end.
	var payload []byte
	payload = append(payload, zig_zag_encode_i64(-int64(countsLen))...)
	payload = append(payload, zig_zag_encode_i64(1)...)
	enc := cov_decodeerr_stream(t, encodingCookie, int32(len(payload)), 0, cov_decodeerr_sig,
		cov_decodeerr_low, cov_decodeerr_high, 1.0, payload)
	rp, err := cov_decodeerr_decodeNoPanic(t, enc)
	cov_decodeerr_wantErr(t, "positive-index-overflow", rp, err, "overflows counts length")
}

// A zero run that lands dstIndex exactly on countsLen is itself legal (it just
// terminates the payload); only a following positive write overflows. This pins
// the boundary so the overflow guard is not off-by-one.
func TestCov_decodeerr_ZeroRunToExactEndIsValid(t *testing.T) {
	countsLen := cov_decodeerr_countsLen()
	payload := zig_zag_encode_i64(-int64(countsLen)) // fills the whole space with zeros
	enc := cov_decodeerr_stream(t, encodingCookie, int32(len(payload)), 0, cov_decodeerr_sig,
		cov_decodeerr_low, cov_decodeerr_high, 1.0, payload)
	rp, err := cov_decodeerr_decodeNoPanic(t, enc)
	if err != nil {
		t.Fatalf("zero-run-to-exact-end: unexpected error: %v", err)
	}
	if rp == nil {
		t.Fatal("zero-run-to-exact-end: nil histogram")
	}
	if rp.TotalCount() != 0 {
		t.Fatalf("zero-run-to-exact-end: total = %d, want 0", rp.TotalCount())
	}
	if rp.Populated() != 0 {
		t.Fatalf("zero-run-to-exact-end: populated = %d, want 0", rp.Populated())
	}
}

// ---- valid wide-count (width 8) round trip ----------------------------------

func TestCov_decodeerr_WideCountRoundTrip(t *testing.T) {
	// Record counts large enough to force the sparse backing to width 8, encode,
	// then DecodePacked and check exact parity against the dense decoder.
	d := New(cov_decodeerr_low, cov_decodeerr_high, int(cov_decodeerr_sig))
	p := NewPacked(cov_decodeerr_low, cov_decodeerr_high, int(cov_decodeerr_sig))
	type rec struct {
		v, n int64
	}
	recs := []rec{
		{6147, 5_000_000_000}, // > 0xFFFFFFFF -> forces width 8
		{100, 3},
		{3599000000, 70000}, // > 0xFFFF, exercises a mid-width count too
		{1, 1},
	}
	for _, r := range recs {
		if err := d.RecordValues(r.v, r.n); err != nil {
			t.Fatal(err)
		}
		if err := p.RecordValues(r.v, r.n); err != nil {
			t.Fatal(err)
		}
	}
	if p.CountWidth() != 8 {
		t.Fatalf("precondition: source packed width = %d, want 8", p.CountWidth())
	}

	enc, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}

	got, err := cov_decodeerr_decodeNoPanic(t, enc)
	if err != nil {
		t.Fatalf("DecodePacked of wide-count stream: %v", err)
	}
	if got == nil {
		t.Fatal("wide-count: nil histogram")
	}
	// Decoded backing must have widened to 8 to hold the 5e9 count.
	if got.CountWidth() != 8 {
		t.Fatalf("decoded width = %d, want 8", got.CountWidth())
	}
	if got.TotalCount() != d.TotalCount() {
		t.Fatalf("decoded total = %d, want %d", got.TotalCount(), d.TotalCount())
	}
	if got.Min() != d.Min() || got.Max() != d.Max() {
		t.Fatalf("decoded min/max = %d/%d, want %d/%d", got.Min(), got.Max(), d.Min(), d.Max())
	}
	// Exact per-bucket count parity against the dense reference.
	for i := int32(0); i < d.countsLen; i++ {
		want := d.counts[i]
		gotC := got.CountAtValue(d.valueFromFlatIndex(i))
		if want != gotC {
			t.Fatalf("decoded count at flat idx %d = %d, want %d", i, gotC, want)
		}
	}
	// The 5e9 count survives intact (would truncate at any width < 8).
	if got.CountAtValue(6147) != 5_000_000_000 {
		t.Fatalf("decoded count at 6147 = %d, want 5000000000", got.CountAtValue(6147))
	}
	for _, pc := range []float64{0, 50, 90, 99, 99.9, 100} {
		if got.ValueAtPercentile(pc) != d.ValueAtPercentile(pc) {
			t.Fatalf("p%.4g: decoded %d != dense %d", pc, got.ValueAtPercentile(pc), d.ValueAtPercentile(pc))
		}
	}
}

// A hand-crafted valid width-8 payload (positive count > 0xFFFFFFFF) decoded
// directly, independent of the encoder, to cover the wide count path from raw
// bytes and confirm no truncation.
func TestCov_decodeerr_WideCountCraftedPayload(t *testing.T) {
	const bigCount int64 = 0x1_2345_6789 // > 0xFFFFFFFF
	var payload []byte
	payload = append(payload, zig_zag_encode_i64(-10)...) // skip first 10 buckets
	payload = append(payload, zig_zag_encode_i64(bigCount)...)
	enc := cov_decodeerr_stream(t, encodingCookie, int32(len(payload)), 0, cov_decodeerr_sig,
		cov_decodeerr_low, cov_decodeerr_high, 1.0, payload)

	got, err := cov_decodeerr_decodeNoPanic(t, enc)
	if err != nil {
		t.Fatalf("crafted wide-count decode: %v", err)
	}
	if got.CountWidth() != 8 {
		t.Fatalf("crafted wide-count width = %d, want 8", got.CountWidth())
	}
	if got.TotalCount() != bigCount {
		t.Fatalf("crafted wide-count total = %d, want %d", got.TotalCount(), bigCount)
	}
	// Flat index 10 carries the count; confirm via the dense value mapping.
	v := got.geom.valueFromFlatIndex(10)
	if got.CountAtValue(v) != bigCount {
		t.Fatalf("crafted wide-count at idx 10 = %d, want %d", got.CountAtValue(v), bigCount)
	}
	if got.Populated() != 1 {
		t.Fatalf("crafted wide-count populated = %d, want 1", got.Populated())
	}
}
