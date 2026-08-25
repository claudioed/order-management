// Package usecases: shared allocation and release machinery.
//
// Before the choreographed-release redesign, allocation and release were
// two separately invoked public use cases (AllocateOrder, ReleaseOrder)
// driven by their own REST verbs. That exposed internal saga-step
// mechanics as public API: a caller had to know to call /allocate then
// /release, when all it actually wants is to place an order. Both are now
// folded into ONE flow — allocateAndRelease — invoked from inside
// ReceiveOrder (implicitly, right after intake) and RetryAllocation
// (explicitly, as an operator/recovery action). See ADR 0005.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// maxCauseLen bounds OrderAllocationPartiallyFailed.Cause so a verbose
// upstream error (or an accidentally-embedded response body) never turns a
// visibility event into an unbounded log payload.
const maxCauseLen = 500

// truncateCause defensively bounds an error string for safe logging. It
// never returns more than maxCauseLen runes.
func truncateCause(s string) string {
	r := []rune(s)
	if len(r) <= maxCauseLen {
		return s
	}
	return string(r[:maxCauseLen]) + "…"
}

// allocationOutcome counts what one allocation pass achieved.
type allocationOutcome struct {
	allocated   int
	backordered int
}

// allocateLines runs the reserve-per-line loop shared by every caller that
// attempts allocation. retry selects which domain transition is legal for
// the lines being processed: Order.Allocate for Pending lines, and
// Order.RetryAllocate — the only route out of Backordered — for retries.
//
// The 409-vs-everything-else distinction is the heart of this function:
//
//   - 409 (ports.ErrInsufficientStock) is a BUSINESS FACT — that specific
//     line is Backordered, and allocation continues with the next line.
//   - a transport failure, a 5xx, or any other non-2xx is NOT a business
//     fact. It is ambiguous: the reservation may or may not exist upstream.
//     The whole call fails; no line is silently marked Backordered.
func allocateLines(
	ctx context.Context,
	inventory ports.InventoryReservationClient,
	events ports.EventPublisher,
	clock ports.Clock,
	o *order.Order,
	lines []*order.OrderLine,
	retry bool,
) (allocationOutcome, error) {
	var outcome allocationOutcome

	for _, line := range lines {
		result, err := inventory.Reserve(ctx, ports.ReservationRequest{
			SKU:       line.SKU(),
			Quantity:  line.Quantity(),
			DemandRef: o.ID(),
		})

		switch {
		case err == nil:
			// fall through to the allocate transition below

		case errors.Is(err, ports.ErrInsufficientStock):
			if err := o.MarkBackordered(line.LineNo()); err != nil {
				return outcome, err
			}
			outcome.backordered++
			if err := events.Publish(ctx, shared.NewOrderLineBackordered(
				clock.Now(), o.ID(), line.LineNo(), line.SKU(), line.Quantity(),
			)); err != nil {
				return outcome, err
			}
			continue

		default:
			// Ambiguous: not a business fact. Fail closed.
			return outcome, err
		}

		transition := o.Allocate
		if retry {
			transition = o.RetryAllocate
		}
		if err := transition(line.LineNo(), result.ReservationID); err != nil {
			return outcome, err
		}
		outcome.allocated++
		if err := events.Publish(ctx, shared.NewOrderLineAllocated(
			clock.Now(), o.ID(), line.LineNo(), line.SKU(), line.Quantity(), result.ReservationID,
		)); err != nil {
			return outcome, err
		}
	}

	return outcome, nil
}

// publishOrderAllocationOutcome emits the order-level event that matches
// the aggregate's derived status after an allocation-then-release pass. A
// fully backordered order emits no order-level event: its per-line
// OrderLineBackordered facts already carry the whole story.
//
// StatusReleased/StatusPartiallyReleased are included alongside
// StatusAllocated/StatusPartiallyAllocated because allocateAndRelease runs
// release in the SAME pass, right after allocation succeeds: by the time
// this function reads o.Status(), a ship-complete order that just became
// fully allocated has typically already been carried through to
// StatusReleased, and a partial-shipment order to StatusPartiallyReleased.
// OrderAllocated/OrderPartiallyAllocated are the SAME facts as before,
// just now additionally carrying the lines released in this pass — they
// are not renamed, because from an integration consumer's point of view
// "this order's allocation pass concluded, and here is what was released
// as part of it" is exactly the same milestone as pre-redesign
// OrderAllocated, just enriched.
func publishOrderAllocationOutcome(
	ctx context.Context,
	events ports.EventPublisher,
	clock ports.Clock,
	o *order.Order,
	outcome allocationOutcome,
	released []shared.ReleasedLine,
) error {
	if outcome.allocated == 0 && outcome.backordered == 0 {
		return nil
	}

	var promiseDate time.Time
	if d := o.PromiseDate(); d != nil {
		promiseDate = *d
	}

	switch o.Status() {
	case order.StatusAllocated, order.StatusReleased:
		return events.Publish(ctx, shared.NewOrderAllocated(clock.Now(), o.ID(), promiseDate, released))
	case order.StatusPartiallyAllocated, order.StatusPartiallyReleased:
		return events.Publish(ctx, shared.NewOrderPartiallyAllocated(
			clock.Now(), o.ID(), outcome.allocated, outcome.backordered, promiseDate, released,
		))
	default:
		return nil
	}
}

