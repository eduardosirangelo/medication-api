package medication

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
)

// Wire types are the public contract (openapi.yaml). Keeping them apart from
// the domain lets either evolve without breaking the other.

type medicationRequest struct {
	Name   string        `json:"name"`
	Form   string        `json:"form"`
	Dosage dosageRequest `json:"dosage"`
	NDC    string        `json:"ndc"`
	RxCUI  string        `json:"rxcui"`
	Route  string        `json:"route"`
}

type dosageRequest struct {
	Value    decimalField `json:"value"`
	Unit     string       `json:"unit"`
	PerValue decimalField `json:"per_value"`
	PerUnit  string       `json:"per_unit"`
}

// decimalField keeps the number's text untouched on its way to the domain,
// like json.Number, without json.Number's leniency: encoding/json also fills
// a json.Number from a quoted string such as "500".
//
// Other wrong types are recorded rather than returned as decode errors:
// encoding/json drops the field path from errors a custom unmarshaler
// returns, and the 400 must still name the field.
type decimalField struct {
	text string
	// "" for a number or null (null means absent), else the JSON type.
	wrongType string
}

func (d *decimalField) UnmarshalJSON(b []byte) error {
	// The decoder has already validated the syntax, so the first byte is
	// enough to tell the JSON type.
	switch b[0] {
	case 'n':
	case '"':
		d.wrongType = "string"
		return json.Unmarshal(b, &d.text)
	case 't', 'f':
		d.wrongType = "bool"
	case '{':
		d.wrongType = "object"
	case '[':
		d.wrongType = "array"
	default:
		d.text = string(b)
	}
	return nil
}

// input converts the request for the domain, or explains why it can't: a
// wrong JSON type is the wrong shape (400, as for every other field), while
// a number sent as a string is well-formed but against the contract (422,
// listed together with every other invalid field).
func (r medicationRequest) input() (Input, *httpx.Problem) {
	in := Input{
		Name: r.Name,
		Form: r.Form,
		Dosage: DosageInput{
			Value:    r.Dosage.Value.text,
			Unit:     r.Dosage.Unit,
			PerValue: r.Dosage.PerValue.text,
			PerUnit:  r.Dosage.PerUnit,
		},
		NDC:   r.NDC,
		RxCUI: r.RxCUI,
		Route: r.Route,
	}
	var quoted []FieldError
	for _, f := range []struct {
		path string
		d    decimalField
	}{{"dosage.value", r.Dosage.Value}, {"dosage.per_value", r.Dosage.PerValue}} {
		switch f.d.wrongType {
		case "":
		case "string":
			quoted = append(quoted, FieldError{Field: f.path, Message: "must be a JSON number, not a string"})
		default:
			p := httpx.NewProblem(http.StatusBadRequest,
				fmt.Sprintf("Field %q has the wrong JSON type (got %s).", f.path, f.d.wrongType))
			return Input{}, &p
		}
	}
	if len(quoted) > 0 {
		p := validationProblem(withSpecErrors(quoted, in))
		return Input{}, &p
	}
	return in, nil
}

type medicationResponse struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Dosage    dosageResponse `json:"dosage"`
	Form      string         `json:"form"`
	NDC       string         `json:"ndc,omitempty"`
	RxCUI     string         `json:"rxcui,omitempty"`
	Route     string         `json:"route,omitempty"`
	Version   int64          `json:"version"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// next_cursor is always present (null on the last page) so clients can rely
// on the field rather than on its absence.
type listResponse struct {
	Data       []medicationResponse `json:"data"`
	NextCursor *string              `json:"next_cursor"`
}

func toListResponse(page Page, f ListFilter) listResponse {
	resp := listResponse{Data: make([]medicationResponse, len(page.Items))} // [] not null when empty
	for i, m := range page.Items {
		resp.Data[i] = toResponse(m)
	}
	if page.HasMore {
		next := encodeCursor(page.Items[len(page.Items)-1].ID, f)
		resp.NextCursor = &next
	}
	return resp
}

type dosageResponse struct {
	Value    json.Number `json:"value"`
	Unit     string      `json:"unit"`
	PerValue json.Number `json:"per_value,omitempty"`
	PerUnit  string      `json:"per_unit,omitempty"`
}

func toResponse(m Medication) medicationResponse {
	return medicationResponse{
		ID:        m.ID.String(),
		Name:      m.Name,
		Dosage:    toDosageResponse(m.Dosage),
		Form:      string(m.Form),
		NDC:       m.NDC.Hyphenated(),
		RxCUI:     string(m.RxCUI),
		Route:     string(m.Route),
		Version:   m.Version,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func toDosageResponse(d Dosage) dosageResponse {
	resp := dosageResponse{Value: json.Number(d.Value.String()), Unit: string(d.Unit)}
	if d.Per != nil {
		resp.PerValue = json.Number(d.Per.Value.String())
		resp.PerUnit = string(d.Per.Unit)
	}
	return resp
}
