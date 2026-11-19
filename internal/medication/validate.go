package medication

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const MaxNameLength = 200

// Input is raw, untrusted client data for creating or replacing a medication.
// Numbers arrive as strings (from json.Number) so no float conversion ever
// touches them; an empty string means "not provided".
type Input struct {
	Name   string
	Form   string
	Dosage DosageInput
	NDC    string
	RxCUI  string
	Route  string
}

type DosageInput struct {
	Value    string
	Unit     string
	PerValue string
	PerUnit  string
}

// Derived from the canonical lists so error messages never drift from them.
var (
	formList  = joinStrings(Forms)
	unitList  = joinStrings(Units)
	routeList = joinStrings(Routes)
)

// NewSpec validates and normalizes in, returning either a Spec that is
// guaranteed valid or a *ValidationError listing every invalid field.
func NewSpec(in Input) (Spec, error) {
	var verr ValidationError
	spec := Spec{
		Name:   parseName(in.Name, &verr),
		Form:   parseForm(in.Form, &verr),
		Dosage: parseDosage(in.Dosage, &verr),
		NDC:    parseOptionalNDC(in.NDC, &verr),
		RxCUI:  parseOptionalRxCUI(in.RxCUI, &verr),
		Route:  parseOptionalRoute(in.Route, &verr),
	}
	if len(verr.Fields) > 0 {
		return Spec{}, &verr
	}
	return spec, nil
}

// NormalizeName is exported so search terms get the same treatment as stored
// names. NFC makes "é" typed as e + combining accent byte-identical to the
// precomposed "é", so visually equal names hit the same unique-index entry.
func NormalizeName(s string) string {
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

func parseForm(raw string, verr *ValidationError) Form {
	if isBlank(raw) {
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
		Unit:  parseUnit("dosage.unit", in.Unit, verr),
	}
	if per, ok := parsePer(in, verr); ok {
		checkConcentration(d.Unit, per.Unit, verr)
		d.Per = &per
	}
	return d
}

func parsePer(in DosageInput, verr *ValidationError) (Quantity, bool) {
	hasValue, hasUnit := !isBlank(in.PerValue), !isBlank(in.PerUnit)
	switch {
	case !hasValue && !hasUnit:
		return Quantity{}, false
	case !hasValue:
		verr.add("dosage.per_value", "is required when dosage.per_unit is set")
		return Quantity{}, false
	case !hasUnit:
		verr.add("dosage.per_unit", "is required when dosage.per_value is set")
		return Quantity{}, false
	}
	return Quantity{
		Value: parsePositiveAmount("dosage.per_value", in.PerValue, verr),
		Unit:  parseUnit("dosage.per_unit", in.PerUnit, verr),
	}, true
}

func checkConcentration(unit, perUnit Unit, verr *ValidationError) {
	switch {
	case unit == "" || perUnit == "":
		// A unit already failed to parse; a second error would only repeat it.
	case unit == UnitPercent || perUnit == UnitPercent:
		verr.add("dosage.per_unit", "cannot be combined with %: a percentage is already a concentration")
	case unit == perUnit:
		verr.add("dosage.per_unit", "must differ from dosage.unit")
	}
}

func parsePositiveAmount(field, raw string, verr *ValidationError) Amount {
	if isBlank(raw) {
		verr.add(field, "is required")
		return 0
	}
	a, err := ParseAmount(strings.TrimSpace(raw))
	switch {
	case err != nil:
		verr.add(field, err.Error())
	case a == 0:
		verr.add(field, "must be greater than 0")
	}
	return a
}

func parseUnit(field, raw string, verr *ValidationError) Unit {
	if isBlank(raw) {
		verr.add(field, "is required")
		return ""
	}
	u, ok := ParseUnit(raw)
	if !ok {
		verr.add(field, "must be one of: "+unitList)
	}
	return u
}

func parseOptionalNDC(raw string, verr *ValidationError) NDC {
	if isBlank(raw) {
		return ""
	}
	n, err := ParseNDC(raw)
	if err != nil {
		verr.add("ndc", err.Error())
	}
	return n
}

func parseOptionalRxCUI(raw string, verr *ValidationError) RxCUI {
	if isBlank(raw) {
		return ""
	}
	id, err := ParseRxCUI(raw)
	if err != nil {
		verr.add("rxcui", err.Error())
	}
	return id
}

func parseOptionalRoute(raw string, verr *ValidationError) Route {
	if isBlank(raw) {
		return ""
	}
	r, ok := ParseRoute(raw)
	if !ok {
		verr.add("route", "must be one of: "+routeList)
	}
	return r
}

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

func joinStrings[T ~string](vals []T) string {
	s := make([]string, len(vals))
	for i, v := range vals {
		s[i] = string(v)
	}
	return strings.Join(s, ", ")
}
