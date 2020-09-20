package hdrhistogram

import (
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"math"
	"reflect"
	"testing"
)

func TestHistogram_encodeIntoByteBuffer(t *testing.T) {
	hist := New(1, 9007199254740991, 2)
	hist.RecordValue(42)
	buffer, err := hist.encodeIntoByteBuffer()
	assert.Nil(t, err)
	assert.Equal(t, 42, buffer.Len())
}

func TestHistogram_DumpLoadWhiteBox(t *testing.T) {
	hist := New(1, 100000, 3)
	for i := 1; i <= 100; i++ {
		hist.RecordValue(int64(i))
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

func Test_zig_zag_decode_i64(t *testing.T) {
	largeV := int64(math.Exp2(50))
	type args struct {
		buffer []byte
	}
	tests := []struct {
		name            string
		args            args
		wantSignedValue int64
		wantBytesRead   int
	}{
		{"56", args{zig_zag_encode_i64(56)}, 56, 1},
		{"-1515", args{zig_zag_encode_i64(-1515)}, -1515, 2},
		{"456", args{zig_zag_encode_i64(456)}, 456, 2},
		{"largeV", args{zig_zag_encode_i64(largeV)}, largeV, 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSignedValue, gotBytesRead := zig_zag_decode_i64(tt.args.buffer)
			if gotSignedValue != tt.wantSignedValue {
				t.Errorf("zig_zag_decode_i64() gotSignedValue = %v, want %v", gotSignedValue, tt.wantSignedValue)
			}
			if gotBytesRead != tt.wantBytesRead {
				t.Errorf("zig_zag_decode_i64() gotBytesRead = %v, want %v", gotBytesRead, tt.wantBytesRead)
			}
		})
	}
}

func Test_zig_zag_encode_i64(t *testing.T) {
	largeV := int64(math.Exp2(50))
	type args struct {
		value int64
	}
	tests := []struct {
		name       string
		args       args
		wantBuffer []byte
	}{
		{"56", args{56}, []byte{112}},
		{"-56", args{-56}, []byte{111}},
		{"456", args{456}, []byte{144, 7}},
		{"-456", args{-456}, []byte{143, 7}},
		{"2^50", args{largeV}, []byte{128, 128, 128, 128, 128, 128, 128, 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotBuffer := zig_zag_encode_i64(tt.args.value); !reflect.DeepEqual(gotBuffer, tt.wantBuffer) {
				t.Errorf("zig_zag_encode_i64() = %v, want %v", gotBuffer, tt.wantBuffer)
			}
		})
	}
}
