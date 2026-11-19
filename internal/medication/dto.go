package medication

import (
	"encoding/json"
	"time"
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

// json.Number keeps the decimal text untouched on its way to the domain.
type dosageRequest struct {
	Value    json.Number `json:"value"`
	Unit     string      `json:"unit"`
	PerValue json.Number `json:"per_value"`
	PerUnit  string      `json:"per_unit"`
}

func (r medicationRequest) toInput() Input {
	return Input{
		Name: r.Name,
		Form: r.Form,
		Dosage: DosageInput{
			Value:    r.Dosage.Value.String(),
			Unit:     r.Dosage.Unit,
			PerValue: r.Dosage.PerValue.String(),
			PerUnit:  r.Dosage.PerUnit,
		},
		NDC:   r.NDC,
		RxCUI: r.RxCUI,
		Route: r.Route,
	}
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
