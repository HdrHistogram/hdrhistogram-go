package hdrhistogram

// A WindowedHistogram combines histograms to provide windowed statistics.
//
// It provides no internal synchronization: callers must synchronize recording
// into Current, Rotate and Merge against each other and against reads of the
// histograms they return.
type WindowedHistogram struct {
	idx int
	h   []Histogram
	m   *Histogram

	// Current is the window's histogram that receives new values. It is one of
	// the window's own histograms, so a later Rotate resets it once it becomes
	// the oldest; copy it with Clone to keep its values.
	Current *Histogram
}

// NewWindowed creates a new WindowedHistogram with N underlying histograms with
// the given parameters. n must be at least 1.
func NewWindowed(n int, minValue, maxValue int64, sigfigs int) *WindowedHistogram {
	w := WindowedHistogram{
		idx: -1,
		h:   make([]Histogram, n),
		m:   New(minValue, maxValue, sigfigs),
	}

	for i := range w.h {
		w.h[i] = *New(minValue, maxValue, sigfigs)
	}
	w.Rotate()

	return &w
}

// Merge returns a histogram which includes the recorded values from all the
// sections of the window. The returned histogram is reused: every call to
// Merge resets and refills the same histogram, so a result is only valid until
// the next Merge. Use Clone to keep an independent snapshot:
//
//	saved := w.Merge().Clone()
func (w *WindowedHistogram) Merge() *Histogram {
	w.m.Reset()
	for i := range w.h {
		w.m.Merge(&w.h[i])
	}
	return w.m
}

// Rotate resets the oldest histogram and rotates it to be used as the current
// histogram.
func (w *WindowedHistogram) Rotate() {
	w.idx++
	w.Current = &w.h[w.idx%len(w.h)]
	w.Current.Reset()
}
