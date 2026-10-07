// Package hdrhistogram provides facilities for encoding and decoding
// high dynamic range histograms using the HdrHistogram V2 format.
//
// The V2 format uses a modified ZigZag LEB128 encoding scheme optimized
// for compactness:
//   - Consecutive zero counters are represented as a negative integer
//     indicating the run length of zeros.
//   - Non-zero counters are represented as positive integers.
//
// This encoding allows a typical histogram (with 2 digits of precision
// covering a range from 1 microsecond to 1 day) to be represented in
// less than a single MTU-sized packet (~1500 bytes).
//
// Histograms can be serialized to or deserialized from compressed
// Base64-encoded binary representations for efficient transmission
// or archival. The implementation supports only the V2 compressed
// encoding format.
package hdrhistogram

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	V2EncodingCookieBase           int32 = 0x1c849303
	V2CompressedEncodingCookieBase int32 = 0x1c849304
	encodingCookie                 int32 = V2EncodingCookieBase | 0x10
	compressedEncodingCookie       int32 = V2CompressedEncodingCookieBase | 0x10

	ENCODING_HEADER_SIZE = 40
)

// Encode returns a snapshot view of the Histogram.
// The snapshot is compact binary representations of the state of the histogram.
// They are intended to be used for archival or transmission to other systems for further analysis.
func (h *Histogram) Encode(version int32) (buffer []byte, err error) {
	switch version {
	case V2CompressedEncodingCookieBase:
		buffer, err = h.dumpV2CompressedEncoding()
	default:
		err = fmt.Errorf("the provided enconding version %d is not supported", version)
	}
	return
}

// Decode returns a new Histogram by decoding it from a String containing
// a base64 encoded compressed histogram representation. Invalid serialized
// geometry is rejected instead of applying New's argument normalization.
// Conversion-ratio metadata is not retained; re-encoding emits a ratio of 1.0.
func Decode(encoded []byte) (rh *Histogram, err error) {
	var decoded []byte
	decoded, err = base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		return
	}
	// The 8-byte header (cookie + compressed length) must be present before we
	// slice it, otherwise a short/truncated input would panic on decoded[0:8].
	if len(decoded) < 8 {
		err = fmt.Errorf("encoded histogram too short: got %d bytes, need at least 8", len(decoded))
		return
	}
	rbuf := bytes.NewBuffer(decoded[0:8])
	r32 := make([]int32, 2)
	err = binary.Read(rbuf, binary.BigEndian, &r32)
	if err != nil {
		return
	}
	Cookie := r32[0] & ^0xf0
	lengthOfCompressedContents := r32[1]
	if Cookie != V2CompressedEncodingCookieBase {
		err = fmt.Errorf("encoding not supported, only V2 is supported. got %d want %d", Cookie, V2CompressedEncodingCookieBase)
		return
	}
	decodeLengthOfCompressedContents := int32(len(decoded[8:]))
	// A negative length (from attacker-controlled bytes) must be rejected: the
	// slice decoded[8:8+negative] would panic on an invalid low>high bound.
	if lengthOfCompressedContents < 0 {
		err = fmt.Errorf("negative lengthOfCompressedContents: %d", lengthOfCompressedContents)
		return
	}
	if lengthOfCompressedContents > decodeLengthOfCompressedContents {
		err = fmt.Errorf("the compressed contents buffer is smaller than the lengthOfCompressedContents. got %d want %d", decodeLengthOfCompressedContents, lengthOfCompressedContents)
		return
	}
	rh, err = decodeCompressedFormat(decoded[8:8+lengthOfCompressedContents], ENCODING_HEADER_SIZE)
	return
}

// internal method to encode a histogram in V2 Compressed format
func (h *Histogram) dumpV2CompressedEncoding() (outBuffer []byte, err error) {
	// final buffer
	buf := new(bytes.Buffer)
	err = binary.Write(buf, binary.BigEndian, compressedEncodingCookie)
	if err != nil {
		return
	}
	toCompress, err := h.encodeIntoByteBuffer()
	if err != nil {
		return
	}
	uncompressedBytes := toCompress.Bytes()

	var b bytes.Buffer
	w, err := zlib.NewWriterLevel(&b, zlib.BestCompression)
	if err != nil {
		return
	}
	_, err = w.Write(uncompressedBytes)
	if err != nil {
		return
	}
	err = w.Close()
	if err != nil {
		return
	}

	// LengthOfCompressedContents
	compressedContents := b.Bytes()
	err = binary.Write(buf, binary.BigEndian, int32(len(compressedContents)))
	if err != nil {
		return
	}
	err = binary.Write(buf, binary.BigEndian, compressedContents)
	if err != nil {
		return
	}
	outBuffer = []byte(base64.StdEncoding.EncodeToString(buf.Bytes()))
	return
}

