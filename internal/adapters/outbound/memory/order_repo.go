// Package memory provides in-memory implementations of the outbound ports,
// used by the unit/httptest suites and by `go run ./cmd/order` with no
// DATABASE_URL set, so the service is fully functional without Postgres.
package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// OrderRepo is an in-memory implementation of ports.OrderRepo.
type OrderRepo struct {
	mu     sync.RWMutex
	orders map[shared.OrderId]*order.Order
	nextID int
}

// NewOrderRepo constructs an empty OrderRepo.
func NewOrderRepo() *OrderRepo {
	return &OrderRepo{orders: make(map[shared.OrderId]*order.Order)}
}

func (r *OrderRepo) Save(_ context.Context, o *order.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.orders[o.ID()] = o
	return nil
}

// FindByID returns (nil, nil) when no order has this id. "Not found" is
// the application's concern (usecases.ErrOrderNotFound), not the
// repository's.
func (r *OrderRepo) FindByID(_ context.Context, id shared.OrderId) (*order.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.orders[id]
	if !ok {
		return nil, nil
	}
	return o, nil
}

func (r *OrderRepo) NextID(_ context.Context) (shared.OrderId, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	return shared.OrderId(fmt.Sprintf("ord-%d", r.nextID)), nil
}
