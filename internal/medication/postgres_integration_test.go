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
	"slices"
	"strconv"
	"strings"
	"sync"
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

func createNamed(t *testing.T, repo *PostgresRepository, name, form string) Medication {
	t.Helper()
	m, err := repo.Create(t.Context(), newMed(t, Input{Name: name, Form: form, Dosage: DosageInput{Value: "1", Unit: "mg"}}))
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return m
}

func names(ms []Medication) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}

func TestPostgres_ListFilters(t *testing.T) {
	repo := freshRepo(t)
	for _, n := range []string{"Amoxicillin", "Co-Amoxiclav", "Ibuprofen", "Dipirona Sódica", "50% Dextrose", "50X Dextrose", "a_b", "axb"} {
		createNamed(t, repo, n, "tablet")
	}
	createNamed(t, repo, "Amoxicillin Susp", "liquid")
	deleted := createNamed(t, repo, "Amoxicillin Old", "tablet")
	if _, err := testPool.Exec(t.Context(), "UPDATE medications SET deleted_at = now() WHERE id = $1", deleted.ID); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		filter ListFilter
		want   []string // newest first
	}{
		{ListFilter{NameContains: "amox"}, []string{"Amoxicillin Susp", "Co-Amoxiclav", "Amoxicillin"}},
		{ListFilter{NameContains: "AMOX", Form: FormTablet}, []string{"Co-Amoxiclav", "Amoxicillin"}},
		{ListFilter{NameContains: "sódica"}, []string{"Dipirona Sódica"}},
		{ListFilter{NameContains: "50%"}, []string{"50% Dextrose"}}, // % is literal, not a wildcard
		{ListFilter{NameContains: "a_b"}, []string{"a_b"}},          // _ is literal too
		{ListFilter{Form: FormLiquid}, []string{"Amoxicillin Susp"}},
		{ListFilter{NameContains: "nothing"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.filter.NameContains+"|"+string(tt.filter.Form), func(t *testing.T) {
			got, err := repo.List(t.Context(), tt.filter, uuid.Nil, 50)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(names(got), ",") != strings.Join(tt.want, ",") {
				t.Errorf("got %v, want %v", names(got), tt.want)
			}
		})
	}
}

// Keyset pagination is stable under concurrent inserts: new rows get larger
// UUIDv7s and appear before page one, never shifting later pages (unlike OFFSET).
func TestPostgres_ListKeysetIsStableUnderInserts(t *testing.T) {
	repo := freshRepo(t)
	svc := NewService(repo)
	var created []string
	for i := range 7 {
		created = append(created, createNamed(t, repo, "Med "+strconv.Itoa(i), "tablet").Name)
	}

	var seen []string
	params := ListParams{Limit: 3}
	for page := 0; ; page++ {
		p, err := svc.List(t.Context(), params)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, names(p.Items)...)
		if page == 0 {
			createNamed(t, repo, "Inserted mid-walk", "tablet")
		}
		if !p.HasMore {
			break
		}
		params.After = p.Items[len(p.Items)-1].ID
	}

	slices.Reverse(created)
	if strings.Join(seen, ",") != strings.Join(created, ",") {
		t.Errorf("walk = %v\nwant  %v (each once, newest first, insert not interleaved)", seen, created)
	}
}

// With enough rows for the planner to prefer indexes, each filter must be
// served by its index rather than a sequential scan.
func TestPostgres_ListUsesIndexes(t *testing.T) {
	freshRepo(t)
	ctx := t.Context()
	_, err := testPool.Exec(ctx, `
		INSERT INTO medications (id, name, dosage_value, dosage_unit, form)
		SELECT gen_random_uuid(), 'Drug ' || g, g, 'mg', (ARRAY['tablet','capsule','liquid'])[1 + g % 3]
		FROM generate_series(1, 20000) g`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, "ANALYZE medications"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		filter    ListFilter
		wantIndex string
	}{
		{ListFilter{}, "medications_active_id_idx"},
		{ListFilter{Form: FormLiquid}, "medications_active_form_id_idx"},
		{ListFilter{NameContains: "rug 1234"}, "medications_active_name_trgm_idx"},
	}
	for _, tt := range tests {
		query, args := buildListQuery(tt.filter, uuid.Nil, 21)
		var plan strings.Builder
		rows, err := testPool.Query(ctx, "EXPLAIN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			_ = rows.Scan(&line)
			plan.WriteString(line + "\n")
		}
		rows.Close()
		if !strings.Contains(plan.String(), tt.wantIndex) {
			t.Errorf("filter %+v: plan does not use %s:\n%s", tt.filter, tt.wantIndex, plan.String())
		}
	}
}

