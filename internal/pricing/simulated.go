package pricing

import (
	"context"
	"errors"
	"hash/fnv"
	"math/rand/v2"
	"time"
)

var ErrSimulatedFailure = errors.New("simulated network failure")

// SimulatedConfig describes one fake network. PricePercent scales a
// reference price derived from the NDC (100 = reference, 90 = 10% cheaper).
type SimulatedConfig struct {
	Name         string
	PricePercent int64
	Latency      time.Duration
	FailureRate  float64 // 0 never fails, 1 always fails
}

// SimulatedNetwork prices deterministically from the NDC and quantity, so
// the same request always gets the same price and tests can assert on it.
type SimulatedNetwork struct {
	cfg    SimulatedConfig
	random func() float64
}

func NewSimulatedNetwork(cfg SimulatedConfig) *SimulatedNetwork {
	return &SimulatedNetwork{cfg: cfg, random: rand.Float64}
}

// DefaultNetworks are tuned to show the budget at work with the default
// 300ms: the cheapest network is too slow and always misses it.
func DefaultNetworks() []Network {
	return []Network{
		NewSimulatedNetwork(SimulatedConfig{Name: "network-a", PricePercent: 100, Latency: 40 * time.Millisecond}),
		NewSimulatedNetwork(SimulatedConfig{Name: "network-b", PricePercent: 94, Latency: 120 * time.Millisecond, FailureRate: 0.05}),
		NewSimulatedNetwork(SimulatedConfig{Name: "network-c", PricePercent: 88, Latency: 450 * time.Millisecond}),
	}
}

func (n *SimulatedNetwork) Name() string { return n.cfg.Name }

func (n *SimulatedNetwork) Quote(ctx context.Context, req Request) (Quote, error) {
	timer := time.NewTimer(n.cfg.Latency)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return Quote{}, ctx.Err()
	case <-timer.C:
	}
	if n.random() < n.cfg.FailureRate {
		return Quote{}, ErrSimulatedFailure
	}
	total := referenceUnitCents(req.NDC) * int64(req.Quantity) * n.cfg.PricePercent
	return Quote{PriceCents: (total + 50) / 100}, nil // round half up to whole cents
}

// Between $0.05 and $20.00 per unit: plausible for generics, nothing more.
func referenceUnitCents(ndc string) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(ndc)) // hash.Hash never returns an error
	return 5 + int64(h.Sum32()%1996)
}
