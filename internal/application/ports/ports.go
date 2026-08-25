// Package ports declares the outbound interfaces the application layer
// depends on. Adapters implement these; the application never imports an
// adapter package.
//
// InventoryReservationClient is a cross-context call into inventory-
// storage's published HTTP contract. It is expressed in this context's own
// types on purpose: Order Management is the Customer in a Customer/Supplier
// relationship and does not import a single Go package from that Supplier.
// See ADR 0002.
//
// Release, by contrast, is no longer a synchronous outbound call at all —
// since the choreographed-release redesign (see ADR 0005), a released line
// is announced as a fact on the enriched OrderAllocated /
// OrderPartiallyAllocated integration events, published to Kafka via
// ports.EventPublisher. There is deliberately no WorkReleaseClient-shaped
// port here any more.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

var (
	// ErrInsufficientStock is the BUSINESS FACT behind inventory-storage's
	// HTTP 409 on POST /reservations: there is not enough usable stock for
	// this line right now. Only an InventoryReservationClient that
	// actually saw a 409 may return it — a transport failure or a 5xx is
	// NOT this error, and must never be silently turned into a backorder.
	ErrInsufficientStock = errors.New("insufficient usable stock for this order line")

	// ErrDownstreamNotConfigured is returned by the permissive (no-op)
	// outbound clients. Unlike the fail-open classification lookups
	// elsewhere in this fleet, reserving real stock and releasing real
	// work must never appear to succeed against a no-op, so the permissive
	// clients fail loudly instead of fabricating a result. Only
	// MODE=http is suitable for a real integration test or deployment.
	ErrDownstreamNotConfigured = errors.New("downstream client is running in permissive (no-op) mode and cannot perform this operation")
)

// OrderRepo persists and retrieves Order aggregates.
type OrderRepo interface {
	Save(ctx context.Context, o *order.Order) error
	FindByID(ctx context.Context, id shared.OrderId) (*order.Order, error)
	NextID(ctx context.Context) (shared.OrderId, error)
}

// EventPublisher publishes domain events. v1 wires a log publisher; the
// signature is deliberately the shape a Kafka producer satisfies, so the
// deferred broker integration is purely additive.
type EventPublisher interface {
	Publish(ctx context.Context, event shared.DomainEvent) error
}

// Clock abstracts current time so use cases and tests are deterministic.
type Clock interface {
	Now() time.Time
}

// ReservationRequest is this context's request shape for
// inventory-storage's POST /reservations. DemandRef carries the OrderId —
// which is precisely the identity this bounded context was created to own.
type ReservationRequest struct {
	SKU       shared.SKU
	Quantity  int
	DemandRef shared.OrderId
}

// ReservationResult carries back the only piece of inventory-storage's
// reservation this context stores: its id, needed to revoke on
// cancellation. Reservation state itself stays owned by inventory-storage.
type ReservationResult struct {
	ReservationID string
}

// InventoryReservationClient is the outbound port for inventory-storage's
// published reservation contract.
//
// Reserve MUST return ErrInsufficientStock (and only that) for a 409, and
// a distinct error for anything else — transport failure, 4xx other than
// 409, or 5xx — so AllocateOrder can fail closed on ambiguity instead of
// recording a backorder that inventory-storage never asserted.
type InventoryReservationClient interface {
	Reserve(ctx context.Context, req ReservationRequest) (ReservationResult, error)
	RevokeReservation(ctx context.Context, reservationID string) error
}
