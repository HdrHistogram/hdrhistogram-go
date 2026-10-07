hdrhistogram-go
===============

<a href="https://pkg.go.dev/github.com/HdrHistogram/hdrhistogram-go"><img src="https://pkg.go.dev/badge/github.com/HdrHistogram/hdrhistogram-go" alt="PkgGoDev"></a>
[![Gitter](https://badges.gitter.im/Join_Chat.svg)](https://gitter.im/HdrHistogram/HdrHistogram)
![Test](https://github.com/HdrHistogram/hdrhistogram-go/workflows/Test/badge.svg?branch=master)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/HdrHistogram/hdrhistogram-go/blob/master/LICENSE)
[![Codecov](https://codecov.io/gh/HdrHistogram/hdrhistogram-go/branch/master/graph/badge.svg)](https://codecov.io/gh/HdrHistogram/hdrhistogram-go)


A pure Go implementation of the [HDR Histogram](https://github.com/HdrHistogram/HdrHistogram).

> A Histogram that supports recording and analyzing sampled data value counts
> across a configurable integer value range with configurable value precision
> within the range. Value precision is expressed as the number of significant
> digits in the value recording, and provides control over value quantization
> behavior across the value range and the subsequent value resolution at any
> given level.

For documentation, check [godoc](https://pkg.go.dev/github.com/HdrHistogram/hdrhistogram-go).


## Getting Started

### Installing
Use `go get` to retrieve the hdrhistogram-go implementation and to add it to your `GOPATH` workspace, or project's Go module dependencies.

```go
go get github.com/HdrHistogram/hdrhistogram-go
```

To update the implementation use `go get -u` to retrieve the latest version of the hdrhistogram.

```go
go get github.com/HdrHistogram/hdrhistogram-go
```


### Go Modules

If you are using Go modules, your `go get` will default to the latest tagged
release version of the histogram. To get a specific release version, use
`@<tag>` in your `go get` command.

```go
go get github.com/HdrHistogram/hdrhistogram-go@v0.9.0
```

To get the latest HdrHistogram/hdrhistogram-go master repository change use `@latest`.

```go
go get github.com/HdrHistogram/hdrhistogram-go@latest
```

### Repo transfer and impact on go dependencies
-------------------------------------------
This repository has been transferred under the github HdrHstogram umbrella with the help from the orginal
author in Sept 2020. The main reasons are to group all implementations under the same roof and to provide more active contribution
from the community as the orginal repository was archived several years ago.

Unfortunately such URL change will break go applications that depend on this library
directly or indirectly, as discussed [here](https://github.com/HdrHistogram/hdrhistogram-go/issues/30#issuecomment-696365251).

The dependency URL should be modified to point to the new repository URL.
The tag "v0.9.0" was applied at the point of transfer and will reflect the exact code that was frozen in the
original repository.

If you are using Go modules, you can update to the exact point of transfter using the `@v0.9.0` tag in your `go get` command.

```
go mod edit -replace github.com/codahale/hdrhistogram=github.com/HdrHistogram/hdrhistogram-go@v0.9.0
```

### Ownership and snapshots

A `Histogram` must not be copied by value: a struct copy shares its counts array. Use `Clone` for an independent copy, for example `saved := w.Merge().Clone()` to keep a `WindowedHistogram` result, which is reused by the next `Merge`. Histograms provide no internal synchronization. Call `Snapshot.Validate` before `Import` for snapshots from untrusted sources.

## Packed histograms
-------

`New` allocates the full counts array up front: about 184 KB at a typical latency configuration (`New(1, 3600000000, 3)`), however few values are recorded. When you keep **many sparsely populated histograms** (per endpoint, per tenant, per connection, or a ring of per-second slices), use `PackedHistogram` instead. Its storage grows with the number of populated buckets, holding each count in 1, 2, 4 or 8 bytes as needed:

| Populated buckets | `Histogram` | `PackedHistogram` |
|---|--:|--:|
| 10 | 184 KB | ~0.3 KB |
| 100 | 184 KB | ~0.8 KB |
| 1,600 | 184 KB | ~10 KB |

It uses the same bucket layout and constructor arguments as `Histogram`. `Encode` emits the standard V2 compressed format, with the same bytes as the dense encoder on equivalent data. `DecodePacked` and `Decode` read V2 streams from Go, Java and C writers, including shifted Java histograms: the `normalizingIndexOffset` header field only describes a writer's in-memory layout, so both decoders ignore it. The conversion ratio is kept as metadata and written back on encode; counts stay integer bucket counts. See the [C/Java compatibility guide](PACKED_COMPATIBILITY.md) for query, constructor and wire-format policies.

The trade-off is recording speed: recording into an existing bucket is a binary search, and populating a new bucket is O(populated). Keep the dense `Histogram` for hot recording paths and for histograms where most buckets fill up. Do not copy an initialized `PackedHistogram` by value; synchronize all mutations against concurrent reads and writes.

```go
h := hdrhistogram.NewPacked(1, 3600000000, 3)
h.RecordValue(1234)
p99 := h.ValueAtPercentile(99)
encoded, err := h.Encode() // V2 compressed, same bytes as Histogram.Encode
```

For rolling windows, record into a dense histogram and keep the completed slices packed:

| Need | API |
|---|---|
| dense → packed | `MergeFrom(*Histogram)` |
| packed → dense | `MergeInto(*Histogram)` (dense parity when the source count sum fits in `int64`) |
| packed → packed | `Merge(*PackedHistogram)` |
| reuse a slot | `Reset()` keeps its storage; `Compact()` shrinks it back to fit |
| visit populated buckets | `ForEachBucket` (also usable with `range`) |

```go
// Each second: move the active dense slice into the oldest packed slot.
slot := ring[second%len(ring)]
slot.Reset()
slot.MergeFrom(active)
active.Reset()

// On demand: aggregate the window into a dense histogram.
window := hdrhistogram.New(1, 30000000, 3)
for _, s := range ring {
	s.MergeInto(window)
}
p99 := window.ValueAtQuantile(99)
```

The merge methods return the count they had to drop (values out of the destination's range, or counts that would overflow the destination's total). Dense and packed `RecordValues` reject a count that would overflow the total, so totals never wrap. Costs differ by operation:

| Operation | Work and allocations |
|---|---|
| `MergeInto` | O(source populated buckets), with no allocations, including when geometries differ. |
| `MergeFrom` | Scans the entire dense source counts array. Into an empty packed destination, buckets append in order; into a populated destination, insertions may shift existing entries. Growth in capacity or count width can allocate. Reusing sufficient capacity and width after `Reset` avoids allocation. |
| `Merge` | With matching indexing, distinct histograms and totals safely below overflow, work is linear in both populated sizes. Capacity or count-width growth can allocate. Self-merges, differing geometry and possible total overflow use bucket-by-bucket insertion, which can be quadratic when entries must shift. |

`Reset` retains capacity and count width; `Compact` releases spare capacity and can narrow counts, so later merges may allocate again. See [`ExamplePackedHistogram_MergeInto`](https://pkg.go.dev/github.com/HdrHistogram/hdrhistogram-go#example-PackedHistogram.MergeInto) for a complete ring.

Packed histograms also support `Mean`, `StdDev`, `RecordCorrectedValue`, and geometry getters. Use `Clone` for an independent sparse copy that preserves exact geometry, counts and log metadata; `Clone` followed by `Reset` creates an empty sibling with the same geometry. Use `HistogramLogWriter.OutputIntervalPackedHistogram` (or its `WithLogOptions` variant) to write a packed interval directly without a dense conversion. See the [compatibility guide](PACKED_COMPATIBILITY.md) for copying and saturated-count limitations.

## Fuzzing
-------

Every `FuzzXxx` target in the module root is fuzzed in CI, with no list to maintain:

* **Fuzz smoke** (every push and PR): 30 seconds per target, starting from the corpus the nightly run has built.
* **Fuzz nightly**: 30 minutes per target. Each target's corpus is kept in the Actions cache, so every night continues from the last; on Sundays each corpus is first minimised (`.github/scripts/fuzz-corpus-minimize.sh`). A failure uploads the failing input as an artifact and opens (or updates) a tracking issue. It can also be started by hand with a longer `fuzzminutes` or `minimize` enabled.
* **ClusterFuzzLite**: libFuzzer with AddressSanitizer on PRs, plus a nightly batch run and corpus pruning. New targets must be registered in `.clusterfuzzlite/build.sh`, and no target name may be a prefix of another fuzz function's name; CI fails otherwise.

Locally, `make fuzz FUZZTIME=1m` fuzzes every target in turn. To reproduce a CI failure, copy the uploaded input into `testdata/fuzz/<Target>/` and run `go test -run='^<Target>$' .`.

## Credits
-------

Many thanks for Coda Hale for contributing the initial implementation and transfering the repository here.
