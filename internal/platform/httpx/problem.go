package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/requestid"
)

// ProblemContentType is the RFC 9457 media type for error responses.
const ProblemContentType = "application/problem+json"

// Problem is an RFC 9457 "Problem Details for HTTP APIs" body.
type Problem struct {
	// Type is a URI reference identifying the problem kind. "about:blank"
	// means "nothing beyond the HTTP status"; Title must then be the status text.
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Instance identifies this occurrence; we use the request path.
	Instance string `json:"instance,omitempty"`

	// Extension members.
	RequestID string       `json:"request_id,omitempty"`
	Errors    []FieldError `json:"errors,omitempty"`
}

// FieldError points at one invalid input field (dot path, e.g. "dosage.unit").
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// NewProblem returns a generic problem for status with an optional detail.
func NewProblem(status int, detail string) Problem {
	return Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	}
}

// WriteProblem writes p as application/problem+json, filling Instance and
// RequestID from the request when they are empty. Detail must never contain
// internal error text (SQL, stack traces): it is shown to clients.
func WriteProblem(w http.ResponseWriter, r *http.Request, p Problem) {
	if p.Instance == "" {
		p.Instance = r.URL.Path
	}
	if p.RequestID == "" {
		p.RequestID = requestid.From(r.Context())
	}
	writeJSON(w, r, p.Status, ProblemContentType, p)
}

// WriteJSON writes v as application/json with the given status.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	writeJSON(w, r, status, "application/json", v)
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, contentType string, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		// Only possible with a programming error (unsupported type); never leak it.
		slog.ErrorContext(r.Context(), "encode response", slog.Any("error", err))
		status = http.StatusInternalServerError
		contentType = ProblemContentType
		body = []byte(`{"type":"about:blank","title":"Internal Server Error","status":500}`)
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body) // client went away: nothing useful to do
}
