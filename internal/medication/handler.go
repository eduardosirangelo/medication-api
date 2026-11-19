package medication

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
)

// basePath is the versioned collection URI.
const basePath = "/v1/medications"

// Handler translates HTTP to service calls and domain results/errors back to
// HTTP. It holds no business rules.
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler returns a Handler for svc.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// Register adds the medication routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+basePath, h.create)
	mux.HandleFunc("GET "+basePath+"/{id}", h.get)
	// TODO(slice-6): GET  /v1/medications        (list, filters, cursor)
	// TODO(slice-7): PUT  /v1/medications/{id}   (If-Match required)
	// TODO(slice-8): DELETE /v1/medications/{id} (soft delete, If-Match optional)
	// TODO: PATCH with application/merge-patch+json (RFC 7396).
}

// create handles POST /v1/medications → 201 + Location + ETag.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	// TODO: honour an Idempotency-Key header so retried POSTs return the
	// original response instead of 409 (store key → response for ~24h).
	var req medicationRequest
	if p := httpx.DecodeJSON(r, &req); p != nil {
		httpx.WriteProblem(w, r, *p)
		return
	}

	m, err := h.svc.Create(r.Context(), req.toInput())
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	w.Header().Set("Location", basePath+"/"+m.ID.String())
	w.Header().Set("ETag", httpx.ETag(m.Version))
	httpx.WriteJSON(w, r, http.StatusCreated, toResponse(m))
}

// get handles GET /v1/medications/{id} → 200 + ETag, or 304 when the client's
// If-None-Match already names the current version.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r)
	if !ok {
		h.writeError(w, r, ErrNotFound)
		return
	}
	m, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	etag := httpx.ETag(m.Version)
	w.Header().Set("ETag", etag)
	// Clients may cache, but must revalidate (cheap with If-None-Match).
	w.Header().Set("Cache-Control", "private, no-cache")
	if httpx.NoneMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toResponse(m))
}

// parseID reads the {id} path value. A malformed ID cannot name an existing
// resource, so callers answer 404 rather than 400: the URI simply doesn't exist.
func parseID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	return id, err == nil
}

// writeError maps domain errors to problem responses. Anything unrecognised is
// a 500 whose details go to the log, never to the client.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		p := httpx.NewProblem(http.StatusUnprocessableEntity, "The request contains invalid fields.")
		for _, f := range verr.Fields {
			p.Errors = append(p.Errors, httpx.FieldError{Field: f.Field, Message: f.Message})
		}
		httpx.WriteProblem(w, r, p)
	case errors.Is(err, ErrNotFound):
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusNotFound, "Medication not found."))
	case errors.Is(err, ErrDuplicate):
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusConflict,
			"An active medication with the same name, dosage and form already exists."))
	case errors.Is(err, ErrVersionMismatch):
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusPreconditionFailed,
			"The medication was modified by another request; fetch it again and retry with the new ETag."))
	case errors.Is(err, context.Canceled):
		// The client went away; nobody will read the response.
		h.log.DebugContext(r.Context(), "request cancelled by client", slog.Any("error", err))
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusServiceUnavailable, ""))
	default:
		h.log.ErrorContext(r.Context(), "unexpected error", slog.Any("error", err))
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusInternalServerError, ""))
	}
}
