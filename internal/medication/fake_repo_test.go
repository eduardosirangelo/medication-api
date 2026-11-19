package medication

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository that mimics the database rules that
// matter to callers: version/timestamps on insert, case-insensitive active
// uniqueness, ErrNotFound. Integration tests cover the real SQL.
type fakeRepo struct {
	mu    sync.Mutex
	rows  map[uuid.UUID]Medication
	now   time.Time
	err   error // if set, every call fails with it
	calls int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		rows: map[uuid.UUID]Medication{},
		now:  time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
	}
}

var _ Repository = (*fakeRepo)(nil)

func identityKey(s Spec) string {
	return strings.ToLower(s.Name) + "|" + s.Dosage.String() + "|" + string(s.Form)
}

func (f *fakeRepo) Create(_ context.Context, m Medication) (Medication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return Medication{}, f.err
	}
	for _, existing := range f.rows {
		if identityKey(existing.Spec) == identityKey(m.Spec) {
			return Medication{}, ErrDuplicate
		}
		if m.NDC != "" && existing.NDC == m.NDC {
			return Medication{}, ErrDuplicateNDC
		}
	}
	m.Version, m.CreatedAt, m.UpdatedAt = 1, f.now, f.now
	f.rows[m.ID] = m
	return m, nil
}

func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (Medication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return Medication{}, f.err
	}
	m, ok := f.rows[id]
	if !ok {
		return Medication{}, ErrNotFound
	}
	return m, nil
}
