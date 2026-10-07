package hdrhistogram

import (
	"math"
	"math/rand"
	"testing"
)

type windowGeom struct {
	low, high int64
	sig       int
}

var windowGeoms = []windowGeom{
	{1, 3600000000, 3},
	{1, 1000000, 2},
	{1000, 1 << 40, 4},
	{1, math.MaxInt64, 1},
}

// fillBoth records the same random values into a dense and a packed histogram.
func fillBoth(r *rand.Rand, g windowGeom, n int) (*Histogram, *PackedHistogram) {
	d := New(g.low, g.high, g.sig)
	p := NewPacked(g.low, g.high, g.sig)
	for i := 0; i < n; i++ {
		v := r.Int63n(g.high/2+1) + g.low
		if r.Intn(4) == 0 {
			v = r.Int63n(1000) + g.low
		}
		c := int64(1)
		if r.Intn(8) == 0 {
			c = r.Int63n(1 << 20)
		}
		if d.RecordValues(v, c) == nil {
			if err := p.RecordValues(v, c); err != nil {
				panic(err)
			}
		}
	}
	return d, p
}

func countsEqual(t *testing.T, ctx string, a, b *Histogram) {
	t.Helper()
	if a.TotalCount() != b.TotalCount() {
		t.Fatalf("%s: total %d != %d", ctx, a.TotalCount(), b.TotalCount())
	}
	if len(a.counts) != len(b.counts) {
		t.Fatalf("%s: countsLen %d != %d", ctx, len(a.counts), len(b.counts))
	}
	for i := range a.counts {
		if a.counts[i] != b.counts[i] {
			t.Fatalf("%s: counts[%d] %d != %d", ctx, i, a.counts[i], b.counts[i])
		}
	}
}

func TestPackedResetReusesStorage(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	d, p := fillBoth(r, windowGeoms[0], 5000)
	if p.CountWidth() < 4 {
		t.Fatalf("setup: width %d, want a widened slot", p.CountWidth())
	}
	idxCap, cntCap, width := cap(p.idx), cap(p.cnt), p.CountWidth()
	p.Reset()
	fresh := NewPacked(1, 3600000000, 3)
	if p.TotalCount() != 0 || p.Populated() != 0 || p.Max() != fresh.Max() || p.Min() != fresh.Min() ||
		p.ValueAtPercentile(50) != 0 || p.CountAtValue(1000) != 0 {
		t.Fatal("Reset left state behind")
	}
	pe, _ := p.Encode()
	fe, _ := fresh.Encode()
	if string(pe) != string(fe) {
		t.Fatal("a reset histogram does not encode like a fresh one")
	}
	if cap(p.idx) != idxCap || cap(p.cnt) != cntCap || p.CountWidth() != width {
		t.Fatalf("Reset released storage or width: caps %d/%d -> %d/%d, width %d -> %d",
			idxCap, cntCap, cap(p.idx), cap(p.cnt), width, p.CountWidth())
	}
	// Reused slot behaves exactly like dense after the same records.
	d.Reset()
	d2, _ := fillBoth(rand.New(rand.NewSource(2)), windowGeoms[0], 3000)
	d.Merge(d2)
	p.MergeFrom(d2)
	back := New(1, 3600000000, 3)
	p.MergeInto(back)
	countsEqual(t, "reused slot", d, back)
	if p.ValueAtPercentile(99.9) != d.ValueAtPercentile(99.9) {
		t.Fatal("percentile differs after reuse")
	}
}

func TestPackedForEachBucketMatchesDenseIteration(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for _, g := range windowGeoms {
		d, p := fillBoth(r, g, 2000)
		it := d.rIterator()
		n := 0
		p.ForEachBucket(func(v, c int64) bool {
			if !it.next() {
				t.Fatalf("%v: packed has more buckets than dense", g)
			}
			if v != it.valueFromIdx || c != it.countAtIdx {
				t.Fatalf("%v: bucket %d: packed (%d,%d), dense (%d,%d)", g, n, v, c, it.valueFromIdx, it.countAtIdx)
			}
			n++
			return true
		})
		if it.next() || n != int(p.Populated()) {
			t.Fatalf("%v: dense has more buckets than packed (%d visited)", g, n)
		}
		// Early stop.
		seen := 0
		p.ForEachBucket(func(_, _ int64) bool { seen++; return seen < 3 })
		if p.Populated() >= 3 && seen != 3 {
			t.Fatalf("%v: early stop visited %d buckets", g, seen)
		}
	}
}

