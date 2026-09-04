package order

// FulfillmentClass classifies an order's demand shape — how many lines and
// how many distinct SKUs it carries — independent of which process path
// any line is routed to and independent of any equipment or building
// concern. It exists to answer "what kind of demand is this" for
// downstream planning/capacity decisions, never "which floor path should
// handle it": a process path is a queue this or a sibling context
// dispatches into, while FulfillmentClass is a value on the shipment
// itself. Conflating the two would produce a combinatorial explosion of
// path names (singles × hazmat × gift, etc.) the moment a new order
// characteristic is added — see the platform's process-path reference
// material on this exact failure mode.
//
// It is deliberately NOT a stored field: like Order.Status, it is derived
// fresh from Lines() on every call, so it can never drift from the lines
// that actually define it.
type FulfillmentClass string

const (
	// ClassSingle: exactly one line, quantity one. The industry term is
	// single-line single-unit — the only shape that legitimately bypasses
	// multi-item consolidation downstream.
	ClassSingle FulfillmentClass = "SINGLE"

	// ClassSameSKUMulti: exactly one line, quantity greater than one —
	// single-line multi-unit. Several units of the identical SKU
	// requested as one line. Distinct from ClassMultiLineMulti because
	// some downstream planning treats "more of the same item" differently
	// from "an assortment," even though both are non-single shipments.
	ClassSameSKUMulti FulfillmentClass = "SAME_SKU_MULTI"

	// ClassMultiLineMulti: more than one line, regardless of whether any
	// two lines happen to share a SKU — multi-line multi-unit. This is
	// the general assortment case and the only shape requiring
	// cross-line consolidation before it can be packed as one shipment.
	ClassMultiLineMulti FulfillmentClass = "MULTI_LINE_MULTI"
)

// classify derives an order's FulfillmentClass from its current lines.
// Line count is the primary discriminator (a second line of the same SKU
// is still two lines, hence MultiLineMulti — see the doc comment on
// ClassMultiLineMulti and its dedicated test); SKU count only
// distinguishes the two single-line cases from each other.
func classify(lines []*OrderLine) FulfillmentClass {
	if len(lines) == 1 {
		if lines[0].Quantity() == 1 {
			return ClassSingle
		}
		return ClassSameSKUMulti
	}
	return ClassMultiLineMulti
}

// FulfillmentClass classifies this order's current demand shape. Computed
// on every call from Lines() — never stored on the aggregate or
// persisted — so it always reflects the order's actual current lines,
// the same discipline Status() already follows.
func (o *Order) FulfillmentClass() FulfillmentClass {
	return classify(o.lines)
}
