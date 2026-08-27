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
)
