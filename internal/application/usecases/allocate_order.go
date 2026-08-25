package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// AllocateOrder reserves stock for every Pending line by calling
// inventory-storage's POST /reservations once per line.
//
// The 409-vs-everything-else distinction is the heart of this use case:
//
//   - 409 (ports.ErrInsufficientStock) is a BUSINESS FACT — that specific
//     line is Backordered, and allocation continues with the next line.
//   - a transport failure, a 5xx, or any other non-2xx is NOT a business
//     fact. It is ambiguous: the reservation may or may not exist upstream.
//     The whole call fails; no line is silently marked Backordered.
//
// On such a hard failure the lines already reserved in this run ARE
// persisted before the error is returned. Doing otherwise would strand
// real reservations inside inventory-storage with nothing in this context
// referencing them (and so nothing able to revoke them). Lines still
// Pending stay Pending, so simply calling AllocateOrder again resumes
// where it stopped — already-allocated lines are skipped, which is exactly
// the "cannot allocate the same line twice" invariant doing its job. When
// this happens, an OrderAllocationPartiallyFailed event is published so the
// resulting partial state is operationally visible rather than only
// discoverable by reading this comment.
type AllocateOrder struct {
	Orders    ports.OrderRepo
	Inventory ports.InventoryReservationClient
	Events    ports.EventPublisher
	Clock     ports.Clock
	Promise   order.LeadTimePolicy
}

func (uc *AllocateOrder) Execute(ctx context.Context, id shared.OrderId) (*order.Order, error) {
	o, err := uc.Orders.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrOrderNotFound
	}

	outcome, allocErr := allocateLines(ctx, uc.Inventory, uc.Events, uc.Clock, o, o.LinesWithStatus(order.LinePending), false)
	if allocErr != nil {
		// Persist whatever was genuinely reserved upstream before
		// surfacing the hard failure — see the type comment.
		if outcome.allocated > 0 {
			uc.setPromiseDate(o)
			if saveErr := uc.Orders.Save(ctx, o); saveErr != nil {
				return nil, errors.Join(allocErr, saveErr)
			}
			// Best-effort visibility: a failure publishing this event
			// must never mask or replace allocErr, the use case's real
			// failure — it is joined in exactly like the saveErr case
			// above so errors.Is(err, allocErr) still holds.
			remaining := len(o.LinesWithStatus(order.LinePending))
			if pubErr := uc.Events.Publish(ctx, shared.NewOrderAllocationPartiallyFailed(
				uc.Clock.Now(), o.ID(), outcome.allocated, remaining, truncateCause(allocErr.Error()),
			)); pubErr != nil {
				return nil, errors.Join(allocErr, pubErr)
			}
		}
		return nil, allocErr
	}

	uc.setPromiseDate(o)
	if err := uc.Orders.Save(ctx, o); err != nil {
		return nil, err
	}
	if err := publishOrderAllocationOutcome(ctx, uc.Events, uc.Clock, o, outcome); err != nil {
		return nil, err
	}
	return o, nil
}

func (uc *AllocateOrder) setPromiseDate(o *order.Order) {
	if d, ok := uc.Promise.PromiseDate(uc.Clock.Now(), o); ok {
		o.SetPromiseDate(d)
	}
}

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

// allocateLines runs the reserve-per-line loop shared by AllocateOrder and
// RetryAllocation. retry selects which domain transition is legal for the
// lines being processed: Order.Allocate for Pending lines, and
// Order.RetryAllocate — the only route out of Backordered — for retries.
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
// the aggregate's derived status after an allocation pass. A fully
// backordered order emits no order-level event: its per-line
// OrderLineBackordered facts already carry the whole story.
func publishOrderAllocationOutcome(
	ctx context.Context,
	events ports.EventPublisher,
	clock ports.Clock,
	o *order.Order,
	outcome allocationOutcome,
) error {
	if outcome.allocated == 0 && outcome.backordered == 0 {
		return nil
	}

	var promiseDate time.Time
	if d := o.PromiseDate(); d != nil {
		promiseDate = *d
	}

	switch o.Status() {
	case order.StatusAllocated:
		return events.Publish(ctx, shared.NewOrderAllocated(clock.Now(), o.ID(), promiseDate))
	case order.StatusPartiallyAllocated:
		return events.Publish(ctx, shared.NewOrderPartiallyAllocated(
			clock.Now(), o.ID(), outcome.allocated, outcome.backordered, promiseDate,
		))
	default:
		return nil
	}
}
