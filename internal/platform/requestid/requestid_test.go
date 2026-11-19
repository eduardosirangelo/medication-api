package requestid

import (
	"strings"
	"testing"
)

func TestNew_IsValidAndUnique(t *testing.T) {
	a, b := New(), New()
	if !Valid(a) || !Valid(b) {
		t.Fatalf("generated IDs must be valid: %q %q", a, b)
	}
	if a == b {
		t.Fatal("generated IDs must differ")
	}
}

func TestValid(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"abc-123_DEF.456:7", true},
		{"0f8fad5b-d9cb-469f-a165-70867728950e", true},
		{strings.Repeat("a", 128), true},
		{"", false},
		{strings.Repeat("a", 129), false},
		{"has space", false},
		{"new\nline", false},
		{`quote"`, false},
		{"ünicode", false},
	}
	for _, tt := range tests {
		if got := Valid(tt.id); got != tt.want {
			t.Errorf("Valid(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestContextRoundTrip(t *testing.T) {
	if got := From(t.Context()); got != "" {
		t.Fatalf("empty ctx: got %q", got)
	}
	if got := From(With(t.Context(), "x1")); got != "x1" {
		t.Fatalf("got %q, want x1", got)
	}
}
