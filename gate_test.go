package glimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

func compile(t *testing.T, spec Spec, opts ...Option) *Gate {
	t.Helper()
	g, err := Compile(spec, opts...)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return g
}

func peerKey(addr string) Key {
	return Key{Kind: KindRequests, Scope: ScopePeer, ID: addr}
}

func TestGateNilAllows(t *testing.T) {
	var g *Gate
	if d := g.Allow(context.Background(), peerKey("10.0.0.1"), 1); !d.Allow {
		t.Fatal("a nil gate allows everything")
	}
	if got := g.Size(); got != 0 {
		t.Fatalf("Size = %d, want 0", got)
	}
	g.Release(context.Background(), peerKey("10.0.0.1"), 1)
}

func TestGateNoPolicyAllowsWithoutState(t *testing.T) {
	g := compile(t, Spec{})
	key := Key{Kind: KindBytes, Scope: ScopeSession, ID: "s1", Direction: DirIn}
	if d := g.Allow(context.Background(), key, 4096); !d.Allow {
		t.Fatal("no policy must allow")
	}
	if got := g.Size(); got != 0 {
		t.Fatalf("Size = %d, want 0 (no policy, no state)", got)
	}
}

func TestGateRateLimit(t *testing.T) {
	clock := newFakeClock()
	g := compile(t, Spec{Policies: []Policy{
		{Kind: KindRequests, Scope: ScopePeer, Rate: 2, Burst: 2},
	}}, WithClock(clock.Now))
	key := peerKey("10.0.0.1")
	for i := 0; i < 2; i++ {
		if d := g.Allow(context.Background(), key, 1); !d.Allow {
			t.Fatalf("burst %d must be allowed", i)
		}
	}
	d := g.Allow(context.Background(), key, 1)
	if d.Allow {
		t.Fatal("third request must be denied")
	}
	if d.Reason != ReasonRate {
		t.Fatalf("reason = %q, want %q", d.Reason, ReasonRate)
	}
	if d.RetryAfter <= 0 || d.RetryAfter > time.Second {
		t.Fatalf("retry after = %v, want (0, 1s]", d.RetryAfter)
	}
	clock.Advance(500 * time.Millisecond)
	if d := g.Allow(context.Background(), key, 1); !d.Allow {
		t.Fatal("half a second refills one of two per second")
	}
}

func TestGateDirectionIsSeparate(t *testing.T) {
	clock := newFakeClock()
	g := compile(t, Spec{Policies: []Policy{
		{Kind: KindBytes, Scope: ScopeSession, Rate: 1, Burst: 1, Dir: DirIn},
		{Kind: KindBytes, Scope: ScopeSession, Rate: 1, Burst: 1, Dir: DirOut},
	}}, WithClock(clock.Now))
	in := Key{Kind: KindBytes, Scope: ScopeSession, ID: "s1", Direction: DirIn}
	out := Key{Kind: KindBytes, Scope: ScopeSession, ID: "s1", Direction: DirOut}
	if d := g.Allow(context.Background(), in, 1); !d.Allow {
		t.Fatal("first inbound byte must pass")
	}
	if d := g.Allow(context.Background(), in, 1); d.Allow {
		t.Fatal("second inbound byte must be denied")
	}
	if d := g.Allow(context.Background(), out, 1); !d.Allow {
		t.Fatal("outbound has its own bucket")
	}
}

