package pricing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/eduardosirangelo/medication-api/internal/medication"
	"github.com/eduardosirangelo/medication-api/internal/platform/httpx"
)

var (
	withNDC    = uuid.MustParse("019a9c3e-7a10-7000-8000-000000000001")
	withoutNDC = uuid.MustParse("019a9c3e-7a10-7000-8000-000000000002")
	brokenID   = uuid.MustParse("019a9c3e-7a10-7000-8000-000000000003")
	slowID     = uuid.MustParse("019a9c3e-7a10-7000-8000-000000000004")
)

type fakeLookup struct{}

func (fakeLookup) Get(_ context.Context, id uuid.UUID) (medication.Medication, error) {
	switch id {
	case withNDC:
		return medication.Medication{ID: id, Spec: medication.Spec{Name: "Amoxicillin", NDC: "00093415573"}}, nil
	case withoutNDC:
		return medication.Medication{ID: id, Spec: medication.Spec{Name: "Ibuprofen"}}, nil
	case brokenID:
		return medication.Medication{}, errors.New("connection reset")
	case slowID:
		return medication.Medication{}, fmt.Errorf("select medication: %w", medication.ErrTimeout)
	default:
		return medication.Medication{}, medication.ErrNotFound
	}
}

func serve(t *testing.T, networks []Network, target string) *httptest.ResponseRecorder {
	t.Helper()
	return serveWithLog(t, slog.New(slog.NewTextHandler(io.Discard, nil)), networks, target)
}

func serveWithLog(t *testing.T, log *slog.Logger, networks []Network, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(fakeLookup{}, NewComparer(networks, testBudget), log).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))
	return rec
}

func pricesURL(id uuid.UUID, query string) string {
	return "/v1/medications/" + id.String() + "/prices" + query
}

func TestPrices_OK(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := serve(t, []Network{
			fakeNetwork{name: "a", latency: 40 * time.Millisecond, price: 1200},
			fakeNetwork{name: "b", latency: 80 * time.Millisecond, price: 990},
			fakeNetwork{name: "c", latency: time.Second, price: 100},
		}, pricesURL(withNDC, "?quantity=30"))

		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status %d, Cache-Control %q: %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
		}
		var got priceResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := priceResponse{
			MedicationID: withNDC.String(), NDC: "00093-4155-73", Quantity: 30, Currency: "USD",
			Best: quoteResponse{Network: "b", PriceCents: 990, LatencyMS: 80},
			Quotes: []quoteResponse{
				{Network: "b", PriceCents: 990, LatencyMS: 80},
				{Network: "a", PriceCents: 1200, LatencyMS: 40},
			},
			Unavailable: []unavailableResponse{{Network: "c", Reason: ReasonTimeout}},
			Simulated:   true,
		}
		if !equalJSON(t, got, want) {
			t.Errorf("got  %s\nwant %+v", rec.Body, want)
		}
	})
}

func TestPrices_UnavailableIsAnEmptyArrayNotNull(t *testing.T) {
	rec := serve(t, []Network{fakeNetwork{name: "a", price: 100}}, pricesURL(withNDC, "?quantity=1"))
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["unavailable"]) != "[]" {
		t.Errorf("unavailable = %s", raw["unavailable"])
	}
}

func TestPrices_Errors(t *testing.T) {
	ok := []Network{fakeNetwork{name: "a", price: 100}}
	down := []Network{fakeNetwork{name: "a", err: errors.New("down")}}
	tests := map[string]struct {
		networks []Network
		target   string
		status   int
		field    string
	}{
		"missing quantity":    {ok, pricesURL(withNDC, ""), 400, "quantity"},
		"zero quantity":       {ok, pricesURL(withNDC, "?quantity=0"), 400, "quantity"},
		"too large":           {ok, pricesURL(withNDC, "?quantity=1001"), 400, "quantity"},
		"not a number":        {ok, pricesURL(withNDC, "?quantity=ten"), 400, "quantity"},
		"fractional":          {ok, pricesURL(withNDC, "?quantity=1.5"), 400, "quantity"},
		"repeated":            {ok, pricesURL(withNDC, "?quantity=1&quantity=2"), 400, "quantity"},
		"unknown parameter":   {ok, pricesURL(withNDC, "?quantity=1&qty=2"), 400, "qty"},
		"malformed id":        {ok, "/v1/medications/not-a-uuid/prices?quantity=1", 404, ""},
		"unknown medication":  {ok, pricesURL(uuid.New(), "?quantity=1"), 404, ""},
		"no NDC":              {ok, pricesURL(withoutNDC, "?quantity=1"), 422, ""},
		"all networks failed": {down, pricesURL(withNDC, "?quantity=1"), 503, ""},
		"lookup failure":      {ok, pricesURL(brokenID, "?quantity=1"), 500, ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := serve(t, tt.networks, tt.target)
			if rec.Code != tt.status || rec.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status %d (%s): %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
			}
			var p httpx.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			if tt.field != "" && (len(p.Errors) != 1 || p.Errors[0].Field != tt.field) {
				t.Errorf("errors = %+v, want field %s", p.Errors, tt.field)
			}
		})
	}
}

func TestPrices_AllUnavailableAsksClientToRetry(t *testing.T) {
	rec := serve(t, []Network{fakeNetwork{name: "a", err: errors.New("down")}}, pricesURL(withNDC, "?quantity=1"))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestPrices_LookupTimeoutAsksClientToRetry(t *testing.T) {
	rec := serve(t, []Network{fakeNetwork{name: "a", price: 100}}, pricesURL(slowID, "?quantity=1"))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("status %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

// A network missing the budget is routine; one failing deserves attention.
func TestPrices_UnavailableNetworkLogLevels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		log := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		networks := []Network{
			fakeNetwork{name: "ok", price: 100},
			fakeNetwork{name: "slow", price: 90, latency: 2 * testBudget},
			fakeNetwork{name: "broken", err: errors.New("bad gateway")},
		}
		if rec := serveWithLog(t, log, networks, pricesURL(withNDC, "?quantity=1")); rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}

		levels := map[string]string{}
		for line := range strings.Lines(logs.String()) {
			var entry struct{ Level, Network string }
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatal(err)
			}
			levels[entry.Network] = entry.Level
		}
		if levels["slow"] != "DEBUG" || levels["broken"] != "WARN" || len(levels) != 2 {
			t.Fatalf("levels by network = %v; want slow DEBUG, broken WARN", levels)
		}
	})
}

func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		t.Fatal(errA, errB)
	}
	return string(ja) == string(jb)
}
