package hdrhistogram

import (
	"math"
	"reflect"
	"testing"
)

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
		wantErr         bool
	}{
		{"empty", args{[]byte{}}, 0, 0, false},
		{"1", args{[]byte{1}}, -1, 1, false},
		{"2", args{[]byte{2}}, 1, 1, false},
		{"3", args{[]byte{3}}, -2, 1, false},
		{"4", args{[]byte{4}}, 2, 1, false},
		{"truncated 2nd byte", args{[]byte{128}}, 0, 1, true},
		{"truncated 3rd byte", args{[]byte{128, 128}}, 0, 2, true},
		{"truncated 4th byte", args{[]byte{128, 128, 128}}, 0, 3, true},
		{"truncated 5th byte", args{[]byte{128, 128, 128, 128}}, 0, 4, true},
		{"truncated 6th byte", args{[]byte{128, 128, 128, 128, 128}}, 0, 5, true},
		{"truncated 7th byte", args{[]byte{128, 128, 128, 128, 128, 128}}, 0, 6, true},
		{"truncated 8th byte", args{[]byte{128, 128, 128, 128, 128, 128, 128}}, 0, 7, true},
		{"truncated 9th byte", args{[]byte{128, 128, 128, 128, 128, 128, 128, 128}}, 0, 8, true},
		{"56", args{zig_zag_encode_i64(56)}, 56, 1, false},
		{"-1515", args{zig_zag_encode_i64(-1515)}, -1515, 2, false},
		{"456", args{zig_zag_encode_i64(456)}, 456, 2, false},
		{"largeV", args{zig_zag_encode_i64(largeV)}, largeV, 8, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSignedValue, gotBytesRead, gotErr := zig_zag_decode_i64(tt.args.buffer)
			if gotSignedValue != tt.wantSignedValue {
				t.Errorf("zig_zag_decode_i64() gotSignedValue = %v, want %v", gotSignedValue, tt.wantSignedValue)
			}
			if gotBytesRead != tt.wantBytesRead {
				t.Errorf("zig_zag_decode_i64() gotBytesRead = %v, want %v", gotBytesRead, tt.wantBytesRead)
			}
			if gotErr == nil && tt.wantErr {
				t.Errorf("zig_zag_decode_i64() gotErr = %v, wanted error", gotErr)
			}
			if tt.wantErr == false && gotErr != nil {
				t.Errorf("zig_zag_decode_i64() gotErr = %v, wanted nil", gotErr)
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
