package hdrhistogram

import (
	"math"
	"testing"
)

// buildWidth returns a packed histogram (and a dense twin) forced to the given
// count width, populated with `buckets` distinct values so the blocked scan has
// at least one full block plus a tail (buckets not a multiple of packedScanBlock).
func buildWidth(t *testing.T, width int, buckets int) (*PackedHistogram, *Histogram) {
	t.Helper()
	p := NewPacked(1, 1000000000, 3)
	d := New(1, 1000000000, 3)
	// one big count on the first value to force the width, count 1 elsewhere.
	big := map[int]int64{1: 1, 2: 300, 4: 70000, 8: 5000000000}[width]
	for b := 0; b < buckets; b++ {
		v := int64(1000 + b*1000)
		c := int64(1)
		if b == 0 {
			c = big
		}
		if err := p.RecordValues(v, c); err != nil {
			t.Fatal(err)
		}
		if err := d.RecordValues(v, c); err != nil {
			t.Fatal(err)
		}
	}
	if p.CountWidth() != width {
		t.Fatalf("wanted width %d, got %d", width, p.CountWidth())
	}
	if int(p.Populated()) != buckets {
		t.Fatalf("wanted %d buckets, got %d", buckets, p.Populated())
	}
	return p, d
}

// blocked valueFromIdxAtCount / ValueAtPercentile: every width, exercising the
// block-skip, block-crossing, and tail paths via a full percentile sweep, all
// checked bit-for-bit against dense.
func TestCov_perfpaths_ValueAtPercentileAllWidths(t *testing.T) {
	for _, width := range []int{1, 2, 4, 8} {
		for _, buckets := range []int{1, 8, 12, 20} { // <block, ==block, block+tail, multi-block+tail
			p, d := buildWidth(t, width, buckets)
			for _, pc := range []float64{0, 0.5, 1, 25, 50, 75, 90, 99, 99.9, 100, -5, 150, math.Inf(1)} {
				want, got := d.ValueAtPercentile(pc), p.ValueAtPercentile(pc)
				// dense is undefined on +Inf (unclamped); only compare where dense is defined.
				if math.IsInf(pc, 0) {
					if got < 0 {
						t.Fatalf("w%d b%d p%v: packed out of range %d", width, buckets, pc, got)
					}
					continue
				}
				if want != got {
					t.Fatalf("w%d b%d p%v: dense %d != packed %d", width, buckets, pc, want, got)
				}
			}
		}
	}
}

// blocked ValueAtPercentilesSlice: every width, ordered/unordered, block+tail,
// parity with the singular for every element.
func TestCov_perfpaths_ValueAtPercentilesSliceAllWidths(t *testing.T) {
	slices := [][]float64{
		{0, 50, 90, 99, 99.9, 100},
		{100, 99.9, 50, 0, 25, 75}, // unordered
		{50, 50, 99, 99},           // duplicates
		{99.9},
	}
	for _, width := range []int{1, 2, 4, 8} {
		for _, buckets := range []int{9, 12, 25} {
			p, _ := buildWidth(t, width, buckets)
			for _, pcts := range slices {
				got := p.ValueAtPercentilesSlice(pcts)
				for i, pc := range pcts {
					if want := p.ValueAtPercentile(pc); got[i] != want {
						t.Fatalf("w%d b%d slice p%v: %d != singular %d", width, buckets, pc, got[i], want)
					}
				}
			}
		}
	}
}

// sparseAdd record fast-paths and widen fallbacks at each width, plus the
// insert capacity-growth (realloc) and in-capacity reuse branches.
func TestCov_perfpaths_RecordFastAndWiden(t *testing.T) {
	// Hit fast-path at each width: record a bucket, then hit it again staying in width.
	widthCounts := []struct {
		width int
		first int64
		bump  int64
	}{
		{1, 1, 10},            // width 1 hit stays <=0xFF
		{2, 300, 100},         // width 2 hit stays <=0xFFFF
		{4, 70000, 1000},      // width 4 hit stays <=0xFFFFFFFF
		{8, 5000000000, 1000}, // width 8 default path
	}
	for _, wc := range widthCounts {
		p := NewPacked(1, 1000000000, 3)
		if err := p.RecordValues(500, wc.first); err != nil {
			t.Fatal(err)
		}
		if p.CountWidth() != wc.width {
			t.Fatalf("setup width %d != %d", p.CountWidth(), wc.width)
		}
		before := p.CountAtValue(500)
		if err := p.RecordValues(500, wc.bump); err != nil { // hit fast path
			t.Fatal(err)
		}
		if got := p.CountAtValue(500); got != before+wc.bump {
			t.Fatalf("w%d hit fast path: %d != %d", wc.width, got, before+wc.bump)
		}
	}
	// Widen-fallback out of each narrow width via a hit that crosses the boundary.
	for _, w := range []struct {
		start int64
		cross int64
		to    int
	}{
		{1, 0xFF, 2}, {100, 0xFFFF, 4}, {70000, 0xFFFFFFFF, 8},
	} {
		p := NewPacked(1, 1000000000, 3)
		_ = p.RecordValues(500, w.start)
		_ = p.RecordValues(500, w.cross) // hit that overflows current width -> widen fallback
		if p.CountWidth() != w.to {
			t.Fatalf("widen fallback: got width %d want %d", p.CountWidth(), w.to)
		}
		if got := p.CountAtValue(500); got != w.start+w.cross {
			t.Fatalf("widen fallback value: %d != %d", got, w.start+w.cross)
		}
	}
	// Insert path: many distinct buckets in DESCENDING order maximizes memmoves
	// and drives both cnt realloc (cap exceeded) and in-cap reuse branches.
	p := NewPacked(1, 1000000000, 3)
	d := New(1, 1000000000, 3)
	for v := int64(200000); v >= 1000; v -= 1000 {
		_ = p.RecordValue(v)
		_ = d.RecordValue(v)
	}
	for i := int32(0); i < d.countsLen; i++ {
		if p.CountAtValue(d.valueFromFlatIndex(i)) != d.counts[i] {
			t.Fatalf("descending insert parity broke at flat %d", i)
		}
	}
	if p.GetMemorySize() <= 0 {
		t.Fatalf("GetMemorySize should be > 0 after inserts")
	}
}
