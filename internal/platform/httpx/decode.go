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

// DecodeJSON strictly decodes a single JSON object from the request body into
// dst. On failure it returns a Problem ready to be written:
//
//	415  Content-Type is not application/json (charset, if given, must be UTF-8)
//	413  body exceeds the MaxBodyBytes middleware limit
//	400  empty body, malformed JSON, wrong JSON type for a field, unknown
//	     field, or anything after the first JSON value
//
// 400 means "this is not the shape the endpoint accepts"; semantic problems
// with well-formed input are the handler's to report (422).
func DecodeJSON(r *http.Request, dst any) *Problem {
	if p := requireJSONContentType(r); p != nil {
		return p
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // typos like "dosge" fail loudly instead of being ignored
	dec.UseNumber()             // never round-trip numbers through float64

	if err := dec.Decode(dst); err != nil {
		return decodeProblem(err)
	}
	// Reject trailing data such as `{...}{...}` or `{...} garbage`.
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

// jsonKind reduces UnmarshalTypeError.Value ("number -5", "string") to the
// JSON kind, so client-supplied values are not echoed back.
func jsonKind(v string) string {
	kind, _, _ := strings.Cut(v, " ")
	return kind
}
