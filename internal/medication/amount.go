package medication

import (
	"errors"
	"strconv"
	"strings"
)

// Amount is an exact, non-negative decimal with at most 4 fractional digits,
// stored as an integer number of ten-thousandths (500 mg → 5_000_000).
//
// It mirrors the NUMERIC(12,4) column: 8 integer digits, 4 fractional. Using a
// fixed-point integer instead of float64 means 0.1 is exactly 0.1, values
// compare with ==, and nothing is ever silently rounded — for a dosage,
// rounding is a correctness bug, so excess precision is rejected instead.
type Amount int64

const (
	amountScale      = 10_000
	amountFracDigits = 4
	amountIntDigits  = 8
	// MaxAmount is 99,999,999.9999, the largest value NUMERIC(12,4) holds.
	MaxAmount Amount = 99_999_999_9999
)

// Parse errors; their messages are shown to API clients as field errors.
var (
	ErrAmountSyntax    = errors.New("must be a positive decimal number, e.g. 500 or 2.5")
	ErrAmountPrecision = errors.New("must have at most 4 decimal places")
	ErrAmountRange     = errors.New("must be at most 99999999.9999")
)

// ParseAmount parses a plain decimal string such as "500", "2.5" or
// "500.0000". Signs, exponents and blanks are rejected. Trailing fractional
// zeros don't count towards precision ("0.50000" is 0.5).
func ParseAmount(s string) (Amount, error) {
	intPart, frac, hasDot := strings.Cut(s, ".")
	if !allDigits(intPart) || (hasDot && !allDigits(frac)) {
		return 0, ErrAmountSyntax
	}

	frac = strings.TrimRight(frac, "0")
	if len(frac) > amountFracDigits {
		return 0, ErrAmountPrecision
	}
	intPart = strings.TrimLeft(intPart, "0")
	if len(intPart) > amountIntDigits {
		return 0, ErrAmountRange
	}

	var whole, fraction int64
	if intPart != "" {
		whole, _ = strconv.ParseInt(intPart, 10, 64) // ≤ 8 digits: cannot fail
	}
	if frac != "" {
		fraction, _ = strconv.ParseInt(frac+strings.Repeat("0", amountFracDigits-len(frac)), 10, 64)
	}
	return Amount(whole*amountScale + fraction), nil
}

// String returns the canonical form: no trailing fractional zeros and no
// decimal point for whole numbers ("500", "2.5", "0.0001").
func (a Amount) String() string {
	whole := strconv.FormatInt(int64(a)/amountScale, 10)
	frac := int64(a) % amountScale
	if frac == 0 {
		return whole
	}
	f := strconv.FormatInt(frac, 10)
	f = strings.Repeat("0", amountFracDigits-len(f)) + f // left-pad: 5 → "0005"
	return whole + "." + strings.TrimRight(f, "0")
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
