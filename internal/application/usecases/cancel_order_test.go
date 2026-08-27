package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/order"
)

func TestCancelOrderRevokesEveryAllocatedReservation(t *testing.T) {
	f, o := allocatedFixture(t, false, line("SKU-1", 1, "pick"), line("SKU-2", 1, "singles"))

	got, err := f.cancelOrder().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got.Status() != order.StatusCancelled {
		t.Fatalf("Status() = %q, want %q", got.Status(), order.StatusCancelled)
	}
	assertLineStatuses(t, got, order.LineCancelled, order.LineCancelled)
	assertEventNames(t, f.events, "OrderCancelled")

	if len(f.inventory.revokeCalls) != 2 {
		t.Fatalf("revoke calls = %v, want one per allocated line", f.inventory.revokeCalls)
	}
}

func TestCancelOrderBeforeAllocationRevokesNothing(t *testing.T) {
	f := newFixture()
	f.inventory.reserveErr = errBoom // block the implicit allocation entirely
	o := f.mustReceive(t, false, line("SKU-1", 1, "pick"))
	assertLineStatuses(t, o, order.LinePending)

	got, err := f.cancelOrder().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Status() != order.StatusCancelled {
		t.Fatalf("Status() = %q, want %q", got.Status(), order.StatusCancelled)
	}
	if len(f.inventory.revokeCalls) != 0 {
		t.Fatalf("a never-allocated order has nothing to revoke, got %v", f.inventory.revokeCalls)
	}
}

func TestCancelOrderSkipsBackorderedLines(t *testing.T) {
	// A ship-complete order (allowPartialShipment=false) blocks release
	// while line 2 is still Backordered (BR3), so line 1 stays merely
	// Allocated — exactly the state this test needs to exercise
	// CancelOrder revoking only the allocated line's reservation.
	f, o := backorderedFixture(t, false)
	assertLineStatuses(t, o, order.LineAllocated, order.LineBackordered)

	if _, err := f.cancelOrder().Execute(context.Background(), o.ID()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(f.inventory.revokeCalls) != 1 {
		t.Fatalf("revoke calls = %v, want only the allocated line's reservation", f.inventory.revokeCalls)
	}
}

// partiallyReleasedFixture builds an order with line 1 Released and line 2
// merely Allocated (not released), directly via domain calls — the
// choreographed-release redesign releases every eligible line in the SAME
// pass as allocation, so this specific in-between state is no longer
// reachable through any public use case and must be constructed by hand
// for a test that needs to exercise BR6's boundary against it.
func partiallyReleasedFixture(t *testing.T) (*fixture, *order.Order) {
	t.Helper()
	f := newFixture()
	f.inventory.reserveErr = errBoom
	o := f.mustReceive(t, true, line("SKU-1", 1, "pick"), line("SKU-2", 1, "pick"))
	f.inventory.reserveErr = nil

	for _, l := range o.Lines() {
		result, err := f.inventory.Reserve(context.Background(), ports.ReservationRequest{
			SKU: l.SKU(), Quantity: l.Quantity(), DemandRef: o.ID(),
		})
		if err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		if err := o.Allocate(l.LineNo(), result.ReservationID); err != nil {
			t.Fatalf("Allocate(%d): %v", l.LineNo(), err)
		}
	}
	if err := o.Release(1); err != nil {
		t.Fatalf("Release(1): %v", err)
	}
	if err := f.orders.Save(context.Background(), o); err != nil {
		t.Fatalf("Save: %v", err)
	}
	f.events.reset()
	return f, o
}

// BR6: cancellation is illegal once ANY line has been released, and the
// check happens BEFORE anything is revoked upstream.
func TestCancelOrderIsRejectedOnceALineIsReleased(t *testing.T) {
	t.Run("a fully released order", func(t *testing.T) {
		// A single-line ship-complete order releases automatically inside
		// ReceiveOrder once its one line allocates cleanly.
		f := newFixture()
		o := f.mustReceive(t, false, line("SKU-1", 1, "pick"))
		if o.Status() != order.StatusReleased {
			t.Fatalf("Status() = %q, want %q", o.Status(), order.StatusReleased)
		}

		_, err := f.cancelOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, order.ErrOrderAlreadyReleased) {
			t.Fatalf("err = %v, want %v", err, order.ErrOrderAlreadyReleased)
		}
		if len(f.inventory.revokeCalls) != 0 {
			t.Fatalf("a rejected cancellation must not touch inventory-storage, got %v", f.inventory.revokeCalls)
		}
	})

	t.Run("a partially released order still has an allocated line", func(t *testing.T) {
		f, o := partiallyReleasedFixture(t)

		_, err := f.cancelOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, order.ErrOrderAlreadyReleased) {
			t.Fatalf("err = %v, want %v", err, order.ErrOrderAlreadyReleased)
		}
		// The boundary is checked before any upstream side effect.
		if len(f.inventory.revokeCalls) != 0 {
			t.Fatalf("a rejected cancellation must not touch inventory-storage, got %v", f.inventory.revokeCalls)
		}
		assertEventNames(t, f.events)

		stored, getErr := f.getOrder().Execute(context.Background(), o.ID())
		if getErr != nil {
			t.Fatalf("GetOrder: %v", getErr)
		}
		if stored.Status() == order.StatusCancelled {
			t.Fatal("the order must not be cancelled")
		}
	})
}

func TestCancelOrderErrorPaths(t *testing.T) {
	t.Run("unknown order", func(t *testing.T) {
		f := newFixture()
		_, err := f.cancelOrder().Execute(context.Background(), "ord-missing")
		if !errors.Is(err, usecases.ErrOrderNotFound) {
			t.Fatalf("err = %v, want %v", err, usecases.ErrOrderNotFound)
		}
	})

	t.Run("repository lookup fails", func(t *testing.T) {
		f := newFixture()
		f.orders = &failingRepo{inner: f.orders, findErr: errBoom}
		_, err := f.cancelOrder().Execute(context.Background(), "ord-1")
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("a failed revoke fails the whole cancellation", func(t *testing.T) {
		tests := []struct {
			name string
			err  error
		}{
			{name: "transport failure", err: errBoom},
			{name: "permissive (no-op) downstream", err: ports.ErrDownstreamNotConfigured},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f, o := allocatedFixture(t, false, line("SKU-1", 1, "pick"))
				f.inventory.revokeErr = tt.err

				_, err := f.cancelOrder().Execute(context.Background(), o.ID())
				if !errors.Is(err, tt.err) {
					t.Fatalf("err = %v, want %v", err, tt.err)
				}

				// Fail closed: the order is NOT cancelled, so retrying is
				// safe and the reservation id is still recorded.
				stored, getErr := f.getOrder().Execute(context.Background(), o.ID())
				if getErr != nil {
					t.Fatalf("GetOrder: %v", getErr)
				}
				assertLineStatuses(t, stored, order.LineAllocated)
				assertEventNames(t, f.events)
			})
		}
	})

	t.Run("save fails", func(t *testing.T) {
		f, o := allocatedFixture(t, false, line("SKU-1", 1, "pick"))
		f.orders = &failingRepo{inner: f.orders, saveErr: errBoom}

		_, err := f.cancelOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("publisher fails", func(t *testing.T) {
		f, o := allocatedFixture(t, false, line("SKU-1", 1, "pick"))
		f.events.err = errBoom

		_, err := f.cancelOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})
}
