package medication

import (
	"encoding/json"
	"time"
)

// Wire types. The domain has no JSON tags: these structs are the public
// contract (documented in openapi.yaml) and can evolve independently.

type medicationRequest struct {
	Name   string        `json:"name"`
	Form   string        `json:"form"`
	Dosage dosageRequest `json:"dosage"`
}

// Amounts are json.Number so the decimal text reaches the domain untouched.
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
	}
}

type medicationResponse struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Dosage    dosageResponse `json:"dosage"`
	Form      string         `json:"form"`
	Version   int64          `json:"version"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Numbers are emitted as JSON numbers in canonical decimal form (500, 2.5).
type dosageResponse struct {
	Value    json.Number `json:"value"`
	Unit     string      `json:"unit"`
	PerValue json.Number `json:"per_value,omitempty"`
	PerUnit  string      `json:"per_unit,omitempty"`
}

func toResponse(m Medication) medicationResponse {
	d := dosageResponse{
		Value: json.Number(m.Dosage.Value.String()),
		Unit:  string(m.Dosage.Unit),
	}
	if m.Dosage.Per != nil {
		d.PerValue = json.Number(m.Dosage.Per.Value.String())
		d.PerUnit = string(m.Dosage.Per.Unit)
	}
	return medicationResponse{
		ID:        m.ID.String(),
		Name:      m.Name,
		Dosage:    d,
		Form:      string(m.Form),
		Version:   m.Version,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
