package hdrhistogram

import (
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestHistogram_encodeIntoByteBuffer(t *testing.T) {
	hist := New(1, 9007199254740991, 2)
	err := hist.RecordValue(42)
	assert.Nil(t, err)
	buffer, err := hist.encodeIntoByteBuffer()
	assert.Nil(t, err)
	assert.Equal(t, 42, buffer.Len())
}

func TestHistogram_DumpLoadWhiteBox(t *testing.T) {
	hist := New(1, 100000, 3)
	for i := 1; i <= 100; i++ {
		err := hist.RecordValue(int64(i))
		assert.Nil(t, err)
	}
	dumpedHistogram, err := hist.Encode(V2CompressedEncodingCookieBase)
	assert.Nil(t, err)
	hist2, err := Decode(dumpedHistogram)
	assert.Nil(t, err)
	assert.Equal(t, hist.totalCount, hist2.totalCount)
	assert.Equal(t, hist.countsLen, hist2.countsLen)
	if diff := cmp.Diff(hist.counts, hist2.counts); diff != "" {
		t.Errorf("counts differs: (-got +want)\n%s", diff)
	}
}
