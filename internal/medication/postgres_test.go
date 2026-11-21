package medication

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestTranslate(t *testing.T) {
	other := errors.New("connection reset")
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"identity violation", &pgconn.PgError{Code: pgUniqueViolation, ConstraintName: "medications_active_identity_key"}, ErrDuplicate},
		{"ndc violation", &pgconn.PgError{Code: pgUniqueViolation, ConstraintName: "medications_active_ndc_key"}, ErrDuplicateNDC},
		{"statement timeout", &pgconn.PgError{Code: pgQueryCanceled}, ErrTimeout},
		{"request deadline", fmt.Errorf("query: %w", context.DeadlineExceeded), ErrTimeout},
		{"other error unchanged", other, other},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := translate(tt.err); !errors.Is(got, tt.want) {
				t.Fatalf("translate(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestTranslate_KeepsTheCauseForTheLog(t *testing.T) {
	cause := &pgconn.PgError{Code: pgQueryCanceled}
	var pgErr *pgconn.PgError
	if got := translate(cause); !errors.As(got, &pgErr) {
		t.Fatalf("cause lost: %v", got)
	}
}

func TestTranslate_UnknownUniqueConstraintIsNotAConflict(t *testing.T) {
	err := translate(&pgconn.PgError{Code: pgUniqueViolation, ConstraintName: "medications_pkey"})
	if errors.Is(err, ErrDuplicate) || errors.Is(err, ErrDuplicateNDC) {
		t.Fatalf("primary-key collision mapped to a conflict: %v", err)
	}
}
