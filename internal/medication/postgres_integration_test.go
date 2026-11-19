//go:build integration

package medication

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/dbtest"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	pg, err := dbtest.Start(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testPool, err = pg.MigratedPool(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = pg.Terminate()
		os.Exit(1)
	}
	code := m.Run()
	testPool.Close()
	_ = pg.Terminate()
	os.Exit(code)
}

// freshRepo empties the table so each test starts from a known state.
// Tests in this package therefore must not run in parallel.
func freshRepo(t *testing.T) *PostgresRepository {
	t.Helper()
	if _, err := testPool.Exec(t.Context(), "TRUNCATE medications"); err != nil {
		t.Fatal(err)
	}
	return NewPostgresRepository(testPool)
}

func mustSpec(t *testing.T, in Input) Spec {
	t.Helper()
	s, err := NewSpec(in)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newMed(t *testing.T, in Input) Medication {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	return Medication{ID: id, Spec: mustSpec(t, in)}
}

func TestPostgres_CreateAndGetRoundTrip(t *testing.T) {
	repo := freshRepo(t)
	ctx := t.Context()

	for _, in := range []Input{
		{Name: "Paracetamol", Form: "tablet", Dosage: DosageInput{Value: "500", Unit: "mg"}},
		{Name: "Amoxicillin", Form: "liquid", Dosage: DosageInput{Value: "250", Unit: "mg", PerValue: "5", PerUnit: "mL"}},
		{Name: "Levothyroxine", Form: "tablet", Dosage: DosageInput{Value: "0.0125", Unit: "mg"}},
		{Name: "Max", Form: "capsule", Dosage: DosageInput{Value: "99999999.9999", Unit: "IU"}},
	} {
		t.Run(in.Name, func(t *testing.T) {
			m := newMed(t, in)
			before := time.Now().Add(-time.Second)

			created, err := repo.Create(ctx, m)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if created.Version != 1 || created.CreatedAt.Before(before) || !created.CreatedAt.Equal(created.UpdatedAt) {
				t.Errorf("db-assigned fields: version=%d created=%v updated=%v", created.Version, created.CreatedAt, created.UpdatedAt)
			}
			if created.CreatedAt.Location() != time.UTC {
				t.Error("timestamps must be UTC")
			}

			got, err := repo.Get(ctx, m.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			// Exact decimal round trip through NUMERIC(12,4).
			if got.Dosage.String() != m.Dosage.String() || got.Name != m.Name || got.Form != m.Form || got.ID != m.ID {
				t.Errorf("round trip: stored %+v (%s), read %+v (%s)", m.Spec, m.Dosage, got.Spec, got.Dosage)
			}
			if !got.CreatedAt.Equal(created.CreatedAt) {
				t.Errorf("created_at changed: %v vs %v", got.CreatedAt, created.CreatedAt)
			}
		})
	}
}

func TestPostgres_GetMissing(t *testing.T) {
	repo := freshRepo(t)
	if _, err := repo.Get(t.Context(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestPostgres_UniqueActiveIdentity(t *testing.T) {
	base := Input{Name: "Ibuprofen", Form: "tablet", Dosage: DosageInput{Value: "200", Unit: "mg"}}
	tests := []struct {
		name    string
		second  func(Input) Input
		wantDup bool
	}{
		{"identical", func(in Input) Input { return in }, true},
		{"different case", func(in Input) Input { in.Name = "IBUPROFEN"; return in }, true},
		{"same amount different scale", func(in Input) Input { in.Dosage.Value = "200.000"; return in }, true},
		{"different dosage", func(in Input) Input { in.Dosage.Value = "400"; return in }, false},
		{"different unit", func(in Input) Input { in.Dosage.Unit = "mcg"; return in }, false},
		{"different form", func(in Input) Input { in.Form = "capsule"; return in }, false},
		{"concentration vs plain", func(in Input) Input { in.Dosage.PerValue, in.Dosage.PerUnit = "5", "mL"; return in }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := freshRepo(t)
			if _, err := repo.Create(t.Context(), newMed(t, base)); err != nil {
				t.Fatal(err)
			}
			_, err := repo.Create(t.Context(), newMed(t, tt.second(base)))
			if tt.wantDup && !errors.Is(err, ErrDuplicate) {
				t.Fatalf("want ErrDuplicate, got %v", err)
			}
			if !tt.wantDup && err != nil {
				t.Fatalf("want success, got %v", err)
			}
		})
	}
}

// Identical plain dosages (per_* NULL) must conflict too. Without NULLS NOT
// DISTINCT on the index they would not, because NULL is never equal to NULL.
func TestPostgres_UniqueWithAndWithoutPer(t *testing.T) {
	for _, in := range []Input{
		{Name: "Plain", Form: "tablet", Dosage: DosageInput{Value: "1", Unit: "mg"}},
		{Name: "Conc", Form: "liquid", Dosage: DosageInput{Value: "1", Unit: "mg", PerValue: "1", PerUnit: "mL"}},
	} {
		repo := freshRepo(t)
		if _, err := repo.Create(t.Context(), newMed(t, in)); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Create(t.Context(), newMed(t, in)); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("%s: want ErrDuplicate, got %v", in.Name, err)
		}
	}
}

func TestPostgres_SoftDeletedRowsAreInvisibleAndDontBlock(t *testing.T) {
	repo := freshRepo(t)
	ctx := t.Context()
	in := Input{Name: "Aspirin", Form: "tablet", Dosage: DosageInput{Value: "100", Unit: "mg"}}

	first, err := repo.Create(ctx, newMed(t, in))
	if err != nil {
		t.Fatal(err)
	}
	// Soft delete directly; the Delete method arrives in slice 8.
	if _, err := testPool.Exec(ctx, "UPDATE medications SET deleted_at = now() WHERE id = $1", first.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.Get(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("soft-deleted row must be not found, got %v", err)
	}
	if _, err := repo.Create(ctx, newMed(t, in)); err != nil {
		t.Fatalf("re-creating a deleted medication must succeed: %v", err)
	}
}

// The CHECK constraints are a second line of defence behind domain validation.
func TestPostgres_CheckConstraintsRejectInvalidRows(t *testing.T) {
	freshRepo(t)
	insert := `INSERT INTO medications (id, name, dosage_value, dosage_unit, dosage_per_value, dosage_per_unit, form)
	           VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6)`
	tests := []struct {
		name string
		args []any
	}{
		{"empty name", []any{"", "1", "mg", nil, nil, "tablet"}},
		{"zero dosage", []any{"X", "0", "mg", nil, nil, "tablet"}},
		{"too precise", []any{"X", "1.00001", "mg", nil, nil, "tablet"}}, // documents a gap, see below
		{"unknown unit", []any{"X", "1", "kg", nil, nil, "tablet"}},
		{"unknown form", []any{"X", "1", "mg", nil, nil, "syrup"}},
		{"per value without unit", []any{"X", "1", "mg", "5", nil, "liquid"}},
		{"per unit same as unit", []any{"X", "1", "mg", "5", "mg", "liquid"}},
		{"percent with per", []any{"X", "1", "%", "5", "mL", "liquid"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := testPool.Exec(t.Context(), insert, tt.args...)
			if tt.name == "too precise" {
				// NUMERIC(12,4) silently rounds extra digits — which is exactly
				// why the domain rejects them before they reach the database.
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" { // check_violation
				t.Fatalf("want check_violation, got %v", err)
			}
		})
	}
}

func TestPostgres_IdentifiersRoundTrip(t *testing.T) {
	repo := freshRepo(t)
	m := newMed(t, Input{
		Name: "Amoxicillin", Form: "capsule", Dosage: DosageInput{Value: "500", Unit: "mg"},
		NDC: "0093-3109-01", RxCUI: "308191", Route: "oral",
	})
	if _, err := repo.Create(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NDC != "00093310901" || got.RxCUI != "308191" || got.Route != RouteOral {
		t.Errorf("identifiers = %q %q %q", got.NDC, got.RxCUI, got.Route)
	}

	var stored string
	_ = testPool.QueryRow(t.Context(), "SELECT ndc FROM medications WHERE id = $1", m.ID).Scan(&stored)
	if stored != "00093310901" {
		t.Errorf("stored ndc = %q, want unhyphenated 11 digits", stored)
	}
}

func TestPostgres_NDCUniqueAmongActiveRows(t *testing.T) {
	withNDC := func(name, ndc string) Medication {
		return newMed(t, Input{Name: name, Form: "tablet", Dosage: DosageInput{Value: "1", Unit: "mg"}, NDC: ndc})
	}

	t.Run("same NDC, different identity → ErrDuplicateNDC", func(t *testing.T) {
		repo := freshRepo(t)
		if _, err := repo.Create(t.Context(), withNDC("A", "12345-6789-01")); err != nil {
			t.Fatal(err)
		}
		_, err := repo.Create(t.Context(), withNDC("B", "12345678901"))
		if !errors.Is(err, ErrDuplicateNDC) {
			t.Fatalf("want ErrDuplicateNDC, got %v", err)
		}
	})

	t.Run("many rows without NDC are fine", func(t *testing.T) {
		repo := freshRepo(t)
		for _, name := range []string{"A", "B", "C"} {
			if _, err := repo.Create(t.Context(), withNDC(name, "")); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
	})

	t.Run("soft-deleted NDC can be reused", func(t *testing.T) {
		repo := freshRepo(t)
		first, err := repo.Create(t.Context(), withNDC("A", "12345-6789-01"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := testPool.Exec(t.Context(), "UPDATE medications SET deleted_at = now() WHERE id = $1", first.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Create(t.Context(), withNDC("B", "12345-6789-01")); err != nil {
			t.Fatalf("reuse after soft delete: %v", err)
		}
	})
}

func TestPostgres_NDCIndexIsValid(t *testing.T) {
	// CONCURRENTLY builds can leave an INVALID index that enforces nothing.
	var valid bool
	err := testPool.QueryRow(t.Context(), `
		SELECT i.indisvalid FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = 'medications_active_ndc_key'`).Scan(&valid)
	if err != nil || !valid {
		t.Fatalf("index valid = %v, err = %v", valid, err)
	}
}

func TestPostgres_IdentifierCheckConstraints(t *testing.T) {
	freshRepo(t)
	insert := `INSERT INTO medications (id, name, dosage_value, dosage_unit, form, ndc, rxcui, route)
	           VALUES (gen_random_uuid(), 'X', 1, 'mg', 'tablet', $1, $2, $3)`
	tests := []struct {
		name string
		args []any
	}{
		{"hyphenated ndc", []any{"12345-6789-01", nil, nil}},
		{"10-digit ndc", []any{"1234567890", nil, nil}},
		{"non-numeric rxcui", []any{nil, "RX1", nil}},
		{"unknown route", []any{nil, nil, "by mouth"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := testPool.Exec(t.Context(), insert, tt.args...)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Fatalf("want check_violation, got %v", err)
			}
		})
	}
}

// End to end through the real handler, service and repository.
func TestHTTP_CreateThenGet_RealDatabase(t *testing.T) {
	repo := freshRepo(t)
	mux := http.NewServeMux()
	NewHandler(NewService(repo), slog.New(slog.DiscardHandler)).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := `{"name":"Amoxicillin","form":"liquid","dosage":{"value":250,"unit":"mg","per_value":5,"per_unit":"mL"}}`
	post := func() (int, http.Header) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/v1/medications", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, resp.Header
	}

	status, created := post()
	if status != http.StatusCreated {
		t.Fatalf("POST = %d", status)
	}
	if dup, _ := post(); dup != http.StatusConflict {
		t.Fatalf("second POST = %d, want 409", dup)
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+created.Get("Location"), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("ETag") != created.Get("ETag") {
		t.Fatalf("GET = %d ETag %q (create ETag %q)", resp.StatusCode, resp.Header.Get("ETag"), created.Get("ETag"))
	}
}
