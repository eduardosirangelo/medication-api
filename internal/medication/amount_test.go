package medication

import (
	"errors"
	"testing"
)

func TestParseAmount_Valid(t *testing.T) {
	tests := []struct {
		in        string
		want      Amount
		canonical string
	}{
		{"500", 500_0000, "500"},
		{"500.0", 500_0000, "500"},
		{"500.0000", 500_0000, "500"}, // as returned by NUMERIC(12,4)
		{"2.5", 2_5000, "2.5"},
		{"0.1", 1000, "0.1"},
		{"0.0001", 1, "0.0001"},
		{"0.50000", 5000, "0.5"}, // trailing zeros don't count as precision
		{"0", 0, "0"},
		{"007", 7_0000, "7"},
		{"12.3456", 12_3456, "12.3456"},
		{"99999999.9999", MaxAmount, "99999999.9999"},
		{"0000000099999999", 99_999_999_0000, "99999999"}, // leading zeros don't count as digits
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseAmount(tt.in)
			if err != nil {
				t.Fatalf("ParseAmount(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseAmount(%q) = %d, want %d", tt.in, got, tt.want)
			}
			if s := got.String(); s != tt.canonical {
				t.Errorf("String() = %q, want %q", s, tt.canonical)
			}
		})
	}
}

func TestParseAmount_Invalid(t *testing.T) {
	tests := []struct {
		in   string
		want error
	}{
		{"", ErrAmountSyntax},
		{"-1", ErrAmountSyntax},
		{"+1", ErrAmountSyntax},
		{"1e3", ErrAmountSyntax},
		{"1.", ErrAmountSyntax},
		{".5", ErrAmountSyntax},
		{"1.2.3", ErrAmountSyntax},
		{"1,5", ErrAmountSyntax},
		{" 1", ErrAmountSyntax},
		{"abc", ErrAmountSyntax},
		{"NaN", ErrAmountSyntax},
		{"0.00001", ErrAmountPrecision},
		{"1.23456", ErrAmountPrecision},
		{"100000000", ErrAmountRange},
		{"123456789012345678901234567890", ErrAmountRange}, // no int64 overflow
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			_, err := ParseAmount(tt.in)
			if !errors.Is(err, tt.want) {
				t.Errorf("ParseAmount(%q) err = %v, want %v", tt.in, err, tt.want)
			}
		})
	}
}

// Every value String produces must parse back to itself.
func FuzzAmountRoundTrip(f *testing.F) {
	for _, s := range []string{"0", "1", "2.5", "0.0001", "99999999.9999", "1.23456", "-1", "1e3"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		a, err := ParseAmount(s)
		if err != nil {
			return
		}
		if a < 0 || a > MaxAmount {
			t.Fatalf("ParseAmount(%q) = %d out of range", s, a)
		}
		back, err := ParseAmount(a.String())
		if err != nil || back != a {
			t.Fatalf("round trip %q → %d → %q → %d (%v)", s, a, a.String(), back, err)
		}
	})
}

// ParseAmount runs on every dosage field of every write; it should not allocate.
func BenchmarkParseAmount(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ParseAmount("1234.5678"); err != nil {
			b.Fatal(err)
		}
	}
}
