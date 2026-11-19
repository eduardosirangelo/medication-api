// Package medication is the medication catalog: domain model, validation,
// service, persistence and HTTP handlers.
//
// It catalogs medication products (name, strength, form, identifiers), not
// patient data: no PHI belongs here. A per-patient prescription would be a
// separate resource with its own PHI/HIPAA scope.
package medication

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Medication is a catalog entry as stored and returned by the API. Soft
// deletion is a persistence detail: deleted rows never reach the domain.
type Medication struct {
	ID uuid.UUID
	Spec
	// Version backs the ETag used for optimistic concurrency control.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Spec is the validated, normalized, client-controlled part of a medication.
// The only way to build one from user input is NewSpec.
type Spec struct {
	Name   string
	Dosage Dosage
	Form   Form

	// Optional public identifiers; zero values mean "not provided".
	NDC   NDC
	RxCUI RxCUI
	Route Route

	// TODO: DEA controlled-substance schedule (CII–CV).
	// TODO: Rx-only vs OTC status.
	// TODO: import/sync from a public drug database (e.g. RxNorm, FDA NDC
	// directory) instead of manual entry.
}

// UUIDv7 is time-ordered: inserts append to the primary-key B-tree instead of
// splitting random pages, and the list endpoint can page by ID.
func NewID() (uuid.UUID, error) { return uuid.NewV7() }

// Dosage is the product strength (e.g. 500 mg, or 250 mg/5 mL for a
// concentration). It is not a patient's directions for use (sig).
type Dosage struct {
	Value Amount
	Unit  Unit
	Per   *Quantity
}

type Quantity struct {
	Value Amount
	Unit  Unit
}

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

var Forms = []Form{FormTablet, FormCapsule, FormLiquid}

var formsByName = indexByName(Forms)

func ParseForm(s string) (Form, bool) {
	f, ok := formsByName[strings.ToLower(strings.TrimSpace(s))]
	return f, ok
}

// Unit is a dosage unit. Canonical spellings follow clinical convention: mL
// (a capital L can't be misread as 1), IU, and mcg rather than µg, which can
// be misread as mg — a thousandfold dosing error.
type Unit string

const (
	UnitMg      Unit = "mg"
	UnitMcg     Unit = "mcg"
	UnitG       Unit = "g"
	UnitML      Unit = "mL"
	UnitIU      Unit = "IU"
	UnitPercent Unit = "%"
)

var Units = []Unit{UnitMg, UnitMcg, UnitG, UnitML, UnitIU, UnitPercent}

// Both micro signs are accepted because users can't tell them apart:
// U+00B5 (keyboard µ) and U+03BC (Greek mu).
var unitAliases = map[string]Unit{
	"mg":  UnitMg,
	"mcg": UnitMcg, "ug": UnitMcg, "µg": UnitMcg, "μg": UnitMcg,
	"g":  UnitG,
	"ml": UnitML,
	"iu": UnitIU,
	"%":  UnitPercent,
}

func ParseUnit(s string) (Unit, bool) {
	u, ok := unitAliases[strings.ToLower(strings.TrimSpace(s))]
	return u, ok
}
