package order

// PromiseGroup is the ADR 0014 §3 / ADR 0017 value object: a set of line
// numbers that share one Promise, because they can all make the same
// cutoff. A ship-complete order (AllowPartialShipment=false) always has
// exactly one group covering every allocated line — the "exactly as
// today" floor ADR 0014 §3 guarantees. A partial-shipment order
// (AllowPartialShipment=true) may have several: lines are grouped by the
// cutoff each one can independently make.
//
// LineNos is kept as a plain slice, not a set, because line order is
// already meaningful elsewhere on this aggregate (OrderLine.LineNo is a
// stable 1-based position) and a small, sorted slice is both simpler to
// persist (Postgres INT[] or a join table) and simpler to read in a log
// line than a map.
type PromiseGroup struct {
	LineNos []int
	Promise Promise
}