func TestPostgres_Update(t *testing.T) {
	spec := func(name string) Spec {
		return mustSpec(t, Input{Name: name, Form: "tablet", Dosage: DosageInput{Value: "10", Unit: "mg"}, NDC: "12345-6789-01"})
	}

	t.Run("bumps version, advances updated_at, keeps created_at", func(t *testing.T) {
		repo := freshRepo(t)
		created := createNamed(t, repo, "Before", "tablet")
		time.Sleep(2 * time.Millisecond)
		updated, err := repo.Update(t.Context(), created.ID, []int64{1}, spec("After"))
		if err != nil {
			t.Fatal(err)
		}
		if updated.Version != 2 || updated.Name != "After" || updated.NDC != "12345678901" ||
			!updated.CreatedAt.Equal(created.CreatedAt) || !updated.UpdatedAt.After(created.UpdatedAt) {
			t.Errorf("created %+v\nupdated %+v", created, updated)
		}
	})

	t.Run("stale version, missing and soft-deleted rows", func(t *testing.T) {
		repo := freshRepo(t)
		m := createNamed(t, repo, "X", "tablet")
		if _, err := repo.Update(t.Context(), m.ID, []int64{2, 3}, spec("Y")); !errors.Is(err, ErrVersionMismatch) {
			t.Errorf("stale: %v", err)
		}
		if _, err := repo.Update(t.Context(), uuid.New(), []int64{1}, spec("Y")); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing: %v", err)
		}
		if _, err := testPool.Exec(t.Context(), "UPDATE medications SET deleted_at = now() WHERE id = $1", m.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Update(t.Context(), m.ID, []int64{1}, spec("Y")); !errors.Is(err, ErrNotFound) {
			t.Errorf("soft-deleted: %v", err)
		}
	})

	t.Run("unique violations map to domain errors", func(t *testing.T) {
		repo := freshRepo(t)
		taken := createNamed(t, repo, "Taken", "tablet")
		other := createNamed(t, repo, "Other", "tablet")
		if _, err := repo.Update(t.Context(), taken.ID, []int64{1}, spec("Taken")); err != nil {
			t.Fatal(err) // now holds the NDC
		}
		if _, err := repo.Update(t.Context(), other.ID, []int64{1}, spec("Taken")); !errors.Is(err, ErrDuplicate) {
			t.Errorf("identity: %v", err)
		}
		if _, err := repo.Update(t.Context(), other.ID, []int64{1}, spec("Different")); !errors.Is(err, ErrDuplicateNDC) {
			t.Errorf("ndc: %v", err)
		}
	})
}

// The lost-update guarantee: N writers holding the same ETag race; exactly
// one wins and every other one is told its version is stale.
func TestPostgres_UpdateConcurrentWritersExactlyOneWins(t *testing.T) {
	repo := freshRepo(t)
	m := createNamed(t, repo, "Contended", "tablet")

	const writers = 10
	results := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			s := mustSpec(t, Input{Name: "Writer " + strconv.Itoa(i), Form: "tablet", Dosage: DosageInput{Value: "1", Unit: "mg"}})
			_, err := repo.Update(t.Context(), m.ID, []int64{1}, s)
			results <- err
		})
	}
	wg.Wait()
	close(results)

	var won, stale int
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrVersionMismatch):
			stale++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if won != 1 || stale != writers-1 {
		t.Fatalf("won = %d, stale = %d; want 1 and %d", won, stale, writers-1)
	}
	if got, _ := repo.Get(t.Context(), m.ID); got.Version != 2 {
		t.Errorf("final version = %d, want 2", got.Version)
	}
}

