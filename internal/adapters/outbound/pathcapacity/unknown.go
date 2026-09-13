// Package pathcapacity provides the outbound adapter(s) implementing
// ports.PathCapacity. wes-work-planning does not publish remaining
// capacity per (path, CPT) yet (see ADR-0014's rollout step 3), so this
// package currently has exactly one implementation: UnknownPathCapacity,
// which always reports known=false. PromisePolicy treats an unknown
// capacity as "not a constraint" — this is the honest v1 stated in the
// ADR, not a placeholder pretending to know something it does not. A
// future Kafka-fed implementation, consuming PathCapacityChanged, will
// live alongside this one and replace it in the composition root without
// touching PromisePolicy at all.
package pathcapacity

import "github.com/claudioed/order-management/internal/domain/shared"

// UnknownPathCapacity is the seam ADR-0014 describes as filled in a
// later phase: it satisfies ports.PathCapacity but never claims to know
// anything.
type UnknownPathCapacity struct{}

// NewUnknown constructs an UnknownPathCapacity. It has no state.
func NewUnknown() UnknownPathCapacity { return UnknownPathCapacity{} }

// Remaining always reports known=false: this adapter has no capacity
// data source.
func (UnknownPathCapacity) Remaining(_ shared.PathId, _ string) (int, bool) {
	return 0, false
}
