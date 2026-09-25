// Package usecases implements the application's use cases: one struct per
// use case, depending only on the domain and on application/ports. No use
// case imports an adapter package.
package usecases

import "errors"

var (
	// ErrOrderNotFound is returned when an order id addresses no order.
	ErrOrderNotFound = errors.New("order not found")

	// ErrNoAllocatedLines is returned by ReleaseOrder when the order has
	// nothing in Allocated state to release.
	ErrNoAllocatedLines = errors.New("order has no allocated lines to release")

	// ErrOrderNotHeld is returned by ReleaseHeldOrder when the order was
	// received with releaseOnAllocation=true (ADR 0020 §1). Such an
	// order releases its lines at allocation and has nothing to release
	// on demand; the call means the caller has the wrong order or the
	// wrong expectation, and silently succeeding would hide that.
	ErrOrderNotHeld = errors.New("order was not held at intake")

	// ErrNoBackorderedLines is returned by RetryAllocation when the order
	// has no Backordered line to retry — retrying an order that is not
	// backordered is a caller mistake, not a no-op.
	ErrNoBackorderedLines = errors.New("order has no backordered lines to retry")

	// ErrPromiseDateNotSet is returned by ReleaseOrder when the order has
	// no promise date. It is set at allocation time, so reaching release
	// without one means the order was never allocated.
	ErrPromiseDateNotSet = errors.New("order has no promise date; allocate it first")
)
