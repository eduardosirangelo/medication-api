package seed

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/eduardo-sirangelo/medication-api/internal/medication"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestCatalog_EveryEntryIsValid(t *testing.T) {
	for _, in := range Catalog {
		if _, err := medication.NewSpec(in); err != nil {
			t.Errorf("%s %s: %v", in.Name, in.Dosage.Value, err)
		}
	}
}

func TestCatalog_CoversTheShapesReviewersExpect(t *testing.T) {
	var concentration, withIDs, bare bool
	for _, in := range Catalog {
		concentration = concentration || in.Dosage.PerUnit != ""
		withIDs = withIDs || (in.NDC != "" && in.RxCUI != "")
		bare = bare || (in.NDC == "" && in.RxCUI == "" && in.Route == "")
	}
	if !concentration || !withIDs || !bare {
		t.Errorf("concentration=%v withIDs=%v bare=%v", concentration, withIDs, bare)
	}
}

// fakeStore answers each call with the next scripted outcome.
type fakeStore struct {
	outcomes []outcome
	calls    int
}

type outcome struct {
	inserted bool
	err      error
}

func (f *fakeStore) CreateIfAbsent(_ context.Context, m medication.Medication) (medication.Medication, bool, error) {
	o := f.outcomes[f.calls]
	f.calls++
	return m, o.inserted, o.err
}

func validEntries(n int) []medication.Input {
	entries := make([]medication.Input, n)
	for i := range entries {
		entries[i] = Catalog[i]
	}
	return entries
}

func TestRun_CountsInsertedAndSkippedAndStopsOnErrors(t *testing.T) {
	boom := errors.New("connection reset")
	store := &fakeStore{outcomes: []outcome{{inserted: true}, {inserted: false}, {inserted: false}, {err: boom}}}

	res, err := Run(t.Context(), store, validEntries(4), discard)
	if !errors.Is(err, boom) {
		t.Fatalf("want wrapped boom, got %v", err)
	}
	if res != (Result{Inserted: 1, Skipped: 2}) {
		t.Errorf("result = %+v", res)
	}
}

func TestRun_InvalidEntryStopsBeforeTheStore(t *testing.T) {
	store := &fakeStore{}
	_, err := Run(t.Context(), store, []medication.Input{{Name: "No dosage", Form: "tablet"}}, discard)

	var verr *medication.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	if store.calls != 0 {
		t.Errorf("store called %d times for an invalid entry", store.calls)
	}
}

func TestRun_PassesNormalizedSpecWithFreshIDs(t *testing.T) {
	var got []medication.Medication
	store := storeFunc(func(m medication.Medication) { got = append(got, m) })
	in := []medication.Input{
		{Name: "  Ibuprofen ", Form: "TABLET", Dosage: medication.DosageInput{Value: "200.0", Unit: "MG"}, NDC: "0573-0164-40"},
		{Name: "Ibuprofen", Form: "tablet", Dosage: medication.DosageInput{Value: "400", Unit: "mg"}},
	}
	if _, err := Run(t.Context(), store, in, discard); err != nil {
		t.Fatal(err)
	}
	if got[0].Name != "Ibuprofen" || got[0].Form != medication.FormTablet || got[0].Dosage.String() != "200 mg" ||
		got[0].NDC != "00573016440" {
		t.Errorf("not normalized: %+v", got[0])
	}
	if got[0].ID == got[1].ID || got[0].ID.Version() != 7 {
		t.Errorf("ids: %s %s", got[0].ID, got[1].ID)
	}
}

type storeFunc func(medication.Medication)

func (f storeFunc) CreateIfAbsent(_ context.Context, m medication.Medication) (medication.Medication, bool, error) {
	f(m)
	return m, true, nil
}
