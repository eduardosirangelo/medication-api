package pricing

import (
	"testing"
)

// Zero-latency networks, so this measures the fan-out itself: goroutines,
// channel, timer and sorting, not simulated waiting.
func BenchmarkComparer_BestPrice(b *testing.B) {
	networks := make([]Network, 3)
	for i, pct := range []int64{100, 94, 88} {
		networks[i] = NewSimulatedNetwork(SimulatedConfig{Name: "n", PricePercent: pct})
	}
	comparer := NewComparer(networks, testBudget)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := comparer.BestPrice(b.Context(), testRequest); err != nil {
			b.Fatal(err)
		}
	}
}
