package order

import (
	"time"

	"github.com/claudioed/order-management/internal/domain/shared"
)

// DefaultLeadTime is the promise-date lead time applied to a process path
// with no configured lead time of its own.
const DefaultLeadTime = 48 * time.Hour

// LeadTimePolicy is the domain policy that turns "when was this order
// allocated" into "when do we promise it".
//
// v1 deliberately has no live carrier-rate integration: there is no such
// service anywhere in this fleet to call. A configurable per-path lead
// time is the honest v1 model — real code with real behaviour, not a
// hardcoded field pretending to be a calculation. Swapping in a carrier
// API later replaces this policy without touching the Order aggregate.
type LeadTimePolicy struct {
	// Default applies to any path not named in PerPath. A zero value
	// means DefaultLeadTime.
	Default time.Duration
	// PerPath overrides Default for specific process paths, e.g. a
	// "singles" path that is faster than "pick".
	PerPath map[shared.PathId]time.Duration
}

// NewLeadTimePolicy builds a policy. A non-positive defaultLeadTime falls
// back to DefaultLeadTime; non-positive per-path overrides are ignored, so
// a misconfigured override can never promise an order in the past.
func NewLeadTimePolicy(defaultLeadTime time.Duration, perPath map[shared.PathId]time.Duration) LeadTimePolicy {
	if defaultLeadTime <= 0 {
		defaultLeadTime = DefaultLeadTime
	}
	cleaned := make(map[shared.PathId]time.Duration, len(perPath))
	for path, d := range perPath {
		if d > 0 {
			cleaned[path] = d
		}
	}
	return LeadTimePolicy{Default: defaultLeadTime, PerPath: cleaned}
}

// LeadTimeFor returns the lead time configured for a process path.
func (p LeadTimePolicy) LeadTimeFor(pathID shared.PathId) time.Duration {
	if d, ok := p.PerPath[pathID]; ok && d > 0 {
		return d
	}
	if p.Default > 0 {
		return p.Default
	}
	return DefaultLeadTime
}

// PromiseDate computes the order-level promise date at allocation time:
// now plus the SLOWEST lead time among the lines that are actually
// allocated. The slowest line governs because the promise is made for the
// order, not per line — a customer is not promised a date the order as a
// whole cannot meet.
//
// Lines that are not Allocated (still Pending, Backordered, Cancelled)
// contribute nothing: an unallocated line has no committed work yet, so it
// cannot pull the promise out. Returns ok=false when no line is allocated,
// which is exactly when an order has no promise date to publish.
func (p LeadTimePolicy) PromiseDate(now time.Time, o *Order) (time.Time, bool) {
	var longest time.Duration
	found := false
	for _, l := range o.Lines() {
		if l.Status() != LineAllocated && l.Status() != LineReleased {
			continue
		}
		found = true
		if lead := p.LeadTimeFor(l.PathID()); lead > longest {
			longest = lead
		}
	}
	if !found {
		return time.Time{}, false
	}
	return now.Add(longest), true
}
