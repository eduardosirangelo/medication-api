package medication

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Repository persists medications. It is declared here, by its consumer, so
// the service depends on behaviour rather than on pgx, and tests can use a
// fake. Implementations return ErrNotFound for missing or soft-deleted rows,
// and ErrDuplicate / ErrDuplicateNDC for uniqueness violations.
type Repository interface {
	// Create returns the stored row, including database-assigned version and timestamps.
	Create(ctx context.Context, m Medication) (Medication, error)
	Get(ctx context.Context, id uuid.UUID) (Medication, error)
	// List returns up to limit active medications matching f with ID < after
	// (all when after is uuid.Nil), newest first.
	List(ctx context.Context, f ListFilter, after uuid.UUID, limit int) ([]Medication, error)

	// TODO(slice-7): Update(ctx, id, expectedVersion, Spec) (Medication, error)
	// TODO(slice-8): Delete(ctx, id, expectedVersion *int64) error
}

// Service implements the medication use cases: it owns validation and ID
// generation; the HTTP layer only translates and the repository only stores.
type Service struct {
	repo  Repository
	newID func() (uuid.UUID, error) // a field so tests can make IDs predictable
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, newID: NewID}
}

func (s *Service) Create(ctx context.Context, in Input) (Medication, error) {
	spec, err := NewSpec(in)
	if err != nil {
		return Medication{}, err
	}
	id, err := s.newID()
	if err != nil {
		return Medication{}, fmt.Errorf("generate id: %w", err)
	}
	m, err := s.repo.Create(ctx, Medication{ID: id, Spec: spec})
	if err != nil {
		return Medication{}, fmt.Errorf("create medication: %w", err)
	}
	return m, nil
}

// One extra row is fetched to learn whether another page exists without a
// separate COUNT, which would scan every matching row.
func (s *Service) List(ctx context.Context, p ListParams) (Page, error) {
	items, err := s.repo.List(ctx, p.ListFilter, p.After, p.Limit+1)
	if err != nil {
		return Page{}, fmt.Errorf("list medications: %w", err)
	}
	if len(items) > p.Limit {
		return Page{Items: items[:p.Limit], HasMore: true}, nil
	}
	return Page{Items: items}, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Medication, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return Medication{}, fmt.Errorf("get medication %s: %w", id, err)
	}
	return m, nil
}
