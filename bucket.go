package glimit

import (
	"math"
	"sync"
	"time"
)

// Bucket is a token bucket for rate policies: Rate units per second with a
// burst capacity. It is exported so a library that already owns a richer
// shaper can reuse the accounting without adopting Gate.
//
// Time is passed in rather than read, so callers and tests share one clock.
type Bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

// NewBucket returns a full bucket of burst units refilled at rate per second.
// A rate <= 0 means no refill; callers normally guard against that.
func NewBucket(rate float64, burst int) *Bucket {
	if burst < 1 {
		burst = 1
	}
	return &Bucket{rate: rate, burst: float64(burst), tokens: float64(burst)}
}

// WouldAllow reports whether n units fit now and how long until they would.
func (b *Bucket) WouldAllow(n int, now time.Time) (bool, time.Duration) {
	if n <= 0 {
		return true, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill(now)
	if b.tokens >= float64(n) {
		return true, 0
	}
	return false, b.retryFor(n)
}

// Take consumes n units when they are available.
func (b *Bucket) Take(n int, now time.Time) (bool, time.Duration) {
	if n <= 0 {
		return true, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill(now)
	if b.tokens < float64(n) {
		return false, b.retryFor(n)
	}
	b.tokens -= float64(n)
	return true, 0
}

// Refund returns n units taken earlier, never above the burst capacity.
func (b *Bucket) Refund(n int) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	b.tokens = math.Min(b.burst, b.tokens+float64(n))
	b.mu.Unlock()
}

func (b *Bucket) refill(now time.Time) {
	if b.last.IsZero() {
		b.last = now
		return
	}
	if now.Before(b.last) || b.rate <= 0 {
		b.last = now
		return
	}
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	if elapsed <= 0 {
		return
	}
	b.tokens = math.Min(b.burst, b.tokens+elapsed*b.rate)
}

func (b *Bucket) retryFor(n int) time.Duration {
	if b.rate <= 0 {
		return 0
	}
	wait := time.Duration((float64(n)-b.tokens)/b.rate*float64(time.Second)) + time.Millisecond
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	return wait
}
