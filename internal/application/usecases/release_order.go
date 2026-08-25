package usecases

import (
	"context"
	"fmt"

	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// ReleaseOrder enqueues each Allocated line as a work unit on
// wes-work-planning's process path for that line, via
// POST /paths/{pathId}/work-units.
//
// BR3 is enforced before anything leaves the process: a ship-complete
// order (AllowPartialShipment=false) releases nothing while any line is
// still Pending or Backordered (order.ErrShipCompleteBlocked). An order
// that allows partial shipment releases its allocated lines independently.
//
// A line that is not Allocated is rejected by the aggregate
// (order.ErrLineNotAllocated); this use case never even offers one, since
// it iterates only Allocated lines.
type ReleaseOrder struct {
	Orders ports.OrderRepo
	Work   ports.WorkReleaseClient
	Events ports.EventPublisher
	Clock  ports.Clock
}

func (uc *ReleaseOrder) Execute(ctx context.Context, id shared.OrderId) (*order.Order, error) {
	o, err := uc.Orders.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrOrderNotFound
	}

	if err := o.EnsureReleasable(); err != nil {
		return nil, err
	}

	allocated := o.LinesWithStatus(order.LineAllocated)
	if len(allocated) == 0 {
		return nil, ErrNoAllocatedLines
	}

	promiseDate := o.PromiseDate()
	if promiseDate == nil {
		return nil, ErrPromiseDateNotSet
	}

	released := 0
	for _, line := range allocated {
		workUnitID := WorkUnitID(o.ID(), line.LineNo())
		result, err := uc.Work.EnqueueWorkUnit(ctx, ports.WorkUnitRequest{
			PathID:     line.PathID(),
			WorkUnitID: workUnitID,
			CPT:        *promiseDate,
			Reference:  o.ID(),
			SKU:        line.SKU(),
			GiftWrap:   line.GiftWrap(),
		})
		if err != nil {
			// Releasing work is not a business-fact-bearing call the
			// way allocation is: there is no "no capacity" answer to
			// record on the line. Any failure is a hard failure, and
			// the lines released before it are persisted below so the
			// work units already accepted upstream stay accounted for.
			if released > 0 {
				if saveErr := uc.Orders.Save(ctx, o); saveErr != nil {
					return nil, saveErr
				}
			}
			return nil, err
		}

		if err := o.Release(line.LineNo()); err != nil {
			return nil, err
		}
		released++
		if err := uc.Events.Publish(ctx, shared.NewOrderLineReleased(
			uc.Clock.Now(), o.ID(), line.LineNo(), line.PathID(), result.WorkUnitID,
		)); err != nil {
			return nil, err
		}
	}

	if err := uc.Orders.Save(ctx, o); err != nil {
		return nil, err
	}

	if o.Status() == order.StatusReleased {
		if err := uc.Events.Publish(ctx, shared.NewOrderReleased(uc.Clock.Now(), o.ID())); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// WorkUnitID builds the client-supplied, deterministic work-unit id this
// context sends to wes-work-planning. That endpoint requires the id to be
// unique within a pool and rejects a duplicate with 409, so deriving it
// from (orderId, lineNo) makes a re-release of the same line a detectable
// conflict upstream rather than a silent duplicate work unit. The shape
// matches the `order-77213-line-1` convention already documented on that
// endpoint.
func WorkUnitID(orderID shared.OrderId, lineNo int) string {
	return fmt.Sprintf("%s-line-%d", orderID.String(), lineNo)
}
