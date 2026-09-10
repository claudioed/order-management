package order

import "github.com/claudioed/order-management/internal/domain/shared"

// PathSelectionPolicy decides which wes-work-planning process path an
// OrderLine's work should be enqueued onto, based on attributes already
// known at intake (SKU, Quantity, GiftWrap). It is a pure domain policy:
// no I/O, no catalogue access. "Which path does this line's shape imply"
// and "is that path currently active in the fleet's real catalogue" are
// two different questions with two different sources of truth -- this
// type answers only the first. ReceiveOrder answers the second
// separately, via ports.ProcessPathCatalogue.
//
// v1 has exactly one rule: every line resolves to shared.DefaultPathId.
// That looks trivial, but it replaces an UNREACHABLE hardcoded constant
// (the previous design fixed PathID to "pick" at the HTTP adapter layer,
// before the domain ever saw the line) with a real, testable domain
// policy object -- see order-management ADR-0013. Attribute-driven
// routing (hazmat-capable paths, oversize, a gift-wrap-capable path) is
// deliberately deferred until process-path-management's
// RequiredCapabilities field is populated with real fleet data; building
// a richer rule today would mean validating lines against capabilities
// that don't exist anywhere yet.
type PathSelectionPolicy struct{}

// Select resolves the PathId for a line described by sku, quantity, and
// giftWrap. All three are accepted now, even though v1's single rule
// ignores them, so a future richer rule can change this method's body
// without changing every caller's signature.
func (PathSelectionPolicy) Select(sku shared.SKU, quantity int, giftWrap bool) shared.PathId {
	return shared.DefaultPathId
}
