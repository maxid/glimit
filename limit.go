package glimit

import (
	"context"
	"time"
)

// Kind is the resource a limit accounts for.
type Kind string

const (
	// KindConnections bounds concurrent sessions. A caller holds the credit
	// from a successful Allow until it calls Release, so this is a gauge.
	KindConnections Kind = "connections"
	// KindRequests bounds events per second: accepts, decoded frames, calls.
	KindRequests Kind = "requests"
	// KindBytes bounds bytes per second. Direction splits reads from writes.
	KindBytes Kind = "bytes"
	// KindInflight bounds concurrent operations that are not sessions
	// (uploads, outstanding requests). Credit is held until Release.
	KindInflight Kind = "inflight"
)

// Scope is the subject a limit is accounted per. Identity lives in Key.ID and
// must never become a metric label: it is unbounded by nature.
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopePeer    Scope = "peer"
	ScopeSession Scope = "session"
)

// Direction separates inbound from outbound accounting. DirAny matches both.
type Direction string

const (
	DirAny Direction = ""
	DirIn  Direction = "in"
	DirOut Direction = "out"
)

// Reason is why a request was denied. The vocabulary is closed on purpose: it
// is also the reason label of the rejection metric, so it must stay bounded.
type Reason string

const (
	// ReasonRate is a sustained event or byte rate over the configured limit.
	ReasonRate Reason = "rate"
	// ReasonBurst is an instantaneous burst a policy tuned for it rejected.
	ReasonBurst Reason = "burst"
	// ReasonConcurrency is too many concurrent connections for the subject.
	ReasonConcurrency Reason = "concurrency"
	// ReasonBytes is a sustained byte rate over the configured limit.
	ReasonBytes Reason = "bytes"
	// ReasonQuota is a subscribed quota (tenant, licence) and is reserved for
	// a Provider; no default policy produces it.
	ReasonQuota Reason = "quota"
	// ReasonOverload is the caller refusing load it cannot serve and is
	// reserved for a Provider.
	ReasonOverload Reason = "overload"
	// ReasonUnavailable is the limiter itself being unable to decide safely
	// (its per-key state table is exhausted). Fail closed, not open.
	ReasonUnavailable Reason = "unavailable"
)

// Key identifies one counter: what is measured, for whom, in which direction.
type Key struct {
	Kind      Kind
	Scope     Scope
	ID        string
	Direction Direction
}

// Decision is the outcome of one Allow call. RetryAfter is how long until the
// same request could succeed; it is zero when the limiter cannot estimate it
// (concurrency denials, for instance, depend on a peer closing).
type Decision struct {
	Allow      bool
	Reason     Reason
	RetryAfter time.Duration
	Key        Key
}

// Limiter is the seam a library accepts. Implementations must be safe for
// concurrent use; *Gate is the default one.
//
// Allow charges cost units to key. For KindConnections and KindInflight a
// successful call takes credit that the caller must hand back with Release,
// including when the session ends for an unrelated reason. Release is a no-op
// for rate kinds.
type Limiter interface {
	Allow(ctx context.Context, key Key, cost int) Decision
	Release(ctx context.Context, key Key, cost int)
}

// Provider supplies policies at decision time. It is the single extension
// point for policy sources that are not a static Spec: tenant quota, licence
// entitlements, adaptive load shedding. A Provider must not block and must
// return the policies currently in force; invalid policies are ignored.
type Provider interface {
	For(ctx context.Context, key Key) []Policy
}

// ProviderFunc adapts a function to Provider.
type ProviderFunc func(ctx context.Context, key Key) []Policy

// For implements Provider.
func (f ProviderFunc) For(ctx context.Context, key Key) []Policy {
	if f == nil {
		return nil
	}
	return f(ctx, key)
}
