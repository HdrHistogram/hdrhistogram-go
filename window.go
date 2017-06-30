package hdrhistogram

// A WindowedHistogram combines histograms to provide windowed statistics.
type WindowedHistogram struct {
	idx int
	h   []*Histogram
	m   *Histogram
	tmp *Histogram

	Current *Histogram
}

// NewWindowed creates a new WindowedHistogram with N underlying histograms with
// the given parameters.
func NewWindowed(n int, minValue, maxValue int64, sigfigs int) *WindowedHistogram {
	w := WindowedHistogram{
		idx: -1,
		h:   make([]*Histogram, n),
		m:   New(minValue, maxValue, sigfigs),
		tmp: New(minValue, maxValue, sigfigs),
	}

	for i := range w.h {
		w.h[i] = New(minValue, maxValue, sigfigs)
	}
	w.Rotate()

	return &w
}

// Merge returns a histogram which includes the recorded values from all the
// sections of the window.
func (w *WindowedHistogram) Merge() *Histogram {
	w.tmp.Reset()
	w.tmp.Merge(w.m)
	w.tmp.Merge(w.Current)
	return w.tmp
}

// Rotate resets the oldest histogram and rotates it to be used as the current
// histogram. Returns merged histogram the same Merge would return.
func (w *WindowedHistogram) Rotate() *Histogram {
	if w.Current != nil {
		w.m.Merge(w.Current)
	}
	w.idx++
	w.Current = w.h[w.idx%len(w.h)]
	w.m.Unmerge(w.Current)
	w.Current.Reset()
	return w.m
}
