package medication

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// MaxNameLength is the maximum name length in characters (runes), after
// normalisation.
const MaxNameLength = 200

// Allowed-value lists for error messages, derived so they never drift.
var (
	formList = joinStrings(Forms)
	unitList = joinStrings(Units)
)

func joinStrings[T ~string](vals []T) string {
	s := make([]string, len(vals))
	for i, v := range vals {
		s[i] = string(v)
	}
	return strings.Join(s, ", ")
}

// Input is raw, untrusted client data for creating or replacing a medication.
// Numbers arrive as strings (from json.Number) so no float conversion ever
// touches them. An empty string means "not provided".
type Input struct {
	Name   string
	Form   string
	Dosage DosageInput
}

// DosageInput is the raw dosage; PerValue/PerUnit are both set or both empty.
type DosageInput struct {
	Value    string
	Unit     string
	PerValue string
	PerUnit  string
}

// NewSpec validates and normalises in. It returns a *ValidationError listing
// every invalid field, or a Spec that is guaranteed valid ("parse, don't
// validate": code holding a Spec never re-checks it).
//
// Normalisation:
//   - name: Unicode NFC, trimmed, internal whitespace collapsed to one space;
//     letter case is preserved (uniqueness is case-insensitive in the DB);
//   - form and units: case-insensitive, aliases accepted, stored canonical;
//   - amounts: exact decimals, canonical form ("500.0" → 500).
func NewSpec(in Input) (Spec, error) {
	var verr ValidationError
	spec := Spec{
		Name:   parseName(in.Name, &verr),
		Form:   parseFormField(in.Form, &verr),
		Dosage: parseDosage(in.Dosage, &verr),
	}
	if len(verr.Fields) > 0 {
		return Spec{}, &verr
	}
	return spec, nil
}

// NormalizeName applies the same normalisation as NewSpec, so search terms
// can be compared against stored names on equal footing.
func NormalizeName(s string) string {
	// NFC first: "é" typed as e + combining accent becomes the single code
	// point é, so visually identical names are byte-identical (and hit the
	// same unique index entry). strings.Fields trims and splits on any Unicode
	// whitespace (tabs, newlines, NBSP), which re-joining collapses.
	return strings.Join(strings.Fields(norm.NFC.String(s)), " ")
}

func parseName(raw string, verr *ValidationError) string {
	name := NormalizeName(raw)
	switch {
	case name == "":
		verr.add("name", "is required")
	case !utf8.ValidString(name):
		verr.add("name", "must be valid UTF-8")
	case utf8.RuneCountInString(name) > MaxNameLength:
		verr.add("name", "must be at most 200 characters")
	case strings.ContainsFunc(name, unicode.IsControl):
		verr.add("name", "must not contain control characters")
	}
	return name
}

func parseFormField(raw string, verr *ValidationError) Form {
	if strings.TrimSpace(raw) == "" {
		verr.add("form", "is required")
		return ""
	}
	f, ok := ParseForm(raw)
	if !ok {
		verr.add("form", "must be one of: "+formList)
	}
	return f
}

func parseDosage(in DosageInput, verr *ValidationError) Dosage {
	d := Dosage{
		Value: parsePositiveAmount("dosage.value", in.Value, verr),
		Unit:  parseUnitField("dosage.unit", in.Unit, verr),
	}

	hasPerValue := strings.TrimSpace(in.PerValue) != ""
	hasPerUnit := strings.TrimSpace(in.PerUnit) != ""
	switch {
	case !hasPerValue && !hasPerUnit:
		return d
	case !hasPerValue:
		verr.add("dosage.per_value", "is required when dosage.per_unit is set")
		return d
	case !hasPerUnit:
		verr.add("dosage.per_unit", "is required when dosage.per_value is set")
		return d
	}

	per := Quantity{
		Value: parsePositiveAmount("dosage.per_value", in.PerValue, verr),
		Unit:  parseUnitField("dosage.per_unit", in.PerUnit, verr),
	}
	// Cross-field rules, only meaningful when both units parsed.
	switch {
	case d.Unit == "" || per.Unit == "":
	case d.Unit == UnitPercent || per.Unit == UnitPercent:
		verr.add("dosage.per_unit", "cannot be combined with %: a percentage is already a concentration")
	case per.Unit == d.Unit:
		verr.add("dosage.per_unit", "must differ from dosage.unit")
	}
	d.Per = &per
	return d
}

func parsePositiveAmount(field, raw string, verr *ValidationError) Amount {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		verr.add(field, "is required")
		return 0
	}
	a, err := ParseAmount(raw)
	switch {
	case err != nil:
		verr.add(field, err.Error())
	case a == 0:
		verr.add(field, "must be greater than 0")
	}
	return a
}

func parseUnitField(field, raw string, verr *ValidationError) Unit {
	if strings.TrimSpace(raw) == "" {
		verr.add(field, "is required")
		return ""
	}
	u, ok := ParseUnit(raw)
	if !ok {
		verr.add(field, "must be one of: "+unitList)
	}
	return u
}
