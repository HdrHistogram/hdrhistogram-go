package hdrhistogram

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHistogramLogWriter_empty(t *testing.T) {
	var b bytes.Buffer
	writer := NewHistogramLogWriter(&b)
	err := writer.OutputLogFormatVersion()
	assert.Nil(t, err)
	var startTimeWritten int64 = 1000
	err = writer.OutputStartTime(startTimeWritten)
	assert.Nil(t, err)
	err = writer.OutputLogFormatVersion()
	assert.Nil(t, err)
	err = writer.OutputLegend()
	assert.Nil(t, err)
	got, _ := b.ReadString('\n')
	want := "#[Histogram log format version 1.3]\n"
	assert.Equal(t, want, got)
	got, _ = b.ReadString('\n')
	// avoid failing tests due to GMT time differences ( so we want all to be equal up until the first + )
	want = "#[StartTime: 1 (seconds since epoch), 1970-01-01"
	assert.Contains(t, got, want)
}

func TestHistogramLogWriterReader(t *testing.T) {
	var b bytes.Buffer
	writer := NewHistogramLogWriter(&b)
	err := writer.OutputLogFormatVersion()
	assert.Equal(t, nil, err)
	var startTimeWritten int64 = 1000
	err = writer.OutputStartTime(startTimeWritten)
	assert.Nil(t, err)
	err = writer.OutputLogFormatVersion()
	assert.Nil(t, err)
	err = writer.OutputLegend()
	assert.Nil(t, err)
	histogram := New(1, 1000, 3)
	for i := 0; i < 10; i++ {
		err = histogram.RecordValue(int64(i))
		assert.Nil(t, err)
	}
	histogram.SetStartTimeMs(1000)
	histogram.SetEndTimeMs(2000)
	err = writer.OutputIntervalHistogram(histogram)
	assert.Equal(t, nil, err)
	r := bytes.NewReader(b.Bytes())
	reader := NewHistogramLogReader(r)
	outHistogram, err := reader.NextIntervalHistogram()
	assert.Equal(t, nil, err)
	assert.Equal(t, histogram.TotalCount(), outHistogram.TotalCount())
	assert.Equal(t, histogram.LowestTrackableValue(), outHistogram.LowestTrackableValue())
	assert.Equal(t, histogram.HighestTrackableValue(), outHistogram.HighestTrackableValue())
	assert.Equal(t, histogram.StartTimeMs(), outHistogram.StartTimeMs())
	assert.Equal(t, histogram.EndTimeMs(), outHistogram.EndTimeMs())
}

// Interval 0 of both logV2 fixtures is the same jHiccup snapshot. These
// golden values pin the decoded contents so a regression in the reader or
// the V2 decoder is caught, rather than only that a non-nil histogram was
// returned.
const (
	goldenIntv0TotalCount int64 = 741
	goldenIntv0StartMs    int64 = 1441812279601
	goldenIntv0EndMs      int64 = 1441812280608
	goldenIntv0Max        int64 = 2768895
	goldenIntv0P50        int64 = 344063
	goldenIntv0P99        int64 = 409599
)

func assertGoldenInterval0(t *testing.T, h *Histogram) {
	t.Helper()
	assert.Equal(t, goldenIntv0TotalCount, h.TotalCount())
	assert.Equal(t, goldenIntv0StartMs, h.StartTimeMs())
	assert.Equal(t, goldenIntv0EndMs, h.EndTimeMs())
	assert.Equal(t, goldenIntv0Max, h.Max())
	assert.Equal(t, goldenIntv0P50, h.ValueAtPercentile(50))
	assert.Equal(t, goldenIntv0P99, h.ValueAtPercentile(99))
}

func TestHistogramLogReader_logV2(t *testing.T) {
	dat, err := os.ReadFile("./test/jHiccup-2.0.7S.logV2.hlog")
	assert.Equal(t, nil, err)
	r := bytes.NewReader(dat)
	reader := NewHistogramLogReader(r)

	first, err := reader.NextIntervalHistogram()
	assert.Nil(t, err)
	assert.NotNil(t, first)
	assertGoldenInterval0(t, first)

	// Drain the rest and assert the full interval count.
	count := 1
	for {
		h, err := reader.NextIntervalHistogram()
		assert.Nil(t, err)
		if h == nil {
			break
		}
		count++
	}
	assert.Equal(t, 62, count)
}

func TestHistogramLogReader_tagged_log(t *testing.T) {
	dat, err := os.ReadFile("./test/tagged-Log.logV2.hlog")
	assert.Equal(t, nil, err)
	r := bytes.NewReader(dat)
	reader := NewHistogramLogReader(r)

	first, err := reader.NextIntervalHistogram()
	assert.Nil(t, err)
	assert.NotNil(t, first)
	assertGoldenInterval0(t, first)

	count := 1
	for {
		h, err := reader.NextIntervalHistogram()
		assert.Nil(t, err)
		if h == nil {
			break
		}
		count++
	}
	assert.Equal(t, 42, count)
}
