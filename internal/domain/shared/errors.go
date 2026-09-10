// Package shared holds the value objects, domain events, and error types
// common to every aggregate in the Order Management domain.
package shared

import "errors"

var (
	// ErrEmptyOrderID is returned when an OrderId is constructed from an
	// empty string. An order without an identity cannot be referenced by
	// inventory-storage (as a demandRef) or by wes-work-planning (as a
	// work-unit reference), so the domain refuses to create one.
	ErrEmptyOrderID = errors.New("order id must not be empty")

	// ErrEmptySKU is returned when a SKU is constructed from an empty
	// string. Every OrderLine must say what is being ordered.
	ErrEmptySKU = errors.New("sku must not be empty")

	// ErrEmptyPathID is returned when a PathId is constructed from an
	// empty string. Callers that omit a path get DefaultPathId instead of
	// this error — see NewPathIdOrDefault.
	ErrEmptyPathID = errors.New("path id must not be empty")

	// ErrNonPositiveQuantity is returned when an OrderLine quantity is
	// zero or negative. Ordering nothing is not a business fact worth
	// persisting.
	ErrNonPositiveQuantity = errors.New("quantity must be greater than zero")

	// ErrUnknownProcessPath is returned by ReceiveOrder when a line's
	// resolved PathId (via order.PathSelectionPolicy) is not currently
	// active in process-path-management's real catalogue. This is a
	// caller-facing input error (RFC 7807 400), not an infrastructure
	// failure: it means the order, as constructed, cannot be routed
	// anywhere real right now. See order-management ADR-0013 -- this
	// closes the gap where an invalid path was previously discovered
	// only by wes-work-planning, one saga step after the caller already
	// received a 201.
	ErrUnknownProcessPath = errors.New("resolved process path is not active in the process-path catalogue")
)
