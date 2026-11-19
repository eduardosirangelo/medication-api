package medication

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func validInput() Input {
	return Input{
		Name:   "Paracetamol",
		Form:   "tablet",
		Dosage: DosageInput{Value: "500", Unit: "mg"},
	}
}

func TestNewSpec_Valid(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*Input)
		want   Spec
	}{
		{
			"plain dosage",
			func(*Input) {},
			Spec{Name: "Paracetamol", Form: FormTablet, Dosage: Dosage{Value: 500_0000, Unit: UnitMg}},
		},
		{
			"concentration",
			func(in *Input) {
				in.Name, in.Form = "Amoxicillin", "liquid"
				in.Dosage = DosageInput{Value: "250", Unit: "mg", PerValue: "5", PerUnit: "mL"}
			},
			Spec{Name: "Amoxicillin", Form: FormLiquid, Dosage: Dosage{
				Value: 250_0000, Unit: UnitMg, Per: &Quantity{Value: 5_0000, Unit: UnitML},
			}},
		},
		{
			"normalises name, form, units and amounts",
			func(in *Input) {
				in.Name = "  Dipirona \t  Sódica\n"
				in.Form = "Liquid"
				in.Dosage = DosageInput{Value: "500.00", Unit: "MG", PerValue: " 1 ", PerUnit: "ml"}
			},
			Spec{Name: "Dipirona Sódica", Form: FormLiquid, Dosage: Dosage{
				Value: 500_0000, Unit: UnitMg, Per: &Quantity{Value: 1_0000, Unit: UnitML},
			}},
		},
		{
			"composes decomposed accents (NFC)",
			func(in *Input) { in.Name = "Sódica" }, // o + combining acute
			Spec{Name: "Sódica", Form: FormTablet, Dosage: Dosage{Value: 500_0000, Unit: UnitMg}},
		},
		{
			"preserves letter case of name",
			func(in *Input) { in.Name = "iBUprofen" },
			Spec{Name: "iBUprofen", Form: FormTablet, Dosage: Dosage{Value: 500_0000, Unit: UnitMg}},
		},
		{
			"name at max length",
			func(in *Input) { in.Name = strings.Repeat("é", MaxNameLength) }, // runes, not bytes
			Spec{Name: strings.Repeat("é", MaxNameLength), Form: FormTablet, Dosage: Dosage{Value: 500_0000, Unit: UnitMg}},
		},
		{
			"identifiers normalized",
			func(in *Input) { in.NDC, in.RxCUI, in.Route = "0071-0155-23", " 198440 ", "Oral" },
			Spec{
				Name: "Paracetamol", Form: FormTablet, Dosage: Dosage{Value: 500_0000, Unit: UnitMg},
				NDC: "00071015523", RxCUI: "198440", Route: RouteOral,
			},
		},
		{
			"blank identifiers are absent",
			func(in *Input) { in.NDC, in.RxCUI, in.Route = " ", "", "\t" },
			Spec{Name: "Paracetamol", Form: FormTablet, Dosage: Dosage{Value: 500_0000, Unit: UnitMg}},
		},
		{
			"percentage without per",
			func(in *Input) { in.Dosage = DosageInput{Value: "0.5", Unit: "%"} },
			Spec{Name: "Paracetamol", Form: FormTablet, Dosage: Dosage{Value: 5000, Unit: UnitPercent}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validInput()
			tt.modify(&in)
			got, err := NewSpec(in)
			if err != nil {
				t.Fatalf("NewSpec: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v (%s)\nwant %+v (%s)", got, got.Dosage, tt.want, tt.want.Dosage)
			}
		})
	}
}

