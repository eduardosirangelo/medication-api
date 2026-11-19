package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// 400 means "not the shape this endpoint accepts"; well-formed input with
// invalid content is the handler's to report as 422.
func DecodeJSON(r *http.Request, dst any) *Problem {
	if p := requireJSONContentType(r); p != nil {
		return p
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // a typo like "dosge" must fail, not be ignored
	dec.UseNumber()             // numbers must never round-trip through float64

	if err := dec.Decode(dst); err != nil {
		return decodeProblem(err)
	}
	// Without this, `{...}{...}` or `{...} garbage` would be half-read silently.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if p := maxBytesProblem(err); p != nil {
			return p
		}
		p := NewProblem(http.StatusBadRequest, "Request body must contain a single JSON object.")
		return &p
	}
	return nil
}

func requireJSONContentType(r *http.Request) *Problem {
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err == nil && mt == "application/json" {
		if cs, ok := params["charset"]; !ok || strings.EqualFold(cs, "utf-8") {
			return nil
		}
	}
	p := NewProblem(http.StatusUnsupportedMediaType, "Content-Type must be application/json.")
	return &p
}

func decodeProblem(err error) *Problem {
	if p := maxBytesProblem(err); p != nil {
		return p
	}

	var (
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
		detail    string
	)
	switch {
	case errors.Is(err, io.EOF):
		detail = "Request body must not be empty."
	case errors.As(err, &syntaxErr):
		detail = fmt.Sprintf("Malformed JSON at byte offset %d.", syntaxErr.Offset)
	case errors.Is(err, io.ErrUnexpectedEOF):
		detail = "Malformed JSON: unexpected end of input."
	case errors.As(err, &typeErr):
		if typeErr.Field == "" {
			detail = "Request body must be a JSON object."
		} else {
			detail = fmt.Sprintf("Field %q has the wrong JSON type (got %s).", typeErr.Field, jsonKind(typeErr.Value))
		}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		// encoding/json has no typed error for this; the message is stable API.
		detail = fmt.Sprintf("Unknown field %s.", strings.TrimPrefix(err.Error(), "json: unknown field "))
	default:
		detail = "Malformed JSON."
	}
	p := NewProblem(http.StatusBadRequest, detail)
	return &p
}

func maxBytesProblem(err error) *Problem {
	var mbe *http.MaxBytesError
	if !errors.As(err, &mbe) {
		return nil
	}
	p := NewProblem(http.StatusRequestEntityTooLarge,
		fmt.Sprintf("Request body must not exceed %d bytes.", mbe.Limit))
	return &p
}

// Only the JSON kind is reported, so client-supplied values are never echoed.
func jsonKind(v string) string {
	kind, _, _ := strings.Cut(v, " ")
	return kind
}
