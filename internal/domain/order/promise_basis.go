package order

import "time"

// PromiseBasis names which policy produced a Promise, so analytics can
// separate real capability-derived promises from LeadTimePolicy
// fallbacks instead of averaging them together (ADR 0014).
type PromiseBasis string

const (
	// BasisCapability: the promise names a concrete CPT window every
	// allocated line can make, derived from process-path-management's
	// published cycle time, eligibility and CPT schedule.
	BasisCapability PromiseBasis = "Capability"

	// BasisLeadTime: LeadTimePolicy produced the promise — no CPT
	// schedule for the site, a path with unknown cycle time, or no
	// window in the queried horizon that every allocated line could
	// make. This is the same policy and behaviour this service used
	// before ADR 0014, kept unchanged as the honest fallback.
	BasisLeadTime PromiseBasis = "LeadTime"
)

func (b PromiseBasis) String() string { return string(b) }

// Promise is the value object ADR 0014 introduces: a delivery promise
// expressed as a discrete CPT window (CptId, CutoffAt) rather than a
// bare timestamp, tagged with which policy produced it.
//
// CptId is empty for a LeadTime-basis promise: LeadTimePolicy has no
// notion of a departure identity, only a computed instant. CutoffAt is
// always populated — it is the value persisted on the wire as
// promise_date/promiseDate for backward compatibility (see ADR 0014
// §1) regardless of which basis produced it.
type Promise struct {
	CptId    string
	CutoffAt time.Time
	Basis    PromiseBasis
}
