package pricing_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/eduardo-sirangelo/medication-api/internal/pricing"
)

func ExampleComparer_BestPrice() {
	comparer := pricing.NewComparer([]pricing.Network{
		pricing.NewSimulatedNetwork(pricing.SimulatedConfig{Name: "network-a", PricePercent: 100}),
		pricing.NewSimulatedNetwork(pricing.SimulatedConfig{Name: "network-b", PricePercent: 90}),
		pricing.NewSimulatedNetwork(pricing.SimulatedConfig{Name: "network-c", PricePercent: 80, FailureRate: 1}),
	}, 300*time.Millisecond)

	res, err := comparer.BestPrice(context.Background(), pricing.Request{NDC: "00093415573", Quantity: 30})
	if errors.Is(err, pricing.ErrNoQuotes) {
		fmt.Println("no quotes")
		return
	}
	best, _ := res.Best()
	fmt.Printf("best: %s $%d.%02d\n", best.Network, best.PriceCents/100, best.PriceCents%100)
	for _, u := range res.Unavailable {
		fmt.Printf("unavailable: %s (%s)\n", u.Network, u.Reason)
	}
	// Output:
	// best: network-b $304.83
	// unavailable: network-c (error)
}
