package shared

import "time"

// DomainEvent is a past-tense fact produced by the Order aggregate.
// Adapters (outbound/events) serialize and publish these; the domain never
// depends on the publishing mechanism.
type DomainEvent interface {
	EventName() string
	OccurredAt() time.Time
}

type base struct {
	Name string    `json:"eventName"`
	At   time.Time `json:"occurredAt"`
}

func (b base) EventName() string     { return b.Name }
func (b base) OccurredAt() time.Time { return b.At }

func newBase(name string, occurredAt time.Time) base {
	return base{Name: name, At: occurredAt}
}

// OrderReceived: an order was accepted into the building with its lines.
type OrderReceived struct {
	base
	OrderID   OrderId
	LineCount int
}

func NewOrderReceived(occurredAt time.Time, orderID OrderId, lineCount int) OrderReceived {
	return OrderReceived{base: newBase("OrderReceived", occurredAt), OrderID: orderID, LineCount: lineCount}
}

// OrderLineAllocated: inventory-storage accepted a reservation for a line.
// ReservationID is inventory-storage's id — this context stores the
// reference only; inventory-storage remains the sole owner of reservation
// state.
type OrderLineAllocated struct {
	base
	OrderID       OrderId
	LineNo        int
	SKU           SKU
	Quantity      int
	ReservationID string
}

func NewOrderLineAllocated(occurredAt time.Time, orderID OrderId, lineNo int, sku SKU, quantity int, reservationID string) OrderLineAllocated {
	return OrderLineAllocated{
		base:    newBase("OrderLineAllocated", occurredAt),
		OrderID: orderID, LineNo: lineNo, SKU: sku, Quantity: quantity, ReservationID: reservationID,
	}
}

// OrderLineBackordered: inventory-storage reported insufficient usable
// stock (HTTP 409) for a line. This is a BUSINESS FACT — it is never
// produced from a transport or 5xx failure.
type OrderLineBackordered struct {
	base
	OrderID  OrderId
	LineNo   int
	SKU      SKU
	Quantity int
}

func NewOrderLineBackordered(occurredAt time.Time, orderID OrderId, lineNo int, sku SKU, quantity int) OrderLineBackordered {
	return OrderLineBackordered{
		base:    newBase("OrderLineBackordered", occurredAt),
		OrderID: orderID, LineNo: lineNo, SKU: sku, Quantity: quantity,
	}
}

// OrderAllocated: every line on the order is allocated.
type OrderAllocated struct {
	base
	OrderID     OrderId
	PromiseDate time.Time
}

func NewOrderAllocated(occurredAt time.Time, orderID OrderId, promiseDate time.Time) OrderAllocated {
	return OrderAllocated{base: newBase("OrderAllocated", occurredAt), OrderID: orderID, PromiseDate: promiseDate}
}

// OrderPartiallyAllocated: some lines allocated, some backordered, on an
// order that allows partial shipment.
type OrderPartiallyAllocated struct {
	base
	OrderID          OrderId
	AllocatedLines   int
	BackorderedLines int
	PromiseDate      time.Time
}

func NewOrderPartiallyAllocated(occurredAt time.Time, orderID OrderId, allocated, backordered int, promiseDate time.Time) OrderPartiallyAllocated {
	return OrderPartiallyAllocated{
		base:    newBase("OrderPartiallyAllocated", occurredAt),
		OrderID: orderID, AllocatedLines: allocated, BackorderedLines: backordered, PromiseDate: promiseDate,
	}
}

// OrderLineReleased: a line's work was enqueued onto a wes-work-planning
// process path.
type OrderLineReleased struct {
	base
	OrderID    OrderId
	LineNo     int
	PathID     PathId
	WorkUnitID string
}

func NewOrderLineReleased(occurredAt time.Time, orderID OrderId, lineNo int, pathID PathId, workUnitID string) OrderLineReleased {
	return OrderLineReleased{
		base:    newBase("OrderLineReleased", occurredAt),
		OrderID: orderID, LineNo: lineNo, PathID: pathID, WorkUnitID: workUnitID,
	}
}

// OrderReleased: every line on the order has been released as work.
type OrderReleased struct {
	base
	OrderID OrderId
}

func NewOrderReleased(occurredAt time.Time, orderID OrderId) OrderReleased {
	return OrderReleased{base: newBase("OrderReleased", occurredAt), OrderID: orderID}
}

// OrderCancelled: the order was cancelled before any line was released,
// and every allocated line's reservation was revoked upstream.
type OrderCancelled struct {
	base
	OrderID             OrderId
	RevokedReservations int
}

func NewOrderCancelled(occurredAt time.Time, orderID OrderId, revoked int) OrderCancelled {
	return OrderCancelled{base: newBase("OrderCancelled", occurredAt), OrderID: orderID, RevokedReservations: revoked}
}

// OrderAllocationPartiallyFailed: AllocateOrder hit a hard (non-business,
// non-409) failure partway through allocating an order's lines —
// AllocatedLines were genuinely reserved upstream before the failure, and
// RemainingLines are still Pending. The already-succeeded reservations ARE
// kept — this is deliberate (see ADR-0003): discarding them would strand
// real reservations inside inventory-storage that nothing in this context
// could then revoke, and a subsequent AllocateOrder call safely resumes by
// skipping the already-Allocated lines. This event exists purely so that
// outcome is operationally visible, rather than only discoverable by
// reading a source code comment.
type OrderAllocationPartiallyFailed struct {
	base
	OrderID        OrderId
	AllocatedLines int
	RemainingLines int
	// Cause is a short, safe-to-log description of the failure (the
	// error's Error() string, defensively truncated) — never the full
	// error internals or an upstream response body.
	Cause string
}

func NewOrderAllocationPartiallyFailed(occurredAt time.Time, orderID OrderId, allocatedLines, remainingLines int, cause string) OrderAllocationPartiallyFailed {
	return OrderAllocationPartiallyFailed{
		base:    newBase("OrderAllocationPartiallyFailed", occurredAt),
		OrderID: orderID, AllocatedLines: allocatedLines, RemainingLines: remainingLines, Cause: cause,
	}
}
