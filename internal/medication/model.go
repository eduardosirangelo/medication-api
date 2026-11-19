// Package medication is the medication catalog: domain model, validation,
// service, persistence and HTTP handlers.
//
// This is a catalog of medication products (name + strength + form), not
// patient data: no PHI belongs here. A per-patient prescription would be a
// separate resource with its own PHI/HIPAA scope.
package medication

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Medication is a catalog entry as stored and returned by the API.
// Soft deletion is a persistence detail: deleted rows never reach the domain.
type Medication struct {
	ID uuid.UUID
	Spec
	// Version starts at 1 and increments on every update; it backs the ETag
	// used for optimistic concurrency control.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Spec is the validated, normalised client-controlled part of a medication.
// The only way to get one from user input is NewSpec.
type Spec struct {
	Name   string
	Dosage Dosage
	Form   Form
}

// NewID returns a time-ordered UUIDv7: unique without coordination, and
// roughly insertion-ordered, which keeps the primary-key B-tree compact and
// lets the list endpoint page by ID.
func NewID() (uuid.UUID, error) { return uuid.NewV7() }

// Dosage is a strength, optionally expressed as a concentration:
//
//	500 mg          → {Value: 500, Unit: mg}
//	250 mg / 5 mL   → {Value: 250, Unit: mg, Per: &{Value: 5, Unit: mL}}
type Dosage struct {
	Value Amount
	Unit  Unit
	Per   *Quantity // nil unless the dosage is a concentration
}

// Quantity is an amount with its unit.
type Quantity struct {
	Value Amount
	Unit  Unit
}

// String renders the dosage for humans and logs, e.g. "250 mg/5 mL".
func (d Dosage) String() string {
	s := d.Value.String() + " " + string(d.Unit)
	if d.Per != nil {
		s += "/" + d.Per.Value.String() + " " + string(d.Per.Unit)
	}
	return s
}

// Form is the pharmaceutical form. Canonical values are lowercase.
type Form string

const (
	FormTablet  Form = "tablet"
	FormCapsule Form = "capsule"
	FormLiquid  Form = "liquid"
)

// Forms lists every valid form, in documentation order.
var Forms = []Form{FormTablet, FormCapsule, FormLiquid}

// ParseForm accepts any letter case ("Tablet", "TABLET") and returns the
// canonical value.
func ParseForm(s string) (Form, bool) {
	f := Form(strings.ToLower(strings.TrimSpace(s)))
	switch f {
	case FormTablet, FormCapsule, FormLiquid:
		return f, true
	}
	return "", false
}

// Unit is a dosage unit from a fixed list. Canonical spellings follow common
// clinical usage (mL with a capital L, IU uppercase).
type Unit string

const (
	UnitMg      Unit = "mg"
	UnitMcg     Unit = "mcg"
	UnitG       Unit = "g"
	UnitML      Unit = "mL"
	UnitIU      Unit = "IU"
	UnitPercent Unit = "%"
)

// Units lists every valid unit, in documentation order.
var Units = []Unit{UnitMg, UnitMcg, UnitG, UnitML, UnitIU, UnitPercent}

// unitAliases maps lowercase spellings to canonical units. Both micro signs
// are accepted: U+00B5 (keyboard µ) and U+03BC (Greek mu).
var unitAliases = map[string]Unit{
	"mg":  UnitMg,
	"mcg": UnitMcg, "ug": UnitMcg, "µg": UnitMcg, "μg": UnitMcg,
	"g":  UnitG,
	"ml": UnitML,
	"iu": UnitIU,
	"%":  UnitPercent,
}

// ParseUnit accepts any letter case and a few common aliases ("ml", "ug",
// "µg") and returns the canonical unit.
func ParseUnit(s string) (Unit, bool) {
	u, ok := unitAliases[strings.ToLower(strings.TrimSpace(s))]
	return u, ok
}
