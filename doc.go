// Package glimit is the shared vocabulary for rate, byte-rate, concurrency and
// quota limits: what is measured, whom it is measured for, and what a denial
// means. It owns no policy source — a static Spec, a tenant quota, a licence or
// an adaptive controller all arrive through the same two interfaces.
//
// A library accepts a Limiter and consults it at its own decision points. The
// caller keeps its existing limits; this layer narrows them, never widens them:
//
//	allow = builtin check AND limiter.Allow(key, cost)
//
// Enforcement is admission, not shaping: Allow is binary, never sleeps and
// never queues. Shaping (delaying instead of rejecting) belongs to the
// protocol library that needs it.
package glimit
