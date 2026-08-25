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
// OrderId, and persists a new Order in Received status. Nothing is
// reserved and nothing is released here — allocation is a separate,
// explicitly invoked step.
type ReceiveOrder struct {
	Orders ports.OrderRepo
	Events ports.EventPublisher
	Clock  ports.Clock
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
	return o, nil
}
