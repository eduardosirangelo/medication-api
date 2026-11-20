// Package seed loads a small sample catalog for local development and demos.
// It only runs when someone invokes `api seed`; neither serve nor migrate
// calls it, so production catalogs never receive sample data by accident.
package seed

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/eduardo-sirangelo/medication-api/internal/medication"
)

// Store is what the seeder writes to; medication.PostgresRepository
// implements it. It is not medication.Service on purpose: Service.Create
// reports an existing row as a failed INSERT, which PostgreSQL logs as an
// ERROR, and a re-run seed would log one per entry.
type Store interface {
	CreateIfAbsent(ctx context.Context, m medication.Medication) (medication.Medication, bool, error)
}

// Result counts what a run did, so a second run visibly inserts nothing.
type Result struct {
	Inserted int
	Skipped  int
}

// Run validates every entry exactly like API input (medication.NewSpec) and
// inserts the ones that do not exist yet. Existence is decided by the unique
// indexes inside the INSERT, not by a lookup first, so concurrent runs cannot
// duplicate entries either. An invalid entry is a bug in the catalog and stops
// the run instead of being skipped.
func Run(ctx context.Context, store Store, entries []medication.Input, log *slog.Logger) (Result, error) {
	var res Result
	for _, in := range entries {
		m, err := newMedication(in)
		if err != nil {
			return res, fmt.Errorf("seed %q: %w", in.Name, err)
		}
		created, inserted, err := store.CreateIfAbsent(ctx, m)
		switch {
		case err != nil:
			return res, fmt.Errorf("seed %q: %w", in.Name, err)
		case inserted:
			res.Inserted++
			log.InfoContext(ctx, "seeded", "id", created.ID, "name", created.Name, "dosage", created.Dosage.String())
		default:
			res.Skipped++
			log.DebugContext(ctx, "seed entry exists", "name", m.Name, "dosage", m.Dosage.String())
		}
	}
	return res, nil
}

func newMedication(in medication.Input) (medication.Medication, error) {
	spec, err := medication.NewSpec(in)
	if err != nil {
		return medication.Medication{}, err
	}
	id, err := medication.NewID()
	if err != nil {
		return medication.Medication{}, fmt.Errorf("generate id: %w", err)
	}
	return medication.Medication{ID: id, Spec: spec}, nil
}

// NDCs and RxCUIs below are format-valid illustrations, not looked up in the
// FDA NDC Directory or RxNorm; a real catalog would import them (see the
// drug-database TODO in medication.Spec).
var Catalog = toInputs([]entry{
	{"Acetaminophen", "tablet", "500", "mg", "", "", "50580-0451-01", "198440", "oral"},
	{"Ibuprofen", "tablet", "200", "mg", "", "", "0573-0164-40", "310965", "oral"},
	{"Ibuprofen", "tablet", "400", "mg", "", "", "", "", ""},
	{"Lisinopril", "tablet", "10", "mg", "", "", "68180-0513-01", "314076", "oral"},
	{"Metformin Hydrochloride", "tablet", "500", "mg", "", "", "0093-1048-01", "861007", "oral"},
	{"Atorvastatin Calcium", "tablet", "20", "mg", "", "", "", "617310", "oral"},
	{"Levothyroxine Sodium", "tablet", "50", "mcg", "", "", "0378-1801-01", "", ""},
	{"Cetirizine Hydrochloride", "tablet", "10", "mg", "", "", "", "", ""},
	{"Nitroglycerin", "tablet", "0.4", "mg", "", "", "", "", "sublingual"},
	{"Amoxicillin", "capsule", "500", "mg", "", "", "65862-0017-01", "308191", "oral"},
	{"Omeprazole", "capsule", "20", "mg", "", "", "", "402014", "oral"},
	{"Fluoxetine", "capsule", "20", "mg", "", "", "", "", ""},
	{"Vitamin D3", "capsule", "1000", "IU", "", "", "", "", ""},
	{"Amoxicillin", "liquid", "250", "mg", "5", "mL", "0093-4155-73", "", "oral"},
	{"Acetaminophen", "liquid", "160", "mg", "5", "mL", "", "", "oral"},
	{"Cetirizine Hydrochloride", "liquid", "1", "mg", "1", "mL", "", "", ""},
})

// A table reads better than nested literals; empty strings mean "not set".
type entry struct {
	name, form, value, unit, perValue, perUnit, ndc, rxcui, route string
}

func toInputs(entries []entry) []medication.Input {
	inputs := make([]medication.Input, len(entries))
	for i, e := range entries {
		inputs[i] = medication.Input{
			Name: e.name, Form: e.form, NDC: e.ndc, RxCUI: e.rxcui, Route: e.route,
			Dosage: medication.DosageInput{Value: e.value, Unit: e.unit, PerValue: e.perValue, PerUnit: e.perUnit},
		}
	}
	return inputs
}
