---
id: 0008-fulfillment-class-demand-shape-classifier
slug: /adr/0008-fulfillment-class-demand-shape-classifier
title: 0008. FulfillmentClass — a demand-shape classifier, not a process-path name
sidebar_label: 0008. FulfillmentClass classifier
description: ADR 0008 — add Order.FulfillmentClass(), a derived value object classifying an order's demand shape (single / same-SKU multi / multi-line multi), computed on every call and propagated as an additive field on the frozen ReleasedLine/OrderAllocated Kafka contract.
---

# 0008. FulfillmentClass — a demand-shape classifier, not a process-path name

## Status

Accepted.

## Context

This platform's process-path reference material (the Amazon-fulfillment
DDD analysis this fleet's WES tier is modelled against) draws a sharp
distinction between two things that look similar on the surface:

- **Order composition** — how many lines an order has, and whether they
  share a SKU. This is a fact about *demand*.
- **Process path** — which named queue (Pick, Pack, SLAM, and in future
  Rebin) a unit of work is dispatched into inside `fulfillment-execution`
  or `wes-work-planning`. This is a fact about *execution*.

The reference material is explicit that conflating the two is a real
failure mode: "if you push each combination into the path name, you get a
combinatorial explosion — singles × conveyable × hazmat × gift, and a new
path every time marketing invents a promotion." The prescribed fix is to
model order shape as **a classifier, a value object on the shipment**,
computed once and consumed by whichever downstream policy needs it,
never baked into a path's identity.

This service (`order-management`) is the Core, upstream owner of `Order`
and `OrderLine` — the only bounded context with visibility into an
order's full line composition before release. It is therefore the only
correct place to compute this classifier; no downstream context
(`wes-work-planning`, `fulfillment-execution`) has, or should have, the
full picture of an order's original demand shape once individual lines
have already been split out as separate `ReleasedLine` entries.

Forces:

- **`Order.Status()` already sets the precedent for derive-don't-store.**
  This service has no field on `Order` that can drift from the lines it
  summarises — status is computed fresh on every call. A new classifier
  should follow the identical discipline rather than introduce the first
  cached/stored derived value in this aggregate.
- **The existing `GiftWrap` propagation pattern is the correct template
  for getting a fact downstream.** `OrderLine.GiftWrap` already rides the
  frozen `ReleasedLine`/`OrderAllocated` Kafka contract as an additive
  field that pre-existing consumers can ignore. `FulfillmentClass` is the
  same shape of problem: a fact order-management knows that a downstream
  context might want, added additively to an already-frozen wire
  contract rather than requiring a new topic or a new consumer round-trip.
- **The discriminator is line count, not merely SKU count.** The
  reference material's own taxonomy (single-line single-unit / single-line
  multi-unit / multi-line multi-unit) and the Dematic sortation patent it
  cites are both explicit that two lines of the *same* SKU are still a
  multi-line order for routing purposes — "it is more likely that a
  multiple item order contains at least multiple inventory items of
  different SKUs [but] both go to sortation. The same-SKU multi is a
  multi." Only when there is exactly one line does SKU quantity
  distinguish `Single` from `SameSKUMulti`.
- **This classifier must not gate or influence any existing invariant.**
  Exactly like `GiftWrap` (see fulfillment-execution's ADR-0011) and
  `Fragile`/hazmat (ADR-0009), a demand-shape classifier is a hint for
  downstream planning, never a capability-matching or station-eligibility
  concern. It has zero effect on `Order.Allocate`, `Release`,
  `EnsureReleasable`, or any other domain transition in this service.

### Alternatives considered

**Bake order shape into `PathId` naming** (e.g. a `pick-single` vs
`pick-multi` path). Rejected outright — this is precisely the
combinatorial-explosion failure mode the reference material warns
against, and it would also violate this service's own ADR-0005 decision
that `PathId` is an internal, caller-invisible default with no
routing-policy behavior yet.

**Compute the classifier downstream** (in `wes-work-planning`, from the
set of `ReleasedLine` entries sharing an `OrderID`). Rejected: by the time
lines are released, they already arrive as a flat list with no reliable
signal of "these N released lines came from the same order," and
reconstructing that grouping downstream duplicates knowledge
`order-management` already has for free, for no benefit.

**Add a new integration event just for the classification fact.**
Rejected as premature: no consumer today asks for this fact independent
of a line's release — riding the existing `ReleasedLine` payload (the
`GiftWrap` precedent) is minimal and additive, and a dedicated event can
be added later, non-breakingly, if a real independent need for it
appears.

## Decision

