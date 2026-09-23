package order

// ValidateIntakeIntent enforces ADR 0020 §4: an order that is held at
// intake (releaseOnAllocation=false) must be ship-complete.
//
// The two are contradictory. A caller that holds an order is deciding
// whether to commit to it as a whole — it will send one answer to
// whoever asked. Per-shipment-group promising (ADR 0017) would hand it
// several different cutoffs for one decision it can only make once.
//
// This lives in the domain, and is called at intake, rather than being
// left as a convention in the calling context. That placement is the
// whole point: trusting the caller to always pass the right combination
// fails the first time someone "improves" held orders by enabling split
// shipments, and for network-originated demand it fails EXTERNALLY,
// where this fleet is penalised for a partial shipment against a
// fill-or-kill commitment.
//
// Note this is deliberately NOT stored on the aggregate. The hold state
// is "allocated but not released" — expressible in the line statuses the
// aggregate already owns — so a persisted flag would add a column that
// carries no meaning once release has happened, and could drift from the
// line statuses that are the real truth.
func ValidateIntakeIntent(allowPartialShipment, releaseOnAllocation bool) error {
	if !releaseOnAllocation && allowPartialShipment {
		return ErrHeldOrderMustBeShipComplete
	}
	return nil
}
