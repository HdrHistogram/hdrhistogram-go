package hdrhistogram

import (
	"bytes"
	"errors"
	"io"
	"math"
	"runtime"
	"strings"
	"testing"
)

func TestPackedHistogramLogRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name    string
		empty   bool
		base    int64
		options *HistogramLogOptions
		start   int64
		end     int64
	}{
		{name: "absolute", start: 1250, end: 2750},
		{name: "relative", base: 1000, start: 1250, end: 2750},
		{name: "empty", empty: true, start: 1250, end: 2750},
		{name: "options", base: 1000, options: &HistogramLogOptions{3500, 4750, 1}, start: 3500, end: 4750},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPacked(1, 3600000000, 3)
			d := New(1, 3600000000, 3)
			if !tc.empty {
				for _, v := range []int64{0, 100, 100, 1234567, 3000000000} {
					if err := p.RecordValue(v); err != nil {
						t.Fatal(err)
					}
					if err := d.RecordValue(v); err != nil {
						t.Fatal(err)
					}
				}
			}
			p.SetStartTimeMs(1250)
			p.SetEndTimeMs(2750)
			p.SetTag("endpoint")
			d.SetStartTimeMs(p.StartTimeMs())
			d.SetEndTimeMs(p.EndTimeMs())
			d.SetTag(p.Tag())

			var packedLog, denseLog bytes.Buffer
			pw, dw := NewHistogramLogWriter(&packedLog), NewHistogramLogWriter(&denseLog)
			for _, w := range []*HistogramLogWriter{pw, dw} {
				w.SetBaseTime(tc.base)
				if err := w.OutputBaseTime(tc.base); err != nil {
					t.Fatal(err)
				}
			}
			if tc.options == nil {
				if err := pw.OutputIntervalPackedHistogram(p); err != nil {
					t.Fatal(err)
				}
			} else if err := pw.OutputIntervalPackedHistogramWithLogOptions(p, tc.options); err != nil {
				t.Fatal(err)
			}
			if err := dw.OutputIntervalHistogramWithLogOptions(d, tc.options); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(packedLog.Bytes(), denseLog.Bytes()) {
				t.Fatalf("packed log differs from dense log:\n%s\n%s", &packedLog, &denseLog)
			}
			if tc.options != nil && !strings.Contains(packedLog.String(), ",1.250000,3001024511.000000,") {
				t.Fatalf("log options not applied: %s", &packedLog)
			}
			out, err := NewHistogramLogReader(&packedLog).NextIntervalHistogram()
			if err != nil || out == nil {
				t.Fatalf("read interval: histogram=%v err=%v", out, err)
			}
			if !out.Equals(d) {
				t.Fatal("roundtrip changed histogram counts or geometry")
			}
			if out.StartTimeMs() != tc.start || out.EndTimeMs() != tc.end || out.Tag() != "endpoint" {
				t.Fatalf("roundtrip metadata: start=%d end=%d tag=%q", out.StartTimeMs(), out.EndTimeMs(), out.Tag())
			}
			if p.StartTimeMs() != 1250 || p.EndTimeMs() != 2750 || p.Tag() != "endpoint" {
				t.Fatal("logging changed attached metadata")
			}
		})
	}
}

func TestPackedHistogramLogRemainsSparse(t *testing.T) {
	p := NewPacked(1, math.MaxInt64, 5)
	if err := p.RecordValue(1 << 60); err != nil {
		t.Fatal(err)
	}
	w := NewHistogramLogWriter(io.Discard)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := w.OutputIntervalPackedHistogram(p)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	// This geometry would require roughly 48 MiB for dense counts alone.
	// Allow ample room for compression and formatting, but reject conversion.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("sparse log output allocated %d bytes", allocated)
	}
	if p.geom.counts != nil || p.size != 1 || p.TotalCount() != 1 {
		t.Fatal("logging modified sparse storage")
	}
}

type packedLogFailWriter struct{ err error }

func (w packedLogFailWriter) Write([]byte) (int, error) { return 0, w.err }

func TestPackedHistogramLogErrors(t *testing.T) {
	p := NewPacked(1, 1000, 3)
	sentinel := errors.New("write failed")
	if err := NewHistogramLogWriter(packedLogFailWriter{sentinel}).OutputIntervalPackedHistogram(p); !errors.Is(err, sentinel) {
		t.Fatalf("write error = %v, want %v", err, sentinel)
	}
	p.SetTag("bad,tag")
	var out bytes.Buffer
	if err := NewHistogramLogWriter(&out).OutputIntervalPackedHistogram(p); err == nil || out.Len() != 0 {
		t.Fatalf("invalid tag: err=%v output=%q", err, out.String())
	}
}

func TestPackedHistogramResetMetadata(t *testing.T) {
	p := NewPacked(1, 1000, 3)
	if err := p.RecordValues(100, 1000); err != nil {
		t.Fatal(err)
	}
	p.SetStartTimeMs(1000)
	p.SetEndTimeMs(2000)
	p.SetTag("endpoint")
	countCapacity, indexCapacity, width := cap(p.cnt), cap(p.idx), p.width
	p.Reset()
	if p.StartTimeMs() != 0 || p.EndTimeMs() != 0 || p.Tag() != "" || p.TotalCount() != 0 {
		t.Fatal("Reset did not clear counts and interval metadata")
	}
	if cap(p.cnt) != countCapacity || cap(p.idx) != indexCapacity || p.width != width {
		t.Fatal("Reset did not retain reusable sparse storage")
	}
}

func TestHistogramEncodeV2CommonInterface(t *testing.T) {
	type v2Encoder interface {
		EncodeV2() ([]byte, error)
	}
	d, p := New(1, 1000, 3), NewPacked(1, 1000, 3)
	if err := d.RecordValues(100, 2); err != nil {
		t.Fatal(err)
	}
	if err := p.RecordValues(100, 2); err != nil {
		t.Fatal(err)
	}
	for _, encoder := range []v2Encoder{d, p} {
		encoded, err := encoder.EncodeV2()
		if err != nil {
			t.Fatal(err)
		}
		out, err := Decode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if !out.Equals(d) {
			t.Fatalf("%T EncodeV2 changed counts or geometry", encoder)
		}
	}
}