func TestGateConcurrencyHoldsCreditUntilRelease(t *testing.T) {
	g := compile(t, Spec{Policies: []Policy{
		{Kind: KindConnections, Scope: ScopeGlobal, Max: 2},
		{Kind: KindConnections, Scope: ScopePeer, Max: 1},
	}})
	global := Key{Kind: KindConnections, Scope: ScopeGlobal}
	local := Key{Kind: KindConnections, Scope: ScopePeer, ID: "10.0.0.1"}

	if d := g.Allow(context.Background(), global, 1); !d.Allow {
		t.Fatalf("first connection: %+v", d)
	}
	if d := g.Allow(context.Background(), local, 1); !d.Allow {
		t.Fatalf("first peer connection: %+v", d)
	}
	d := g.Allow(context.Background(), local, 1)
	if d.Allow || d.Reason != ReasonConcurrency {
		t.Fatalf("second peer connection = %+v, want a concurrency denial", d)
	}
	if d := g.Allow(context.Background(), global, 1); !d.Allow {
		t.Fatalf("second global connection: %+v", d)
	}
	if d := g.Allow(context.Background(), global, 1); d.Allow {
		t.Fatal("third connection must exceed the global cap")
	}

	g.Release(context.Background(), local, 1)
	if d := g.Allow(context.Background(), local, 1); !d.Allow {
		t.Fatal("released credit must be reusable")
	}
}

func TestGateReleaseWithoutAcquireIsHarmless(t *testing.T) {
	g := compile(t, Spec{Policies: []Policy{
		{Kind: KindConnections, Scope: ScopeGlobal, Max: 1},
	}})
	g.Release(context.Background(), Key{Kind: KindConnections, Scope: ScopeGlobal}, 5)
	key := Key{Kind: KindConnections, Scope: ScopeGlobal}
	if d := g.Allow(context.Background(), key, 1); !d.Allow {
		t.Fatal("releasing unused credit must not free it twice")
	}
	if d := g.Allow(context.Background(), key, 1); d.Allow {
		t.Fatal("a single slot cannot admit two connections")
	}
}

func TestGateStrictestPolicyWins(t *testing.T) {
	clock := newFakeClock()
	g := compile(t, Spec{Policies: []Policy{
		{Kind: KindRequests, Scope: ScopePeer, Rate: 100, Burst: 100},
	}}, WithClock(clock.Now), WithProvider(ProviderFunc(
		func(context.Context, Key) []Policy {
			return []Policy{{Kind: KindRequests, Scope: ScopePeer, Rate: 1, Burst: 1}}
		})))

	key := peerKey("10.0.0.2")
	if d := g.Allow(context.Background(), key, 1); !d.Allow {
		t.Fatal("first request fits both policies")
	}
	if d := g.Allow(context.Background(), key, 1); d.Allow {
		t.Fatal("the strictest policy must deny")
	}
}

func TestGateQuotaReasonOutranksRate(t *testing.T) {
	clock := newFakeClock()
	g := compile(t, Spec{Policies: []Policy{
		{Kind: KindRequests, Scope: ScopeGlobal, Rate: 1, Burst: 1},
	}}, WithClock(clock.Now), WithProvider(ProviderFunc(
		func(context.Context, Key) []Policy {
			return []Policy{{Kind: KindRequests, Scope: ScopeGlobal, Rate: 0.5, Burst: 1, Reason: ReasonQuota}}
		})))
	key := Key{Kind: KindRequests, Scope: ScopeGlobal}
	if d := g.Allow(context.Background(), key, 1); !d.Allow {
		t.Fatal("first request must fit both policies")
	}
	// Both policies deny now; the quota ranks first so it names the denial.
	d := g.Allow(context.Background(), key, 1)
	if d.Allow {
		t.Fatal("request must be denied")
	}
	if d.Reason != ReasonQuota {
		t.Fatalf("reason = %q, want %q", d.Reason, ReasonQuota)
	}
}

func TestGateSharedPolicyKeepsOneSlot(t *testing.T) {
	clock := newFakeClock()
	policy := Policy{Kind: KindRequests, Scope: ScopeGlobal, Rate: 1, Burst: 1}
	g := compile(t, Spec{Policies: []Policy{policy}},
		WithClock(clock.Now),
		WithProvider(ProviderFunc(func(context.Context, Key) []Policy {
			return []Policy{policy} // the same numbers: one bucket, not two
		})))
	key := Key{Kind: KindRequests, Scope: ScopeGlobal}
	if d := g.Allow(context.Background(), key, 1); !d.Allow {
		t.Fatal("duplicate policies are deduplicated")
	}
	if d := g.Allow(context.Background(), key, 1); d.Allow {
		t.Fatal("a duplicated policy must not double the budget")
	}
}

