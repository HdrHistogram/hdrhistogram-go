package hdrhistogram

import "fmt"

// validateWireRange checks the serialized range before geometry construction.
// wireGeometry separately validates precision and builds the exact geometry.
func validateWireRange(low, high int64) error {
	if low < 1 {
		// Released v1.0.0 streams used these headers with unit magnitude 0.
		// Match wireGeometry's legacy interpretation before checking the range.
		low = 1
	}
	// Divide rather than multiplying low, which could overflow int64.
	if high < 2 || low > high/2 {
		return fmt.Errorf("highest trackable value %d must be at least twice lowest discernible value %d", high, low)
	}
	return nil
}
