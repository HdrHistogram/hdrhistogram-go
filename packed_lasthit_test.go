package hdrhistogram

import (
	"math/rand"
	"testing"
)

// The last-hit write cache must give the same counts as the binary-search path after
// every operation that can leave it stale: inserts that shift idx, Reset, Compact,
// Merge and MergeFrom, the cache Clone copies, and the zero-valued cache of DecodePacked.
func TestPackedLastHitCacheStaysCorrect(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	p := NewPacked(1, 1_000_000, 3)
	d := New(1, 1_000_000, 3)
	record := func(v, n int64) {
		if perr, derr := p.RecordValues(v, n), d.RecordValues(v, n); (perr == nil) != (derr == nil) {
			t.Fatalf("RecordValues(%d, %d): packed %v, dense %v", v, n, perr, derr)
		}
	}
	check := func(step string) {
		t.Helper()
		if msg := packedCheckState(p); msg != "" {
			t.Fatalf("%s: %s", step, msg)
		}
		it := d.rIterator()
		j := int32(0)
		for it.next() {
			if j >= p.size || p.geom.valueFromFlatIndex(p.idx[j]) != it.valueFromIdx || p.slotGet(j) != it.countAtIdx {
				t.Fatalf("%s: packed bucket %d differs from dense (%d, %d)", step, j, it.valueFromIdx, it.countAtIdx)
			}
			j++
		}
		if j != p.size || p.TotalCount() != d.TotalCount() {
			t.Fatalf("%s: packed has %d buckets total %d, dense %d buckets total %d", step, p.size, p.TotalCount(), j, d.TotalCount())
		}
	}
	var last int64 // the value the cache holds after a burst
	burst := func() {
		v := rng.Int63n(1_000_000) + 1
		for i := 0; i < 1+rng.Intn(20); i++ {
			record(v, 1+rng.Int63n(300)) // repeats hit the cache
			if rng.Intn(4) == 0 {
				record(rng.Int63n(v)+1, 1) // insert below v shifts v's position in idx
			}
		}
		record(v, 1)
		last = v
	}
	for round := 0; round < 200; round++ {
		burst()
		check("burst")
		switch round % 6 {
		case 0:
			p.Compact()
		case 1:
			// Merge inserts buckets below the cached one without going through the cache.
			src := NewPacked(1, 1_000_000, 3)
			_ = src.RecordValues(rng.Int63n(last)+1, 5)
			_ = src.RecordValues(rng.Int63n(last)+1, 7)
			if p.Merge(src) != 0 || d.Merge(Import(&Snapshot{LowestTrackableValue: 1, HighestTrackableValue: 1_000_000, SignificantFigures: 3, Counts: denseCounts(src)})) != 0 {
				t.Fatal("merge dropped counts")
			}
		case 2:
			p = p.Clone()
		case 3:
			enc, err := p.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if p, err = DecodePacked(enc); err != nil {
				t.Fatal(err)
			}
		case 4:
			p.Reset()
			d.Reset()
		case 5:
			// MergeFrom also inserts below the cached bucket without updating the cache.
			src := New(1, 1_000_000, 3)
			_ = src.RecordValues(rng.Int63n(last)+1, 3)
			_ = src.RecordValues(rng.Int63n(last)+1, 4)
			if p.MergeFrom(src) != 0 || d.Merge(src) != 0 {
				t.Fatal("MergeFrom dropped counts")
			}
		}
		check("after operation")
		record(last, 1) // the first record after the operation reuses the possibly stale cache
		check("record after operation")
		burst()
		check("burst after operation")
	}
}

func denseCounts(p *PackedHistogram) []int64 {
	out := make([]int64, p.geom.countsLen)
	for i := int32(0); i < p.size; i++ {
		out[p.idx[i]] = p.slotGet(i)
	}
	return out
}

// The cache must actually hit: after recording a value, lastPos points at its bucket,
// including when inserts below it shift its position.
func TestPackedLastHitCacheHits(t *testing.T) {
	p := NewPacked(1, 1_000_000, 3)
	for _, v := range []int64{5000, 5000, 10, 5000, 20, 5000, 5000} {
		if err := p.RecordValue(v); err != nil {
			t.Fatal(err)
		}
		ci := int32(p.geom.countsIndexFor(v))
		if p.lastIndex != ci || p.lastPos >= p.size || p.idx[p.lastPos] != ci {
			t.Fatalf("after recording %d: cache (%d, %d) does not point at its bucket", v, p.lastIndex, p.lastPos)
		}
	}
	if p.CountAtValue(5000) != 5 || p.TotalCount() != 7 {
		t.Fatalf("count(5000) %d total %d, want 5 and 7", p.CountAtValue(5000), p.TotalCount())
	}
}
