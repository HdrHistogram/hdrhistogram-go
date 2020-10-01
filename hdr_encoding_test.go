package hdrhistogram_test

import (
	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestHistogram_Load_Errors(t *testing.T) {
	//	should throw an error when trying to decompress an histogram using V1 encoding
	v1 := []byte("HISTIgAAAFd42pNpmazIwMAYxgABTBDKT4GBgdnNYMcCBvsPUBkeBkYGZqA8MwMbAzsDC5DFBCTZgJCDQY1BjkGLQZRBlUEPCB8zWDCYMxgDZZkZhgJgHDibAY8JB/A=")
	_, err := hdrhistogram.Decode(v1)
	assert.NotNil(t, err)
}

func TestHistogram_Load(t *testing.T) {
	inputBase64 := []byte("HISTFAAAAB542pNpmSzMwMDAxAABzFCaEUoz2X+AMIKZAEARAtM=")
	rh, err := hdrhistogram.Decode(inputBase64)
	assert.Nil(t, err)
	assert.Equal(t, int64(1), rh.TotalCount())
	assert.Equal(t, float64(42.0), rh.Mean())

	rh, err = hdrhistogram.Decode([]byte("HISTFAAAAB94nJNpmSzMwMDABMSMQMzMAAGMUJoJxg9mAgA1TQGm"))
	assert.Nil(t, err)
	assert.Equal(t, int64(1), rh.TotalCount())
	assert.Equal(t, float64(42.0), rh.Mean())

	// empty histogram
	// 20,1000,3
	empty := []byte("HISTFAAAABl4nJNpmSzMgADMUFoEyn1h/wHCAgBDogN4")
	rh, err = hdrhistogram.Decode(empty)
	assert.Nil(t, err)
	assert.Equal(t, int64(20), rh.LowestTrackableValue())
	assert.Equal(t, int64(1000), rh.HighestTrackableValue())
	assert.Equal(t, int64(3), rh.SignificantFigures())
	assert.Equal(t, int64(0), rh.TotalCount())

	bigBuffer := []byte("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")
	rh, err = hdrhistogram.Decode(bigBuffer)
	assert.Nil(t, err)
	assert.Equal(t, int64(10000), rh.TotalCount())
}

func TestHistogram_Dump_empty(t *testing.T) {
	// empty histogram
	// 20,1000,3
	empty := []byte("HISTFAAAABl4nJNpmSzMgADMUFoEyn1h/wHCAgBDogN4")
	loadedHist, err := hdrhistogram.Decode(empty)
	assert.Nil(t, err)
	assert.Equal(t, int64(20), loadedHist.LowestTrackableValue())
	assert.Equal(t, int64(1000), loadedHist.HighestTrackableValue())
	assert.Equal(t, int64(3), loadedHist.SignificantFigures())

}

func TestHistogram_Dump_Load_Merge(t *testing.T) {
	h1 := hdrhistogram.New(1, 1000, 3)
	h2 := hdrhistogram.New(1, 1000, 3)

	for i := 0; i < 100; i++ {
		if err := h1.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	for i := 100; i < 200; i++ {
		if err := h2.RecordValue(int64(i)); err != nil {
			t.Fatal(err)
		}
	}

	bufferH1, err := h1.Encode(hdrhistogram.V2CompressedEncodingCookieBase)
	assert.Nil(t, err)

	bufferH2, err := h2.Encode(hdrhistogram.V2CompressedEncodingCookieBase)
	assert.Nil(t, err)

	h1Decoded, err := hdrhistogram.Decode(bufferH1)
	assert.Nil(t, err)

	assert.Equal(t, int64(1), h1Decoded.LowestTrackableValue())
	assert.Equal(t, int64(1000), h1Decoded.HighestTrackableValue())
	assert.Equal(t, int64(3), h1Decoded.SignificantFigures())
	assert.Equal(t, int64(100), h1Decoded.TotalCount())

	h2Decoded, err := hdrhistogram.Decode(bufferH2)
	assert.Nil(t, err)

	assert.Equal(t, int64(1), h2Decoded.LowestTrackableValue())
	assert.Equal(t, int64(1000), h2Decoded.HighestTrackableValue())
	assert.Equal(t, int64(3), h2Decoded.SignificantFigures())
	assert.Equal(t, int64(100), h2Decoded.TotalCount())

	dropped := h1Decoded.Merge(h2Decoded)
	assert.Equal(t, int64(0), dropped)
	assert.Equal(t, int64(200), h1Decoded.TotalCount())
	assert.Equal(t, int64(1), h1Decoded.LowestTrackableValue())
	assert.Equal(t, int64(1000), h1Decoded.HighestTrackableValue())
}
