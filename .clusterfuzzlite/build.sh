#!/bin/bash -eu
# Use the following environment variables to build the code
# $CXX:               c++ compiler
# $CC:                c compiler
# CFLAGS:             compiler flags for C files
# CXXFLAGS:           compiler flags for CPP files
# LIB_FUZZING_ENGINE: linker flag for fuzzing harnesses

# compile_native_go_fuzzer relies on the go-118-fuzz-build helper to convert
# native Go (testing.F) fuzz targets into libFuzzer harnesses. It installs the
# generator binary AND requires the matching testing shim as a module dependency
# (the rewritten harness imports go-118-fuzz-build/testing).
go install github.com/AdamKorcz/go-118-fuzz-build@latest
go get github.com/AdamKorcz/go-118-fuzz-build/testing

# Build one libFuzzer harness per native Go fuzz target.
compile_native_go_fuzzer $(go list ./...) FuzzDecode fuzz_decode
compile_native_go_fuzzer $(go list ./...) FuzzLogReader fuzz_log_reader
compile_native_go_fuzzer $(go list ./...) FuzzRecordEncodeDecode fuzz_record_encode_decode
compile_native_go_fuzzer $(go list ./...) FuzzZigZagRoundTrip fuzz_zigzag_round_trip
compile_native_go_fuzzer $(go list ./...) FuzzDecodeInvariants fuzz_decode_invariants
compile_native_go_fuzzer $(go list ./...) FuzzPercentileQueries fuzz_percentile_queries
compile_native_go_fuzzer $(go list ./...) FuzzZigZagDecodeBytes fuzz_zigzag_decode_bytes
compile_native_go_fuzzer $(go list ./...) FuzzMergeMetamorphic fuzz_merge_metamorphic

# Prepare corpus for the log reader fuzzer from the checked-in .hlog samples.
zip -j $OUT/fuzz_log_reader_seed_corpus.zip \
  $SRC/hdrhistogram-go/*.hlog \
  $SRC/hdrhistogram-go/test/*.hlog
