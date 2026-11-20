package pricing

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestSimulatedNetwork_PriceIsDeterministicAndScaled(t *testing.T) {
	reference := NewSimulatedNetwork(SimulatedConfig{Name: "ref", PricePercent: 100})
	cheaper := NewSimulatedNetwork(SimulatedConfig{Name: "cheap", PricePercent: 90})

	q1, err1 := reference.Quote(t.Context(), testRequest)
	q2, err2 := reference.Quote(t.Context(), testRequest)
	if err1 != nil || err2 != nil || q1.PriceCents != q2.PriceCents {
		t.Fatalf("same request priced differently: %+v %+v (%v %v)", q1, q2, err1, err2)
	}
	unit := referenceUnitCents(testRequest.NDC)
	if want := unit * int64(testRequest.Quantity); q1.PriceCents != want {
		t.Errorf("reference price = %d, want %d", q1.PriceCents, want)
	}
	q3, _ := cheaper.Quote(t.Context(), testRequest)
	if q3.PriceCents >= q1.PriceCents {
		t.Errorf("90%% network (%d) not cheaper than reference (%d)", q3.PriceCents, q1.PriceCents)
	}

	other, _ := reference.Quote(t.Context(), Request{NDC: "50580045101", Quantity: testRequest.Quantity})
	if other.PriceCents == q1.PriceCents {
		t.Error("different NDCs should usually price differently")
	}
}

func TestReferenceUnitCents_StaysInRange(t *testing.T) {
	for _, ndc := range []string{"", "00000000000", "99999999999", "00093415573", "50580045101"} {
		if c := referenceUnitCents(ndc); c < 5 || c > 2000 {
			t.Errorf("%q: %d cents per unit", ndc, c)
		}
	}
}

func TestSimulatedNetwork_RoundsHalfUpToWholeCents(t *testing.T) {
	n := NewSimulatedNetwork(SimulatedConfig{Name: "x", PricePercent: 50})
	req := Request{NDC: "00093415573", Quantity: 1}
	q, _ := n.Quote(t.Context(), req)
	if want := (referenceUnitCents(req.NDC)*50 + 50) / 100; q.PriceCents != want {
		t.Errorf("price = %d, want %d", q.PriceCents, want)
	}
}

func TestSimulatedNetwork_Failure(t *testing.T) {
	n := NewSimulatedNetwork(SimulatedConfig{Name: "x", PricePercent: 100, FailureRate: 0.5})
	n.random = func() float64 { return 0.49 }
	if _, err := n.Quote(t.Context(), testRequest); !errors.Is(err, ErrSimulatedFailure) {
		t.Fatalf("err = %v", err)
	}
	n.random = func() float64 { return 0.5 }
	if _, err := n.Quote(t.Context(), testRequest); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestSimulatedNetwork_HonoursLatencyAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := NewSimulatedNetwork(SimulatedConfig{Name: "x", PricePercent: 100, Latency: 200 * time.Millisecond})

		start := time.Now()
		if _, err := n.Quote(t.Context(), testRequest); err != nil || time.Since(start) != 200*time.Millisecond {
			t.Fatalf("err = %v, took %v", err, time.Since(start))
		}

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		start = time.Now()
		if _, err := n.Quote(ctx, testRequest); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 50*time.Millisecond {
			t.Fatalf("err = %v, took %v", err, time.Since(start))
		}
	})
}

// Guards the demo: with the default budget, network-c (cheapest) must time
// out and network-a must answer, or the endpoint stops illustrating anything.
func TestDefaultNetworks_ShowTheBudgetAtWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		networks := DefaultNetworks()
		for _, n := range networks {
			n.(*SimulatedNetwork).random = func() float64 { return 1 } // no random failures
		}
		res, err := NewComparer(networks, 300*time.Millisecond).BestPrice(t.Context(), testRequest)
		if err != nil {
			t.Fatal(err)
		}
		if best, _ := res.Best(); best.Network != "network-b" {
			t.Errorf("best = %+v", best)
		}
		assertUnavailable(t, res, "network-c", ReasonTimeout)
	})
}
