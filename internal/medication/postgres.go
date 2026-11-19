package medication

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgUniqueViolation = "23505"

// Each unique constraint maps to its own domain error; any other unique
// violation (e.g. a primary-key collision) is a bug, not a client conflict.
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

func (r *PostgresRepository) Create(ctx context.Context, m Medication) (Medication, error) {
	perValue, perUnit := perArgs(m.Dosage)
	row := r.pool.QueryRow(ctx, `
		INSERT INTO medications (id, name, dosage_value, dosage_unit, dosage_per_value, dosage_per_unit,
		                         form, ndc, rxcui, route)
		VALUES ($1, $2, $3::numeric, $4, $5::numeric, $6, $7, $8, $9, $10)
		RETURNING `+selectColumns,
		m.ID, m.Name, m.Dosage.Value.String(), string(m.Dosage.Unit), perValue, perUnit,
		string(m.Form), nullable(m.NDC), nullable(m.RxCUI), nullable(m.Route),
	)
	created, err := scanMedication(row)
	if err != nil {
		return Medication{}, fmt.Errorf("insert medication: %w", translateUniqueViolation(err))
	}
	return created, nil
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
