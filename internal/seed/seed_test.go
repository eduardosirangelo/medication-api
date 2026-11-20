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

type fakeCreator struct {
	errs  []error
	calls int
}

func (f *fakeCreator) Create(_ context.Context, in medication.Input) (medication.Medication, error) {
	err := f.errs[f.calls]
	f.calls++
	return medication.Medication{Spec: medication.Spec{Name: in.Name}}, err
}

func TestRun_SkipsDuplicatesAndStopsOnOtherErrors(t *testing.T) {
	entries := make([]medication.Input, 4)
	boom := errors.New("connection reset")
	svc := &fakeCreator{errs: []error{nil, medication.ErrDuplicate, medication.ErrDuplicateNDC, boom}}

	res, err := Run(t.Context(), svc, entries, discard)
	if !errors.Is(err, boom) {
		t.Fatalf("want wrapped boom, got %v", err)
	}
	if res != (Result{Inserted: 1, Skipped: 2}) {
		t.Errorf("result = %+v", res)
	}
}
