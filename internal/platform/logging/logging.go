// Package logging builds the process-wide structured JSON logger.
package logging

import (
	"io"
	"log/slog"
)

// ServiceName is attached to every log line so aggregated logs can be filtered.
const ServiceName = "medication-api"

// New returns a JSON slog logger writing to w at the given minimum level.
// Request/response bodies must never be logged through it (see README).
func New(w io.Writer, level slog.Leveler) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h).With(slog.String("service", ServiceName))
}
