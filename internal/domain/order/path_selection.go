package order

import "github.com/claudioed/order-management/internal/domain/shared"

// EligibilitySource is the minimal catalogue question PathSelectionPolicy
// needs: a path's declared Eligibility rule. Any adapter satisfying
// ports.ProcessPathCatalogue's superset of methods satisfies this
// narrower domain-owned interface automatically (Go's structural
// interface-to-interface assignability), exactly the pattern
// PromisePolicy's CapabilitySource/ScheduleSource/CapacitySource already
// use -- no adapter code needs to know this type exists. A nil
// EligibilitySource is a legal, tested value (the catalogue is not yet
// wired -- see ReceiveOrder's Catalogue field doc comment): Select then
// falls back to today's behaviour, exactly ADR-0013's default.
type EligibilitySource interface {
	Eligibility(pathID shared.PathId) (shared.Eligibility, bool)
}

// PathSelectionPolicy decides which wes-work-planning process path an
// OrderLine's work should be enqueued onto, based on attributes known at
// intake (SKU, Quantity, GiftWrap) and the line's derived product
// attributes (e.g. "hazmat", "fragile", looked up via the fleet's
// product-classification sync edge -- see ReceiveOrder), evaluated
// against a candidate path's declared Eligibility. It is a pure domain
// policy: no I/O of its own. The application layer (ReceiveOrder)
// performs the classification lookup and passes plain values in, and
// supplies the EligibilitySource so this type never touches Kafka/HTTP.
//
// ADR-0013 shipped v1 with exactly one rule ("every line resolves to
// shared.DefaultPathId") and deliberately deferred attribute-driven
// routing until real eligibility data existed. ADR-0016 (step B of
// ADR-0014) is that phase: this type now actually evaluates Eligibility
// rather than ignoring its inputs. What it does NOT do -- and this is a
// documented, honest v1 limitation, not an oversight -- is choose AMONG
// multiple real paths: ports.ProcessPathCatalogue has no "list every
// known path" method today, so there is no way for this policy to
// discover a genuinely different, eligible alternative when
// shared.DefaultPathId's own Eligibility rejects a line. See ADR-0016's
// Consequences section for what a real multi-path routing decision would
// need. Given that constraint, the honest v1 rule is: evaluate the one
// candidate this service can reach (shared.DefaultPathId) against the
// line's attributes, and report whether it is eligible at all --
// ReceiveOrder turns an ineligible result into a caller-facing rejection
// (shared.ErrLineIneligibleForResolvedPath) rather than silently routing
// a hazmat line onto a path whose Eligibility explicitly excludes it.
type PathSelectionPolicy struct{}

// giftWrapAttribute is the product-attribute-vocabulary string this
// policy uses to represent "this line requested gift wrap" when
// evaluating it against a path's declared RequiredProductAttributes /
// ExcludedProductAttributes -- the same free-form vocabulary as
// "hazmat"/"fragile" (see shared.Eligibility's doc comment, which
// explicitly lists "giftWrap" alongside them).
const giftWrapAttribute = "giftWrap"

// Select resolves the PathId for a line described by sku, quantity,
// giftWrap and productAttributes (free-form strings from the fleet's
// existing product-classification vocabulary, e.g. "hazmat", "fragile",
// looked up via ReceiveOrder's classification lookup -- sku itself is
// accepted for symmetry with ADR-0013's original signature and so a
// future rule keyed directly on SKU needs no signature change again).
//
// catalogue is consulted for shared.DefaultPathId's declared Eligibility.
// A nil catalogue, or a catalogue that does not (yet) know
// DefaultPathId's eligibility, fails OPEN to today's ADR-0013 behaviour
// (ok=true, shared.DefaultPathId) -- missing catalogue data is a "not yet
// wired" state, never a rejection trigger, matching this fleet's
// established convention for a read-only membership/eligibility check.
//
// ok=false means: this line's own attributes are not eligible for
// shared.DefaultPathId, and this policy has no other real path to
// consider (see the type doc comment's honest v1 limitation). The
// returned PathId is the zero value in that case; callers must not use
// it.
func (PathSelectionPolicy) Select(sku shared.SKU, quantity int, giftWrap bool, productAttributes []string, catalogue EligibilitySource) (shared.PathId, bool) {
	_ = sku // accepted for signature symmetry/future use; v1's rule does not key on it directly

	if catalogue == nil {
		return shared.DefaultPathId, true
	}

	eligibility, known := catalogue.Eligibility(shared.DefaultPathId)
	if !known {
		return shared.DefaultPathId, true
	}

	attributes := productAttributes
	if giftWrap {
		attributes = append(append([]string(nil), productAttributes...), giftWrapAttribute)
	}

	if !lineEligible(eligibility, quantity, attributes) {
		return "", false
	}
	return shared.DefaultPathId, true
}

// lineEligible reports whether a line with the given quantity and
// productAttributes satisfies eligibility's declared rule: quantity
// within MaxUnitsPerLine (when bounded), every RequiredProductAttribute
// present, and no ExcludedProductAttribute present. NonSortable is a
// property of the freight the path carries as a whole, not a per-line
// input this policy has a signal for, so it is deliberately not
// evaluated here.
func lineEligible(eligibility shared.Eligibility, quantity int, productAttributes []string) bool {
	if max := eligibility.MaxUnitsPerLine(); max != nil && quantity > *max {
		return false
	}

	for _, required := range eligibility.RequiredProductAttributes() {
		if !containsAttribute(productAttributes, required) {
			return false
		}
	}
	for _, excluded := range eligibility.ExcludedProductAttributes() {
		if containsAttribute(productAttributes, excluded) {
			return false
		}
	}
	return true
}

func containsAttribute(attributes []string, target string) bool {
	for _, a := range attributes {
		if a == target {
			return true
		}
	}
	return false
}
