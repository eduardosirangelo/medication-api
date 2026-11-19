package medication

import (
	"errors"
	"testing"
)

func TestParseNDC_Valid(t *testing.T) {
	tests := []struct {
		in, wantDigits, wantHyphenated string
	}{
		{"1234-5678-90", "01234567890", "01234-5678-90"},  // 4-4-2: pad labeler
		{"12345-678-90", "12345067890", "12345-0678-90"},  // 5-3-2: pad product
		{"12345-6789-0", "12345678900", "12345-6789-00"},  // 5-4-1: pad package
		{"12345-6789-01", "12345678901", "12345-6789-01"}, // 5-4-2 already 11
		{"12345678901", "12345678901", "12345-6789-01"},   // plain 11
		{" 0071-0155-23 ", "00071015523", "00071-0155-23"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseNDC(tt.in)
			if err != nil {
				t.Fatalf("ParseNDC: %v", err)
			}
			if string(got) != tt.wantDigits || got.Hyphenated() != tt.wantHyphenated {
				t.Errorf("got %s (%s), want %s (%s)", got, got.Hyphenated(), tt.wantDigits, tt.wantHyphenated)
			}
		})
	}
}

// Every 10-digit layout of the same package must normalize to one value.
func TestParseNDC_LayoutsOfSamePackageAreEqual(t *testing.T) {
	a, _ := ParseNDC("0071-0155-23")
	b, _ := ParseNDC("00071-0155-23")
	c, _ := ParseNDC("00071015523")
	if a != b || b != c {
		t.Fatalf("not equal: %s %s %s", a, b, c)
	}
}

func TestParseNDC_Invalid(t *testing.T) {
	for _, in := range []string{
		"",
		"1234567890",     // 10 digits unhyphenated: ambiguous
		"123456789012",   // 12 digits
		"1234-567-89",    // 4-3-2
		"123456-789-01",  // 6-3-2
		"12345-6789-012", // 5-4-3
		"12345-6789",     // two segments
		"1234-5678-9a",
		"12345 6789 01",
		"12345--678901",
	} {
		if _, err := ParseNDC(in); !errors.Is(err, ErrNDCFormat) {
			t.Errorf("ParseNDC(%q) err = %v, want ErrNDCFormat", in, err)
		}
	}
}

func TestParseRxCUI(t *testing.T) {
	for _, in := range []string{"161", "198440", "1234567890"} {
		if got, err := ParseRxCUI(in); err != nil || string(got) != in {
			t.Errorf("ParseRxCUI(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "12345678901", "RX161", "-1", "1.5"} {
		if _, err := ParseRxCUI(in); !errors.Is(err, ErrRxCUIFormat) {
			t.Errorf("ParseRxCUI(%q) err = %v", in, err)
		}
	}
}

func TestParseRoute(t *testing.T) {
	for _, r := range Routes {
		if got, ok := ParseRoute(string(r)); !ok || got != r {
			t.Errorf("route %q does not round-trip", r)
		}
	}
	if got, ok := ParseRoute(" Oral "); !ok || got != RouteOral {
		t.Errorf("case/space-insensitive parse failed: %q %v", got, ok)
	}
	for _, in := range []string{"", "by mouth", "iv", "orally"} {
		if _, ok := ParseRoute(in); ok {
			t.Errorf("ParseRoute(%q) should fail", in)
		}
	}
}
