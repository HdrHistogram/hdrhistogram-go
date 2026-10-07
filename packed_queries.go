package hdrhistogram

import "math"

// Mean returns the approximate arithmetic mean of the recorded values, using
// the median of each equivalent-value range. It returns zero when empty.
// It visits only populated buckets and does not allocate.
//
// If a decoded histogram's counts exceed MaxInt64, TotalCount is saturated but
// Mean uses the sum of the bucket counts in float64 to normalize the distribution.
func (p *PackedHistogram) Mean() float64 {
	total := p.statisticsCount()
	if total == 0 {
		return 0
	}
	var mean float64
	for i := int32(0); i < p.size; i++ {
		v := p.geom.valueFromFlatIndex(p.idx[i])
		mean += float64(p.slotGet(i)) * float64(p.geom.medianEquivalentValue(v)) / total
	}
	return mean
}

// StdDev returns the approximate population standard deviation of the recorded
// values, using the median of each equivalent-value range. It returns zero when
// empty. Like Mean, it visits only populated buckets without allocating and
// normalizes overflowing decoded counts using their sum in float64.
func (p *PackedHistogram) StdDev() float64 {
	total := p.statisticsCount()
	if total == 0 {
		return 0
	}
	mean := p.Mean()
	var variance float64
	for i := int32(0); i < p.size; i++ {
		v := p.geom.valueFromFlatIndex(p.idx[i])
		dev := float64(p.geom.medianEquivalentValue(v)) - mean
		variance += dev * dev * float64(p.slotGet(i)) / total
	}
	return math.Sqrt(variance)
}

// Only decoding can produce counts whose sum exceeds the saturated total.
func (p *PackedHistogram) statisticsCount() float64 {
	if p.totalCount < math.MaxInt64 {
		return float64(p.totalCount)
	}
	var total float64
	for i := int32(0); i < p.size; i++ {
		total += float64(p.slotGet(i))
	}
	return total
}

// SignificantFigures returns the significant figures used to create the histogram.
func (p *PackedHistogram) SignificantFigures() int64 {
	return p.geom.SignificantFigures()
}

// LowestTrackableValue returns the configured lower bound, after constructor
// normalization, consistently with Histogram.LowestTrackableValue.
func (p *PackedHistogram) LowestTrackableValue() int64 {
	return p.geom.LowestTrackableValue()
}

// HighestTrackableValue returns the configured upper bound, consistently with
// Histogram.HighestTrackableValue. The allocated bucket geometry may also accept
// larger values in the final bucket.
func (p *PackedHistogram) HighestTrackableValue() int64 {
	return p.geom.HighestTrackableValue()
}

// Clone returns an independent sparse copy, preserving the exact geometry,
// bucket counts, count width, and metadata. This includes decoded distributions
// whose counts exceed MaxInt64, with their saturated TotalCount. It allocates
// storage proportional to the populated buckets, without a dense counts array
// or spare capacity retained by Reset.
//
// To create an empty histogram with exactly the same geometry, call Clone
// followed by Reset. This also preserves decoded geometry that NewPacked would
// normalize if reconstructed using the geometry accessors.
func (p *PackedHistogram) Clone() *PackedHistogram {
	clone := *p
	geom := *p.geom
	geom.counts = nil
	clone.geom = &geom
	clone.idx = append([]int32(nil), p.idx...)
	clone.cnt = append([]byte(nil), p.cnt...)
	return &clone
}

// RecordCorrectedValue records v and corrects for stalls in a process that
// records values at expectedInterval. It also records v-expectedInterval,
// v-2*expectedInterval, and so on, while those values are at least expectedInterval.
// A nonpositive interval records only v, matching Histogram.RecordCorrectedValue.
//
// It uses sparse storage. If recording fails, it returns the error immediately;
// the original observation and any synthetic observations already recorded are
// retained. In particular, reaching the total-count limit can leave a partial
// correction.
func (p *PackedHistogram) RecordCorrectedValue(v, expectedInterval int64) error {
	if err := p.RecordValue(v); err != nil {
		return err
	}
	if expectedInterval <= 0 || v <= expectedInterval {
		return nil
	}
	for missing := v - expectedInterval; missing >= expectedInterval; missing -= expectedInterval {
		if err := p.RecordValue(missing); err != nil {
			return err
		}
	}
	return nil
}
