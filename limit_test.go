package glimit

import (
	"sync"
	"testing"
	"time"
)

// fakeClock makes refill deterministic without sleeping.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1700000000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestSpecValidate(t *testing.T) {
	valid := []Policy{
		{Kind: KindConnections, Scope: ScopeGlobal, Max: 10},
		{Kind: KindConnections, Scope: ScopePeer, Max: 1, Reason: ReasonQuota},
		{Kind: KindRequests, Scope: ScopeGlobal, Rate: 100},
		{Kind: KindRequests, Scope: ScopeSession, Rate: 5, Burst: 10, Dir: DirIn},
		{Kind: KindBytes, Scope: ScopePeer, Rate: 1 << 20, Dir: DirOut},
		{Kind: KindInflight, Scope: ScopeSession, Max: 4},
	}
	for i, p := range valid {
		if err := p.Validate(); err != nil {
			t.Fatalf("valid[%d] %+v: %v", i, p, err)
		}
	}
	invalid := []Policy{
		{Kind: "bogus", Scope: ScopeGlobal, Max: 1},
		{Kind: KindConnections, Scope: "bogus", Max: 1},
		{Kind: KindConnections, Scope: ScopeGlobal},
		{Kind: KindConnections, Scope: ScopeGlobal, Max: 1, Rate: 5},
		{Kind: KindRequests, Scope: ScopeGlobal, Max: 1},
		{Kind: KindRequests, Scope: ScopeGlobal, Rate: -1},
		{Kind: KindBytes, Scope: ScopeGlobal, Rate: 1, Dir: "sideways"},
		{Kind: KindConnections, Scope: ScopeGlobal, Max: 1, Dir: DirIn},
		{Kind: KindConnections, Scope: ScopeGlobal, Max: 1, Reason: "bogus"},
	}
	for i, p := range invalid {
		if err := p.Validate(); err == nil {
			t.Fatalf("invalid[%d] %+v: expected an error", i, p)
		}
	}
}

func TestPolicyBurst(t *testing.T) {
	tests := []struct {
		rate  float64
		burst int
		want  int
	}{
		{rate: 1, want: 1},
		{rate: 0.5, want: 1},
		{rate: 2.5, want: 3},
		{rate: 10, burst: 2, want: 2},
	}
	for _, tc := range tests {
		p := Policy{Kind: KindRequests, Rate: tc.rate, Burst: tc.burst}
		if got := p.burstSize(); got != tc.want {
			t.Fatalf("burstSize(%v, %d) = %d, want %d", tc.rate, tc.burst, got, tc.want)
		}
	}
}

func TestBucketTakeAndRefill(t *testing.T) {
	b := NewBucket(2, 2) // two units per second, burst two
	now := time.Unix(1700000000, 0)
	if ok, _ := b.Take(2, now); !ok {
		t.Fatal("first burst must fit")
	}
	ok, retry := b.Take(1, now)
	if ok {
		t.Fatal("empty bucket must deny")
	}
	if retry < 400*time.Millisecond || retry > 600*time.Millisecond {
		t.Fatalf("retry = %v, want about 500ms", retry)
	}
	if ok, _ := b.Take(1, now.Add(500*time.Millisecond)); !ok {
		t.Fatal("half a second must refill one unit")
	}
	if ok, _ := b.Take(2, now.Add(500*time.Millisecond)); ok {
		t.Fatal("refill cannot exceed the burst capacity")
	}
}

func TestBucketRefund(t *testing.T) {
	b := NewBucket(1, 3)
	now := time.Unix(1700000000, 0)
	if ok, _ := b.Take(3, now); !ok {
		t.Fatal("burst must fit")
	}
	b.Refund(2)
	if ok, _ := b.Take(2, now); !ok {
		t.Fatal("refund must make the units available again")
	}
	b.Refund(100)
	if ok, _ := b.Take(3, now); !ok {
		t.Fatal("refund is clamped to the burst capacity")
	}
}

func TestBucketNonPositiveCost(t *testing.T) {
	b := NewBucket(1, 1)
	if ok, _ := b.Take(0, time.Unix(1700000000, 0)); !ok {
		t.Fatal("zero cost always fits")
	}
	if ok, _ := b.Take(-1, time.Unix(1700000000, 0)); !ok {
		t.Fatal("negative cost always fits")
	}
}

func TestCounter(t *testing.T) {
	c := NewCounter(2)
	if !c.Acquire(2) {
		t.Fatal("two of two must fit")
	}
	if c.WouldFit(1) {
		t.Fatal("a full counter must not fit another unit")
	}
	if c.Acquire(1) {
		t.Fatal("a full counter must deny")
	}
	c.Release(1)
	if got := c.Value(); got != 1 {
		t.Fatalf("value = %d, want 1", got)
	}
	c.Release(10)
	if got := c.Value(); got != 0 {
		t.Fatalf("value = %d, want 0 (release clamps)", got)
	}
	if !NewCounter(0).Acquire(1 << 20) {
		t.Fatal("max <= 0 means unlimited")
	}
}