func (h *Histogram) encodeIntoByteBuffer() (*bytes.Buffer, error) {

	countsBytes, err := h.fillBufferFromCountsArray()
	if err != nil {
		return nil, err
	}

	toCompress := new(bytes.Buffer)
	err = binary.Write(toCompress, binary.BigEndian, encodingCookie) // 0-3
	if err != nil {
		return nil, err
	}
	err = binary.Write(toCompress, binary.BigEndian, int32(len(countsBytes))) // 3-7
	if err != nil {
		return nil, err
	}
	err = binary.Write(toCompress, binary.BigEndian, h.getNormalizingIndexOffset()) // 8-11
	if err != nil {
		return nil, err
	}
	err = binary.Write(toCompress, binary.BigEndian, int32(h.significantFigures)) // 12-15
	if err != nil {
		return nil, err
	}
	err = binary.Write(toCompress, binary.BigEndian, h.lowestDiscernibleValue) // 16-23
	if err != nil {
		return nil, err
	}
	err = binary.Write(toCompress, binary.BigEndian, h.highestTrackableValue) // 24-31
	if err != nil {
		return nil, err
	}
	err = binary.Write(toCompress, binary.BigEndian, h.getIntegerToDoubleValueConversionRatio()) // 32-39
	if err != nil {
		return nil, err
	}
	err = binary.Write(toCompress, binary.BigEndian, countsBytes)
	if err != nil {
		return nil, err
	}
	return toCompress, err
}

