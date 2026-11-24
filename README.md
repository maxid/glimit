English | [简体中文](README.zh-CN.md)

# glimit

The shared vocabulary for **rate limits, byte rates, concurrency caps and quotas**: what is measured,
for whom, and what a denial means. It is a leaf package — standard library only — so a transport, a
protocol library, a web framework and an application can all accept the same contract.

> Status: **development snapshot (untagged)** — contract, token bucket, counter, compiled gate with
> TTL key reclamation, and an example. No tags are published until a stable release; use commit
> pseudo-versions. License: MIT.

## Positioning

`glimit` owns no policy source and no protocol. It answers one question:

```go
d := limiter.Allow(ctx, glimit.Key{
	Kind:  glimit.KindRequests,
	Scope: glimit.ScopePeer,
	ID:    "203.0.113.7",
}, 1)
if !d.Allow {
	// d.Reason is from a closed vocabulary: rate, bytes, concurrency, quota, ...
	// d.RetryAfter is how long until the same request could succeed.
}
```

A library keeps its own limits and consults a `Limiter` in addition:

```
allow = builtin limit AND limiter.Allow(key, cost)
```

Nothing is replaced, so `Limiter == nil` behaves exactly as before. This layer narrows, never widens.

Enforcement is **admission, not shaping**: `Allow` is binary, never sleeps and never queues. A library
that wants to delay instead of reject (a byte shaper, for example) keeps that machinery and may reuse
the exported `Bucket` for its accounting.

## Vocabulary

| Kind | Meaning | Shape |
|---|---|---|
| `connections` | concurrent sessions | `Max`, credit held until `Release` |
| `requests` | events per second (accepts, frames, calls) | `Rate` + `Burst` |
| `bytes` | bytes per second, split by `Dir` (`in`/`out`) | `Rate` + `Burst` |
| `inflight` | concurrent operations (uploads, outstanding requests) | `Max`, credit held until `Release` |

Scope is `global`, `peer` or `session`. Identity lives in `Key.ID` and must never become a metric
label — it is unbounded by nature.

Reasons form a closed, bounded vocabulary, which is also the reason label of a rejection metric:
`rate`, `burst`, `concurrency`, `bytes`, `quota`, `overload`, `unavailable`. The last three are
reserved for a policy source — a tenant quota, a licence entitlement, adaptive load shedding — and no
built-in policy produces them.

## Policy sources

A static set:

```go
gate, err := glimit.Compile(glimit.Spec{Policies: []glimit.Policy{
	{Kind: glimit.KindConnections, Scope: glimit.ScopeGlobal, Max: 10_000},
	{Kind: glimit.KindConnections, Scope: glimit.ScopePeer, Max: 32},
	{Kind: glimit.KindBytes, Scope: glimit.ScopeSession, Dir: glimit.DirOut, Rate: 1 << 20},
}})
```

A dynamic one, merged with it — the strictest wins, and every policy must pass:

```go
gate, err := glimit.Compile(spec, glimit.WithProvider(myTenantQuota))
```

`Spec` carries JSON tags only; YAML or TOML configuration is mapped by the consumer so the package
stays dependency free. A provider's policies are re-read on every decision, which is what makes a
licence, a quota store or an adaptive controller a drop-in source instead of a library change.

## Determinism and memory

Time is injected (`WithClock`), so refill is exact in tests and no `sleep` is needed. Per-key state is
reclaimed after a TTL of inactivity (`WithTTL`, five minutes by default) and the table is bounded
(`WithMaxKeys`, 4096 by default); a key holding credit is never reclaimed. When every tracked key
still holds credit the gate denies new keys with `ReasonUnavailable` — it fails closed rather than
growing without bound.

## Install

```bash
go get github.com/maxid/glimit
```

## Example

```bash
go run ./examples/limits
```

## Package layout

```
glimit       contract (Kind/Scope/Key/Policy/Spec/Decision/Reason), Bucket, Counter, Gate
examples/    one runnable program per question
```
