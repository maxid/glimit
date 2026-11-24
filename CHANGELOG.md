# Changelog

> Development snapshots are untagged; entries below are chronological.

## [Unreleased]

### Added

- The contract: `Kind`, `Scope`, `Direction`, `Key`, `Policy`, `Spec`, `Decision` and the closed
  `Reason` vocabulary, plus the `Limiter`, `Provider` and `ProviderFunc` seams.
- `Bucket`, a token bucket that reports a retry hint and supports refund within a decision pass.
- `Counter`, a credit gauge for connections and in-flight operations.
- `Gate` (`Compile`): a static `Spec` merged with an optional `Provider`, strictest-wins, denial
  reason ordered from most specific to least, injected clock, TTL reclamation of idle keys and a
  bounded key table that fails closed.
- `examples/limits`: one runnable answer to "how do I put one gate in front of connections and a
  per-peer request rate?".