func decodeCompressedFormat(compressedContents []byte, headerSize int) (rh *Histogram, err error) {
	b := bytes.NewReader(compressedContents)
	z, err := zlib.NewReader(b)
	if err != nil {
		return
	}
	defer func() {
		if closeErr := z.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	// Read the fixed-size header first, so the geometry is validated and the
	// inflated payload bounded before anything else is read. This bounds
	// decompression, not the counts array: a valid header for a wide geometry
	// (e.g. 1..MaxInt64 at 5 digits) still allocates countsLen*8 bytes.
	header := make([]byte, headerSize)
	if n, rerr := io.ReadFull(z, header); rerr != nil {
		if rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
			return nil, rerr
		}
		return nil, fmt.Errorf("decompressed histogram truncated: got %d bytes, need at least %d", n, headerSize)
	}
	cookie, PayloadLength, _, NumberOfSignificantValueDigits, LowestTrackableValue, HighestTrackableValue, _, err := decodeDeCompressedHeaderFormat(header)
	if err != nil {
		return
	}
	if cookie != V2EncodingCookieBase {
		err = fmt.Errorf("encoding not supported, only V2 is supported. got %d want %d", cookie, V2EncodingCookieBase)
		return
	}
	if PayloadLength < 0 {
		return nil, fmt.Errorf("negative PayloadLength: %d", PayloadLength)
	}
	if err = validateWireGeometry(LowestTrackableValue, HighestTrackableValue, NumberOfSignificantValueDigits); err != nil {
		return nil, fmt.Errorf("corrupt histogram header: %v", err)
	}
	if err = checkGeometry(LowestTrackableValue, int(NumberOfSignificantValueDigits)); err != nil {
		return nil, fmt.Errorf("corrupt histogram header: %v", err)
	}
	geometry := newGeometry(LowestTrackableValue, HighestTrackableValue, int(NumberOfSignificantValueDigits))
	// A valid payload holds at most one zig-zag LEB128 varint (<= 9 bytes) per
	// counts index.
	if maxPayload := int64(geometry.countsLen) * 9; int64(PayloadLength) > maxPayload {
		return nil, fmt.Errorf("PayloadLength %d exceeds the maximum %d for countsLen %d", PayloadLength, maxPayload, geometry.countsLen)
	}
	// Read one byte past PayloadLength so trailing data is detected without
	// inflating it; reaching EOF here also verifies the zlib checksum.
	payload, err := io.ReadAll(io.LimitReader(z, int64(PayloadLength)+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) != int64(PayloadLength) {
		return nil, fmt.Errorf("PayloadLength should have the same size of the actual payload. got %d want %d", len(payload), PayloadLength)
	}
	geometry.counts = make([]int64, geometry.countsLen)
	rh = geometry
	err = fillCountsArrayFromSourceBuffer(payload, rh)
	return rh, err
}

func fillCountsArrayFromSourceBuffer(payload []byte, rh *Histogram) (err error) {
	var payloadSlicePos = 0
	var dstIndex int64 = 0
	var n int
	var count int64
	var zerosCount int64
	for payloadSlicePos < len(payload) {
		count, n, err = zig_zag_decode_i64(payload[payloadSlicePos:])
		if err != nil {
			return
		}
		payloadSlicePos += n
		if count < 0 {
			// A zero-run must not advance the destination index past the counts
			// array; a corrupt payload could otherwise push it out of range before
			// the next positive write.
			zerosCount = -count
			// zerosCount <= 0 only when count == MinInt64, whose negation overflows.
			if zerosCount <= 0 || zerosCount > int64(len(rh.counts))-dstIndex {
				return fmt.Errorf("corrupt histogram payload: zero-run of %d at index %d overflows counts array of length %d", zerosCount, dstIndex, len(rh.counts))
			}
			dstIndex += zerosCount
		} else {
			// setCountAtIndex writes h.counts[dstIndex] unchecked (it is the record
			// hot path); the decode path validates the untrusted index here instead.
			if dstIndex >= int64(len(rh.counts)) {
				return fmt.Errorf("corrupt histogram payload: index %d overflows counts array of length %d", dstIndex, len(rh.counts))
			}
			rh.setCountAtIndex(int(dstIndex), count)
			dstIndex += 1
		}
	}
	return
}

func (h *Histogram) fillBufferFromCountsArray() (buffer []byte, err error) {
	buf := new(bytes.Buffer)
	// V2 encoding format uses a ZigZag LEB128-64b9B encoded long. Positive values are counts,
	// while negative values indicate a repeat zero counts.
	var countsLimit = int32(h.countsIndexFor(h.Max()) + 1)
	var srcIndex int32 = 0
	for srcIndex < countsLimit {
		count := h.counts[srcIndex]
		srcIndex++

		var zeros int64 = 0
		// check for contiguous zeros
		if count == 0 {
			zeros = 1
			for srcIndex < countsLimit && h.counts[srcIndex] == 0 {
				zeros++
				srcIndex++
			}
		}
		if zeros > 1 {
			err = binary.Write(buf, binary.BigEndian, zig_zag_encode_i64(-zeros))
			if err != nil {
				return
			}
		} else {
			err = binary.Write(buf, binary.BigEndian, zig_zag_encode_i64(count))
			if err != nil {
				return
			}
		}
	}
	buffer = buf.Bytes()
	return
}

func decodeDeCompressedHeaderFormat(decoded []byte) (Cookie int32, PayloadLength int32, NormalizingIndexOffSet int32, NumberOfSignificantValueDigits int32, LowestTrackableValue int64, HighestTrackableValue int64, IntegerToDoubleConversionRatio float64, err error) {
	rbuf := bytes.NewBuffer(decoded[0:40])
	r32 := make([]int32, 4)
	r64 := make([]int64, 2)
	err = binary.Read(rbuf, binary.BigEndian, &r32)
	if err != nil {
		return
	}
	err = binary.Read(rbuf, binary.BigEndian, &r64)
	if err != nil {
		return
	}
	err = binary.Read(rbuf, binary.BigEndian, &IntegerToDoubleConversionRatio)
	if err != nil {
		return
	}
	Cookie = r32[0] & ^0xf0
	PayloadLength = r32[1]
	NormalizingIndexOffSet = r32[2]
	NumberOfSignificantValueDigits = r32[3]
	LowestTrackableValue = r64[0]
	HighestTrackableValue = r64[1]
	return
}
