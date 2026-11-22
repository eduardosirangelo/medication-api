package medication

import (
	"errors"
	"strconv"
	"strings"
)

// Amount is an exact, positive decimal with at most 4 fractional digits, held
// as an integer count of ten-thousandths (500 mg → 5_000_000). It mirrors
// NUMERIC(12,4). Zero is the unset value, which validation rejects.
//
// A fixed-point integer instead of float64 keeps 0.1 exact and makes amounts
// comparable with ==, which duplicate detection relies on. Excess precision
// is rejected, never rounded: silently rounding a dose is a correctness bug.
type Amount int64

const (
	amountScale      = 10_000
	amountFracDigits = 4
	amountIntDigits  = 8

	MaxAmount Amount = 99_999_999_9999
)

// The messages are shown to API clients as field errors.
var (
	ErrAmountSyntax    = errors.New("must be a positive decimal number, e.g. 500 or 2.5")
	ErrAmountPrecision = errors.New("must have at most 4 decimal places")
	ErrAmountRange     = errors.New("must be at most 99999999.9999")
)

func ParseAmount(s string) (Amount, error) {
	intPart, frac, hasDot := strings.Cut(s, ".")
	if !allDigits(intPart) || (hasDot && !allDigits(frac)) {
		return 0, ErrAmountSyntax
	}

	// Trailing zeros carry no precision: "0.50000" is 0.5.
	frac = strings.TrimRight(frac, "0")
	if len(frac) > amountFracDigits {
		return 0, ErrAmountPrecision
	}
	// Checked before ParseInt so oversized input can't overflow int64.
	intPart = strings.TrimLeft(intPart, "0")
	if len(intPart) > amountIntDigits {
		return 0, ErrAmountRange
	}

	return Amount(parseDigits(intPart)*amountScale + parseDigits(padRight(frac, amountFracDigits))), nil
}

func (a Amount) String() string {
	whole := strconv.FormatInt(int64(a)/amountScale, 10)
	frac := int64(a) % amountScale
	if frac == 0 {
		return whole
	}
	f := padLeft(strconv.FormatInt(frac, 10), amountFracDigits) // 5 → "0005" so 0.0005 prints right
	return whole + "." + strings.TrimRight(f, "0")
}

func parseDigits(s string) int64 {
	if s == "" {
		return 0
	}
	n, _ := strconv.ParseInt(s, 10, 64) // callers guarantee ≤ 8 digits
	return n
}

func padLeft(s string, width int) string  { return strings.Repeat("0", width-len(s)) + s }
func padRight(s string, width int) string { return s + strings.Repeat("0", width-len(s)) }

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
