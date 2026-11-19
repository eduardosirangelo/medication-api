package medication

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

var fixedID = uuid.MustParse("019a9c3e-7a10-7000-8000-000000000001")

func newTestService(repo *fakeRepo) *Service {
	s := NewService(repo)
	s.newID = func() (uuid.UUID, error) { return fixedID, nil }
	return s
}

func TestService_Create(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)

	m, err := svc.Create(t.Context(), Input{
		Name: "  Paracetamol ", Form: "Tablet",
		Dosage: DosageInput{Value: "500.0", Unit: "MG"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if m.ID != fixedID || m.Name != "Paracetamol" || m.Form != FormTablet ||
		m.Dosage.String() != "500 mg" || m.Version != 1 || m.CreatedAt.IsZero() {
		t.Errorf("unexpected medication: %+v", m)
	}
	if _, ok := repo.rows[fixedID]; !ok {
		t.Error("medication not stored")
	}
}

func TestService_Create_ValidationErrorNeverReachesRepo(t *testing.T) {
	repo := newFakeRepo()
	_, err := newTestService(repo).Create(t.Context(), Input{})

	var verr *ValidationError
	if !errors.As(err, &verr) || len(verr.Fields) == 0 {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	if repo.calls != 0 {
		t.Errorf("repository called %d times for invalid input", repo.calls)
	}
}

func TestService_Create_Duplicate(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo) // real UUIDv7 generator
	in := Input{Name: "Ibuprofen", Form: "capsule", Dosage: DosageInput{Value: "200", Unit: "mg"}}

	if _, err := svc.Create(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	in.Name = "IBUPROFEN"
	if _, err := svc.Create(t.Context(), in); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
}

func TestService_Create_IDGenerationFailure(t *testing.T) {
	svc := NewService(newFakeRepo())
	boom := errors.New("entropy exhausted")
	svc.newID = func() (uuid.UUID, error) { return uuid.Nil, boom }

	_, err := svc.Create(t.Context(), Input{Name: "X", Form: "tablet", Dosage: DosageInput{Value: "1", Unit: "mg"}})
	if !errors.Is(err, boom) {
		t.Fatalf("want wrapped boom, got %v", err)
	}
}

func TestService_Get(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)
	created, err := svc.Create(t.Context(), Input{Name: "X", Form: "tablet", Dosage: DosageInput{Value: "1", Unit: "mg"}})
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(t.Context(), created.ID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if _, err := svc.Get(t.Context(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestService_RepositoryErrorsAreWrapped(t *testing.T) {
	repo := newFakeRepo()
	repo.err = errors.New("connection reset")
	svc := newTestService(repo)

	_, err := svc.Create(t.Context(), Input{Name: "X", Form: "tablet", Dosage: DosageInput{Value: "1", Unit: "mg"}})
	if !errors.Is(err, repo.err) {
		t.Fatalf("create: %v", err)
	}
	_, err = svc.Get(t.Context(), fixedID)
	if !errors.Is(err, repo.err) {
		t.Fatalf("get: %v", err)
	}
}

func TestService_List_FetchesOneExtraToDetectMore(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	for _, name := range []string{"A", "B", "C"} {
		if _, err := svc.Create(t.Context(), Input{Name: name, Form: "tablet", Dosage: DosageInput{Value: "1", Unit: "mg"}}); err != nil {
			t.Fatal(err)
		}
	}

	page, err := svc.List(t.Context(), ListParams{Limit: 2})
	if err != nil || len(page.Items) != 2 || !page.HasMore {
		t.Fatalf("first page: %d items, more=%v, err=%v", len(page.Items), page.HasMore, err)
	}
	page, err = svc.List(t.Context(), ListParams{Limit: 2, After: page.Items[1].ID})
	if err != nil || len(page.Items) != 1 || page.HasMore {
		t.Fatalf("last page: %d items, more=%v, err=%v", len(page.Items), page.HasMore, err)
	}
	page, err = svc.List(t.Context(), ListParams{Limit: 3})
	if err != nil || len(page.Items) != 3 || page.HasMore {
		t.Fatalf("exact fit must not report more: %d items, more=%v", len(page.Items), page.HasMore)
	}
}

func TestService_Replace(t *testing.T) {
	valid := Input{Name: "Ibuprofen", Form: "tablet", Dosage: DosageInput{Value: "400", Unit: "mg"}}

	t.Run("validation runs before the repository", func(t *testing.T) {
		repo := newFakeRepo()
		_, err := newTestService(repo).Replace(t.Context(), fixedID, []int64{1}, Input{})
		var verr *ValidationError
		if !errors.As(err, &verr) || repo.calls != 0 {
			t.Fatalf("err = %v, repo calls = %d", err, repo.calls)
		}
	})

	t.Run("no usable If-Match versions never matches", func(t *testing.T) {
		repo := newFakeRepo()
		_, err := newTestService(repo).Replace(t.Context(), fixedID, nil, valid)
		if !errors.Is(err, ErrVersionMismatch) || repo.calls != 0 {
			t.Fatalf("err = %v, repo calls = %d", err, repo.calls)
		}
	})

	t.Run("bumps version and wraps repository errors", func(t *testing.T) {
		repo := newFakeRepo()
		svc := newTestService(repo)
		if _, err := svc.Create(t.Context(), Input{Name: "X", Form: "tablet", Dosage: DosageInput{Value: "1", Unit: "mg"}}); err != nil {
			t.Fatal(err)
		}
		m, err := svc.Replace(t.Context(), fixedID, []int64{1}, valid)
		if err != nil || m.Version != 2 || m.Name != "Ibuprofen" {
			t.Fatalf("replace: %+v, %v", m, err)
		}
		if _, err := svc.Replace(t.Context(), fixedID, []int64{1}, valid); !errors.Is(err, ErrVersionMismatch) {
			t.Fatalf("stale version: %v", err)
		}
	})
}

func TestService_Delete(t *testing.T) {
	create := func(t *testing.T) (*fakeRepo, *Service) {
		t.Helper()
		repo := newFakeRepo()
		svc := newTestService(repo)
		if _, err := svc.Create(t.Context(), Input{Name: "X", Form: "tablet", Dosage: DosageInput{Value: "1", Unit: "mg"}}); err != nil {
			t.Fatal(err)
		}
		return repo, svc
	}

	t.Run("unconditional", func(t *testing.T) {
		_, svc := create(t)
		if err := svc.Delete(t.Context(), fixedID); err != nil {
			t.Fatal(err)
		}
		if err := svc.Delete(t.Context(), fixedID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("second delete: %v", err)
		}
	})

	t.Run("conditional with no usable versions never reaches the repository", func(t *testing.T) {
		repo, svc := create(t)
		before := repo.calls
		if err := svc.DeleteIfVersion(t.Context(), fixedID, nil); !errors.Is(err, ErrVersionMismatch) {
			t.Fatalf("err = %v", err)
		}
		if repo.calls != before {
			t.Error("repository called")
		}
	})

	t.Run("conditional stale then current", func(t *testing.T) {
		_, svc := create(t)
		if err := svc.DeleteIfVersion(t.Context(), fixedID, []int64{2}); !errors.Is(err, ErrVersionMismatch) {
			t.Fatalf("stale: %v", err)
		}
		if err := svc.DeleteIfVersion(t.Context(), fixedID, []int64{1}); err != nil {
			t.Fatalf("current: %v", err)
		}
	})
}
