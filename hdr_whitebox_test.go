package hdrhistogram

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestHistogram_New_internals(t *testing.T) {
	// test for numberOfSignificantValueDigits if higher than 5 the numberOfSignificantValueDigits will be forced to 5
	hist := New(1, 9007199254740991, 6)
	assert.Equal(t, int64(5), hist.significantFigures)
	// test for numberOfSignificantValueDigits if lower than 1 the numberOfSignificantValueDigits will be forced to 1
	hist = New(1, 9007199254740991, 0)
	assert.Equal(t, int64(1), hist.significantFigures)
}
