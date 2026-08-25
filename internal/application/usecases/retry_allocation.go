package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// RetryAllocation re-attempts allocation for the order's Backordered lines
// and ONLY those lines. It is the single sanctioned route from Backordered
// back to Allocated (Order.RetryAllocate) — nothing else in the system may
// perform that transition.
//
// It is also what unblocks a ship-complete order: while any line is
// Backordered, BR3 holds the whole order back from release, and a
// successful retry is the event that clears it.
//
// A line that is still short of stock stays Backordered; the same
// 409-is-a-business-fact / anything-else-is-ambiguous rule as
// AllocateOrder applies.
type RetryAllocation struct {
	Orders    ports.OrderRepo
	Inventory ports.InventoryReservationClient
	Events    ports.EventPublisher
	Clock     ports.Clock
	Promise   order.LeadTimePolicy
}

func (uc *RetryAllocation) Execute(ctx context.Context, id shared.OrderId) (*order.Order, error) {
	o, err := uc.Orders.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrOrderNotFound
	}

	backordered := o.LinesWithStatus(order.LineBackordered)
	if len(backordered) == 0 {
		return nil, ErrNoBackorderedLines
	}

	outcome, allocErr := allocateLines(ctx, uc.Inventory, uc.Events, uc.Clock, o, backordered, true)
	if allocErr != nil {
		if outcome.allocated > 0 {
			uc.setPromiseDate(o)
			if saveErr := uc.Orders.Save(ctx, o); saveErr != nil {
				return nil, errors.Join(allocErr, saveErr)
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

func (uc *RetryAllocation) setPromiseDate(o *order.Order) {
	if d, ok := uc.Promise.PromiseDate(uc.Clock.Now(), o); ok {
		o.SetPromiseDate(d)
	}
}
