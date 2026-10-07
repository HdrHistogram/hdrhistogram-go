# Packed histogram compatibility

Go `PackedHistogram` preserves the policies of Go `Histogram` where described below. Sharing HDR bucket geometry and a V2 format does not imply identical constructor validation, percentile selection or metadata handling across languages. These are intentional compatibility policies; applications migrating from C or Java should account for them explicitly.

The reference comparisons were reproduced against [C revision 8885476](https://github.com/HdrHistogram/HdrHistogram_c/tree/8885476fc83fa362fec2fb2b5e9cd9544514976e) and [Java revision de84b0a](https://github.com/HdrHistogram/HdrHistogram/tree/de84b0a7de2378abfc405da503bf4898e84ea98e). They describe those revisions, not every release.

## Queries

For three samples `1000, 2000, 3000` with geometry `(100, 100000, 3)`:

| Percentile | Go packed | C packed | Java packed |
|---|---:|---:|---:|
| 40 | 1023 | 1023 | 2047 |
| 66.67 | 2047 | 2047 | 3007 |
| -1 or negative infinity | 960 | 1023 | 1023 |
| NaN | 1023 | 3007 | 1023 |
| positive infinity | 3007 | 3007 | 3007 |

Go and C select the nearest integer rank, computed from `percentile / 100 * total + 0.5` and truncated, with a minimum rank of one. Java uses a ceiling rank with a floating-point adjustment. Go clamps a negative percentile to zero and returns the lowest equivalent value at percentile zero; a positive percentile returns the highest equivalent value. NaN selects the first rank and its highest equivalent value. Percentiles above 100, including positive infinity, select the maximum. Scalar and slice queries follow the same rules, and slice queries preserve input order. These differences matter even for ordinary small histograms, not just unusual inputs.

Extrema retain Go dense bucket-edge semantics:

| Geometry `(1000, 1000000, 3)` | Go packed Min / Max | C packed Min / Max | Java packed Min / Max |
|---|---|---|---|
| Empty | 0 / 511 | MaxInt64 / 0 | 0 / 0 |
| Only value 0 | 0 / 511 | 0 / 0 | 0 / 0 |
| Only value 1 | 0 / 511 | 0 / 511 | 0 / 511 |

Go stores bucket counts, so zero and one are indistinguishable at this geometry. Encoding and decoding retain Go's bucket-edge results. Java's empty extrema are not a portable contract even though the listed values were observed. At a sole `MaxInt64` observation, C/Java minimum tracking can collide with their sentinel; Go returns the bucket's lower edge.

`CountAtValue` returns zero for negative values or values outside the allocated buckets. C packed also returns zero. Java clamps an oversized positive query index to the last bucket and throws for a negative index. For example, after recording `2047` in `(1, 2047, 3)`, Go and C return zero for queries `2048` and `-1`; Java returns one for `2048` and throws for `-1`. A value above the configured maximum but inside the allocated last bucket is still an in-range query in Go.

`Mean` and `StdDev` use bucket midpoint estimates and walk only populated buckets. For decoded distributions whose count sum exceeds `MaxInt64`, they normalize by the actual sum accumulated in floating point, rather than the saturated `TotalCount`. They cannot recover the original samples within a bucket.

## Construction and recording range

| Policy | Go `NewPacked` / `New` | C packed | Java fixed-range packed |
|---|---|---|---|
| Lowest value below 1 | Clamped to 1 | Rejected | Rejected |
| Constructor precision | Clamped to 1–5 | Requires 1–5 | Requires 0–5 |
| Highest value below twice the lowest | Permitted by constructor; streams decode | Rejected | Rejected |
| Value above the configured maximum but inside the allocated buckets | Accepted | Rejected | Accepted |

For example, `NewPacked(1, 1000, 3).RecordValue(1001)` succeeds in Go, as does recording `1000` into `NewPacked(0, -1, 6)`. The latter constructor normalizes the lowest value to 1 and precision to 5, while retaining the supplied highest value. Geometries that cannot be represented in 64 bits can still panic; permissive construction is not unrestricted construction. No strict constructor is added here, to preserve existing callers' normalization behavior. Applications needing a stricter configuration policy should validate their configuration before construction.

Constructor normalization does not authorize reinterpreting foreign wire headers. Both decoders preserve serialized precision exactly, including Java's zero-digit geometry, and reject precision outside 0–5 or geometry that cannot be represented in 64 bits. There is one legacy exception: hdrhistogram-go v1.0.0 wrote lowest values below 1 while using unit magnitude zero, so those fields are read as 1 without changing the bucket mapping. The decoders do not reject a highest value below twice the lowest: Go's constructors accept such ranges and Go has always written them, so rejecting them would make existing Go streams and interval logs unreadable. C and Java reject such headers, so use a highest value of at least twice the lowest for streams meant for those readers.

## V2 interchange

`Encode` writes base64 V2 compressed integer histogram data, using a zero normalizing index offset and the histogram's conversion ratio: `1.0` for histograms built with `NewPacked`, or the ratio a decoded stream carried. It emits the same bytes as Go's dense encoder on equivalent data. `DecodePacked` supports V2 compressed streams with valid supported geometry and a zero normalizing index offset. This is a subset of the streams foreign writers can produce.

| Input feature | Go packed policy | Reference behavior |
|---|---|---|
| Nonzero `normalizingIndexOffset` | Rejected | C packed also rejects it; Java handles shifted histograms. |
| Conversion ratio other than 1 | Kept as metadata and written back by `Encode`; counts stay integer bucket counts | Go dense also keeps it (including through `Export`/`Import`); Java preserves it; C packed normalizes it to 1. |
| Positive bucket counts whose sum exceeds `MaxInt64` | Buckets retained; `TotalCount` saturates at `MaxInt64` | C packed also saturates; Go dense can wrap its total. |

For example, Java `shiftValuesLeft(2)` can produce a nonzero offset. Supporting such streams requires verifying the writer's serialization and index semantics; simply removing the guard is not a verified compatibility solution. Re-encoding an accepted ratio-2.5 C stream preserves the integer distribution but loses its scale metadata. Carry unit conversions separately if they are needed; a Go decode/encode cycle is not a metadata-preserving archival operation. The foreign-writer fixtures in `packed_compatibility_test.go` cover both cases.

## Ownership and independent copies

Create a histogram with `NewPacked` or `DecodePacked`; the zero value is not ready for use. Do not copy an initialized `PackedHistogram` by value. A struct assignment shares its arrays but copies its totals and bookkeeping, so mutation can invalidate both views. Passing its pointer is fine. Java has a `copy()` method and C exposes an opaque type; Go callers should use `Clone` for an independent sparse copy:

```go
snapshot := original.Clone()

// An empty sibling with exactly the same geometry, including decoded precision.
sibling := original.Clone()
sibling.Reset()
```

`Clone` preserves exact geometry, all sparse bucket counts (including a decoded distribution whose sum exceeds `MaxInt64`), and start/end timestamps and tags. `Reset` then clears counts and log metadata while retaining storage. Geometry getters remain useful for inspecting configuration, but passing them into `NewPacked` preserves only geometries representable by that constructor: constructor precision zero is clamped to one, so this recipe cannot reproduce decoded Java zero-digit geometry. Adding counts with `Merge` also cannot fully copy a saturated source, because recording into a packed destination rejects total overflow.

`Encode` followed by `DecodePacked` also creates independent storage and retains individual decoded bucket counts even when their sum exceeds `MaxInt64`. That path requires valid wire geometry, keeps the conversion ratio, and does not include interval timestamps or tags in the histogram payload.

There is no internal synchronization. Concurrent readers are safe only while the histogram is unchanged. Synchronize every mutation, including recording, corrected recording, merges, `Reset`, `Compact` and metadata setters, against readers and other writers. The source of a merge must also remain unchanged while it is read. Distinct pointers produced by a shallow struct copy do not satisfy `Merge`'s independent-storage precondition; `p.Merge(p)` itself is supported.

## Merge and logging limits

`MergeInto` matches `Histogram.Merge` for sources whose positive bucket-count sum fits in `int64`. It adds every populated source bucket that fits the destination's range. Destination counts and totals are added without overflow checks and may wrap. For a decoded saturated source, packed merging still visits every positive bucket, while dense merging may stop early because its source total has wrapped. Exact dense parity is not promised for those sources, and packed merging does not imitate that loss of data. C has no equivalent packed merge API in the reviewed revision; Java's same-layout addition can also wrap its total.

Allocation and complexity guarantees are specific to each operation; see the [merge cost table](README.md#packed-histograms). Matching geometry avoids remapping values but does not avoid scanning a dense source or allocating when packed storage grows.

`RecordCorrectedValue` supports coordinated-omission correction directly in sparse storage. `HistogramLogWriter.OutputIntervalPackedHistogram` and `OutputIntervalPackedHistogramWithLogOptions` write packed intervals directly; the standard log reader continues to return dense histograms. Log timestamps and tags are interval metadata, separate from the histogram's V2 payload.
