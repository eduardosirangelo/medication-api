package medication

import (
	"errors"
	"strings"
)

// Domain errors. The service returns them (possibly wrapped); the HTTP layer
// maps them to status codes with errors.Is/As:
//
//	ErrNotFound         → 404
//	ErrDuplicate        → 409
//	ErrVersionMismatch  → 412
//	*ValidationError    → 422
var (
	ErrNotFound        = errors.New("medication not found")
	ErrDuplicate       = errors.New("an active medication with the same name, dosage and form already exists")
	ErrVersionMismatch = errors.New("medication was modified by another request")
)

// FieldError describes one invalid input field. Field is the dotted path of
// the API field (e.g. "dosage.per_unit"), so clients can attach the message to
// the right form control.
type FieldError struct {
	Field   string
	Message string
}

// ValidationError carries every problem found in an input, not just the first,
// so clients can fix them all in one round trip.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Field + ": " + f.Message
	}
	return "invalid medication: " + strings.Join(parts, "; ")
}

func (e *ValidationError) add(field, msg string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Message: msg})
}
