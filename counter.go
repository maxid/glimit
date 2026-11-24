package glimit

import "sync"

// Counter is a bounded gauge for KindConnections and KindInflight: it holds n
// credits from Acquire until Release. A max <= 0 means unlimited.
type Counter struct {
	mu  sync.Mutex
	max int
	cur int
}

// NewCounter returns a counter with the given cap; max <= 0 allows everything.
func NewCounter(max int) *Counter { return &Counter{max: max} }

// WouldFit reports whether n more credits fit without taking them.
func (c *Counter) WouldFit(n int) bool {
	if n <= 0 {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fits(n)
}

// Acquire takes n credits when they fit.
func (c *Counter) Acquire(n int) bool {
	if n <= 0 {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.fits(n) {
		return false
	}
	c.cur += n
	return true
}

// Release returns n credits, clamped at zero.
func (c *Counter) Release(n int) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	c.cur -= n
	if c.cur < 0 {
		c.cur = 0
	}
	c.mu.Unlock()
}

// Value is the number of credits currently held.
func (c *Counter) Value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

func (c *Counter) fits(n int) bool {
	return c.max <= 0 || c.cur+n <= c.max
}
