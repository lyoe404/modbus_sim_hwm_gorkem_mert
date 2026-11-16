package poller

import (
	"math/rand"
	"time"
)

// Backoff computes exponential retry delays: Base, 2*Base, 4*Base ... capped at Max.
type Backoff struct {
	Base   time.Duration
	Max    time.Duration
	Jitter float64 // 0..1, fraction of random +/- spread (0 = deterministic)
}

// Delay returns the wait before the next attempt after `failures` consecutive failures.
func (b Backoff) Delay(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	d := b.Base
	for i := 1; i < failures && d < b.Max; i++ {
		d *= 2
	}
	if d > b.Max {
		d = b.Max
	}
	if b.Jitter > 0 {
		d = time.Duration(float64(d) * (1 + b.Jitter*(rand.Float64()*2-1)))
	}
	return d
}