**Add `Order.FulfillmentClass() FulfillmentClass`, a derived value
computed fresh from `Lines()` on every call and never stored. Add a
`FulfillmentClass string` field to `shared.ReleasedLine`, populated by
`allocateAndRelease` from the order's own `FulfillmentClass()` at release
time, and add the matching `fulfillment_class` field to the Kafka
`releasedLineData` wire payload.**

```go
type FulfillmentClass string

const (
	ClassSingle         FulfillmentClass = "SINGLE"
	ClassSameSKUMulti   FulfillmentClass = "SAME_SKU_MULTI"
	ClassMultiLineMulti FulfillmentClass = "MULTI_LINE_MULTI"
)

func classify(lines []*OrderLine) FulfillmentClass {
	if len(lines) == 1 {
		if lines[0].Quantity() == 1 {
			return ClassSingle
		}
		return ClassSameSKUMulti
	}
	return ClassMultiLineMulti
}

func (o *Order) FulfillmentClass() FulfillmentClass {
	return classify(o.lines)
}
```

The value is the SAME for every line released in a given pass — it
classifies the whole order, not an individual line — mirroring how
`GiftWrap` and `Fragile` are line-scoped but `FulfillmentClass` is
order-scoped and simply repeated across the order's `ReleasedLine`
entries, since the wire contract has no separate order-level payload
today.

`fulfillment_class` is additive on the already-frozen
`OrderAllocated`/`OrderPartiallyAllocated` Kafka contract: existing
consumers (wes-work-planning's Kafka consumer as it exists today) can
ignore the new field entirely with no behavior change, exactly as they
already do for `gift_wrap`.

### What this decision does NOT do

- It does not change `Order.Status()`, any domain transition, or any
  existing invariant.
- It does not gate `Allocate`, `Release`, or `EnsureReleasable`.
- It does not wire `wes-work-planning` to actually consume or act on
  `fulfillment_class` yet (e.g. routing `ClassSingle` orders to skip a
  future Rebin path) — that is a separate, deliberately deferred decision
  for `wes-work-planning` to make in its own ADR once the field exists to
  consume, tracked as a follow-up in the fleet gap-closure plan.

## Consequences

### Easier

- **No new aggregate, no new consistency boundary.** `FulfillmentClass`
  is a pure function over `Order.lines`, following the exact
  derive-don't-store discipline `Status()` already established.
- **Downstream contexts get a real demand-shape signal for free**,
  computed once by the only context with full visibility into original
  order composition, without duplicating that knowledge.
- **Backward-compatible by construction.** The new field defaults to the
  Go zero value (`""`) for any code path that does not set it, and any
  consumer predating this field simply does not read the JSON key.
- **Sets up, without committing to, real routing behavior later** — e.g.
  a future `wes-work-planning` decision to route `ClassSingle` orders
  straight to `PACK`, bypassing a future `Rebin` path, per the reference
  material's "single-item shipments skip Induct and Rebin" observation.

### Harder

- **A third "hint that rides ReleasedLine" field**, after `GiftWrap`,
  continuing the growth this repo's own precedent already flagged (see
  fulfillment-execution's ADR-0011 Consequences) as a candidate for a
  future request/payload-struct refactor if this pattern keeps repeating.
- **The classifier is currently unconsumed.** No downstream context reads
  `fulfillment_class` yet — its value is proven correct here and
  propagated correctly on the wire, but its usefulness depends entirely
  on a future consumer decision this ADR deliberately does not make.
- **Two independently-maintained copies of the enum's string values**
  (Go `FulfillmentClass` constants here, whatever `wes-work-planning`
  eventually defines to parse them) — the same coordination-without-wire-
  enforcement cost ADR-0005 already accepted for `WorkUnitID`'s formula.

## Verification

Unit-tested at the domain layer
(`internal/domain/order/fulfillment_class_test.go`): all three
classifications, plus a dedicated test proving two lines of the identical
SKU still classify as `MultiLineMulti` (the Dematic-patent-derived
same-SKU-multi-is-still-a-multi rule), plus a test proving the value is
computed fresh on every call rather than cached from construction.
Use-case level (`internal/application/usecases/receive_order_test.go`)
proves `allocateAndRelease` actually stamps the order's own
`FulfillmentClass()` onto every line in the published `OrderAllocated`
event. Kafka-adapter level (`internal/adapters/outbound/kafka/publisher_test.go`)
proves the field survives the JSON round-trip onto
`data.lines[].fulfillment_class`. `go test -race ./...` and
`golangci-lint run ./...` both pass with zero new issues.
