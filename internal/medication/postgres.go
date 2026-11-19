package medication

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Constraint and error codes the repository translates into domain errors.
const (
	uniqueActiveIdentity = "medications_active_identity_key"
	pgUniqueViolation    = "23505"
)

// PostgresRepository implements Repository with pgx.
//
// Amounts cross the boundary as text (`$n::numeric` in, `col::text` out) so
// the exact decimal never passes through a float or a driver-specific type.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository returns a repository using pool.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

var _ Repository = (*PostgresRepository)(nil)

// selectColumns must stay in the order scanMedication reads them.
const selectColumns = `id, name, dosage_value::text, dosage_unit, dosage_per_value::text,
	dosage_per_unit, form, version, created_at, updated_at`

func (r *PostgresRepository) Create(ctx context.Context, m Medication) (Medication, error) {
	perValue, perUnit := perArgs(m.Dosage)
	row := r.pool.QueryRow(ctx, `
		INSERT INTO medications (id, name, dosage_value, dosage_unit, dosage_per_value, dosage_per_unit, form)
		VALUES ($1, $2, $3::numeric, $4, $5::numeric, $6, $7)
		RETURNING `+selectColumns,
		m.ID, m.Name, m.Dosage.Value.String(), string(m.Dosage.Unit), perValue, perUnit, string(m.Form),
	)
	created, err := scanMedication(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == uniqueActiveIdentity {
			return Medication{}, ErrDuplicate
		}
		return Medication{}, fmt.Errorf("insert medication: %w", err)
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

// perArgs returns the nullable per_value/per_unit query arguments.
func perArgs(d Dosage) (value, unit *string) {
	if d.Per == nil {
		return nil, nil
	}
	v, u := d.Per.Value.String(), string(d.Per.Unit)
	return &v, &u
}

// scanMedication reads one row in selectColumns order and converts it to the
// domain type. A value the domain rejects means the row violates invariants
// the CHECK constraints should have prevented: it is reported, not hidden.
func scanMedication(row pgx.Row) (Medication, error) {
	var (
		m                 Medication
		value, unit, form string
		perValue, perUnit *string
	)
	err := row.Scan(&m.ID, &m.Name, &value, &unit, &perValue, &perUnit, &form,
		&m.Version, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return Medication{}, err
	}

	var ok bool
	if m.Dosage.Value, err = ParseAmount(value); err != nil {
		return Medication{}, corruptRow(m.ID, "dosage_value", err)
	}
	if m.Dosage.Unit, ok = ParseUnit(unit); !ok {
		return Medication{}, corruptRow(m.ID, "dosage_unit", nil)
	}
	if m.Form, ok = ParseForm(form); !ok {
		return Medication{}, corruptRow(m.ID, "form", nil)
	}
	if perValue != nil && perUnit != nil {
		per := Quantity{}
		if per.Value, err = ParseAmount(*perValue); err != nil {
			return Medication{}, corruptRow(m.ID, "dosage_per_value", err)
		}
		if per.Unit, ok = ParseUnit(*perUnit); !ok {
			return Medication{}, corruptRow(m.ID, "dosage_per_unit", nil)
		}
		m.Dosage.Per = &per
	}
	m.CreatedAt, m.UpdatedAt = m.CreatedAt.UTC(), m.UpdatedAt.UTC()
	return m, nil
}

func corruptRow(id uuid.UUID, column string, cause error) error {
	return fmt.Errorf("medication %s: invalid %s in database: %w", id, column, errors.Join(errCorruptRow, cause))
}

var errCorruptRow = errors.New("row violates domain invariants")