// MergeInto must equal dst.Merge of the dense equivalent, for the same and
// for different geometries (including dropped counts).
func TestPackedMergeIntoMatchesDenseMerge(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	dsts := []windowGeom{
		{1, 3600000000, 3}, // same
		{1, 1 << 40, 3},    // same indexing, wider range
		{1, 100000, 3},     // same indexing, narrower range: drops
		{1, 3600000000, 2}, // different indexing
		{1000, 1 << 30, 4}, // different indexing and range
		{1024, 1 << 40, 3}, // same digits, different unit magnitude
	}
	for trial := 0; trial < 20; trial++ {
		src, p := fillBoth(r, windowGeoms[0], 1+r.Intn(3000))
		// Populate the last counts index too, so range checks are exercised at
		// the edge (values above highestTrackableValue still land there).
		top := p.highestEquivalent(p.geom.valueFromFlatIndex(p.geom.countsLen - 1))
		if src.RecordValues(top, 3) != nil || p.RecordValues(top, 3) != nil {
			t.Fatal("setup: cannot record into the last bucket")
		}
		for _, dg := range dsts {
			want := New(dg.low, dg.high, dg.sig)
			got := New(dg.low, dg.high, dg.sig)
			pre, _ := fillBoth(r, dg, r.Intn(500)) // a non-empty destination
			want.Merge(pre)
			got.Merge(pre)
			wantDropped := want.Merge(src)
			gotDropped := p.MergeInto(got)
			if gotDropped != wantDropped {
				t.Fatalf("dst %v: dropped %d, dense Merge dropped %d", dg, gotDropped, wantDropped)
			}
			countsEqual(t, "MergeInto", want, got)
		}
	}
}

// MergeFrom must equal recording every dense bucket into p, with counts that
// p cannot hold reported as dropped.
func TestPackedMergeFromMatchesRecording(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for trial := 0; trial < 20; trial++ {
		for _, sg := range windowGeoms[:3] {
			src, _ := fillBoth(r, sg, 1+r.Intn(3000))
			for _, pg := range windowGeoms[:3] {
				got := NewPacked(pg.low, pg.high, pg.sig)
				want := NewPacked(pg.low, pg.high, pg.sig)
				if trial%2 == 1 { // non-empty destination
					_, pre := fillBoth(r, pg, 200)
					pre.ForEachBucket(func(v, c int64) bool { _ = got.RecordValues(v, c); _ = want.RecordValues(v, c); return true })
				}
				var wantDropped int64
				it := src.rIterator()
				for it.next() {
					if want.RecordValues(it.valueFromIdx, it.countAtIdx) != nil {
						wantDropped += it.countAtIdx
					}
				}
				if d := got.MergeFrom(src); d != wantDropped {
					t.Fatalf("src %v -> %v: dropped %d, want %d", sg, pg, d, wantDropped)
				}
				if msg := packedCheckState(got); msg != "" {
					t.Fatalf("src %v -> %v: %s", sg, pg, msg)
				}
				if !packedSameBuckets(got, want) || got.TotalCount() != want.TotalCount() {
					t.Fatalf("src %v -> %v: buckets differ from recording", sg, pg)
				}
			}
		}
	}
}

func TestPackedMergeFromRejectsTotalOverflow(t *testing.T) {
	p := NewPacked(1, 1000, 2)
	if err := p.RecordValues(10, math.MaxInt64-5); err != nil {
		t.Fatal(err)
	}
	src := New(1, 1000, 2)
	_ = src.RecordValues(20, 3)
	_ = src.RecordValues(30, 7)
	if dropped := p.MergeFrom(src); dropped != 7 {
		t.Fatalf("dropped %d, want 7 (the count that would overflow)", dropped)
	}
	if p.TotalCount() != math.MaxInt64-2 || p.CountAtValue(20) != 3 || p.CountAtValue(30) != 0 {
		t.Fatalf("total %d, counts %d/%d", p.TotalCount(), p.CountAtValue(20), p.CountAtValue(30))
	}
}

func TestPackedWindowOpsDoNotAllocate(t *testing.T) {
	r := rand.New(rand.NewSource(6))
	src, p := fillBoth(r, windowGeoms[0], 3000)
	dst := New(1, 3600000000, 3)
	if a := testing.AllocsPerRun(20, func() { p.MergeInto(dst) }); a != 0 {
		t.Fatalf("MergeInto (same geometry) allocs = %v", a)
	}
	if a := testing.AllocsPerRun(20, func() { p.ForEachBucket(func(_, _ int64) bool { return true }) }); a != 0 {
		t.Fatalf("ForEachBucket allocs = %v", a)
	}
	slot := NewPacked(1, 3600000000, 3)
	slot.MergeFrom(src) // grow once
	if a := testing.AllocsPerRun(20, func() { slot.Reset(); slot.MergeFrom(src) }); a != 0 {
		t.Fatalf("Reset+MergeFrom into a warmed slot allocs = %v", a)
	}
}

