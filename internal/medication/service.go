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

	// TODO(slice-6): List(ctx, ListParams) (Page, error)
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

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Medication, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return Medication{}, fmt.Errorf("get medication %s: %w", id, err)
	}
	return m, nil
}
