package hdrhistogram

import (
	"math/rand"
	"testing"
)

// dense-vs-packed parity across random workloads.
func TestPackedParityRandom(t *testing.T) {
	pcts := []float64{0, 1, 25, 50, 75, 90, 99, 99.9, 100}
	for trial := 0; trial < 300; trial++ {
		r := rand.New(rand.NewSource(int64(trial) + 1))
		d := New(1, 3600000000, 3)
		p := NewPacked(1, 3600000000, 3)
		n := 1 + r.Intn(4000)
		for i := 0; i < n; i++ {
			v := r.Int63n(3600000000) + 1
			c := int64(1 + r.Intn(9))
			if err := d.RecordValues(v, c); err != nil {
				t.Fatal(err)
			}
			if err := p.RecordValues(v, c); err != nil {
				t.Fatal(err)
			}
		}
		if d.TotalCount() != p.TotalCount() {
			t.Fatalf("trial %d: total %d != %d", trial, d.TotalCount(), p.TotalCount())
		}
		if d.Min() != p.Min() {
			t.Fatalf("trial %d: min %d != %d", trial, d.Min(), p.Min())
		}
		if d.Max() != p.Max() {
			t.Fatalf("trial %d: max %d != %d", trial, d.Max(), p.Max())
		}
		// count parity, index by index
		for i := int32(0); i < d.countsLen; i++ {
			want := d.counts[i]
			got := p.CountAtValue(d.valueFromFlatIndex(i))
			if want != got {
				t.Fatalf("trial %d: count[%d] dense %d != packed %d", trial, i, want, got)
			}
		}
		// percentile parity
		for _, pc := range pcts {
			if d.ValueAtPercentile(pc) != p.ValueAtPercentile(pc) {
				t.Fatalf("trial %d: p%.4g dense %d != packed %d", trial, pc,
					d.ValueAtPercentile(pc), p.ValueAtPercentile(pc))
			}
		}
	}
}

// count-width grows 1->2->4->8 as a bucket's count crosses each threshold.
func TestPackedWidthGrowth(t *testing.T) {
	p := NewPacked(1, 3600000000, 3)
	steps := []struct {
		add   int64
		width int
	}{{200, 1}, {70000, 4}, {5000000000, 8}}
	for _, s := range steps {
		if err := p.RecordValues(6147, s.add); err != nil {
			t.Fatal(err)
		}
		if p.CountWidth() != s.width {
			t.Fatalf("after +%d expected width %d, got %d", s.add, s.width, p.CountWidth())
		}
	}
	// value round-trips at width 8
	total := int64(200 + 70000 + 5000000000)
	if p.CountAtValue(6147) != total {
		t.Fatalf("count at 6147: got %d want %d", p.CountAtValue(6147), total)
	}
}

// ValueAtPercentile(pct) matches the recorded value for a dense sweep.
func TestPackedValueAtPercentileMatches(t *testing.T) {
	for _, length := range []int{1, 5, 100, 1000, 10000} {
		d := New(1, 9223372036854775807, 2)
		p := NewPacked(1, 9223372036854775807, 2)
		for v := int64(1); v <= int64(length); v++ {
			_ = d.RecordValue(v)
			_ = p.RecordValue(v)
		}
		for v := 1; v <= length; v++ {
			pct := (100.0 * float64(v)) / float64(length)
			if d.ValueAtPercentile(pct) != p.ValueAtPercentile(pct) {
				t.Fatalf("len %d p%.4g: dense %d != packed %d", length, pct,
					d.ValueAtPercentile(pct), p.ValueAtPercentile(pct))
			}
		}
	}
}
