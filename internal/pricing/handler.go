package pricing

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/eduardo-sirangelo/medication-api/internal/medication"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
)

// Lookup is the part of medication.Service the prices endpoint needs.
type Lookup interface {
	Get(ctx context.Context, id uuid.UUID) (medication.Medication, error)
}

type Handler struct {
	meds     Lookup
	comparer *Comparer
	log      *slog.Logger
}

func NewHandler(meds Lookup, comparer *Comparer, log *slog.Logger) *Handler {
	return &Handler{meds: meds, comparer: comparer, log: log}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/medications/{id}/prices", h.prices)
}

func (h *Handler) prices(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusNotFound, "Medication not found."))
		return
	}
	quantity, fieldErr := parseQuantity(r.URL.Query())
	if fieldErr != nil {
		p := httpx.NewProblem(http.StatusBadRequest, "The query string contains invalid parameters.")
		p.Errors = []httpx.FieldError{*fieldErr}
		httpx.WriteProblem(w, r, p)
		return
	}

	m, err := h.meds.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if m.NDC == "" {
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusUnprocessableEntity,
			"The medication has no NDC; prices are quoted per NDC."))
		return
	}

	res, err := h.comparer.BestPrice(r.Context(), Request{NDC: string(m.NDC), Quantity: quantity})
	h.logUnavailable(r.Context(), res.Unavailable)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	// Quotes change on every call and are only valid right now.
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, r, http.StatusOK, toPriceResponse(m, quantity, res))
}

// Unknown or repeated parameters are rejected, as on the list endpoint, so a
// typo like ?qty=30 fails loudly instead of being ignored.
func parseQuantity(q url.Values) (int, *httpx.FieldError) {
	for name := range q {
		if name != "quantity" {
			return 0, &httpx.FieldError{Field: name, Message: "unknown query parameter"}
		}
	}
	values := q["quantity"]
	switch {
	case len(values) == 0:
		return 0, &httpx.FieldError{Field: "quantity", Message: "is required"}
	case len(values) > 1:
		return 0, &httpx.FieldError{Field: "quantity", Message: "must be given once"}
	}
	n, err := strconv.Atoi(values[0])
	if err != nil || n < MinQuantity || n > MaxQuantity {
		return 0, &httpx.FieldError{Field: "quantity", Message: "must be a whole number from " +
			strconv.Itoa(MinQuantity) + " to " + strconv.Itoa(MaxQuantity)}
	}
	return n, nil
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, medication.ErrNotFound):
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusNotFound, "Medication not found."))
	case errors.Is(err, ErrNoQuotes):
		w.Header().Set("Retry-After", "1")
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusServiceUnavailable,
			"No pricing network returned a quote within the time budget; retry later."))
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// The caller gave up (client gone or an upstream deadline); nobody reads the reply.
		h.log.DebugContext(r.Context(), "price request abandoned by caller", slog.Any("error", err))
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusServiceUnavailable, ""))
	default:
		h.log.ErrorContext(r.Context(), "unexpected error", slog.Any("error", err))
		httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusInternalServerError, ""))
	}
}

// Warn, not error: a missing network degrades the answer but the request
// still succeeds; a sustained rate is what should alert.
func (h *Handler) logUnavailable(ctx context.Context, unavailable []Unavailable) {
	for _, u := range unavailable {
		h.log.WarnContext(ctx, "pricing network unavailable",
			slog.String("network", u.Network), slog.String("reason", string(u.Reason)), slog.Any("error", u.Err))
	}
}

type priceResponse struct {
	MedicationID string                `json:"medication_id"`
	NDC          string                `json:"ndc"`
	Quantity     int                   `json:"quantity"`
	Currency     string                `json:"currency"`
	Best         quoteResponse         `json:"best"`
	Quotes       []quoteResponse       `json:"quotes"`
	Unavailable  []unavailableResponse `json:"unavailable"`
	Simulated    bool                  `json:"simulated"`
}

type quoteResponse struct {
	Network    string `json:"network"`
	PriceCents int64  `json:"price_cents"`
	LatencyMS  int64  `json:"latency_ms"`
}

type unavailableResponse struct {
	Network string `json:"network"`
	Reason  Reason `json:"reason"`
}

// Only called with at least one quote: BestPrice returns ErrNoQuotes otherwise.
func toPriceResponse(m medication.Medication, quantity int, res Result) priceResponse {
	quotes := make([]quoteResponse, len(res.Quotes))
	for i, q := range res.Quotes {
		quotes[i] = quoteResponse{Network: q.Network, PriceCents: q.PriceCents, LatencyMS: q.Latency.Milliseconds()}
	}
	unavailable := make([]unavailableResponse, len(res.Unavailable)) // [] not null when empty
	for i, u := range res.Unavailable {
		unavailable[i] = unavailableResponse{Network: u.Network, Reason: u.Reason}
	}
	return priceResponse{
		MedicationID: m.ID.String(),
		NDC:          m.NDC.Hyphenated(),
		Quantity:     quantity,
		Currency:     "USD",
		Best:         quotes[0],
		Quotes:       quotes,
		Unavailable:  unavailable,
		Simulated:    true,
	}
}