func TestGateReclaimsIdleKeys(t *testing.T) {
	clock := newFakeClock()
	g := compile(t, Spec{Policies: []Policy{
		{Kind: KindRequests, Scope: ScopePeer, Rate: 1, Burst: 1},
	}}, WithClock(clock.Now), WithTTL(time.Minute), WithMaxKeys(2))

	g.Allow(context.Background(), peerKey("10.0.0.1"), 1)
	g.Allow(context.Background(), peerKey("10.0.0.2"), 1)
	if got := g.Size(); got != 2 {
		t.Fatalf("Size = %d, want 2", got)
	}
	clock.Advance(2 * time.Minute)
	if d := g.Allow(context.Background(), peerKey("10.0.0.3"), 1); !d.Allow {
		t.Fatalf("an idle table must make room: %+v", d)
	}
	if got := g.Size(); got > 2 {
		t.Fatalf("Size = %d, want <= 2", got)
	}
}

func TestGateDeniesWhenEveryKeyHoldsCredit(t *testing.T) {
	g := compile(t, Spec{Policies: []Policy{
		{Kind: KindConnections, Scope: ScopePeer, Max: 4},
	}}, WithMaxKeys(1))
	first := Key{Kind: KindConnections, Scope: ScopePeer, ID: "10.0.0.1"}
	if d := g.Allow(context.Background(), first, 1); !d.Allow {
		t.Fatal("first key must fit")
	}
	second := Key{Kind: KindConnections, Scope: ScopePeer, ID: "10.0.0.2"}
	d := g.Allow(context.Background(), second, 1)
	if d.Allow || d.Reason != ReasonUnavailable {
		t.Fatalf("exhausted table = %+v, want an unavailable denial", d)
	}
}

func TestGateIgnoresProviderPoliciesItCannotEnforce(t *testing.T) {
	g := compile(t, Spec{}, WithProvider(ProviderFunc(
		func(context.Context, Key) []Policy {
			return []Policy{{Kind: KindRequests, Scope: ScopePeer}} // no rate
		})))
	if d := g.Allow(context.Background(), peerKey("10.0.0.9"), 1); !d.Allow {
		t.Fatal("an unusable policy must be ignored, not enforced")
	}
}

func TestGateBurstCostLargerThanBucket(t *testing.T) {
	clock := newFakeClock()
	g := compile(t, Spec{Policies: []Policy{
		{Kind: KindBytes, Scope: ScopeSession, Rate: 1000, Burst: 1000, Dir: DirIn},
	}}, WithClock(clock.Now))
	key := Key{Kind: KindBytes, Scope: ScopeSession, ID: "s1", Direction: DirIn}
	d := g.Allow(context.Background(), key, 2000)
	if d.Allow {
		t.Fatal("a read larger than the burst cannot pass in one charge")
	}
	if d.Reason != ReasonBytes {
		t.Fatalf("reason = %q, want %q", d.Reason, ReasonBytes)
	}
	if d.RetryAfter <= 0 {
		t.Fatalf("retry after = %v, want a positive estimate", d.RetryAfter)
	}
}

func TestGateConcurrentUse(t *testing.T) {
	g := compile(t, Spec{Policies: []Policy{
		{Kind: KindConnections, Scope: ScopeGlobal, Max: 50},
		{Kind: KindRequests, Scope: ScopeGlobal, Rate: 1000, Burst: 1000},
	}})
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		allowed int
		denied  int
	)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := g.Allow(context.Background(), Key{Kind: KindConnections, Scope: ScopeGlobal}, 1)
			mu.Lock()
			defer mu.Unlock()
			if d.Allow {
				allowed++
			} else {
				denied++
			}
		}()
	}
	wg.Wait()
	if allowed != 50 || denied != 50 {
		t.Fatalf("allowed = %d, denied = %d, want 50/50", allowed, denied)
	}
}
