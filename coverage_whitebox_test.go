package hdrhistogram

import "testing"

// Exercises every byte-length rung (1..9) of the zig-zag LEB128 encoder and the
// matching decode, asserting a faithful round-trip and the expected encoded length.
func TestZigZagAllByteLengths(t *testing.T) {
	cases := []struct {
		v       int64
		wantLen int
	}{
		{0, 1}, {1, 1}, {-1, 1}, {63, 1}, {-64, 1},
		{64, 2}, {1 << 13, 3}, {1 << 20, 4}, {1 << 27, 5},
		{1 << 34, 6}, {1 << 41, 7}, {1 << 48, 8}, {1 << 55, 9},
		{1 << 62, 9}, {(1 << 62) - 1, 9},
		{1<<63 - 1, 9},  // MaxInt64
		{-(1 << 62), 9}, // large negative
		{-(1 << 63), 9}, // MinInt64
	}
	for _, c := range cases {
		buf := zig_zag_encode_i64(c.v)
		if len(buf) != c.wantLen {
			t.Errorf("encode(%d): len %d, want %d", c.v, len(buf), c.wantLen)
		}
		got, n, err := zig_zag_decode_i64(buf)
		if err != nil {
			t.Errorf("decode(encode(%d)) err: %v", c.v, err)
			continue
		}
		if got != c.v {
			t.Errorf("round-trip: got %d, want %d", got, c.v)
		}
		if n != len(buf) {
			t.Errorf("decode(%d) consumed %d, want %d", c.v, n, len(buf))
		}
	}
}
