// Package logging builds the process-wide structured JSON logger.
package logging

import (
	"context"
	"io"
	"log/slog"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/requestid"
)

// ServiceName is attached to every log line so aggregated logs can be filtered.
const ServiceName = "medication-api"

// New returns a JSON slog logger writing to w at the given minimum level.
// Calls made with a context (InfoContext, ErrorContext, ...) automatically get
// the request_id attribute. Request/response bodies must never be logged.
func New(w io.Writer, level slog.Leveler) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(contextHandler{h}).With(slog.String("service", ServiceName))
}

// contextHandler enriches records with values carried in the context.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := requestid.From(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs and WithGroup must re-wrap, otherwise logger.With(...) would return
// the bare inner handler and silently drop the request_id enrichment.
func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
