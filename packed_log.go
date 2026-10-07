package hdrhistogram

// Tag returns the optional interval log tag.
func (p *PackedHistogram) Tag() string { return p.geom.Tag() }

// SetTag sets the optional interval log tag.
func (p *PackedHistogram) SetTag(tag string) { p.geom.SetTag(tag) }

// StartTimeMs returns the interval start time in milliseconds.
func (p *PackedHistogram) StartTimeMs() int64 { return p.geom.StartTimeMs() }

// SetStartTimeMs sets the interval start time in milliseconds.
func (p *PackedHistogram) SetStartTimeMs(startTimeMs int64) {
	p.geom.SetStartTimeMs(startTimeMs)
}

// EndTimeMs returns the interval end time in milliseconds.
func (p *PackedHistogram) EndTimeMs() int64 { return p.geom.EndTimeMs() }

// SetEndTimeMs sets the interval end time in milliseconds.
func (p *PackedHistogram) SetEndTimeMs(endTimeMs int64) { p.geom.SetEndTimeMs(endTimeMs) }

// EncodeV2 returns the standard V2 compressed, base64-encoded representation.
// Both histogram types expose this method for use through a common interface.
func (h *Histogram) EncodeV2() ([]byte, error) {
	return h.Encode(V2CompressedEncodingCookieBase)
}

// EncodeV2 returns the standard V2 compressed, base64-encoded representation
// directly from sparse storage, without constructing a dense histogram.
func (p *PackedHistogram) EncodeV2() ([]byte, error) { return p.Encode() }

// OutputIntervalPackedHistogram outputs a packed interval histogram using its
// attached timestamps and optional tag. Timestamps are in milliseconds; the
// writer subtracts BaseTime and scales the reported maximum by MsToNsRatio,
// just as OutputIntervalHistogram does for dense histograms.
func (lw *HistogramLogWriter) OutputIntervalPackedHistogram(histogram *PackedHistogram) error {
	return lw.OutputIntervalPackedHistogramWithLogOptions(histogram, nil)
}

// OutputIntervalPackedHistogramWithLogOptions outputs a packed interval
// histogram using the same options as OutputIntervalHistogramWithLogOptions.
// Encoding operates directly on sparse storage.
func (lw *HistogramLogWriter) OutputIntervalPackedHistogramWithLogOptions(histogram *PackedHistogram, logOptions *HistogramLogOptions) error {
	return lw.outputIntervalHistogram(histogram, logOptions)
}
