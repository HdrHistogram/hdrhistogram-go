package hdrhistogram

import (
	"bytes"
	"testing"
)

// FuzzLogReader asserts that parsing an untrusted interval log never panics and
// always terminates.
func FuzzLogReader(f *testing.F) {
	f.Add([]byte("#[StartTime: 1.0 (seconds since epoch), x]\n0.1,0.2,0.3,HISTFAAAACx42pJpmSzMwMDAysDAwMjAw\n"))
	f.Add([]byte("Tag=A,0.1,0.2,0.3,HISTFAAAACx\n"))
	f.Add([]byte("Tag=abc\n"))      // B1 crasher: line[4:-1]
	f.Add([]byte("1.0,2.0,3.0,\n")) // B2 crasher: empty payload -> Decode
	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewHistogramLogReader(bytes.NewReader(data))
		for i := 0; i < 10000; i++ {
			h, err := r.NextIntervalHistogram()
			if err != nil || h == nil {
				break
			}
		}
	})
}

// --- Regression tests for the log-reader panics found by the hardening audit. ---

func TestLogReaderMalformedNoPanic(t *testing.T) {
	cases := []string{
		"Tag=abc\n",      // B1: "Tag=" with no comma -> line[4:-1]
		"1.0,2.0,3.0,\n", // B2: empty base64 payload -> Decode short-input
		"Tag=\n",
		"1.0,2.0,3.0,!!!notbase64!!!\n",
	}
	for _, in := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("NextIntervalHistogram on %q panicked: %v", in, r)
				}
			}()
			r := NewHistogramLogReader(bytes.NewReader([]byte(in)))
			for i := 0; i < 100; i++ {
				h, err := r.NextIntervalHistogram()
				if err != nil || h == nil {
					break
				}
			}
		}()
	}
}
