package hdrhistogram_test

import (
	"github.com/HdrHistogram/hdrhistogram-go"
	"os"
)

// The following example details the creation of an histogram used to track
// and analyze the counts of observed integer values between 0 us and 30000000 us ( 30 secs )
// and the printing of the percentile output format
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
