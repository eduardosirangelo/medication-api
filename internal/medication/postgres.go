package medication

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgUniqueViolation = "23505"

// Each unique constraint maps to its own domain error; any other unique
// violation (e.g. a primary-key collision) is a bug, not a client conflict.
//
// Identity is name + dosage + form because an entry is a clinical drug, like
// RxNorm's (ingredient + strength + dose form), not a package: ndc is just one
// representative package code, and other packages belong in a child resource.
var uniqueConstraintErrors = map[string]error{
	"medications_active_identity_key": ErrDuplicate,
	"medications_active_ndc_key":      ErrDuplicateNDC,
}

var errCorruptRow = errors.New("row violates domain invariants")

// PostgresRepository implements Repository with pgx. Amounts cross the
// boundary as text (`$n::numeric` in, `col::text` out) so exact decimals never
// pass through a float or a driver-specific type.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

var _ Repository = (*PostgresRepository)(nil)

// Must stay in the order of medicationRow.scanTargets.
const selectColumns = `id, name, dosage_value::text, dosage_unit, dosage_per_value::text,
	dosage_per_unit, form, ndc, rxcui, route, version, created_at, updated_at`

const insertMedication = `
	INSERT INTO medications (id, name, dosage_value, dosage_unit, dosage_per_value, dosage_per_unit,
	                         form, ndc, rxcui, route)
	VALUES ($1, $2, $3::numeric, $4, $5::numeric, $6, $7, $8, $9, $10)`

func insertArgs(m Medication) []any {
	perValue, perUnit := perArgs(m.Dosage)
	return []any{
		m.ID, m.Name, m.Dosage.Value.String(), string(m.Dosage.Unit), perValue, perUnit,
		string(m.Form), nullable(m.NDC), nullable(m.RxCUI), nullable(m.Route),
	}
}

func (r *PostgresRepository) Create(ctx context.Context, m Medication) (Medication, error) {
	row := r.pool.QueryRow(ctx, insertMedication+` RETURNING `+selectColumns, insertArgs(m)...)
	created, err := scanMedication(row)
	if err != nil {
		return Medication{}, fmt.Errorf("insert medication: %w", translateUniqueViolation(err))
	}
	return created, nil
}

// CreateIfAbsent inserts m unless an active row already has its identity or
// NDC, and reports whether it did. Only the seeder uses it: an API client
// needs the 409 that Create returns, while a re-run seed would otherwise make
// PostgreSQL log one unique-violation ERROR per existing row. With no
// conflict target, DO NOTHING covers every unique index, including the
// partial ones that ignore deleted rows.
func (r *PostgresRepository) CreateIfAbsent(ctx context.Context, m Medication) (Medication, bool, error) {
	row := r.pool.QueryRow(ctx, insertMedication+` ON CONFLICT DO NOTHING RETURNING `+selectColumns, insertArgs(m)...)
	created, err := scanMedication(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Medication{}, false, nil
	case err != nil:
		return Medication{}, false, fmt.Errorf("insert medication: %w", err)
	}
	return created, true, nil
}

// The version check lives in the UPDATE's WHERE clause so checking and
// writing are one atomic step: two concurrent writers holding the same ETag
// can never both succeed.
func (r *PostgresRepository) Update(ctx context.Context, id uuid.UUID, expectedVersions []int64, spec Spec) (Medication, error) {
	perValue, perUnit := perArgs(spec.Dosage)
	row := r.pool.QueryRow(ctx, `
		UPDATE medications
		SET name = $3, dosage_value = $4::numeric, dosage_unit = $5,
		    dosage_per_value = $6::numeric, dosage_per_unit = $7, form = $8,
		    ndc = $9, rxcui = $10, route = $11,
		    version = version + 1, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL AND version = ANY($2::bigint[])
		RETURNING `+selectColumns,
		id, expectedVersions, spec.Name, spec.Dosage.Value.String(), string(spec.Dosage.Unit),
		perValue, perUnit, string(spec.Form), nullable(spec.NDC), nullable(spec.RxCUI), nullable(spec.Route),
	)
	updated, err := scanMedication(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Medication{}, r.whyUnchanged(ctx, id)
	}
	if err != nil {
		return Medication{}, fmt.Errorf("update medication: %w", translateUniqueViolation(err))
	}
	return updated, nil
}

// Soft delete keeps the row for audit and lets the partial unique indexes
// release its name/dosage/form and NDC, so the product can be re-created.
// The version is bumped so any ETag a client still holds becomes stale.
// A static "($2 IS NULL OR ...)" is fine here, unlike in List: the primary
// key alone already picks a single row.
func (r *PostgresRepository) Delete(ctx context.Context, id uuid.UUID, expectedVersions []int64) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE medications
		SET deleted_at = now(), updated_at = now(), version = version + 1
		WHERE id = $1 AND deleted_at IS NULL
		  AND ($2::bigint[] IS NULL OR version = ANY($2::bigint[]))`,
		id, expectedVersions)
	if err != nil {
		return fmt.Errorf("delete medication: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.whyUnchanged(ctx, id)
	}
	return nil
}

// Only reached on the failure path, so the happy path stays one round trip.
func (r *PostgresRepository) whyUnchanged(ctx context.Context, id uuid.UUID) error {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM medications WHERE id = $1 AND deleted_at IS NULL)`, id).Scan(&exists)
	switch {
	case err != nil:
		return fmt.Errorf("check medication existence: %w", err)
	case exists:
		return ErrVersionMismatch
	default:
		return ErrNotFound
	}
}

