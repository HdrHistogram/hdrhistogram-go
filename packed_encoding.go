package hdrhistogram

// V2-compressed serialization for PackedHistogram. The output is byte-identical
// to Histogram.Encode(V2CompressedEncodingCookieBase) on an equivalent dense
// histogram, and DecodePacked accepts streams produced by either encoder, so the
// sparse variant is wire-compatible with every existing HdrHistogram V2 reader.
// The payload is streamed directly from the sparse backing (no dense array is
// ever materialized).

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// fillBufferFromSparse produces the V2 payload (zero-run RLE + ZigZag LEB128)
// identical to Histogram.fillBufferFromCountsArray, walking the flat index space
// [0, countsIndexFor(Max())+1) with a cursor over the populated buckets.
func (p *PackedHistogram) fillBufferFromSparse() []byte {
	buf := new(bytes.Buffer)
	countsLimit := int32(p.geom.countsIndexFor(p.Max()) + 1)
	j := int32(0) // cursor into idx[]; src is monotonic so j only advances
	countAt := func(src int32) int64 {
		for j < p.size && p.idx[j] < src {
			j++
		}
		if j < p.size && p.idx[j] == src {
			return p.slotGet(j)
		}
		return 0
	}
	var src int32
	for src < countsLimit {
		count := countAt(src)
		src++
		var zeros int64
		if count == 0 {
			zeros = 1
			for src < countsLimit && countAt(src) == 0 {
				zeros++
				src++
			}
		}
		if zeros > 1 {
			buf.Write(zig_zag_encode_i64(-zeros))
		} else {
			buf.Write(zig_zag_encode_i64(count))
		}
	}
	return buf.Bytes()
}

// Encode returns the standard V2 compressed (base64) representation, byte-for-byte
// identical to the dense encoder on equivalent data.
func (p *PackedHistogram) Encode() ([]byte, error) {
	payload := p.fillBufferFromSparse()

	// Build the fixed 40-byte header directly (writes to a byte slice cannot
	// fail, so there is no error plumbing here). Byte-for-byte the layout
	// produced by the dense encoder.
	inner := make([]byte, ENCODING_HEADER_SIZE, ENCODING_HEADER_SIZE+len(payload))
	binary.BigEndian.PutUint32(inner[0:], uint32(encodingCookie))                    // 0-3
	binary.BigEndian.PutUint32(inner[4:], uint32(len(payload)))                      // 4-7
	binary.BigEndian.PutUint32(inner[8:], 0)                                         // 8-11 normalizingIndexOffset
	binary.BigEndian.PutUint32(inner[12:], uint32(int32(p.geom.significantFigures))) // 12-15
	binary.BigEndian.PutUint64(inner[16:], uint64(p.geom.lowestDiscernibleValue))    // 16-23
	binary.BigEndian.PutUint64(inner[24:], uint64(p.geom.highestTrackableValue))     // 24-31
	binary.BigEndian.PutUint64(inner[32:], math.Float64bits(1.0))                    // 32-39 conversion ratio
	inner = append(inner, payload...)

	var zb bytes.Buffer
	// NewWriterLevel only errors on an invalid level; BestCompression is valid.
	w, _ := zlib.NewWriterLevel(&zb, zlib.BestCompression)
	// Writes/Close to an in-memory bytes.Buffer never fail.
	_, _ = w.Write(inner)
	_ = w.Close()
	compressed := zb.Bytes()

	out := make([]byte, 8+len(compressed))
	binary.BigEndian.PutUint32(out[0:], uint32(compressedEncodingCookie))
	binary.BigEndian.PutUint32(out[4:], uint32(len(compressed)))
	copy(out[8:], compressed)
	return []byte(base64.StdEncoding.EncodeToString(out)), nil
}

// DecodePacked decodes a standard V2 compressed (base64) stream into a new
// PackedHistogram. It rejects a non-zero normalizingIndexOffset (packed
// histograms are never rotated) rather than mis-indexing it.
func DecodePacked(encoded []byte) (*PackedHistogram, error) {
	decoded, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		return nil, err
	}
	if len(decoded) < 8 {
		return nil, fmt.Errorf("encoded histogram too short: got %d bytes, need at least 8", len(decoded))
	}
	cookie := int32(binary.BigEndian.Uint32(decoded[0:4])) & ^0xf0
	clen := int32(binary.BigEndian.Uint32(decoded[4:8]))
	if cookie != V2CompressedEncodingCookieBase {
		return nil, fmt.Errorf("encoding not supported, only V2 is supported. got %d want %d", cookie, V2CompressedEncodingCookieBase)
	}
	if clen < 0 {
		return nil, fmt.Errorf("negative lengthOfCompressedContents: %d", clen)
	}
	if clen > int32(len(decoded[8:])) {
		return nil, fmt.Errorf("the compressed contents buffer is smaller than the lengthOfCompressedContents. got %d want %d", len(decoded[8:]), clen)
	}
	return decodePackedCompressed(decoded[8 : 8+clen])
}

