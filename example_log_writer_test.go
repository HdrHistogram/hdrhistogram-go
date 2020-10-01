package hdrhistogram_test

import (
	"bytes"
	"fmt"
	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"io/ioutil"
)

// The log format encodes into a single file, multiple histograms with optional shared meta data.
// The following example showcases reading a log file into a slice of histograms
// nolint
func ExampleNewHistogramLogReader() {
	dat, _ := ioutil.ReadFile("./test/tagged-Log.logV2.hlog")
	r := bytes.NewReader(dat)

	// Create a histogram log reader
	reader := hdrhistogram.NewHistogramLogReader(r)
	var histograms []*hdrhistogram.Histogram = make([]*hdrhistogram.Histogram, 0)

	// Read all histograms in the file
	for hist, err := reader.NextIntervalHistogram(); hist != nil && err == nil; hist, err = reader.NextIntervalHistogram() {
		histograms = append(histograms, hist)
	}
	fmt.Printf("Read a total of %d histograms\n", len(histograms))

	min := reader.RangeObservedMin()
	max := reader.RangeObservedMax()
	sigdigits := 3
	overallHistogram := hdrhistogram.New(min, max, sigdigits)

	//// We can then merge all histograms into one and retrieve overall metrics
	for _, hist := range histograms {
		overallHistogram.Merge(hist)
	}
	fmt.Printf("Overall count: %d samples\n", overallHistogram.TotalCount())
	fmt.Printf("Overall Percentile 50: %d\n", overallHistogram.ValueAtQuantile(50.0))

	// Output:
	// Read a total of 42 histograms
	// Overall count: 32290 samples
	// Overall Percentile 50: 344319

}

// The log format encodes into a single file, multiple histograms with optional shared meta data.
// The following example showcases writing multiple histograms into a log file and then
// processing them again to confirm a proper encode-decode flow
// nolint
func ExampleNewHistogramLogWriter() {
	var buff bytes.Buffer

	// Create a histogram log writer to write to a bytes.Buffer
	writer := hdrhistogram.NewHistogramLogWriter(&buff)

	writer.OutputLogFormatVersion()
	writer.OutputStartTime(0)
	writer.OutputLegend()

	// Lets create 3 distinct histograms to exemply the logwriter features
	// each one with a time-frame of 60 secs ( 60000 ms )
	hist1 := hdrhistogram.New(1, 30000000, 3)
	hist1.SetStartTimeMs(0)
	hist1.SetEndTimeMs(60000)
	for _, sample := range []int64{10, 20, 30, 40} {
		hist1.RecordValue(sample)
	}
	hist2 := hdrhistogram.New(1, 3000, 3)
	hist1.SetStartTimeMs(60001)
	hist1.SetEndTimeMs(120000)
	for _, sample := range []int64{50, 70, 80, 60} {
		hist2.RecordValue(sample)
	}
	hist3 := hdrhistogram.New(1, 30000, 3)
	hist1.SetStartTimeMs(120001)
	hist1.SetEndTimeMs(180000)
	for _, sample := range []int64{90, 100} {
		hist3.RecordValue(sample)
	}
	writer.OutputIntervalHistogram(hist1)
	writer.OutputIntervalHistogram(hist2)
	writer.OutputIntervalHistogram(hist3)

	ioutil.WriteFile("example.logV2.hlog", buff.Bytes(), 0644)

	// read check
	// Lets read all again and confirm that the total sample count is 10
	dat, _ := ioutil.ReadFile("example.logV2.hlog")
	r := bytes.NewReader(dat)

	// Create a histogram log reader
	reader := hdrhistogram.NewHistogramLogReader(r)
	var histograms []*hdrhistogram.Histogram = make([]*hdrhistogram.Histogram, 0)

	// Read all histograms in the file
	for hist, err := reader.NextIntervalHistogram(); hist != nil && err == nil; hist, err = reader.NextIntervalHistogram() {
		histograms = append(histograms, hist)
	}
	fmt.Printf("Read a total of %d histograms\n", len(histograms))

	min := reader.RangeObservedMin()
	max := reader.RangeObservedMax()
	sigdigits := 3
	overallHistogram := hdrhistogram.New(min, max, sigdigits)

	//// We can then merge all histograms into one and retrieve overall metrics
	for _, hist := range histograms {
		overallHistogram.Merge(hist)
	}
	fmt.Printf("Overall count: %d samples\n", overallHistogram.TotalCount())
	// Output:
	// Read a total of 3 histograms
	// Overall count: 10 samples
}
