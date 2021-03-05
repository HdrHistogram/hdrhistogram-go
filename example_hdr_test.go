package hdrhistogram_test

import (
	"fmt"
	"github.com/HdrHistogram/hdrhistogram-go"
	"os"
)

// This latency Histogram could be used to track and analyze the counts of
// observed integer values between 1 us and 30000000 us ( 30 secs )
// while maintaining a value precision of 4 significant digits across that range,
// translating to a value resolution of :
//   - 1 microsecond up to 10 milliseconds,
//   - 100 microsecond (or better) from 10 milliseconds up to 10 seconds,
//   - 300 microsecond (or better) from 10 seconds up to 30 seconds,
// nolint
func ExampleNew() {
	lH := hdrhistogram.New(1, 30000000, 4)
	input := []int64{
		459876, 669187, 711612, 816326, 931423, 1033197, 1131895, 2477317,
		3964974, 12718782,
	}

	for _, sample := range input {
		lH.RecordValue(sample)
	}

	fmt.Printf("Percentile 50: %d\n", lH.ValueAtQuantile(50.0))

	// Output:
	// Percentile 50: 931423
}

// This latency Histogram could be used to track and analyze the counts of
// observed integer values between 0 us and 30000000 us ( 30 secs )
// while maintaining a value precision of 3 significant digits across that range,
// translating to a value resolution of :
//   - 1 microsecond up to 1 millisecond,
//   - 1 millisecond (or better) up to one second,
//   - 1 second (or better) up to it's maximum tracked value ( 30 seconds ).
// nolint
func ExampleHistogram_RecordValue() {
	lH := hdrhistogram.New(1, 30000000, 3)
	input := []int64{
		459876, 669187, 711612, 816326, 931423, 1033197, 1131895, 2477317,
		3964974, 12718782,
	}

	for _, sample := range input {
		lH.RecordValue(sample)
	}

	fmt.Printf("Percentile 50: %d\n", lH.ValueAtQuantile(50.0))

	// Output:
	// Percentile 50: 931839
}

// The following example details the creation of an histogram used to track
// and analyze the counts of observed integer values between 0 us and 30000000 us ( 30 secs )
// and the printing of the percentile output format
// nolint
func ExampleHistogram_PercentilesPrint() {
	lH := hdrhistogram.New(1, 30000000, 3)
	input := []int64{
		459876, 669187, 711612, 816326, 931423, 1033197, 1131895, 2477317,
		3964974, 12718782,
	}

	for _, sample := range input {
		lH.RecordValue(sample)
	}

	lH.PercentilesPrint(os.Stdout, 1, 1.0)
	// Output:
	//  Value	Percentile	TotalCount	1/(1-Percentile)
	//
	//   460031.000     0.000000            1         1.00
	//   931839.000     0.500000            5         2.00
	//  2478079.000     0.750000            8         4.00
	//  3966975.000     0.875000            9         8.00
	// 12722175.000     0.937500           10        16.00
	// 12722175.000     1.000000           10          inf
	// #[Mean    =  2491481.600, StdDeviation   =  3557920.109]
	// #[Max     = 12722175.000, Total count    =           10]
	// #[Buckets =           15, SubBuckets     =         2048]
}

// When doing an percentile analysis we normally require more than one percentile to be calculated for the given histogram.
//
// When that is the case ValueAtPercentiles() will deeply optimize the total time to retrieve the percentiles vs the other option
// which is multiple calls to ValueAtQuantile().
//
// nolint
func ExampleHistogram_ValueAtPercentiles() {
	histogram := hdrhistogram.New(1, 30000000, 3)

	for i := 0; i < 1000000; i++ {
		histogram.RecordValue(int64(i))
	}

	percentileValuesMap := histogram.ValueAtPercentiles([]float64{50.0, 95.0, 99.0, 99.9})
	fmt.Printf("Percentile 50: %d\n", percentileValuesMap[50.0])
	fmt.Printf("Percentile 95: %d\n", percentileValuesMap[95.0])
	fmt.Printf("Percentile 99: %d\n", percentileValuesMap[99.0])
	fmt.Printf("Percentile 99.9: %d\n", percentileValuesMap[99.9])

	// Output:
	// Percentile 50: 500223
	// Percentile 95: 950271
	// Percentile 99: 990207
	// Percentile 99.9: 999423

}
