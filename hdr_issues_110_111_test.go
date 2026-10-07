package hdrhistogram

import (
	"math"
	"strings"
	"testing"
)

func countsSum(h *Histogram) int64 {
	var sum int64
	for _, c := range h.counts {
		sum += c
	}
	return sum
}

// #110: a value copy of a window result shares counts with the window; Clone
// gives an independent snapshot that survives Rotate and later Merges.
func TestWindowedSnapshotWithClone(t *testing.T) {
	w := NewWindowed(1, 1, 10_000_000_000, 3)
	if err := w.Current.RecordValue(1000); err != nil {
		t.Fatal(err)
	}
	saved := w.Merge().Clone()
	shallow := *w.Merge() // the hazard the issue describes
	w.Rotate()
	w.Merge()

	if saved.TotalCount() != 1 || countsSum(saved) != 1 || saved.ValueAtPercentile(99) != 1000 {
		t.Fatalf("clone: total %d sum %d p99 %d, want 1 1 1000", saved.TotalCount(), countsSum(saved), saved.ValueAtPercentile(99))
	}
	// The shallow copy shows the documented hazard: its counts were reset.
	if shallow.TotalCount() != 1 || countsSum(&shallow) != 0 {
		t.Fatalf("expected the shallow copy to share (now reset) counts: total %d sum %d", shallow.TotalCount(), countsSum(&shallow))
	}
	// Import(Export()) is also an independent snapshot of a window result.
	w2 := NewWindowed(2, 1, 10_000_000_000, 3)
	_ = w2.Current.RecordValue(1000)
	viaImport := Import(w2.Merge().Export())
	w2.Rotate()
	w2.Rotate()
	w2.Merge()
	if viaImport.TotalCount() != 1 || viaImport.ValueAtPercentile(99) != 1000 {
		t.Fatal("Import(Export()) snapshot changed after Rotate and Merge")
	}
	// Merge results are reused: a previously returned pointer follows later merges.
	first := w.Merge()
	_ = w.Current.RecordValue(5000)
	if second := w.Merge(); first != second || first.TotalCount() != 1 {
		t.Fatal("Merge should return the same reused histogram")
	}
}

func TestHistogramCloneIsIndependent(t *testing.T) {
	h := New(1, 3600000000, 3)
	for _, v := range []int64{1, 100, 12345, 3600000000} {
		_ = h.RecordValue(v)
	}
	h.SetTag("tag")
	h.SetStartTimeMs(10)
	h.SetEndTimeMs(20)
	c := h.Clone()
	if !c.Equals(h) || c.Tag() != "tag" || c.StartTimeMs() != 10 || c.EndTimeMs() != 20 {
		t.Fatal("clone differs from its source")
	}
	if len(c.counts) > 0 && &c.counts[0] == &h.counts[0] {
		t.Fatal("clone shares counts storage")
	}
	h.Reset()
	if c.TotalCount() != 4 || countsSum(c) != 4 || c.ValueAtQuantile(50) != 100 {
		t.Fatalf("Reset of the source changed the clone: total %d sum %d", c.TotalCount(), countsSum(c))
	}
	_ = c.RecordValue(7)
	if h.TotalCount() != 0 || countsSum(h) != 0 {
		t.Fatal("recording into the clone changed the source")
	}
	// A decoded histogram's exact geometry and conversion ratio are kept.
	d, err := Decode([]byte(javaZeroDigits1000))
	if err != nil {
		t.Fatal(err)
	}
	if dc := d.Clone(); !dc.Equals(d) || dc.significantFigures != 0 {
		t.Fatal("Clone changed a 0-digit decoded histogram")
	}
	h.conversionRatio = 2.5
	if h.Clone().getIntegerToDoubleValueConversionRatio() != 2.5 {
		t.Fatal("Clone dropped the conversion ratio")
	}
}

// #111: negative snapshot counts used to be stored while TotalCount ignored
// them, leaving a total the counts could not reach.
func TestImportNegativeCountsStayConsistent(t *testing.T) {
	h := New(1, 10_000_000_000, 3)
	if err := h.RecordValue(1000); err != nil {
		t.Fatal(err)
	}
	s := h.Export()
	s.Counts[0] = -1
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("Validate: %v", err)
	}
	imported := Import(s)
	if imported.TotalCount() != 1 || countsSum(imported) != 1 || imported.ValueAtPercentile(99) != 1000 {
		t.Fatalf("imported: total %d sum %d p99 %d, want 1 1 1000",
			imported.TotalCount(), countsSum(imported), imported.ValueAtPercentile(99))
	}
	if imported.counts[0] != 0 {
		t.Fatalf("negative count stored as %d, want 0", imported.counts[0])
	}
}

func TestSnapshotValidate(t *testing.T) {
	valid := New(1, 3600000000, 3)
	_ = valid.RecordValues(12345, 7)
	if err := valid.Export().Validate(); err != nil {
		t.Fatalf("valid export: %v", err)
	}
	if err := New(1, 1000, 3).Export().Validate(); err != nil {
		t.Fatalf("empty export: %v", err)
	}
	if d, _ := Decode([]byte(javaZeroDigits1000)); d.Export().Validate() != nil {
		t.Fatal("a decoded 0-digit histogram's export must validate")
	}
	// A shorter Counts slice is fine (Import zero-fills the rest).
	if err := (&Snapshot{LowestTrackableValue: 1, HighestTrackableValue: 1000, SignificantFigures: 3, Counts: []int64{1}}).Validate(); err != nil {
		t.Fatalf("short counts: %v", err)
	}

	for _, tc := range []struct {
		name string
		mut  func(s *Snapshot)
		want string
	}{
		{"negative", func(s *Snapshot) { s.Counts[3] = -5 }, "negative"},
		{"beyond range", func(s *Snapshot) { s.Counts = append(s.Counts, 1) }, "beyond"},
		{"sum overflow", func(s *Snapshot) { s.Counts[0], s.Counts[1] = math.MaxInt64, 1 }, "sum past MaxInt64"},
		{"digits", func(s *Snapshot) { s.SignificantFigures = 6 }, "significant figures"},
		{"geometry", func(s *Snapshot) { s.LowestTrackableValue, s.SignificantFigures = math.MaxInt64/2, 1 }, "too large"},
	} {
		s := New(1, 1000, 3).Export()
		tc.mut(s)
		if err := s.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: Validate err = %v, want %q", tc.name, err, tc.want)
		}
	}
	// A sum of exactly MaxInt64 is valid.
	exact := New(1, 1000, 3).Export()
	exact.Counts[0], exact.Counts[1] = math.MaxInt64-1, 1
	if err := exact.Validate(); err != nil {
		t.Fatalf("sum of exactly MaxInt64: %v", err)
	}
	if err := (*Snapshot)(nil).Validate(); err == nil {
		t.Fatal("nil snapshot validated")
	}
	// Trailing zero counts beyond the range are harmless.
	s := New(1, 1000, 3).Export()
	s.Counts = append(s.Counts, 0, 0)
	if err := s.Validate(); err != nil {
		t.Fatalf("trailing zeros: %v", err)
	}
}

// Import has no error return: for geometry New cannot represent it panics as
// New does, which Validate reports as an error first.
func TestImportUnrepresentableGeometryPanicsAfterValidateRejects(t *testing.T) {
	s := &Snapshot{LowestTrackableValue: 1 << 61, HighestTrackableValue: math.MaxInt64, SignificantFigures: 3}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("Validate: %v", err)
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Import of unrepresentable geometry did not panic")
		}
	}()
	Import(s)
}
