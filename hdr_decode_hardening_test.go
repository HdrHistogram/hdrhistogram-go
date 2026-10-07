package hdrhistogram

import (
	"math"
	"runtime"
	"strings"
	"testing"
	"time"
)

// largestValidLow returns the largest lowestDiscernibleValue checkGeometry
// accepts for sig significant digits.
func largestValidLow(sig int) int64 {
	_, half := geometryMagnitudes(1, sig)
	return int64(1) << uint(61-half)
}

// withinDeadline fails the test if fn does not return within d (the bucket
// computation used to loop forever on unrepresentable geometries).
func withinDeadline(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %v", what, d)
	}
}

func TestCheckGeometryBoundary(t *testing.T) {
	for sig := 1; sig <= 5; sig++ {
		low := largestValidLow(sig)
		if err := checkGeometry(low, sig); err != nil {
			t.Fatalf("sig %d: largest valid low %d rejected: %v", sig, low, err)
		}
		if err := checkGeometry(low*2, sig); err == nil {
			t.Fatalf("sig %d: low %d accepted, but its smallest untrackable value overflows", sig, low*2)
		}
	}
	// Out-of-range digits are clamped exactly as New clamps them.
	if err := checkGeometry(largestValidLow(5)*2, 9); err == nil {
		t.Fatal("sig 9 should be checked as 5")
	}
	if err := checkGeometry(math.MinInt64, 0); err != nil {
		t.Fatalf("low < 1 is clamped to 1 and must be accepted: %v", err)
	}
}

func TestNewPanicsInsteadOfHangingOnUnrepresentableGeometry(t *testing.T) {
	for sig := 1; sig <= 5; sig++ {
		low := largestValidLow(sig)
		withinDeadline(t, 5*time.Second, "New at the boundary", func() {
			h := New(low, math.MaxInt64, sig)
			if err := h.RecordValue(math.MaxInt64 / 2); err != nil {
				t.Errorf("sig %d: record at the boundary geometry: %v", sig, err)
			}
		})
		var recovered interface{}
		withinDeadline(t, 5*time.Second, "New past the boundary", func() {
			defer func() { recovered = recover() }()
			New(low*2, math.MaxInt64, sig)
		})
		if msg, _ := recovered.(string); !strings.Contains(msg, "too large") {
			t.Fatalf("sig %d: New(%d, ...) recovered %v, want a 'too large' panic", sig, low*2, recovered)
		}
	}
}

// Both decoders must reject an unrepresentable geometry in the header instead
// of hanging (dense used to loop forever, as did DecodePacked).
func TestDecodeRejectsUnrepresentableGeometry(t *testing.T) {
	for sig := int32(1); sig <= 5; sig++ {
		bad := largestValidLow(int(sig)) * 2
		enc := buildPackedV2Stream(bad, math.MaxInt64, sig, 0, 0, 0, nil)
		withinDeadline(t, 5*time.Second, "Decode", func() {
			if _, err := Decode(enc); err == nil || !strings.Contains(err.Error(), "too large") {
				t.Errorf("sig %d: Decode err = %v, want geometry rejection", sig, err)
			}
		})
		withinDeadline(t, 5*time.Second, "DecodePacked", func() {
			if _, err := DecodePacked(enc); err == nil || !strings.Contains(err.Error(), "too large") {
				t.Errorf("sig %d: DecodePacked err = %v, want geometry rejection", sig, err)
			}
		})
		// The largest valid geometry still round-trips through both decoders.
		ok := buildPackedV2Stream(largestValidLow(int(sig)), math.MaxInt64, sig, 0, 0, 0, nil)
		if _, err := Decode(ok); err != nil {
			t.Fatalf("sig %d: Decode of the largest valid geometry: %v", sig, err)
		}
		if _, err := DecodePacked(ok); err != nil {
			t.Fatalf("sig %d: DecodePacked of the largest valid geometry: %v", sig, err)
		}
	}
}

// A MinInt64 zero-run used to panic the dense decoder with an index out of range.
func TestDecodeRejectsMinInt64ZeroRun(t *testing.T) {
	minRun := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	payload := append([]byte{0x14, 0x14}, minRun...)
	payload = append(payload, 0x0a, 0x0a, 0x0a)
	enc := buildPackedV2Stream(1, 3600000000, 3, int32(len(payload)), 0, 0, payload)
	if _, err := Decode(enc); err == nil {
		t.Fatal("Decode accepted a MinInt64 zero-run")
	}
}

// The dense decoder now bounds the payload by the geometry before inflating it.
func TestDecodeBoundsDecompression(t *testing.T) {
	// 1 MB of zero bytes compresses to ~1 KB, yet is well above this geometry's
	// maximum payload (23552 counts * 9 bytes, about 212 KB).
	const bomb = 1 << 20
	zeros := make([]byte, bomb)
	alloc := func(enc []byte) (uint64, error) {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, err := Decode(enc)
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc, err
	}

	enc := buildPackedV2Stream(1, 3600000000, 3, bomb, 0, 0, zeros)
	got, err := alloc(enc)
	if err == nil || !strings.Contains(err.Error(), "exceeds the maximum") {
		t.Fatalf("err = %v, want PayloadLength rejection", err)
	}
	if got > 512<<10 {
		t.Fatalf("Decode allocated %d bytes before rejecting a %d-byte stream", got, len(enc))
	}

	enc = buildPackedV2Stream(1, 3600000000, 3, 4, 0, 0, zeros)
	got, err = alloc(enc)
	if err == nil || !strings.Contains(err.Error(), "PayloadLength should have the same size") {
		t.Fatalf("err = %v, want payload length mismatch", err)
	}
	if got > 512<<10 {
		t.Fatalf("Decode allocated %d bytes before rejecting a %d-byte stream", got, len(enc))
	}

	if _, err := Decode(buildPackedV2Stream(1, 3600000000, 3, -1, 0, 0, nil)); err == nil {
		t.Fatal("Decode accepted a negative PayloadLength")
	}
}
