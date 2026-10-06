package hdrhistogram

// Deep coverage for the PackedHistogram slot accessors and width helpers:
//   - packedWidthMax  (every width case + the default branch)
//   - slotGet / slotSet at widths 1,2,4,8 (low/high slot indices, boundary
//     values, little-endian byte layout, truncating-store contract)
//   - widenToFit boundary crossings (width_max -> width_max+1 re-pack)
//
// In-package (white-box) so it can build PackedHistogram literals and read the
// unexported cnt/width/size fields for exact byte-level parity checks.

import (
	"encoding/binary"
	"math"
	"testing"
)

// cov_slots_sized builds a PackedHistogram with a freshly zeroed cnt buffer of
// exactly size*width bytes at the requested width. geom is left nil: the slot
// accessors and width helpers never touch it.
func cov_slots_sized(width uint8, size int32) *PackedHistogram {
	return &PackedHistogram{
		width: width,
		size:  size,
		cnt:   make([]byte, int(size)*int(width)),
	}
}

// cov_slots_widths is the full set of supported count widths.
var cov_slots_widths = []uint8{1, 2, 4, 8}

func TestCov_slots_packedWidthMax(t *testing.T) {
	cases := []struct {
		w    uint8
		want int64
	}{
		{1, 0xFF},
		{2, 0xFFFF},
		{4, 0xFFFFFFFF},
		{8, math.MaxInt64}, // default branch
		// The switch has no explicit case for these; they fall through the
		// default arm and must therefore report the widest capacity.
		{0, math.MaxInt64},
		{3, math.MaxInt64},
		{16, math.MaxInt64},
	}
	for _, c := range cases {
		if got := packedWidthMax(c.w); got != c.want {
			t.Errorf("packedWidthMax(%d) = %d, want %d", c.w, got, c.want)
		}
	}
}

// packedWidthMax must exactly match the number of distinct values a slot of that
// width can hold: max == 2^(8*width)-1 for the fixed widths.
func TestCov_slots_packedWidthMaxIsByteCapacity(t *testing.T) {
	for _, w := range []uint8{1, 2, 4} {
		want := int64(1)<<(8*uint(w)) - 1
		if got := packedWidthMax(w); got != want {
			t.Errorf("packedWidthMax(%d) = %#x, want %#x", w, got, want)
		}
	}
}

// Round-trip every width at the low slot (0), a middle slot, and the high slot
// (size-1). Values include the documented boundaries 0, 1, and packedWidthMax.
func TestCov_slots_RoundTripAllWidths(t *testing.T) {
	for _, w := range cov_slots_widths {
		max := packedWidthMax(w)
		vals := []int64{0, 1, 2, max - 1, max}
		if w == 8 {
			// packedWidthMax(8) == MaxInt64; also cover assorted large values.
			vals = []int64{0, 1, 1 << 32, 1<<40 + 7, math.MaxInt64}
		}
		for _, v := range vals {
			const size = int32(5)
			for _, slot := range []int32{0, 2, size - 1} {
				p := cov_slots_sized(w, size)
				p.slotSet(slot, v)
				if got := p.slotGet(slot); got != v {
					t.Errorf("width=%d slot=%d: slotGet=%d, want %d", w, slot, got, v)
				}
				// Every other slot must remain zero (no cross-slot bleed).
				for other := int32(0); other < size; other++ {
					if other == slot {
						continue
					}
					if got := p.slotGet(other); got != 0 {
						t.Errorf("width=%d wrote slot=%d val=%d but slot=%d = %d, want 0",
							w, slot, v, other, got)
					}
				}
			}
		}
	}
}

// Two distinct slots must be independently addressable (adjacent-slot isolation),
// proving off = i*width is honored on both read and write.
func TestCov_slots_AdjacentSlotsIndependent(t *testing.T) {
	for _, w := range cov_slots_widths {
		max := packedWidthMax(w)
		a, b := int64(1), max
		if w == 8 {
			a, b = 1, math.MaxInt64
		}
		p := cov_slots_sized(w, 4)
		p.slotSet(1, a)
		p.slotSet(2, b)
		if got := p.slotGet(1); got != a {
			t.Errorf("width=%d slot 1 = %d, want %d", w, got, a)
		}
		if got := p.slotGet(2); got != b {
			t.Errorf("width=%d slot 2 = %d, want %d", w, got, b)
		}
		if got := p.slotGet(0); got != 0 {
			t.Errorf("width=%d slot 0 = %d, want 0", w, got)
		}
		if got := p.slotGet(3); got != 0 {
			t.Errorf("width=%d slot 3 = %d, want 0", w, got)
		}
	}
}

