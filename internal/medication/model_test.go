package medication

import (
	"testing"
	"time"
)

func TestParseForm(t *testing.T) {
	tests := []struct {
		in   string
		want Form
		ok   bool
	}{
		{"tablet", FormTablet, true},
		{"Tablet", FormTablet, true}, // spec spelling
		{" CAPSULE ", FormCapsule, true},
		{"liquid", FormLiquid, true},
		{"syrup", "", false},
		{"", "", false},
		{"tablets", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseForm(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseForm(%q) = %q,%v want %q,%v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseUnit(t *testing.T) {
	tests := []struct {
		in   string
		want Unit
		ok   bool
	}{
		{"mg", UnitMg, true},
		{"MG", UnitMg, true},
		{"mcg", UnitMcg, true},
		{"ug", UnitMcg, true},
		{"µg", UnitMcg, true}, // micro sign
		{"μg", UnitMcg, true}, // Greek mu
		{"g", UnitG, true},
		{"ml", UnitML, true},
		{"mL", UnitML, true},
		{"ML", UnitML, true},
		{"iu", UnitIU, true},
		{"%", UnitPercent, true},
		{"kg", "", false},
		{"mg/ml", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseUnit(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseUnit(%q) = %q,%v want %q,%v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// Every documented value must parse to itself (catches a constant added to
// Units/Forms but forgotten in the parser, or vice versa).
func TestCanonicalValuesRoundTrip(t *testing.T) {
	for _, u := range Units {
		if got, ok := ParseUnit(string(u)); !ok || got != u {
			t.Errorf("unit %q does not round-trip: %q,%v", u, got, ok)
		}
	}
	for _, f := range Forms {
		if got, ok := ParseForm(string(f)); !ok || got != f {
			t.Errorf("form %q does not round-trip: %q,%v", f, got, ok)
		}
	}
}

func TestDosageString(t *testing.T) {
	plain := Dosage{Value: 500_0000, Unit: UnitMg}
	if s := plain.String(); s != "500 mg" {
		t.Errorf("got %q", s)
	}
	conc := Dosage{Value: 250_0000, Unit: UnitMg, Per: &Quantity{Value: 5_0000, Unit: UnitML}}
	if s := conc.String(); s != "250 mg/5 mL" {
		t.Errorf("got %q", s)
	}
}

func TestNewID_IsTimeOrderedV7(t *testing.T) {
	a, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // v7 ordering is by millisecond timestamp
	b, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if a.Version() != 7 || b.Version() != 7 {
		t.Fatalf("versions = %d, %d; want 7", a.Version(), b.Version())
	}
	if a.String() >= b.String() {
		t.Errorf("IDs not time-ordered: %s >= %s", a, b)
	}
}
