package glimit

import (
	"context"
	"sort"
	"sync"
	"time"
)

const (
	defaultTTL     = 5 * time.Minute
	defaultMaxKeys = 4096
)

// Gate compiles a Spec into per-key state and implements Limiter. Every policy
// that governs a key must pass for the request to be allowed; the first denial
// in reason order is the one reported.
//
// State is kept per key and reclaimed after TTL of inactivity, so a limiter
// keyed by peer address cannot grow without bound. A key with credit still held
// is never reclaimed.
type Gate struct {
	mu       sync.Mutex
	static   []Policy
	provider Provider
	now      func() time.Time
	ttl      time.Duration
	maxKeys  int
	keys     map[Key]*keyState
}

// Option configures a Gate.
type Option func(*Gate)

// WithProvider adds a dynamic policy source. Its policies are merged with the
// static spec and the strictest wins.
func WithProvider(p Provider) Option {
	return func(g *Gate) { g.provider = p }
}

// WithClock replaces the clock; tests use it to make refill deterministic.
func WithClock(now func() time.Time) Option {
	return func(g *Gate) {
		if now != nil {
			g.now = now
		}
	}
}

// WithTTL sets how long an idle key keeps its state (default 5 minutes).
func WithTTL(d time.Duration) Option {
	return func(g *Gate) {
		if d > 0 {
			g.ttl = d
		}
	}
}

// WithMaxKeys bounds the number of tracked keys (default 4096). At the cap the
// gate reclaims idle keys; if none can be reclaimed it denies new keys with
// ReasonUnavailable rather than growing without bound.
func WithMaxKeys(n int) Option {
	return func(g *Gate) {
		if n > 0 {
			g.maxKeys = n
		}
	}
}

// Compile validates spec and returns the gate that enforces it.
func Compile(spec Spec, opts ...Option) (*Gate, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	g := &Gate{
		static:  append([]Policy(nil), spec.Policies...),
		now:     time.Now,
		ttl:     defaultTTL,
		maxKeys: defaultMaxKeys,
		keys:    make(map[Key]*keyState),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(g)
		}
	}
	return g, nil
}

// Allow charges cost units to key and reports whether every governing policy
// admits them. A nil Gate allows everything, so a library can call it without
// a nil check.
func (g *Gate) Allow(ctx context.Context, key Key, cost int) Decision {
	if g == nil {
		return Decision{Allow: true, Key: key}
	}
	if cost < 0 {
		cost = 0
	}
	policies := g.policies(ctx, key)
	if len(policies) == 0 {
		return Decision{Allow: true, Key: key}
	}
	now := g.now()
	ks := g.state(key, now)
	if ks == nil {
		return Decision{Reason: ReasonUnavailable, Key: key}
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	now = g.now()
	for _, p := range policies {
		if d, denied := ks.slot(p).check(cost, now); denied {
			d.Key = key
			return d
		}
	}
	for _, p := range policies {
		ks.slot(p).take(cost, now)
	}
	ks.last = now
	return Decision{Allow: true, Key: key}
}

// Release returns credit taken by an allowed Allow for a gated policy
// (connections, inflight). It is a no-op for rate keys.
func (g *Gate) Release(ctx context.Context, key Key, cost int) {
	if g == nil || cost <= 0 {
		return
	}
	g.mu.Lock()
	ks := g.keys[key]
	g.mu.Unlock()
	if ks == nil {
		return
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for _, s := range ks.slots {
		if s.counter != nil {
			s.counter.Release(cost)
		}
	}
	ks.last = g.now()
}

// Size reports how many keys currently hold state. It exists for tests and for
// a caller that exports it as a bounded gauge; the number never includes the
// keys themselves.
func (g *Gate) Size() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.keys)
}

