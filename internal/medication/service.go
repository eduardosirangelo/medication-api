package medication

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Repository persists medications. It is defined here, by its consumer, so
// the service depends on behaviour rather than on pgx; tests use a fake.
//
// Implementations must return ErrNotFound for missing or soft-deleted rows
// and ErrDuplicate when the active-identity uniqueness rule is violated.
type Repository interface {
	// Create inserts m (ID and Spec set by the caller) and returns the stored
	// row, including the database-assigned version and timestamps.
	Create(ctx context.Context, m Medication) (Medication, error)
	// Get returns the active medication with the given ID.
	Get(ctx context.Context, id uuid.UUID) (Medication, error)

	// TODO(slice-6): List(ctx, ListParams) (Page, error)
	// TODO(slice-7): Update(ctx, id, expectedVersion, Spec) (Medication, error)
	// TODO(slice-8): Delete(ctx, id, expectedVersion *int64) error
}

// Service implements the medication use cases. It owns validation and ID
// generation; the HTTP layer only translates, the repository only stores.
type Service struct {
	repo  Repository
	newID func() (uuid.UUID, error) // replaceable in tests
}

// NewService returns a Service backed by repo.
func NewService(repo Repository) *Service {
	return &Service{repo: repo, newID: NewID}
}

// Create validates and normalises in, assigns a new ID and stores it.
// Errors: *ValidationError, ErrDuplicate, or an internal error.
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

// Get returns the active medication with id, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Medication, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return Medication{}, fmt.Errorf("get medication %s: %w", id, err)
	}
	return m, nil
}