func softDeletedAt(t *testing.T, id uuid.UUID) (deletedAt *time.Time, version int64) {
	t.Helper()
	err := testPool.QueryRow(t.Context(),
		`SELECT deleted_at, version FROM medications WHERE id = $1`, id).Scan(&deletedAt, &version)
	if err != nil {
		t.Fatal(err)
	}
	return deletedAt, version
}

func TestPostgres_Delete(t *testing.T) {
	t.Run("keeps the row, stamps deleted_at and bumps version", func(t *testing.T) {
		repo := freshRepo(t)
		m := createNamed(t, repo, "X", "tablet")
		if err := repo.Delete(t.Context(), m.ID, nil); err != nil {
			t.Fatal(err)
		}
		deletedAt, version := softDeletedAt(t, m.ID)
		if deletedAt == nil || version != 2 {
			t.Errorf("deleted_at = %v, version = %d", deletedAt, version)
		}
		if _, err := repo.Get(t.Context(), m.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get after delete: %v", err)
		}
	})

	t.Run("conditional", func(t *testing.T) {
		repo := freshRepo(t)
		m := createNamed(t, repo, "X", "tablet")
		if err := repo.Delete(t.Context(), m.ID, []int64{5}); !errors.Is(err, ErrVersionMismatch) {
			t.Fatalf("stale: %v", err)
		}
		if deletedAt, _ := softDeletedAt(t, m.ID); deletedAt != nil {
			t.Fatal("stale conditional delete must not delete")
		}
		if err := repo.Delete(t.Context(), m.ID, []int64{1}); err != nil {
			t.Fatalf("current: %v", err)
		}
	})

	t.Run("missing and already deleted are not found", func(t *testing.T) {
		repo := freshRepo(t)
		m := createNamed(t, repo, "X", "tablet")
		_ = repo.Delete(t.Context(), m.ID, nil)
		if err := repo.Delete(t.Context(), m.ID, nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("already deleted: %v", err)
		}
		if err := repo.Delete(t.Context(), uuid.New(), nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing: %v", err)
		}
	})

	t.Run("identity and NDC are released for re-creation", func(t *testing.T) {
		repo := freshRepo(t)
		in := Input{Name: "Aspirin", Form: "tablet", Dosage: DosageInput{Value: "100", Unit: "mg"}, NDC: "12345-6789-01"}
		first, err := repo.Create(t.Context(), newMed(t, in))
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Delete(t.Context(), first.ID, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Create(t.Context(), newMed(t, in)); err != nil {
			t.Fatalf("re-create: %v", err)
		}
		var total int
		_ = testPool.QueryRow(t.Context(), "SELECT count(*) FROM medications").Scan(&total)
		if total != 2 {
			t.Errorf("rows = %d, want 2 (deleted row kept for audit)", total)
		}
	})
}

// A delete racing an update holding the same ETag: exactly one succeeds.
func TestPostgres_DeleteRacingUpdate(t *testing.T) {
	for range 20 {
		repo := freshRepo(t)
		m := createNamed(t, repo, "Contended", "tablet")
		spec := mustSpec(t, Input{Name: "Renamed", Form: "tablet", Dosage: DosageInput{Value: "1", Unit: "mg"}})

		var updateErr, deleteErr error
		var wg sync.WaitGroup
		wg.Go(func() { _, updateErr = repo.Update(t.Context(), m.ID, []int64{1}, spec) })
		wg.Go(func() { deleteErr = repo.Delete(t.Context(), m.ID, []int64{1}) })
		wg.Wait()

		if (updateErr == nil) == (deleteErr == nil) {
			t.Fatalf("exactly one must win: update=%v delete=%v", updateErr, deleteErr)
		}
		loser := updateErr
		if loser == nil {
			loser = deleteErr
		}
		if !errors.Is(loser, ErrVersionMismatch) && !errors.Is(loser, ErrNotFound) {
			t.Fatalf("loser error: %v", loser)
		}
	}
}

func TestPostgres_CreateIfAbsent(t *testing.T) {
	repo := freshRepo(t)
	ctx := t.Context()
	amox := Input{Name: "Amoxicillin", Form: "capsule", Dosage: DosageInput{Value: "500", Unit: "mg"}, NDC: "65862-0017-01"}

	first, inserted, err := repo.CreateIfAbsent(ctx, newMed(t, amox))
	if err != nil || !inserted || first.Version != 1 {
		t.Fatalf("new row: %+v, inserted=%v, err=%v", first, inserted, err)
	}

	t.Run("same identity is skipped without an error", func(t *testing.T) {
		if _, inserted, err := repo.CreateIfAbsent(ctx, newMed(t, amox)); err != nil || inserted {
			t.Fatalf("inserted=%v, err=%v", inserted, err)
		}
	})
	t.Run("same NDC on another product is skipped too", func(t *testing.T) {
		other := Input{Name: "Other", Form: "tablet", Dosage: DosageInput{Value: "1", Unit: "mg"}, NDC: amox.NDC}
		if _, inserted, err := repo.CreateIfAbsent(ctx, newMed(t, other)); err != nil || inserted {
			t.Fatalf("inserted=%v, err=%v", inserted, err)
		}
	})
	t.Run("Create still reports the duplicate for the API", func(t *testing.T) {
		if _, err := repo.Create(ctx, newMed(t, amox)); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a soft-deleted row does not block it", func(t *testing.T) {
		if err := repo.Delete(ctx, first.ID, nil); err != nil {
			t.Fatal(err)
		}
		again, inserted, err := repo.CreateIfAbsent(ctx, newMed(t, amox))
		if err != nil || !inserted || again.ID == first.ID {
			t.Fatalf("%+v, inserted=%v, err=%v", again, inserted, err)
		}
	})
	t.Run("CHECK violations are still errors", func(t *testing.T) {
		bad := newMed(t, Input{Name: "Bad", Form: "tablet", Dosage: DosageInput{Value: "1", Unit: "mg"}})
		bad.Form = "gummy" // bypasses domain validation to reach the CHECK constraint
		if _, _, err := repo.CreateIfAbsent(ctx, bad); err == nil {
			t.Fatal("want CHECK violation")
		}
	})

	var active int
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM medications WHERE deleted_at IS NULL").Scan(&active); err != nil || active != 1 {
		t.Fatalf("active rows = %d, err = %v", active, err)
	}
}

