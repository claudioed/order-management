package shared

import "time"

// ActivePathCandidate is order.PathSelectionPolicy's own mirror of
// ports.ActivePath (ADR-0021) — one currently active process path a line
// could be routed to, with the capability facts the policy needs to rank
// it. Independently declared here rather than importing the application
// layer's port type, exactly the same reasoning as Eligibility's own doc
// comment (the domain layer never imports ports or adapter types).
type ActivePathCandidate struct {
	PathId         PathId
	CycleTimeP95   time.Duration
	CycleTimeKnown bool
	Eligibility    Eligibility
}