func TestNewSpec_Invalid(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*Input)
		want   []FieldError // exact, in order
	}{
		{"empty name", func(in *Input) { in.Name = "" },
			[]FieldError{{"name", "is required"}}},
		{"whitespace-only name", func(in *Input) { in.Name = " \t\n " },
			[]FieldError{{"name", "is required"}}},
		{"name too long", func(in *Input) { in.Name = strings.Repeat("a", MaxNameLength+1) },
			[]FieldError{{"name", "must be at most 200 characters"}}},
		{"invalid UTF-8 in name", func(in *Input) { in.Name = "Para\xffcetamol" },
			[]FieldError{{"name", "must be valid UTF-8"}}},
		{"control character in name", func(in *Input) { in.Name = "Para\x00cetamol" },
			[]FieldError{{"name", "must not contain control characters"}}},
		{"missing form", func(in *Input) { in.Form = "" },
			[]FieldError{{"form", "is required"}}},
		{"unknown form", func(in *Input) { in.Form = "syrup" },
			[]FieldError{{"form", "must be one of: tablet, capsule, liquid"}}},
		{"missing value", func(in *Input) { in.Dosage.Value = "" },
			[]FieldError{{"dosage.value", "is required"}}},
		{"zero value", func(in *Input) { in.Dosage.Value = "0" },
			[]FieldError{{"dosage.value", "must be greater than 0"}}},
		{"negative value", func(in *Input) { in.Dosage.Value = "-5" },
			[]FieldError{{"dosage.value", ErrAmountSyntax.Error()}}},
		{"too precise", func(in *Input) { in.Dosage.Value = "0.12345" },
			[]FieldError{{"dosage.value", ErrAmountPrecision.Error()}}},
		{"too large", func(in *Input) { in.Dosage.Value = "100000000" },
			[]FieldError{{"dosage.value", ErrAmountRange.Error()}}},
		{"missing unit", func(in *Input) { in.Dosage.Unit = "" },
			[]FieldError{{"dosage.unit", "is required"}}},
		{"unknown unit", func(in *Input) { in.Dosage.Unit = "kg" },
			[]FieldError{{"dosage.unit", "must be one of: mg, mcg, g, mL, IU, %"}}},
		{"per_value without per_unit", func(in *Input) { in.Dosage.PerValue = "5" },
			[]FieldError{{"dosage.per_unit", "is required when dosage.per_value is set"}}},
		{"per_unit without per_value", func(in *Input) { in.Dosage.PerUnit = "mL" },
			[]FieldError{{"dosage.per_value", "is required when dosage.per_unit is set"}}},
		{"zero per_value", func(in *Input) { in.Dosage.PerValue, in.Dosage.PerUnit = "0", "mL" },
			[]FieldError{{"dosage.per_value", "must be greater than 0"}}},
		{"same unit both sides", func(in *Input) { in.Dosage.PerValue, in.Dosage.PerUnit = "5", "MG" },
			[]FieldError{{"dosage.per_unit", "must differ from dosage.unit"}}},
		{"percent with per", func(in *Input) {
			in.Dosage = DosageInput{Value: "1", Unit: "%", PerValue: "5", PerUnit: "mL"}
		}, []FieldError{{"dosage.per_unit", "cannot be combined with %: a percentage is already a concentration"}}},
		{"per_unit is percent", func(in *Input) { in.Dosage.PerValue, in.Dosage.PerUnit = "5", "%" },
			[]FieldError{{"dosage.per_unit", "cannot be combined with %: a percentage is already a concentration"}}},
		{"bad per_unit skips cross-field rules", func(in *Input) { in.Dosage.PerValue, in.Dosage.PerUnit = "5", "cup" },
			[]FieldError{{"dosage.per_unit", "must be one of: mg, mcg, g, mL, IU, %"}}},
		{"bad ndc", func(in *Input) { in.NDC = "1234567890" },
			[]FieldError{{"ndc", ErrNDCFormat.Error()}}},
		{"bad rxcui", func(in *Input) { in.RxCUI = "RX161" },
			[]FieldError{{"rxcui", ErrRxCUIFormat.Error()}}},
		{"bad route", func(in *Input) { in.Route = "by mouth" },
			[]FieldError{{"route", "must be one of: " + routeList}}},
		{
			"every error reported at once",
			func(in *Input) { *in = Input{Dosage: DosageInput{Value: "abc", Unit: "kg", PerValue: "5"}} },
			[]FieldError{
				{"name", "is required"},
				{"form", "is required"},
				{"dosage.value", ErrAmountSyntax.Error()},
				{"dosage.unit", "must be one of: mg, mcg, g, mL, IU, %"},
				{"dosage.per_unit", "is required when dosage.per_value is set"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validInput()
			tt.modify(&in)
			spec, err := NewSpec(in)

			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("want *ValidationError, got %v", err)
			}
			if !reflect.DeepEqual(verr.Fields, tt.want) {
				t.Errorf("fields:\n got  %+v\n want %+v", verr.Fields, tt.want)
			}
			if !reflect.DeepEqual(spec, Spec{}) {
				t.Errorf("invalid input must return zero Spec, got %+v", spec)
			}
		})
	}
}

func TestValidationError_Message(t *testing.T) {
	err := &ValidationError{Fields: []FieldError{{"name", "is required"}, {"form", "is required"}}}
	if got := err.Error(); got != "invalid medication: name: is required; form: is required" {
		t.Errorf("Error() = %q", got)
	}
}

func TestNormalizeName(t *testing.T) {
	tests := map[string]string{
		"  a  b  ":         "a b",
		"a b":              "a b", // NBSP is whitespace
		"é":               "é",
		"Paracetamol 500":  "Paracetamol 500",
		"\t\n":             "",
		"Ibuprofeno  Gel ": "Ibuprofeno Gel",
	}
	for in, want := range tests {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Domain errors must stay distinguishable after wrapping (the HTTP layer
// relies on errors.Is through service/repository wrapping).
func TestDomainErrorsSurviveWrapping(t *testing.T) {
	for _, sentinel := range []error{ErrNotFound, ErrDuplicate, ErrVersionMismatch} {
		wrapped := errors.Join(errors.New("context"), sentinel)
		if !errors.Is(wrapped, sentinel) {
			t.Errorf("%v lost through wrapping", sentinel)
		}
	}
}