// A row lock held by another transaction makes the UPDATE wait, which
// exercises both ways a slow query ends: the server's statement_timeout and
// the request deadline (pgx cancels the query when the context expires).
func TestPostgres_SlowQueryIsErrTimeout(t *testing.T) {
	repo := freshRepo(t)
	m := createNamed(t, repo, "Locked", "tablet")
	lock, err := testPool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Rollback(context.WithoutCancel(t.Context())) })
	if _, err := lock.Exec(t.Context(), "SELECT 1 FROM medications WHERE id = $1 FOR UPDATE", m.ID); err != nil {
		t.Fatal(err)
	}

	cfg := testPool.Config().Copy()
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "200"
	shortStatements, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(shortStatements.Close)

	deadline := func(t *testing.T) context.Context {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		t.Cleanup(cancel)
		return ctx
	}
	tests := []struct {
		name   string
		repo   *PostgresRepository
		ctx    func(*testing.T) context.Context
		wantPg string // SQLSTATE expected in the chain, "" for none
	}{
		{"statement_timeout", NewPostgresRepository(shortStatements), func(t *testing.T) context.Context { return t.Context() }, pgQueryCanceled},
		{"request deadline", repo, deadline, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.repo.Update(tt.ctx(t), m.ID, []int64{1}, m.Spec)
			if !errors.Is(err, ErrTimeout) {
				t.Fatalf("err = %v, want ErrTimeout", err)
			}
			var pgErr *pgconn.PgError
			if tt.wantPg != "" && (!errors.As(err, &pgErr) || pgErr.Code != tt.wantPg) {
				t.Errorf("err = %v, want SQLSTATE %s in the chain", err, tt.wantPg)
			}
		})
	}
}