func decodePackedCompressed(compressed []byte) (rp *PackedHistogram, err error) {
	z, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer func() {
		// Defensive: a zlib reader Close only surfaces an error the successful
		// reads below did not already return, so this assignment is effectively
		// unreachable in practice. Kept for correctness.
		if cerr := z.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	// Read the fixed header first so the payload can be bounded by the geometry
	// before it is inflated: a tiny compressed stream must not be able to force
	// an arbitrarily large allocation.
	hdr := make([]byte, ENCODING_HEADER_SIZE)
	if n, rerr := io.ReadFull(z, hdr); rerr != nil {
		if rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
			return nil, rerr
		}
		return nil, fmt.Errorf("decompressed histogram truncated: got %d bytes, need at least %d", n, ENCODING_HEADER_SIZE)
	}
	cookie, payloadLen, normOff, sig, low, high, _, err := decodeDeCompressedHeaderFormat(hdr)
	if err != nil {
		// Defensive: decodeDeCompressedHeaderFormat reads exactly the 40 bytes
		// guaranteed present by the ReadFull above, so binary.Read cannot
		// fail here. Kept for correctness / parity with the dense decoder.
		return nil, err
	}
	if cookie != V2EncodingCookieBase {
		return nil, fmt.Errorf("encoding not supported, only V2 is supported. got %d want %d", cookie, V2EncodingCookieBase)
	}
	if normOff != 0 {
		return nil, fmt.Errorf("packed decode: non-zero normalizingIndexOffset %d is not supported", normOff)
	}
	if payloadLen < 0 {
		return nil, fmt.Errorf("negative PayloadLength: %d", payloadLen)
	}
	if err = checkGeometry(low, int(sig)); err != nil {
		return nil, fmt.Errorf("corrupt histogram header: %v", err)
	}
	rp = NewPacked(low, high, int(sig))
	// A valid payload holds at most one zig-zag LEB128 varint (<= 9 bytes)
	// per counts index.
	if maxPayload := int64(rp.geom.countsLen) * 9; int64(payloadLen) > maxPayload {
		return nil, fmt.Errorf("PayloadLength %d exceeds the maximum %d for countsLen %d", payloadLen, maxPayload, rp.geom.countsLen)
	}
	// Read one byte past payloadLen so trailing data is detected without
	// inflating it; reaching EOF here also verifies the zlib checksum.
	dec, err := io.ReadAll(io.LimitReader(z, int64(payloadLen)+1))
	if err != nil {
		return nil, err
	}
	if int64(len(dec)) != int64(payloadLen) {
		return nil, fmt.Errorf("PayloadLength should have the same size of the actual payload. got %d want %d", len(dec), payloadLen)
	}
	if err = fillSparseFromPayload(dec, rp); err != nil {
		return nil, err
	}
	return rp, nil
}

// fillSparseFromPayload inflates a V2 payload into the sparse backing. dstIndex
// is monotonically increasing, so every sparseAdd appends at the end.
func fillSparseFromPayload(payload []byte, p *PackedHistogram) error {
	countsLen := int64(p.geom.countsLen)
	var dstIndex, total int64
	pos := 0
	for pos < len(payload) {
		count, n, err := zig_zag_decode_i64(payload[pos:])
		if err != nil {
			return err
		}
		pos += n
		if count < 0 {
			zeros := -count
			// zeros <= 0 only when count == MinInt64, whose negation overflows.
			if zeros <= 0 || zeros > countsLen-dstIndex {
				return fmt.Errorf("corrupt histogram payload: zero-run of %d at index %d overflows counts length %d", zeros, dstIndex, countsLen)
			}
			dstIndex += zeros
		} else {
			if dstIndex >= countsLen {
				return fmt.Errorf("corrupt histogram payload: index %d overflows counts length %d", dstIndex, countsLen)
			}
			if count > 0 {
				p.sparseAdd(int32(dstIndex), count)
				if count > math.MaxInt64-total {
					total = math.MaxInt64
				} else {
					total += count
				}
			}
			dstIndex++
		}
	}
	p.totalCount = total
	return nil
}
