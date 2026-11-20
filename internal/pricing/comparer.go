package pricing

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"
)

// ErrNoQuotes means every network failed or missed the budget.
var ErrNoQuotes = errors.New("no pricing network returned a quote")

var errInvalidQuote = errors.New("network returned a non-positive price")

// Comparer fans a request out to every network at once and returns whatever
// arrived within the budget, so one slow network costs at most the budget,
// never more.
type Comparer struct {
	networks []Network
	budget   time.Duration
}

func NewComparer(networks []Network, budget time.Duration) *Comparer {
	return &Comparer{networks: networks, budget: budget}
}

type answer struct {
	index int
	quote Quote
	err   error
}

// BestPrice returns ErrNoQuotes together with a Result that lists why each
// network is unavailable, and the caller's ctx error if the caller gave up.
func (c *Comparer) BestPrice(ctx context.Context, req Request) (Result, error) {
	budgetCtx, cancel := context.WithTimeout(ctx, c.budget)
	defer cancel()

	// Buffered so a network that answers after the budget can still send and
	// exit: with an unbuffered channel nobody would receive and it would leak.
	answers := make(chan answer, len(c.networks))
	for i, n := range c.networks {
		go func() {
			start := time.Now()
			q, err := n.Quote(budgetCtx, req)
			q.Network, q.Latency = n.Name(), time.Since(start)
			answers <- answer{index: i, quote: q, err: err}
		}()
	}

	received := c.collect(budgetCtx, answers)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	res := c.result(received)
	if len(res.Quotes) == 0 {
		return res, ErrNoQuotes
	}
	return res, nil
}

// collect takes exactly one answer per network, or stops at the budget;
// a nil entry means that network did not answer in time.
func (c *Comparer) collect(ctx context.Context, answers <-chan answer) []*answer {
	received := make([]*answer, len(c.networks))
	for range c.networks {
		select {
		case a := <-answers:
			received[a.index] = &a
		case <-ctx.Done():
			return received
		}
	}
	return received
}

func (c *Comparer) result(received []*answer) Result {
	var res Result
	for i, a := range received {
		name := c.networks[i].Name()
		switch {
		case a == nil:
			res.Unavailable = append(res.Unavailable, Unavailable{Network: name, Reason: ReasonTimeout, Err: context.DeadlineExceeded})
		case a.err != nil:
			res.Unavailable = append(res.Unavailable, Unavailable{Network: name, Reason: reasonFor(a.err), Err: a.err})
		case a.quote.PriceCents <= 0:
			res.Unavailable = append(res.Unavailable, Unavailable{Network: name, Reason: ReasonError, Err: errInvalidQuote})
		default:
			res.Quotes = append(res.Quotes, a.quote)
		}
	}
	// Stable, so equal prices keep configuration order and the winner is predictable.
	slices.SortStableFunc(res.Quotes, func(a, b Quote) int { return cmp.Compare(a.PriceCents, b.PriceCents) })
	return res
}

func reasonFor(err error) Reason {
	if errors.Is(err, context.DeadlineExceeded) {
		return ReasonTimeout
	}
	return ReasonError
}