// MergeFrom between geometries that index the same way but where p has a
// narrower range: buckets past p's counts, including the first index past the
// end, are dropped and never stored.
func TestPackedMergeFromSameIndexingNarrowerRange(t *testing.T) {
	p := NewPacked(1, 100000, 3)
	src := New(1, 1<<40, 3)
	if !sameIndexing(p.geom, src) {
		t.Fatal("setup: geometries should index the same way")
	}
	edge := src.valueFromFlatIndex(p.geom.countsLen) // first index p cannot hold
	_ = src.RecordValues(10, 2)
	_ = src.RecordValues(edge-1, 4) // last index p can hold
	_ = src.RecordValues(edge, 5)
	_ = src.RecordValues(1<<35, 7)
	if d := p.MergeFrom(src); d != 12 {
		t.Fatalf("dropped %d, want 12", d)
	}
	if msg := packedCheckState(p); msg != "" {
		t.Fatal(msg)
	}
	if p.TotalCount() != 6 || p.CountAtValue(10) != 2 || p.CountAtValue(edge-1) != 4 {
		t.Fatalf("total %d, counts %d/%d", p.TotalCount(), p.CountAtValue(10), p.CountAtValue(edge-1))
	}
}

// Merging exactly the remaining headroom is allowed; one more is dropped.
func TestPackedMergeFromOverflowBoundary(t *testing.T) {
	p := NewPacked(1, 1000, 2)
	_ = p.RecordValues(10, math.MaxInt64-5)
	src := New(1, 1000, 2)
	_ = src.RecordValues(20, 5)
	if d := p.MergeFrom(src); d != 0 || p.TotalCount() != math.MaxInt64 {
		t.Fatalf("exact headroom: dropped %d, total %d", d, p.TotalCount())
	}
	one := New(1, 1000, 2)
	_ = one.RecordValues(30, 1)
	if d := p.MergeFrom(one); d != 1 || p.TotalCount() != math.MaxInt64 {
		t.Fatalf("past headroom: dropped %d, total %d", d, p.TotalCount())
	}
}

// MergeInto adds without overflow checks, exactly as Histogram.Merge does.
func TestPackedMergeIntoOverflowMatchesDenseMerge(t *testing.T) {
	p := NewPacked(1, 1000, 2)
	_ = p.RecordValues(10, 5)
	src := New(1, 1000, 2)
	_ = src.RecordValues(10, 5)
	want, got := New(1, 1000, 2), New(1, 1000, 2)
	_ = want.RecordValues(10, math.MaxInt64-1)
	_ = got.RecordValues(10, math.MaxInt64-1)
	want.Merge(src)
	p.MergeInto(got)
	countsEqual(t, "overflowing merge", want, got)
}

// Dropping buckets on the re-recording path must not allocate either.
func TestPackedCrossGeometryDropsDoNotAllocate(t *testing.T) {
	src := New(1, 1<<40, 3)
	for v := int64(1); v < 1<<40; v = v*3/2 + 1 {
		_ = src.RecordValue(v)
	}
	slot := NewPacked(1, 1000000, 2) // different indexing, narrower range
	if d := slot.MergeFrom(src); d == 0 {
		t.Fatal("setup: expected dropped buckets")
	}
	if a := testing.AllocsPerRun(20, func() { slot.Reset(); slot.MergeFrom(src) }); a != 0 {
		t.Fatalf("Reset+MergeFrom with drops allocs = %v", a)
	}
	_, p := fillBoth(rand.New(rand.NewSource(7)), windowGeoms[0], 2000)
	_ = p.RecordValues(3000000000, 1) // beyond the destination below
	dst := New(1, 1000000, 2)
	if a := testing.AllocsPerRun(20, func() { p.MergeInto(dst) }); a != 0 {
		t.Fatalf("MergeInto with drops allocs = %v", a)
	}
}

// Widening the count width keeps the capacity Reset retained.
func TestPackedWidenKeepsCapacity(t *testing.T) {
	p := NewPacked(1, 1000000, 2)
	for v := int64(1); v <= 2000; v++ {
		_ = p.RecordValue(v)
	}
	slots := cap(p.cnt) / p.CountWidth()
	p.Reset()
	_ = p.RecordValues(5, 1<<40) // widen straight to 8 bytes
	if got := cap(p.cnt) / p.CountWidth(); got < slots {
		t.Fatalf("slot capacity %d after widening, had %d", got, slots)
	}
}