// The byte offset written for slot i must be exactly i*width, and slotGet must
// read from that same offset. Verified by inspecting the raw cnt buffer.
func TestCov_slots_ByteOffsetIsIndexTimesWidth(t *testing.T) {
	for _, w := range cov_slots_widths {
		const size = int32(6)
		high := size - 1
		p := cov_slots_sized(w, size)
		// Distinctive non-zero value so the touched bytes are recognizable.
		v := int64(0x01)
		p.slotSet(high, v)
		off := int(high) * int(w)
		// The byte at the slot's base offset holds the low byte of v (LE).
		if p.cnt[off] != byte(v) {
			t.Errorf("width=%d: cnt[%d] = %#x, want %#x", w, off, p.cnt[off], byte(v))
		}
		// All bytes before the slot's region must be untouched (zero).
		for j := 0; j < off; j++ {
			if p.cnt[j] != 0 {
				t.Errorf("width=%d: byte %d before slot region = %#x, want 0", w, j, p.cnt[j])
			}
		}
	}
}

// Little-endian byte layout is part of the on-wire/backing contract. Assert the
// exact bytes slotSet lays down, and that binary.LittleEndian agrees.
func TestCov_slots_LittleEndianLayout(t *testing.T) {
	cases := []struct {
		w    uint8
		val  int64
		want []byte
	}{
		{1, 0xAB, []byte{0xAB}},
		{2, 0x1234, []byte{0x34, 0x12}},
		{4, 0x11223344, []byte{0x44, 0x33, 0x22, 0x11}},
		{8, 0x1122334455667788, []byte{0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11}},
	}
	for _, c := range cases {
		p := cov_slots_sized(c.w, 3)
		const slot = int32(1)
		p.slotSet(slot, c.val)
		off := int(slot) * int(c.w)
		got := p.cnt[off : off+int(c.w)]
		for k := range c.want {
			if got[k] != c.want[k] {
				t.Errorf("width=%d val=%#x: byte %d = %#x, want %#x", c.w, c.val, k, got[k], c.want[k])
			}
		}
		// Cross-check against the standard library's little-endian decode.
		var dec int64
		switch c.w {
		case 1:
			dec = int64(got[0])
		case 2:
			dec = int64(binary.LittleEndian.Uint16(got))
		case 4:
			dec = int64(binary.LittleEndian.Uint32(got))
		case 8:
			dec = int64(binary.LittleEndian.Uint64(got))
		}
		if dec != c.val {
			t.Errorf("width=%d: binary.LittleEndian decode = %#x, want %#x", c.w, dec, c.val)
		}
		if gg := p.slotGet(slot); gg != c.val {
			t.Errorf("width=%d: slotGet = %#x, want %#x", c.w, gg, c.val)
		}
	}
}

// Documented store contract: slotSet is a fixed-width truncating store; the
// caller (sparseAdd) is responsible for calling widenToFit first. Values that
// exceed the current width are stored modulo 2^(8*width) (low bytes kept).
func TestCov_slots_TruncatingStoreContract(t *testing.T) {
	cases := []struct {
		w    uint8
		in   int64
		want int64 // what slotGet returns after the truncating store
	}{
		{1, 0x100, 0x00},   // width_max+1 wraps to 0 at width 1
		{1, 0x1FF, 0xFF},   // keeps low byte
		{2, 0x10000, 0x00}, // width_max+1 wraps to 0 at width 2
		{2, 0x12345, 0x2345},
		{4, 0x100000000, 0x00000000}, // width_max+1 wraps at width 4
		{4, 0x1FFFFFFFF, 0xFFFFFFFF},
	}
	for _, c := range cases {
		p := cov_slots_sized(c.w, 2)
		p.slotSet(0, c.in)
		if got := p.slotGet(0); got != c.want {
			t.Errorf("width=%d slotSet(%#x) then slotGet = %#x, want %#x (truncating store)",
				c.w, c.in, got, c.want)
		}
	}
}

// widenToFit is a no-op when the current width already holds need (nw == width
// early return): the buffer identity and width are preserved.
func TestCov_slots_widenNoOpWhenFits(t *testing.T) {
	for _, w := range cov_slots_widths {
		p := cov_slots_sized(w, 3)
		p.slotSet(0, 1)
		p.slotSet(2, packedWidthMax(w)) // exactly at capacity: must NOT widen
		before := p.cnt
		p.widenToFit(packedWidthMax(w))
		if p.width != w {
			t.Errorf("width=%d: widenToFit(width_max) changed width to %d", w, p.width)
		}
		if &p.cnt[0] != &before[0] {
			t.Errorf("width=%d: widenToFit(width_max) reallocated the buffer unexpectedly", w)
		}
		if got := p.slotGet(2); got != packedWidthMax(w) {
			t.Errorf("width=%d: slot 2 = %d after no-op widen, want %d", w, got, packedWidthMax(w))
		}
	}
}