// policies collects the static and provider policies governing key, deduplicates
// them and orders them so the most specific denial reason is decided first.
func (g *Gate) policies(ctx context.Context, key Key) []Policy {
	var out []Policy
	var seen map[string]struct{}
	add := func(p Policy) {
		if !p.matches(key) || !p.usable() {
			return
		}
		id := p.id()
		if seen == nil {
			seen = make(map[string]struct{}, len(g.static))
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		out = append(out, p)
	}
	if g.provider != nil {
		for _, p := range g.provider.For(ctx, key) {
			add(p)
		}
	}
	for _, p := range g.static {
		add(p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := out[i].reason().rank(), out[j].reason().rank()
		if ri != rj {
			return ri < rj
		}
		return out[i].id() < out[j].id()
	})
	return out
}

// state returns the tracked state for key, creating it when the table has room.
// A nil return means the table is exhausted by keys that still hold credit.
func (g *Gate) state(key Key, now time.Time) *keyState {
	g.mu.Lock()
	if ks, ok := g.keys[key]; ok {
		g.mu.Unlock()
		return ks
	}
	if len(g.keys) >= g.maxKeys {
		g.sweepLocked()
	}
	if len(g.keys) >= g.maxKeys {
		g.mu.Unlock()
		return nil
	}
	ks := &keyState{slots: make(map[string]*slot), last: now}
	g.keys[key] = ks
	g.mu.Unlock()
	return ks
}

// sweepLocked reclaims idle keys. The caller holds g.mu; per-key locks are taken
// with TryLock so a busy key is simply skipped.
func (g *Gate) sweepLocked() {
	now := g.now()
	type candidate struct {
		key  Key
		last time.Time
	}
	var idle []candidate
	for key, ks := range g.keys {
		if !ks.mu.TryLock() {
			continue
		}
		free, last := ks.free(), ks.last
		ks.mu.Unlock()
		if free {
			idle = append(idle, candidate{key: key, last: last})
		}
	}
	for _, c := range idle {
		if now.Sub(c.last) > g.ttl {
			delete(g.keys, c.key)
		}
	}
	if len(g.keys) < g.maxKeys {
		return
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].last.Before(idle[j].last) })
	for _, c := range idle {
		if len(g.keys) < g.maxKeys {
			return
		}
		delete(g.keys, c.key)
	}
}

// keyState is the per-key state table, guarded separately from the gate so keys
// do not contend with each other.
type keyState struct {
	mu    sync.Mutex
	slots map[string]*slot
	last  time.Time
}

// slot is one policy's measurement for one key.
type slot struct {
	policy  Policy
	bucket  *Bucket
	counter *Counter
}

// free reports whether the key holds no credit, so its state can be reclaimed.
// The caller holds the keyState lock.
func (ks *keyState) free() bool {
	for _, s := range ks.slots {
		if s.counter != nil && s.counter.Value() > 0 {
			return false
		}
	}
	return true
}

// slot returns (creating on first use) the measurement for p.
func (ks *keyState) slot(p Policy) *slot {
	id := p.id()
	if s, ok := ks.slots[id]; ok {
		return s
	}
	s := &slot{policy: p}
	if p.gated() {
		s.counter = NewCounter(p.Max)
	} else {
		s.bucket = NewBucket(p.Rate, p.burstSize())
	}
	ks.slots[id] = s
	return s
}

// check reports whether n units fit without taking them.
func (s *slot) check(n int, now time.Time) (Decision, bool) {
	switch {
	case s.bucket != nil:
		if ok, retry := s.bucket.WouldAllow(n, now); !ok {
			return Decision{Reason: s.policy.reason(), RetryAfter: retry}, true
		}
	case s.counter != nil:
		if !s.counter.WouldFit(n) {
			return Decision{Reason: s.policy.reason()}, true
		}
	}
	return Decision{}, false
}

// take charges n units; it only runs after every policy passed check under the
// same key lock.
func (s *slot) take(n int, now time.Time) {
	if s.bucket != nil {
		s.bucket.Take(n, now)
	}
	if s.counter != nil {
		s.counter.Acquire(n)
	}
}