func (r *PostgresRepository) Get(ctx context.Context, id uuid.UUID) (Medication, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+selectColumns+`
		FROM medications
		WHERE id = $1 AND deleted_at IS NULL`, id)
	m, err := scanMedication(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Medication{}, ErrNotFound
	}
	if err != nil {
		return Medication{}, fmt.Errorf("select medication: %w", err)
	}
	return m, nil
}

func (r *PostgresRepository) List(ctx context.Context, f ListFilter, after uuid.UUID, limit int) ([]Medication, error) {
	query, args := buildListQuery(f, after, limit)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list medications: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Medication, error) {
		return scanMedication(row)
	})
	if err != nil {
		return nil, fmt.Errorf("list medications: %w", err)
	}
	return items, nil
}

// Only the active conditions are added. A static query with
// "($n IS NULL OR col = $n)" would be simpler, but once PostgreSQL switches a
// prepared statement to a generic plan it can no longer use the form or
// trigram indexes for those branches.
func buildListQuery(f ListFilter, after uuid.UUID, limit int) (string, []any) {
	var (
		where = []string{"deleted_at IS NULL"}
		args  []any
	)
	addArg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if f.Form != "" {
		where = append(where, "form = "+addArg(string(f.Form)))
	}
	if f.NameContains != "" {
		where = append(where, "name ILIKE "+addArg("%"+escapeLike(f.NameContains)+"%"))
	}
	if after != uuid.Nil {
		where = append(where, "id < "+addArg(after))
	}
	query := "SELECT " + selectColumns + " FROM medications WHERE " + strings.Join(where, " AND ") +
		" ORDER BY id DESC LIMIT " + addArg(limit)
	return query, args
}

// User input must match literally: without escaping, "50%" or "a_b" would act
// as wildcards. Backslash is PostgreSQL's default LIKE escape character.
func escapeLike(s string) string {
	return likeEscaper.Replace(s)
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func translateUniqueViolation(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		if domainErr, ok := uniqueConstraintErrors[pgErr.ConstraintName]; ok {
			return domainErr
		}
	}
	return err
}

func perArgs(d Dosage) (value, unit *string) {
	if d.Per == nil {
		return nil, nil
	}
	v, u := d.Per.Value.String(), string(d.Per.Unit)
	return &v, &u
}

func nullable[T ~string](v T) *string {
	if v == "" {
		return nil
	}
	s := string(v)
	return &s
}

type medicationRow struct {
	id                   uuid.UUID
	name                 string
	value, unit, form    string
	perValue, perUnit    *string
	ndc, rxcui, route    *string
	version              int64
	createdAt, updatedAt time.Time
}

func (r *medicationRow) scanTargets() []any {
	return []any{&r.id, &r.name, &r.value, &r.unit, &r.perValue, &r.perUnit, &r.form,
		&r.ndc, &r.rxcui, &r.route, &r.version, &r.createdAt, &r.updatedAt}
}

func scanMedication(row pgx.Row) (Medication, error) {
	var r medicationRow
	if err := row.Scan(r.scanTargets()...); err != nil {
		return Medication{}, err
	}
	return r.toDomain()
}

// Values the domain rejects mean the row breaks invariants the CHECK
// constraints should have prevented; that is reported, never hidden.
func (r *medicationRow) toDomain() (Medication, error) {
	dosage, err := r.dosage()
	if err != nil {
		return Medication{}, r.corrupt(err)
	}
	form, ok := ParseForm(r.form)
	if !ok {
		return Medication{}, r.corrupt(fmt.Errorf("form %q", r.form))
	}
	ids, err := r.identifiers()
	if err != nil {
		return Medication{}, r.corrupt(err)
	}
	return Medication{
		ID: r.id,
		Spec: Spec{
			Name: r.name, Dosage: dosage, Form: form,
			NDC: ids.NDC, RxCUI: ids.RxCUI, Route: ids.Route,
		},
		Version: r.version,
		// pgx returns timestamps in the process's local zone.
		CreatedAt: r.createdAt.UTC(),
		UpdatedAt: r.updatedAt.UTC(),
	}, nil
}

func (r *medicationRow) dosage() (Dosage, error) {
	value, unit, err := parseQuantity(r.value, r.unit)
	if err != nil {
		return Dosage{}, err
	}
	d := Dosage{Value: value, Unit: unit}
	if r.perValue != nil && r.perUnit != nil {
		perValue, perUnit, err := parseQuantity(*r.perValue, *r.perUnit)
		if err != nil {
			return Dosage{}, err
		}
		d.Per = &Quantity{Value: perValue, Unit: perUnit}
	}
	return d, nil
}

func parseQuantity(value, unit string) (Amount, Unit, error) {
	a, err := ParseAmount(value)
	if err != nil {
		return 0, "", fmt.Errorf("amount %q: %w", value, err)
	}
	u, ok := ParseUnit(unit)
	if !ok {
		return 0, "", fmt.Errorf("unit %q", unit)
	}
	return a, u, nil
}

func (r *medicationRow) identifiers() (Spec, error) {
	var s Spec
	var err error
	if r.ndc != nil {
		if s.NDC, err = ParseNDC(*r.ndc); err != nil {
			return Spec{}, fmt.Errorf("ndc %q: %w", *r.ndc, err)
		}
	}
	if r.rxcui != nil {
		if s.RxCUI, err = ParseRxCUI(*r.rxcui); err != nil {
			return Spec{}, fmt.Errorf("rxcui %q: %w", *r.rxcui, err)
		}
	}
	if r.route != nil {
		var ok bool
		if s.Route, ok = ParseRoute(*r.route); !ok {
			return Spec{}, fmt.Errorf("route %q", *r.route)
		}
	}
	return s, nil
}

func (r *medicationRow) corrupt(cause error) error {
	return fmt.Errorf("medication %s: %w: %w", r.id, errCorruptRow, cause)
}
