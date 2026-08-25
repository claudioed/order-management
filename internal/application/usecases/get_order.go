package usecases

import (
	"context"

	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// GetOrder reads current Order state. It is the one use case that only
// reads: there is no invariant to enforce on a lookup, so it is a direct
// repository read behind a use-case type for symmetry with the others.
type GetOrder struct {
	Orders ports.OrderRepo
}

func (uc *GetOrder) Execute(ctx context.Context, id shared.OrderId) (*order.Order, error) {
	o, err := uc.Orders.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrOrderNotFound
	}
	return o, nil
}
