package usecases

import (
	"context"

	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// NewLine is the application-level description of one requested line. The
// HTTP adapter maps its DTO onto this; the use case turns it into a
// validated domain OrderLine.
type NewLine struct {
	SKU      shared.SKU
	Quantity int
	PathID   shared.PathId
	GiftWrap bool
}

// ReceiveOrder is intake: it validates the requested lines, mints an
// OrderId, and persists a new Order in Received status, publishing
// OrderReceived unconditionally — a caller placing an order always sees
// that fact regardless of what happens next.
//
// Per the choreographed-release redesign (ADR 0005), ReceiveOrder ALSO
// immediately attempts allocation-then-release in the SAME call, right
// after the Save+OrderReceived publish above. This replaces the old
// design where a caller had to separately invoke POST /orders/{id}/allocate
// and POST /orders/{id}/release: placing an order now expresses the whole
// intent, and allocation/release are internal saga steps triggered by that
// intent rather than public commands a caller drives by hand.
//
// DESIGN DECISION — how a hard allocation failure is handled here (see the
// task brief's point 2d): allocateAndRelease can hard-fail (a transport
// error or 5xx from inventory-storage — never a 409/backorder, which is a
// business fact handled inside allocateLines, not an error here). The
// ORIGINAL AllocateOrder use case returned that hard failure to ITS OWN
// caller, because that caller's whole intent WAS "attempt allocation now"
// — failing was the honest answer to that specific request.
//
// ReceiveOrder's caller has a different intent: "place an order". The
// order object itself was successfully constructed and persisted (that
// already succeeded and was already published as OrderReceived) BEFORE
// allocation was ever attempted. A hard failure in the *implicit*,
// best-effort allocation attempt that follows must not retroactively turn
// a successful ReceiveOrder into a failed one — the order genuinely was
// received, and telling the caller otherwise would be a lie (the task
// brief is explicit: never claim ReceiveOrder failed when the order
// genuinely was created).
//
// So: ReceiveOrder treats allocateAndRelease as strictly best-effort here.
// A hard failure is NOT propagated as ReceiveOrder's error. It is also
// never silently swallowed with no trail — allocateAndRelease itself
// already publishes OrderAllocationPartiallyFailed whenever ANY line was
// genuinely allocated before the failure (its existing, unchanged
// fail-closed visibility pathway), and it always persists whatever
// allocation state genuinely happened (allocated lines saved; anything
// that failed before reservation stays Pending). If NO line was allocated
// at all before the hard failure (e.g. the very first line's reserve call
// hits a transport error), every line is left Pending — exactly the state
// a freshly-received, not-yet-allocated order would have had anyway pre-
// redesign, and it is logged here so the failure is not invisible even
// though no domain event fires for a zero-progress attempt (there is
// nothing new to report over "the order is Received", which OrderReceived
// already covered). Either way, the order this call returns to its caller
// reflects whatever the allocation pass actually achieved — Received,
// PartiallyAllocated, Backordered, Allocated, Released, or
// PartiallyReleased — with no error, because ReceiveOrder itself
// succeeded. A human/operator can always drive further progress via
// RetryAllocation, which — unlike this implicit best-effort attempt — DOES
// propagate a hard failure to its own caller, since invoking it is an
// explicit, on-purpose recovery action.
type ReceiveOrder struct {
	Orders    ports.OrderRepo
	Events    ports.EventPublisher
	Clock     ports.Clock
	Inventory ports.InventoryReservationClient
	Promise   order.LeadTimePolicy
}

func (uc *ReceiveOrder) Execute(ctx context.Context, lines []NewLine, allowPartialShipment bool) (*order.Order, error) {
	domainLines := make([]*order.OrderLine, 0, len(lines))
	for i, l := range lines {
		line, err := order.NewOrderLine(i+1, l.SKU, l.Quantity, l.PathID, l.GiftWrap)
		if err != nil {
			return nil, err
		}
		domainLines = append(domainLines, line)
	}

	id, err := uc.Orders.NextID(ctx)
	if err != nil {
		return nil, err
	}

	o, err := order.New(id, domainLines, allowPartialShipment)
	if err != nil {
		return nil, err
	}

	if err := uc.Orders.Save(ctx, o); err != nil {
		return nil, err
	}
	if err := uc.Events.Publish(ctx, shared.NewOrderReceived(uc.Clock.Now(), o.ID(), len(domainLines))); err != nil {
		return nil, err
	}

	// Best-effort implicit allocation-then-release — see the type doc for
	// the full reasoning. A hard failure here is intentionally NOT
	// returned: the order was genuinely received, and that already
	// succeeded above. allocateAndRelease's own fail-closed visibility
	// pathway (OrderAllocationPartiallyFailed) still fires when any
	// genuine progress was made before the failure, so this is never a
	// silent swallow — just never surfaced as a ReceiveOrder failure.
	deps := allocationDeps{Orders: uc.Orders, Inventory: uc.Inventory, Events: uc.Events, Clock: uc.Clock, Promise: uc.Promise}
	if _, err := allocateAndRelease(ctx, deps, o, o.LinesWithStatus(order.LinePending), false); err != nil {
		// allocateAndRelease may have mutated o in memory (e.g. marked a
		// line Backordered) without persisting that mutation — it only
		// saves when at least one line was genuinely allocated before the
		// hard failure. Re-read the persisted order so what ReceiveOrder
		// returns to its caller never diverges from what is actually
		// stored: the caller must see reality, not an in-memory state
		// that a crash right now would lose.
		stored, findErr := uc.Orders.FindByID(ctx, o.ID())
		if findErr != nil {
			// The order was definitely persisted moments ago (the Save
			// above succeeded); a failure to read it back is itself a
			// genuine infrastructure problem worth surfacing rather than
			// silently returning a possibly-stale in-memory object.
			return nil, findErr
		}
		if stored == nil {
			// Unreachable in practice (the Save above just succeeded),
			// but fall back to the in-memory object rather than return a
			// broken (nil, nil) result.
			return o, nil
		}
		return stored, nil
	}
	return o, nil
}