// allocationDeps bundles the outbound dependencies allocateAndRelease
// needs. ReceiveOrder and RetryAllocation each pass their own struct
// fields through one value rather than a long positional parameter list.
type allocationDeps struct {
	Orders    ports.OrderRepo
	Inventory ports.InventoryReservationClient
	Events    ports.EventPublisher
	Clock     ports.Clock
	Promise   order.LeadTimePolicy
}

// setPromiseDate applies deps.Promise's lead-time policy to o, exactly as
// AllocateOrder/RetryAllocation did pre-redesign.
func (deps allocationDeps) setPromiseDate(o *order.Order) {
	if d, ok := deps.Promise.PromiseDate(deps.Clock.Now(), o); ok {
		o.SetPromiseDate(d)
	}
}

// allocateAndRelease is the ONE shared flow this redesign folds allocation
// and release into. Both ReceiveOrder (implicitly, right after intake) and
// RetryAllocation (explicitly) call it with the lines eligible for THIS
// pass (Pending for ReceiveOrder, Backordered for RetryAllocation).
//
// Steps:
//  1. allocateLines — the reserve-per-line loop (unchanged behaviour).
//  2. On a hard (non-business) failure, persist whatever was genuinely
//     reserved before the failure (never stranding a real reservation
//     with nothing in this context able to revoke it) and publish
//     OrderAllocationPartiallyFailed for visibility if anything was
//     reserved. The hard failure is returned to the caller either way —
//     it is each CALLER's decision (see ReceiveOrder vs RetryAllocation)
//     whether to propagate that error to ITS OWN caller, since ReceiveOrder
//     must not fail just because its implicit allocation attempt did.
//  3. On success, apply the promise-date policy, then attempt release:
//     o.EnsureReleasable() enforces BR3 (a ship-complete order releases
//     nothing while any line is unallocated); when it passes, every
//     currently-Allocated line is released via the pure domain transition
//     o.Release, and its released-line detail is collected for the
//     integration event payload.
//  4. Save the order ONCE — covering both the allocation and the release
//     state changes in a single write.
//  5. publishOrderAllocationOutcome exactly once, carrying the lines
//     released in this pass (or nil when release did not run/had nothing
//     to release).
func allocateAndRelease(
	ctx context.Context,
	deps allocationDeps,
	o *order.Order,
	lines []*order.OrderLine,
	retry bool,
) (allocationOutcome, error) {
	outcome, allocErr := allocateLines(ctx, deps.Inventory, deps.Events, deps.Clock, o, lines, retry)
	if allocErr != nil {
		// Persist whatever was genuinely reserved upstream before
		// surfacing the hard failure — see the allocateLines doc.
		if outcome.allocated > 0 {
			deps.setPromiseDate(o)
			if saveErr := deps.Orders.Save(ctx, o); saveErr != nil {
				return outcome, errors.Join(allocErr, saveErr)
			}
			// Best-effort visibility: a failure publishing this event
			// must never mask or replace allocErr, the real failure —
			// it is joined in exactly like the saveErr case above so
			// errors.Is(err, allocErr) still holds.
			remaining := len(o.LinesWithStatus(order.LinePending))
			if pubErr := deps.Events.Publish(ctx, shared.NewOrderAllocationPartiallyFailed(
				deps.Clock.Now(), o.ID(), outcome.allocated, remaining, truncateCause(allocErr.Error()),
			)); pubErr != nil {
				return outcome, errors.Join(allocErr, pubErr)
			}
		}
		return outcome, allocErr
	}

	deps.setPromiseDate(o)

	// BR3-gated release: EnsureReleasable enforces that a ship-complete
	// order releases nothing while any line is still unallocated. When it
	// passes, every line the aggregate now reports Allocated (this pass's
	// newly-allocated lines, and any already-Allocated from an earlier
	// pass) is eligible and is released right here, in the same flow.
	var released []shared.ReleasedLine
	if err := o.EnsureReleasable(); err == nil {
		for _, line := range o.LinesWithStatus(order.LineAllocated) {
			if err := o.Release(line.LineNo()); err != nil {
				return outcome, err
			}
			released = append(released, shared.ReleasedLine{
				LineNo: line.LineNo(), SKU: line.SKU(), PathID: line.PathID(), GiftWrap: line.GiftWrap(),
			})
		}
	}

	if err := deps.Orders.Save(ctx, o); err != nil {
		return outcome, err
	}
	if err := publishOrderAllocationOutcome(ctx, deps.Events, deps.Clock, o, outcome, released); err != nil {
		return outcome, err
	}
	return outcome, nil
}

// WorkUnitID builds the deterministic, deliberately-never-transmitted id
// (order_id, line_no) => work unit reference this context and
// wes-work-planning's Kafka consumer BOTH independently derive from the
// exact same formula: `{orderID}-line-{lineNo}`. It is no longer sent
// anywhere over the wire — the choreographed-release redesign carries no
// synchronous call to wes-work-planning at all — but the formula itself is
// the frozen contract that lets that service's consumer reconstruct the
// same identifier for idempotent processing of the lines[] entries on the
// OrderAllocated/OrderPartiallyAllocated Kafka event. It MUST match
// byte-for-byte; do not change this format without coordinating both
// sides.
func WorkUnitID(orderID shared.OrderId, lineNo int) string {
	return fmt.Sprintf("%s-line-%d", orderID.String(), lineNo)
}
