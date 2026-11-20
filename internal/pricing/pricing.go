// Package pricing compares cash prices for a medication across pricing
// networks under a fixed latency budget. The networks are simulated: no real
// pricing source is contacted and the prices mean nothing outside this demo.
package pricing

import (
	"context"
	"time"
)

// Quantity bounds for a quote, in dispensing units (tablets, capsules, mL).
const (
	MinQuantity = 1
	MaxQuantity = 1000
)

// Request asks for the price of Quantity units of the product with this
// 11-digit NDC. Real networks price per NDC, so the NDC is the key, not the
// catalog ID.
type Request struct {
	NDC      string
	Quantity int
}

// Quote is one network's answer. Money is int64 cents: floats cannot
// represent most decimal amounts exactly.
type Quote struct {
	Network    string
	PriceCents int64
	Latency    time.Duration
}

// Network is a source of price quotes. Quote must return promptly once ctx
// is done; the Comparer stops waiting at its budget either way.
type Network interface {
	Name() string
	Quote(ctx context.Context, req Request) (Quote, error)
}

// Reason says why a network has no quote in a Result.
type Reason string

const (
	ReasonTimeout Reason = "timeout"
	ReasonError   Reason = "error"
)

// Unavailable records a network that produced no usable quote. Err is for
// logs only; clients see just the Reason.
type Unavailable struct {
	Network string
	Reason  Reason
	Err     error
}

// Result holds every quote (cheapest first) and every network without one,
// in configuration order, so responses are stable across calls.
type Result struct {
	Quotes      []Quote
	Unavailable []Unavailable
}

// Best is the cheapest quote; ok is false when no network answered.
func (r Result) Best() (Quote, bool) {
	if len(r.Quotes) == 0 {
		return Quote{}, false
	}
	return r.Quotes[0], true
}
