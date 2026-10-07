package hdrhistogram

import "fmt"

// validateWireGeometry checks serialized arguments before the convenience
// constructors can normalize them. Changing the declared geometry changes the
// meaning of every count index in the payload.
// Precision zero is valid in the V2 format; support for that geometry is a
// separate concern from rejecting invalid header fields.
func validateWireGeometry(low, high int64, sig int32) error {
	if low < 1 {
		return fmt.Errorf("lowest discernible value must be at least 1, got %d", low)
	}
	if sig < 0 || sig > 5 {
		return fmt.Errorf("significant digits must be between 0 and 5, got %d", sig)
	}
	// Divide rather than multiplying low, which could overflow int64.
	if high < 2 || low > high/2 {
		return fmt.Errorf("highest trackable value %d must be at least twice lowest discernible value %d", high, low)
	}
	return nil
}
