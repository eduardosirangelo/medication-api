package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/eduardosirangelo/medication-api/internal/platform/buildinfo"
	"github.com/eduardosirangelo/medication-api/internal/platform/config"
	"github.com/eduardosirangelo/medication-api/internal/platform/health"
)

var httpMethods = []string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

type operation struct {
	// nil when the operation inherits the global security requirement.
	Security *[]map[string][]string `yaml:"security"`
}

// specOperations maps "METHOD /path" to its operation, skipping path-level
// keys such as parameters.
func specOperations(t *testing.T) map[string]operation {
	t.Helper()
	raw, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("openapi.yaml: %v", err)
	}
	ops := map[string]operation{}
	for path, items := range spec.Paths {
		for key, node := range items {
			method := strings.ToUpper(key)
			if !slices.Contains(httpMethods, method) {
				continue
			}
			var op operation
			if err := node.Decode(&op); err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			ops[method+" "+path] = op
		}
	}
	if len(ops) == 0 {
		t.Fatal("no operations parsed from openapi.yaml")
	}
	return ops
}

func testMux() *http.ServeMux {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{Pricing: config.Pricing{Budget: time.Second}}
	return newMux(cfg, log, health.NewReadiness(okPinger{}, time.Second, log), nil, buildinfo.Info{})
}

// matchedPattern is the route ServeMux would pick, or "" when none matches.
func matchedPattern(mux *http.ServeMux, method, path string) string {
	target := strings.ReplaceAll(path, "{id}", "019a9c3e-7a10-7000-8000-000000000001")
	_, pattern := mux.Handler(httptest.NewRequest(method, target, nil)) //nolint:noctx // never sent
	return pattern
}

func TestOpenAPI_EveryOperationIsRouted(t *testing.T) {
	mux := testMux()
	for op := range specOperations(t) {
		method, path, _ := strings.Cut(op, " ")
		if got := matchedPattern(mux, method, path); got != op {
			t.Errorf("%s is documented but routed to %q", op, got)
		}
	}
}

// Catches a route added to a documented path without updating the spec.
func TestOpenAPI_NoUndocumentedMethodsOnDocumentedPaths(t *testing.T) {
	mux := testMux()
	ops := specOperations(t)
	paths := map[string]bool{}
	for op := range ops {
		_, path, _ := strings.Cut(op, " ")
		paths[path] = true
	}
	for path := range paths {
		for _, method := range httpMethods {
			if _, documented := ops[method+" "+path]; !documented && matchedPattern(mux, method, path) != "" {
				t.Errorf("%s %s is served but not in openapi.yaml", method, path)
			}
		}
	}
}

// The spec's `security: []` must agree with what the auth middleware lets through.
func TestOpenAPI_SecurityMatchesPublicPaths(t *testing.T) {
	for op, spec := range specOperations(t) {
		_, path, _ := strings.Cut(op, " ")
		documentedPublic := spec.Security != nil && len(*spec.Security) == 0
		if public := slices.Contains(publicPaths, path); public != documentedPublic {
			t.Errorf("%s: public in middleware = %v, public in spec = %v", op, public, documentedPublic)
		}
	}
}