// widenToFit boundary crossing: at width_max+1 the width must exactly double
// (1->2, 2->4, 4->8) and every existing slot must be re-packed losslessly into
// the wider little-endian layout.
func TestCov_slots_widenSingleStepBoundary(t *testing.T) {
	cases := []struct {
		from     uint8
		to       uint8
		preserve int64 // a live value that already fits at the old width
	}{
		{1, 2, 0xFF},
		{2, 4, 0xFFFF},
		{4, 8, 0xFFFFFFFF},
	}
	for _, c := range cases {
		p := cov_slots_sized(c.from, 3)
		p.slotSet(0, 1)
		p.slotSet(1, c.preserve) // high slot at old capacity
		p.slotSet(2, 0)
		need := packedWidthMax(c.from) + 1 // width_max+1 forces a widen
		p.widenToFit(need)
		if p.width != c.to {
			t.Fatalf("widen from %d for need=%#x: width=%d, want %d", c.from, need, p.width, c.to)
		}
		if len(p.cnt) != int(p.size)*int(c.to) {
			t.Errorf("width %d->%d: cnt len=%d, want %d", c.from, c.to, len(p.cnt), int(p.size)*int(c.to))
		}
		// Existing counts survive the re-pack unchanged...
		if got := p.slotGet(0); got != 1 {
			t.Errorf("width %d->%d: slot 0 = %d, want 1", c.from, c.to, got)
		}
		if got := p.slotGet(1); got != c.preserve {
			t.Errorf("width %d->%d: slot 1 = %d, want %d", c.from, c.to, got, c.preserve)
		}
		// ...and the newly-widened slot can now hold width_max+1 exactly.
		p.slotSet(1, need)
		if got := p.slotGet(1); got != need {
			t.Errorf("width %d->%d: slot 1 after storing need=%#x = %d", c.from, c.to, need, got)
		}
	}
}

// widenToFit must jump multiple steps in one call when need demands it, and cap
// at width 8. From width 1 a need of 0x10000 (> 0xFFFF) skips straight to width 4.
func TestCov_slots_widenMultiStep(t *testing.T) {
	p := cov_slots_sized(1, 2)
	p.slotSet(0, 0xAB)
	p.slotSet(1, 0xCD)
	p.widenToFit(0x10000) // exceeds width-2 capacity -> must land on width 4
	if p.width != 4 {
		t.Fatalf("widenToFit(0x10000) from width 1: width=%d, want 4", p.width)
	}
	if got := p.slotGet(0); got != 0xAB {
		t.Errorf("multi-step widen: slot 0 = %#x, want 0xAB", got)
	}
	if got := p.slotGet(1); got != 0xCD {
		t.Errorf("multi-step widen: slot 1 = %#x, want 0xCD", got)
	}

	// A need beyond every fixed capacity caps at width 8.
	q := cov_slots_sized(1, 1)
	q.slotSet(0, 7)
	q.widenToFit(math.MaxInt64)
	if q.width != 8 {
		t.Errorf("widenToFit(MaxInt64) from width 1: width=%d, want 8", q.width)
	}
	if got := q.slotGet(0); got != 7 {
		t.Errorf("cap widen: slot 0 = %d, want 7", got)
	}
	q.slotSet(0, math.MaxInt64)
	if got := q.slotGet(0); got != math.MaxInt64 {
		t.Errorf("width 8 slot cannot hold MaxInt64: got %d", got)
	}
}

// End-to-end boundary check through the public record path: a single bucket's
// count crossing 0xFF, then 0xFFFF, then 0xFFFFFFFF must widen the backing and
// still report the exact count. This ties slotGet/slotSet/widenToFit together as
// they are actually exercised.
func TestCov_slots_RecordWidthBoundaries(t *testing.T) {
	p := NewPacked(1, math.MaxInt64, 3)
	const v = int64(1000)

	if err := p.RecordValues(v, 0xFF); err != nil {
		t.Fatal(err)
	}
	if p.CountWidth() != 1 {
		t.Fatalf("after 0xFF: width=%d, want 1", p.CountWidth())
	}
	if got := p.CountAtValue(v); got != 0xFF {
		t.Fatalf("after 0xFF: count=%d, want 255", got)
	}

	// Cross into width 2 (0xFF -> 0x100 == width_max+1).
	if err := p.RecordValues(v, 1); err != nil {
		t.Fatal(err)
	}
	if p.CountWidth() != 2 {
		t.Fatalf("after 0x100: width=%d, want 2", p.CountWidth())
	}
	if got := p.CountAtValue(v); got != 0x100 {
		t.Fatalf("after 0x100: count=%d, want 256", got)
	}

	// Cross into width 4 (up to 0x10000).
	if err := p.RecordValues(v, 0x10000-0x100); err != nil {
		t.Fatal(err)
	}
	if p.CountWidth() != 4 {
		t.Fatalf("after 0x10000: width=%d, want 4", p.CountWidth())
	}
	if got := p.CountAtValue(v); got != 0x10000 {
		t.Fatalf("after 0x10000: count=%d, want 65536", got)
	}

	// Cross into width 8 (up to 0x100000000).
	if err := p.RecordValues(v, 0x100000000-0x10000); err != nil {
		t.Fatal(err)
	}
	if p.CountWidth() != 8 {
		t.Fatalf("after 0x100000000: width=%d, want 8", p.CountWidth())
	}
	if got := p.CountAtValue(v); got != 0x100000000 {
		t.Fatalf("after 0x100000000: count=%d, want %d", got, int64(0x100000000))
	}
	if p.TotalCount() != 0x100000000 {
		t.Errorf("total=%d, want %d", p.TotalCount(), int64(0x100000000))
	}
}
