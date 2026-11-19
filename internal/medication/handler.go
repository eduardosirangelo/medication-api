package medication

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
)

const basePath = "/v1/medications"

// Handler translates HTTP to service calls and results back to HTTP. It holds
// no business rules.
type Handler struct {
	svc *Service
	log *slog.Logger
}

func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+basePath, h.create)
	mux.HandleFunc("GET "+basePath, h.list)
	mux.HandleFunc("GET "+basePath+"/{id}", h.get)
	// TODO: ndc and rxcui filters on the list (exact match, already indexed for ndc).
	mux.HandleFunc("PUT "+basePath+"/{id}", h.replace)
	mux.HandleFunc("DELETE "+basePath+"/{id}", h.remove)
	// TODO: POST /v1/medications/{id}/restore for soft-deleted rows, and a
	// retention job that purges them after the audit period.
	// TODO: PATCH with application/merge-patch+json (RFC 7396).
	// TODO: /v1/medications/{id}/products child resource holding every NDC
	// (one per manufacturer/package) for this clinical drug.
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	// TODO: honour Idempotency-Key so a retried POST replays the original
	// response instead of returning 409 (store key → response for ~24h).
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

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	params, err := parseListQuery(r.URL.Query())
	if err != nil {
		h.writeQueryError(w, r, err)
		return
	}
	page, err := h.svc.List(r.Context(), params)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	resp := toListResponse(page, params.ListFilter)
	if resp.NextCursor != nil {
		w.Header().Set("Link", nextLink(r.URL.Query(), *resp.NextCursor))
	}
	httpx.WriteJSON(w, r, http.StatusOK, resp)
}

// RFC 8288 Link lets generic clients paginate without knowing our body shape.
func nextLink(q url.Values, cursor string) string {
	q.Set("cursor", cursor)
	return "<" + basePath + "?" + q.Encode() + `>; rel="next"`
}

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
	// Cacheable, but always revalidated — cheap thanks to If-None-Match.
	w.Header().Set("Cache-Control", "private, no-cache")
	if httpx.NoneMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toResponse(m))
}

// If-Match is required so a client can never overwrite a change it hasn't
// seen (lost update). "*" is refused too: it would match any version and
// defeat that purpose.
func (h *Handler) replace(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r)
	if !ok {
		h.writeError(w, r, ErrNotFound)
		return
	}
	ifMatch := strings.TrimSpace(r.Header.Get("If-Match"))
	if ifMatch == "" || ifMatch == "*" {
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusPreconditionRequired,
			"An If-Match header with the ETag of the current version is required; fetch the medication first."))
		return
	}
	var req medicationRequest
	if p := httpx.DecodeJSON(r, &req); p != nil {
		httpx.WriteProblem(w, r, *p)
		return
	}
	m, err := h.svc.Replace(r.Context(), id, httpx.IfMatchVersions(ifMatch), req.toInput())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("ETag", httpx.ETag(m.Version))
	httpx.WriteJSON(w, r, http.StatusOK, toResponse(m))
}

// If-Match is optional on DELETE: deleting is not a lost-update risk the way
// overwriting is, but a client that sends it gets the same protection as PUT.
func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r)
	if !ok {
		h.writeError(w, r, ErrNotFound)
		return
	}
	if err := h.deleteWithCondition(r, id); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteWithCondition(r *http.Request, id uuid.UUID) error {
	ifMatch := strings.TrimSpace(r.Header.Get("If-Match"))
	if ifMatch == "" || ifMatch == "*" {
		return h.svc.Delete(r.Context(), id)
	}
	return h.svc.DeleteIfVersion(r.Context(), id, httpx.IfMatchVersions(ifMatch))
}

// A malformed ID can't name an existing resource, so it is a 404 like any
// other missing URI, and the database is never asked.
func parseID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	return id, err == nil
}

var conflictDetails = map[error]string{
	ErrDuplicate:    "An active medication with the same name, dosage and form already exists.",
	ErrDuplicateNDC: "An active medication with the same NDC already exists.",
}

func isConflict(err error) bool { return conflictDetail(err) != "" }

func conflictDetail(err error) string {
	for sentinel, detail := range conflictDetails {
		if errors.Is(err, sentinel) {
			return detail
		}
	}
	return ""
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		httpx.WriteProblem(w, r, validationProblem(verr))
	case errors.Is(err, ErrNotFound):
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusNotFound, "Medication not found."))
	case isConflict(err):
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusConflict, conflictDetail(err)))
	case errors.Is(err, ErrVersionMismatch):
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusPreconditionFailed,
			"The medication was modified by another request; fetch it again and retry with the new ETag."))
	case errors.Is(err, context.Canceled):
		// The client went away: not an operational error, and nobody reads the reply.
		h.log.DebugContext(r.Context(), "request cancelled by client", slog.Any("error", err))
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusServiceUnavailable, ""))
	default:
		// Internals go to the log, never to the client.
		h.log.ErrorContext(r.Context(), "unexpected error", slog.Any("error", err))
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusInternalServerError, ""))
	}
}

func (h *Handler) writeQueryError(w http.ResponseWriter, r *http.Request, err error) {
	var verr *ValidationError
	if !errors.As(err, &verr) {
		h.writeError(w, r, err)
		return
	}
	p := httpx.NewProblem(http.StatusBadRequest, "The query string contains invalid parameters.")
	p.Errors = toProblemFields(verr)
	httpx.WriteProblem(w, r, p)
}

func validationProblem(verr *ValidationError) httpx.Problem {
	p := httpx.NewProblem(http.StatusUnprocessableEntity, "The request contains invalid fields.")
	p.Errors = toProblemFields(verr)
	return p
}

func toProblemFields(verr *ValidationError) []httpx.FieldError {
	fields := make([]httpx.FieldError, len(verr.Fields))
	for i, f := range verr.Fields {
		fields[i] = httpx.FieldError{Field: f.Field, Message: f.Message}
	}
	return fields
}
