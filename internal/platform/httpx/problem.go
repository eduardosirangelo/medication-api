package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/eduardosirangelo/medication-api/internal/platform/requestid"
)

const ProblemContentType = "application/problem+json"

// Problem is an RFC 9457 problem details body. RequestID and Errors are
// extension members. Detail is shown to clients, so it must never carry
// internal error text.
type Problem struct {
	// "about:blank" means "nothing beyond the HTTP status"; Title must then be
	// the status text.
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Detail    string       `json:"detail,omitempty"`
	Instance  string       `json:"instance,omitempty"`
	RequestID string       `json:"request_id,omitempty"`
	Errors    []FieldError `json:"errors,omitempty"`
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func NewProblem(status int, detail string) Problem {
	return Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail}
}

func WriteProblem(w http.ResponseWriter, r *http.Request, p Problem) {
	if p.Instance == "" {
		p.Instance = r.URL.Path
	}
	if p.RequestID == "" {
		p.RequestID = requestid.From(r.Context())
	}
	writeJSON(w, r, p.Status, ProblemContentType, p)
}

func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	writeJSON(w, r, status, "application/json", v)
}

// Marshalling before writing the header means an encoding bug can still be
// answered with a clean 500 instead of a 200 with a broken body.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, contentType string, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		slog.ErrorContext(r.Context(), "encode response", slog.Any("error", err))
		status, contentType = http.StatusInternalServerError, ProblemContentType
		body = []byte(`{"type":"about:blank","title":"Internal Server Error","status":500}`)
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body) // a failed write means the client is gone; nothing to do
}
