package pricing

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

const testBudget = 300 * time.Millisecond

var testRequest = Request{NDC: "00093415573", Quantity: 30}

// fakeNetwork answers after latency with a fixed price or error. With
// ignoreCtx it sleeps through cancellation, like a client without timeouts.
type fakeNetwork struct {
	name      string
	latency   time.Duration
	price     int64
	err       error
	ignoreCtx bool
}

func (f fakeNetwork) Name() string { return f.name }

func (f fakeNetwork) Quote(ctx context.Context, _ Request) (Quote, error) {
	if f.ignoreCtx {
		time.Sleep(f.latency)
	} else {
		select {
		case <-ctx.Done():
			return Quote{}, ctx.Err()
		case <-time.After(f.latency):
		}
	}
	return Quote{PriceCents: f.price}, f.err
}

// bestPrice runs BestPrice and reports how much (virtual) time it took.
func bestPrice(t *testing.T, networks ...Network) (Result, time.Duration, error) {
	t.Helper()
	start := time.Now()
	res, err := NewComparer(networks, testBudget).BestPrice(t.Context(), testRequest)
	return res, time.Since(start), err
}

// Inside a synctest bubble time is virtual: a 300ms budget takes no real
// time, and the test fails if any goroutine is left blocked at the end.
func TestBestPrice_LowestPriceWins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		res, took, err := bestPrice(t,
			fakeNetwork{name: "a", latency: 40 * time.Millisecond, price: 1200},
			fakeNetwork{name: "b", latency: 120 * time.Millisecond, price: 990},
			fakeNetwork{name: "c", latency: 10 * time.Millisecond, price: 1500},
		)
		if err != nil {
			t.Fatal(err)
		}
		best, _ := res.Best()
		if best.Network != "b" || best.PriceCents != 990 || best.Latency != 120*time.Millisecond {
			t.Errorf("best = %+v", best)
		}
		if got := networkNames(res.Quotes); got != "b,a,c" {
			t.Errorf("quotes order = %s, want cheapest first", got)
		}
		if took != 120*time.Millisecond {
			t.Errorf("took %v, want the slowest answer (120ms), not the budget", took)
		}
	})
}

func TestBestPrice_SlowNetworkTimesOutAtTheBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		res, took, err := bestPrice(t,
			fakeNetwork{name: "fast", latency: 40 * time.Millisecond, price: 1200},
			fakeNetwork{name: "slow", latency: 450 * time.Millisecond, price: 100},
		)
		if err != nil {
			t.Fatal(err)
		}
		if best, _ := res.Best(); best.Network != "fast" {
			t.Errorf("best = %+v: a cheaper but late quote must not win", best)
		}
		assertUnavailable(t, res, "slow", ReasonTimeout)
		if took != testBudget {
			t.Errorf("took %v, want exactly the budget", took)
		}
	})
}

// The network never looks at ctx, so its goroutine outlives BestPrice. After
// sleeping past its latency, the buffered channel lets it send and exit; with
// an unbuffered one nobody would receive, and synctest would fail the test
// with "blocked goroutines remain".
func TestBestPrice_NetworkIgnoringContextDoesNotDelayOrLeak(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		res, took, err := bestPrice(t,
			fakeNetwork{name: "fast", latency: 10 * time.Millisecond, price: 500},
			fakeNetwork{name: "stuck", latency: 10 * time.Second, price: 100, ignoreCtx: true},
		)
		if err != nil || took != testBudget {
			t.Fatalf("err = %v, took %v", err, took)
		}
		assertUnavailable(t, res, "stuck", ReasonTimeout)

		time.Sleep(10 * time.Second)
		synctest.Wait()
	})
}

func TestBestPrice_ErrorsAndInvalidQuotesAreUnavailable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boom := errors.New("upstream 502")
		res, _, err := bestPrice(t,
			fakeNetwork{name: "ok", latency: 10 * time.Millisecond, price: 700},
			fakeNetwork{name: "failing", latency: 5 * time.Millisecond, err: boom},
			fakeNetwork{name: "free", latency: 5 * time.Millisecond, price: 0},
		)
		if err != nil {
			t.Fatal(err)
		}
		assertUnavailable(t, res, "failing", ReasonError)
		assertUnavailable(t, res, "free", ReasonError)
		if !errors.Is(res.Unavailable[0].Err, boom) {
			t.Errorf("underlying error lost: %v", res.Unavailable[0].Err)
		}
		if len(res.Quotes) != 1 {
			t.Errorf("quotes = %+v", res.Quotes)
		}
	})
}

func TestBestPrice_AllUnavailable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		res, took, err := bestPrice(t,
			fakeNetwork{name: "failing", latency: 5 * time.Millisecond, err: errors.New("down")},
			fakeNetwork{name: "slow", latency: time.Second, price: 100},
		)
		if !errors.Is(err, ErrNoQuotes) {
			t.Fatalf("err = %v, want ErrNoQuotes", err)
		}
		if _, ok := res.Best(); ok {
			t.Error("Best reported a quote")
		}
		if got := len(res.Unavailable); got != 2 || res.Unavailable[0].Network != "failing" || res.Unavailable[1].Network != "slow" {
			t.Errorf("unavailable must list every network in configuration order: %+v", res.Unavailable)
		}
		if took != testBudget {
			t.Errorf("took %v", took)
		}
	})
}

func TestBestPrice_CallerCancellationWins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(50*time.Millisecond, cancel)
		_, err := NewComparer([]Network{
			fakeNetwork{name: "slow", latency: time.Second, price: 100},
		}, testBudget).BestPrice(ctx, testRequest)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

func TestBestPrice_EqualPricesKeepConfigurationOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		res, _, err := bestPrice(t,
			fakeNetwork{name: "first", latency: 30 * time.Millisecond, price: 800},
			fakeNetwork{name: "second", latency: 10 * time.Millisecond, price: 800},
		)
		if best, _ := res.Best(); err != nil || best.Network != "first" {
			t.Fatalf("best = %+v, err = %v", best, err)
		}
	})
}

func assertUnavailable(t *testing.T, res Result, network string, want Reason) {
	t.Helper()
	for _, u := range res.Unavailable {
		if u.Network == network {
			if u.Reason != want {
				t.Errorf("%s: reason %q, want %q", network, u.Reason, want)
			}
			return
		}
	}
	t.Errorf("%s not reported unavailable: %+v", network, res.Unavailable)
}

func networkNames(quotes []Quote) string {
	var s string
	for i, q := range quotes {
		if i > 0 {
			s += ","
		}
		s += q.Network
	}
	return s
}
