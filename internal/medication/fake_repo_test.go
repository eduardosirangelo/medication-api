package medication

import (
	"context"
	"slices"
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
	if err := f.checkUnique(uuid.Nil, m.Spec); err != nil { // a create has no "self" to skip
		return Medication{}, err
	}
	m.Version, m.CreatedAt, m.UpdatedAt = 1, f.now, f.now
	f.rows[m.ID] = m
	return m, nil
}

func (f *fakeRepo) checkUnique(self uuid.UUID, s Spec) error {
	for id, existing := range f.rows {
		switch {
		case id == self:
		case identityKey(existing.Spec) == identityKey(s):
			return ErrDuplicate
		case s.NDC != "" && existing.NDC == s.NDC:
			return ErrDuplicateNDC
		}
	}
	return nil
}

func (f *fakeRepo) Update(_ context.Context, id uuid.UUID, expected []int64, s Spec) (Medication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return Medication{}, f.err
	}
	m, ok := f.rows[id]
	switch {
	case !ok:
		return Medication{}, ErrNotFound
	case !slices.Contains(expected, m.Version):
		return Medication{}, ErrVersionMismatch
	}
	if err := f.checkUnique(id, s); err != nil {
		return Medication{}, err
	}
	m.Spec, m.Version, m.UpdatedAt = s, m.Version+1, m.UpdatedAt.Add(time.Minute)
	f.rows[id] = m
	return m, nil
}

// The fake drops deleted rows outright: callers can't observe the
// difference, and soft-delete SQL is covered by the integration tests.
func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID, expected []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return f.err
	}
	m, ok := f.rows[id]
	switch {
	case !ok:
		return ErrNotFound
	case expected != nil && !slices.Contains(expected, m.Version):
		return ErrVersionMismatch
	}
	delete(f.rows, id)
	return nil
}

func (f *fakeRepo) List(_ context.Context, filter ListFilter, after uuid.UUID, limit int) ([]Medication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var out []Medication
	for _, m := range f.rows {
		if matchesFilter(m, filter) && (after == uuid.Nil || m.ID.String() < after.String()) {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b Medication) int { return strings.Compare(b.ID.String(), a.ID.String()) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func matchesFilter(m Medication, f ListFilter) bool {
	if f.Form != "" && m.Form != f.Form {
		return false
	}
	return strings.Contains(strings.ToLower(m.Name), strings.ToLower(f.NameContains))
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
