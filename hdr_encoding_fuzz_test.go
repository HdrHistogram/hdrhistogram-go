package hdrhistogram

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"testing"
)

// FuzzDecode asserts the public Decode never panics on arbitrary bytes, and that
// any histogram it does accept re-round-trips stably (TotalCount preserved).
func FuzzDecode(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("QUJD")) // "ABC" -> 3 bytes
	f.Add([]byte("not base64 @@@"))
	if h := New(1, 1000, 3); h != nil {
		_ = h.RecordValue(42)
		if enc, err := h.Encode(V2CompressedEncodingCookieBase); err == nil {
			f.Add(enc)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := Decode(data) // must never panic
		if err != nil || h == nil {
			return
		}
		enc, err := h.Encode(V2CompressedEncodingCookieBase)
		if err != nil {
			t.Fatalf("re-encode of a decoded histogram failed: %v", err)
		}
		h2, err := Decode(enc)
		if err != nil {
			t.Fatalf("re-decode of own encoding failed: %v", err)
		}
		if h2.TotalCount() != h.TotalCount() {
			t.Fatalf("TotalCount drift after round-trip: %d != %d", h2.TotalCount(), h.TotalCount())
		}
	})
}

// FuzzRecordEncodeDecode exercises the encode/decode hot path generatively and
// asserts a semantic round-trip (TotalCount and p99 preserved).
func FuzzRecordEncodeDecode(f *testing.F) {
	f.Add(int64(1), int64(1000000), uint8(3), int64(42))
	f.Add(int64(1), int64(3600000000), uint8(3), int64(1))
	f.Add(int64(1000), int64(1000000000), uint8(5), int64(999999))
	f.Fuzz(func(t *testing.T, lo, hi int64, sig uint8, v int64) {
		if lo < 1 || hi <= lo || hi > (1<<40) {
			t.Skip()
		}
		h := New(lo, hi, int(sig%5)+1)
		if h.RecordValue(v) != nil {
			t.Skip() // out of range for this geometry
		}
		enc, err := h.Encode(V2CompressedEncodingCookieBase)
		if err != nil {
			t.Fatalf("encode failed: %v", err)
		}
		h2, err := Decode(enc)
		if err != nil {
			t.Fatalf("decode of own encode failed: %v (lo=%d hi=%d sig=%d v=%d)", err, lo, hi, sig, v)
		}
		if h2.TotalCount() != h.TotalCount() {
			t.Fatalf("count drift: %d != %d", h2.TotalCount(), h.TotalCount())
		}
		if h2.ValueAtQuantile(99) != h.ValueAtQuantile(99) {
			t.Fatalf("p99 drift: %d != %d", h2.ValueAtQuantile(99), h.ValueAtQuantile(99))
		}
	})
}

// FuzzZigZagRoundTrip asserts the LEB128 zig-zag codec is a faithful identity and
// reports the exact number of bytes consumed.
func FuzzZigZagRoundTrip(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(-1))
	f.Add(int64(1 << 62))
	f.Add(int64(-(1 << 62)))
	f.Fuzz(func(t *testing.T, v int64) {
		buf := zig_zag_encode_i64(v)
		got, n, err := zig_zag_decode_i64(buf)
		if err != nil {
			t.Fatalf("decode(encode(%d)) errored: %v", v, err)
		}
		if got != v {
			t.Fatalf("round-trip mismatch: got %d want %d", got, v)
		}
		if n != len(buf) {
			t.Fatalf("consumed %d bytes, encoding was %d", n, len(buf))
		}
	})
}

// --- Regression tests for the decode panics found by the hardening audit. ---
// Each fails (panics) on the pre-fix code and passes with the guards in place.

func TestDecodeShortInputReturnsError(t *testing.T) {
	for _, in := range []string{"", "QQ==" /*1B*/, "QUJD" /*3B*/, "QUJDREVGRw==" /*7B*/} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decode(%q) panicked: %v", in, r)
				}
			}()
			if _, err := Decode([]byte(in)); err == nil {
				t.Errorf("Decode(%q): expected error for short input, got nil", in)
			}
		}()
	}
}

func TestDecodeNegativeLengthReturnsError(t *testing.T) {
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.BigEndian, compressedEncodingCookie)
	_ = binary.Write(buf, binary.BigEndian, int32(-1)) // attacker-controlled negative length
	enc := base64.StdEncoding.EncodeToString(buf.Bytes())
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Decode(negative length) panicked: %v", r)
		}
	}()
	if _, err := Decode([]byte(enc)); err == nil {
		t.Fatal("expected error for negative lengthOfCompressedContents, got nil")
	}
}

func TestFillCountsOverflowReturnsError(t *testing.T) {
	rh := New(1, 1000, 3) // small, fixed countsLen
	// A payload of more positive varints than counts[] can hold must be rejected,
	// not written out of range via setCountAtIndex.
	one := zig_zag_encode_i64(1)
	payload := make([]byte, 0, (len(rh.counts)+10)*len(one))
	for i := 0; i < len(rh.counts)+10; i++ {
		payload = append(payload, one...)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("fillCountsArrayFromSourceBuffer panicked on oversized payload: %v", r)
		}
	}()
	if err := fillCountsArrayFromSourceBuffer(payload, rh); err == nil {
		t.Fatal("expected error for payload exceeding countsLen, got nil")
	}
}
