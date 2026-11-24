package glimit

import (
	"errors"
	"fmt"
	"math"
)

// Policy is one declarative limit. Exactly one shape applies:
//
//   - KindConnections / KindInflight take Max (a hard cap on concurrent units)
//     and no Rate.
//   - KindRequests / KindBytes take Rate (units per second, Burst allowed) and
//     no Max.
//
// Reason overrides the denial reason derived from Kind; it is how a Provider
// reports a quota or overload denial without inventing a new Kind.
type Policy struct {
	Kind   Kind      `json:"kind"`
	Scope  Scope     `json:"scope"`
	Dir    Direction `json:"dir,omitempty"`
	Rate   float64   `json:"rate,omitempty"`
	Burst  int       `json:"burst,omitempty"`
	Max    int       `json:"max,omitempty"`
	Reason Reason    `json:"reason,omitempty"`
}

// Spec is a static policy set. It carries JSON tags only: a YAML or TOML
// configuration maps onto these fields in the consumer, so the package stays
// dependency free.
type Spec struct {
	Policies []Policy `json:"policies"`
}

// Validate reports every problem with the policy set, not just the first.
func (s Spec) Validate() error {
	var errs []error
	for i, p := range s.Policies {
		if err := p.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("policy %d: %w", i, err))
		}
	}
	return errors.Join(errs...)
}

// Validate reports why p cannot be enforced.
func (p Policy) Validate() error {
	switch p.Kind {
	case KindConnections, KindRequests, KindBytes, KindInflight:
	default:
		return fmt.Errorf("unknown kind %q", p.Kind)
	}
	switch p.Scope {
	case ScopeGlobal, ScopePeer, ScopeSession:
	default:
		return fmt.Errorf("unknown scope %q", p.Scope)
	}
	switch p.Dir {
	case DirAny, DirIn, DirOut:
	default:
		return fmt.Errorf("unknown direction %q", p.Dir)
	}
	if p.Dir != DirAny && p.Kind != KindBytes && p.Kind != KindRequests {
		return fmt.Errorf("direction applies to bytes and requests, not %s", p.Kind)
	}
	if p.Rate < 0 || math.IsNaN(p.Rate) || math.IsInf(p.Rate, 0) {
		return fmt.Errorf("rate must be finite and non-negative")
	}
	if p.Burst < 0 || p.Max < 0 {
		return fmt.Errorf("burst and max must be non-negative")
	}
	if p.Reason != "" && p.Reason.rank() == unreasoned {
		return fmt.Errorf("unknown reason %q", p.Reason)
	}
	switch p.Kind {
	case KindConnections, KindInflight:
		if p.Max <= 0 {
			return fmt.Errorf("%s requires max", p.Kind)
		}
		if p.Rate > 0 {
			return fmt.Errorf("%s takes max, not rate", p.Kind)
		}
	case KindRequests, KindBytes:
		if p.Rate <= 0 {
			return fmt.Errorf("%s requires rate", p.Kind)
		}
		if p.Max > 0 {
			return fmt.Errorf("%s takes rate, not max", p.Kind)
		}
	}
	return nil
}

// matches reports whether the policy governs key. Direction is a wildcard when
// the policy leaves it empty.
func (p Policy) matches(key Key) bool {
	return p.Kind == key.Kind && p.Scope == key.Scope &&
		(p.Dir == DirAny || p.Dir == key.Direction)
}

// gated reports whether the policy holds credit until Release.
func (p Policy) gated() bool {
	return p.Kind == KindConnections || p.Kind == KindInflight
}

// usable is the hot-path check: a policy that cannot take effect is ignored
// rather than enforced. Compile validates the static spec; a Provider policy is
// checked here so a malformed one is skipped instead of denying everything.
func (p Policy) usable() bool {
	if p.Reason != "" && p.Reason.rank() == unreasoned {
		return false
	}
	if p.gated() {
		return p.Max > 0 && p.Rate == 0
	}
	return p.Rate > 0 && p.Max == 0
}

// burstSize is the bucket capacity for a rate policy.
func (p Policy) burstSize() int {
	if p.Burst > 0 {
		return p.Burst
	}
	n := int(math.Ceil(p.Rate))
	if n < 1 {
		n = 1
	}
	return n
}

// reason is the denial reason: the explicit override, else one derived from
// the kind.
func (p Policy) reason() Reason {
	if p.Reason != "" {
		return p.Reason
	}
	switch p.Kind {
	case KindBytes:
		return ReasonBytes
	case KindConnections, KindInflight:
		return ReasonConcurrency
	default:
		return ReasonRate
	}
}

// id identifies the state this policy needs (a bucket and/or a counter), so a
// policy re-issued with the same numbers keeps its state and a changed one gets
// a fresh slot.
func (p Policy) id() string {
	return fmt.Sprintf("%s|%s|%s|%g|%d|%d|%s",
		p.Kind, p.Scope, p.Dir, p.Rate, p.Burst, p.Max, p.Reason)
}

// unreasoned ranks unknown reasons after every known one; it is only used for
// validation.
const unreasoned = 99

// rank orders denial reasons from most specific to least. The first denial a
// decision pass meets wins, so a quota always reads as a quota and never as a
// rate.
func (r Reason) rank() int {
	switch r {
	case ReasonQuota:
		return 0
	case ReasonOverload:
		return 1
	case ReasonUnavailable:
		return 2
	case ReasonBurst:
		return 3
	case ReasonRate:
		return 4
	case ReasonBytes:
		return 5
	case ReasonConcurrency:
		return 6
	}
	return unreasoned
}
