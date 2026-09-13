package order

import (
	"time"

	"github.com/claudioed/order-management/internal/domain/shared"
)

// DefaultCPTHorizon bounds how many upcoming CPT windows PromisePolicy
// queries per Promise call when Horizon is unset (zero value).
const DefaultCPTHorizon = 10

// CPTWindow is one concrete, upcoming CPT (Critical Pull Time) this
// policy can promise a line against: a departure identity, the instant
// it cuts off, and the paths currently eligible to make it. It is the
// domain's own copy of the shape a ports.CPTScheduleCache adapter
// produces — kept here (rather than importing a ports type into the
// domain) because PromisePolicy is pure domain logic per ADR 0014 and
// must not depend on the application layer.
type CPTWindow struct {
	CptId           string
	CutoffAt        time.Time
	EligiblePathIds []string
}

// CapabilitySource is the minimal capability question PromisePolicy
// needs: a path's 95th-percentile cycle time. Any adapter satisfying
// ports.ProcessPathCatalogue's superset of methods satisfies this
// narrower domain-owned interface automatically (Go's structural
// interface-to-interface assignability) — no adapter code needs to know
// this type exists.
type CapabilitySource interface {
	CycleTimeP95(pathID shared.PathId) (time.Duration, bool)
}

// ScheduleSource is the minimal CPT-schedule question PromisePolicy
// needs. Satisfied by ports.CPTScheduleCache.
type ScheduleSource interface {
	NextCutoffs(siteId string, from time.Time, n int) ([]CPTWindow, bool)
}

// CapacitySource is the minimal remaining-capacity question PromisePolicy
// needs. Satisfied by ports.PathCapacity (including its current
// always-unknown UnknownPathCapacity implementation).
type CapacitySource interface {
	Remaining(pathID shared.PathId, cptId string) (units int, known bool)
}

// PromisePolicy is the domain service ADR 0014 introduces to replace
// LeadTimePolicy as the PRIMARY promise policy, while keeping
// LeadTimePolicy itself unchanged as the fallback. It is pure domain
// logic: no Kafka, no HTTP. Schedule/Capability/Capacity are small
// interfaces the application layer wires to real outbound ports; nil is
// a legal, tested value for any of them (missing input is a fallback
// trigger, never a panic) except Fallback, which should always be a real
// LeadTimePolicy.
//
// SiteId is a known simplification for this phase: order-management does
// not yet model which site an order ships from (ADR 0014 leaves that to
// a future phase). Every promise in this phase is computed against one
// configured site — see cmd/order/main.go's DEFAULT_SITE_ID wiring.
type PromisePolicy struct {
	Schedule   ScheduleSource
	Capability CapabilitySource
	Capacity   CapacitySource
	Fallback   LeadTimePolicy
	SiteId     string
	// Horizon bounds how many upcoming CPT windows are queried per
	// Promise call. Zero means DefaultCPTHorizon.
	Horizon int
}

func (p PromisePolicy) horizon() int {
	if p.Horizon > 0 {
		return p.Horizon
	}
	return DefaultCPTHorizon
}

// Promise computes the order's promise at allocation time: the earliest
// CPT window every allocated (or already released) line can make,
// falling back to LeadTimePolicy when no capability/schedule input is
// available or no window in the queried horizon qualifies. Returns
// ok=false only when LeadTimePolicy itself would also return ok=false —
// no line allocated yet, meaning there is nothing to promise.
//
// The rule per line, for a given window w: (a) the line's path is in
// w.EligiblePathIds; (b) now + cycleTimeP95(path) <= w.CutoffAt; (c)
// capacity for (path, w.CptId) is unknown, or remaining >= the line's
// quantity. Every allocated line must fit the SAME window — the same
// "most-constrained line governs" spirit LeadTimePolicy already applies
// order-wide.
func (p PromisePolicy) Promise(now time.Time, o *Order) (Promise, bool) {
	lines := allocatedLines(o)
	if len(lines) == 0 {
		return Promise{}, false
	}

	if p.Schedule == nil || p.Capability == nil {
		return p.fallback(now, o)
	}

	windows, known := p.Schedule.NextCutoffs(p.SiteId, now, p.horizon())
	if !known || len(windows) == 0 {
		return p.fallback(now, o)
	}

	for _, w := range windows {
		if p.linesFitWindow(now, lines, w) {
			return Promise{CptId: w.CptId, CutoffAt: w.CutoffAt, Basis: BasisCapability}, true
		}
	}
	return p.fallback(now, o)
}

// linesFitWindow reports whether every line in lines can make w, per the
// three conditions in Promise's doc comment.
func (p PromisePolicy) linesFitWindow(now time.Time, lines []*OrderLine, w CPTWindow) bool {
	eligible := make(map[shared.PathId]bool, len(w.EligiblePathIds))
	for _, id := range w.EligiblePathIds {
		eligible[shared.PathId(id)] = true
	}

	for _, l := range lines {
		if !eligible[l.PathID()] {
			return false
		}
		cycleTime, cycleKnown := p.Capability.CycleTimeP95(l.PathID())
		if !cycleKnown {
			return false
		}
		if now.Add(cycleTime).After(w.CutoffAt) {
			return false
		}
		if p.Capacity != nil {
			if remaining, capKnown := p.Capacity.Remaining(l.PathID(), w.CptId); capKnown && remaining < l.Quantity() {
				return false
			}
		}
	}
	return true
}

func (p PromisePolicy) fallback(now time.Time, o *Order) (Promise, bool) {
	d, ok := p.Fallback.PromiseDate(now, o)
	if !ok {
		return Promise{}, false
	}
	return Promise{CutoffAt: d, Basis: BasisLeadTime}, true
}

// allocatedLines returns the order's lines that are Allocated or
// Released — the same eligibility LeadTimePolicy.PromiseDate already
// applies, kept identical here so both policies agree on "what counts".
func allocatedLines(o *Order) []*OrderLine {
	var out []*OrderLine
	for _, l := range o.Lines() {
		if l.Status() == LineAllocated || l.Status() == LineReleased {
			out = append(out, l)
		}
	}
	return out
}
