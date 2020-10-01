package hdrhistogram

import (
	"bytes"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"testing"
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
	err = writer.OutputIntervalHistogram(histogram)
	assert.Equal(t, nil, err)
	r := bytes.NewReader(b.Bytes())
	reader := NewHistogramLogReader(r)
	outHistogram, err := reader.NextIntervalHistogram()
	assert.Equal(t, nil, err)
	assert.Equal(t, histogram.TotalCount(), outHistogram.TotalCount())
	assert.Equal(t, histogram.LowestTrackableValue(), outHistogram.LowestTrackableValue())
	assert.Equal(t, histogram.HighestTrackableValue(), outHistogram.HighestTrackableValue())
}

func TestHistogramLogReader_logV2(t *testing.T) {
	dat, err := ioutil.ReadFile("./test/jHiccup-2.0.7S.logV2.hlog")
	assert.Equal(t, nil, err)
	r := bytes.NewReader(dat)
	reader := NewHistogramLogReader(r)
	for i := 0; i < 61; i++ {
		outHistogram, err := reader.NextIntervalHistogram()
		assert.Equal(t, nil, err)
		assert.NotNil(t, outHistogram)
	}
}

func TestHistogramLogReader_tagged_log(t *testing.T) {
	dat, err := ioutil.ReadFile("./test/tagged-Log.logV2.hlog")
	assert.Equal(t, nil, err)
	r := bytes.NewReader(dat)
	reader := NewHistogramLogReader(r)
	for i := 0; i < 42; i++ {
		outHistogram, err := reader.NextIntervalHistogram()
		assert.Equal(t, nil, err)
		assert.NotNil(t, outHistogram)
	}
}
