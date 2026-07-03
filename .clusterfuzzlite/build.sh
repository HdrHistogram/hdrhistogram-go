#!/bin/bash -eu
# Use the following environment variables to build the code
# $CXX:               c++ compiler
# $CC:                c compiler
# CFLAGS:             compiler flags for C files
# CXXFLAGS:           compiler flags for CPP files
# LIB_FUZZING_ENGINE: linker flag for fuzzing harnesses

# compile_native_go_fuzzer relies on the go-118-fuzz-build helper to convert
# native Go (testing.F) fuzz targets into libFuzzer harnesses.
go install github.com/AdamKorcz/go-118-fuzz-build@latest

# Build one libFuzzer harness per native Go fuzz target.
compile_native_go_fuzzer $(go list ./...) FuzzDecode fuzz_decode
compile_native_go_fuzzer $(go list ./...) FuzzLogReader fuzz_log_reader
compile_native_go_fuzzer $(go list ./...) FuzzRecordEncodeDecode fuzz_record_encode_decode
compile_native_go_fuzzer $(go list ./...) FuzzZigZagRoundTrip fuzz_zigzag_round_trip

# Prepare corpus for the log reader fuzzer from the checked-in .hlog samples.
zip -j $OUT/fuzz_log_reader_seed_corpus.zip \
  $SRC/hdrhistogram-go/*.hlog \
  $SRC/hdrhistogram-go/test/*.hlog
