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
	o := f.mustReceive(t, false, line("SKU-1", 1, "pick"))

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
	f, o := backorderedFixture(t, true)

	if _, err := f.cancelOrder().Execute(context.Background(), o.ID()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(f.inventory.revokeCalls) != 1 {
		t.Fatalf("revoke calls = %v, want only the allocated line's reservation", f.inventory.revokeCalls)
	}
}

// BR6: cancellation is illegal once ANY line has been released, and the
// check happens BEFORE anything is revoked upstream.
func TestCancelOrderIsRejectedOnceALineIsReleased(t *testing.T) {
	tests := []struct {
		name                 string
		allowPartialShipment bool
		lines                []usecases.NewLine
	}{
		{
			name:  "a fully released order",
			lines: []usecases.NewLine{line("SKU-1", 1, "pick")},
		},
		{
			name:                 "a partially released order still has an allocated line",
			allowPartialShipment: true,
			lines:                []usecases.NewLine{line("SKU-1", 1, "pick"), line("SKU-2", 1, "pick")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, o := allocatedFixture(t, tt.allowPartialShipment, tt.lines...)
			if tt.allowPartialShipment && len(tt.lines) > 1 {
				// Release only line 1 by failing the enqueue for line 2.
				f.work.enqueueErrBySKU["SKU-2"] = errBoom
				if _, err := f.releaseOrder().Execute(context.Background(), o.ID()); !errors.Is(err, errBoom) {
					t.Fatalf("ReleaseOrder: %v", err)
				}
			} else if _, err := f.releaseOrder().Execute(context.Background(), o.ID()); err != nil {
				t.Fatalf("ReleaseOrder: %v", err)
			}
			f.events.reset()

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
