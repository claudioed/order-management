package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/order"
)

// backorderedFixture returns a fixture whose order has line 1 allocated and
// line 2 backordered.
func backorderedFixture(t *testing.T, allowPartialShipment bool) (*fixture, *order.Order) {
	t.Helper()
	f := newFixture()
	f.inventory.reserveErrBySKU["SKU-2"] = ports.ErrInsufficientStock
	o := f.mustReceive(t, allowPartialShipment, line("SKU-1", 1, "pick"), line("SKU-2", 1, "pick"))

	if _, err := f.allocateOrder().Execute(context.Background(), o.ID()); err != nil {
		t.Fatalf("AllocateOrder: %v", err)
	}
	f.events.reset()
	return f, o
}

func TestRetryAllocationClearsABackorderAndUnblocksShipComplete(t *testing.T) {
	f, o := backorderedFixture(t, false)
	delete(f.inventory.reserveErrBySKU, "SKU-2")
	reservesBeforeRetry := len(f.inventory.reserveCalls)

	got, err := f.retryAllocation().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got.Status() != order.StatusAllocated {
		t.Fatalf("Status() = %q, want %q", got.Status(), order.StatusAllocated)
	}
	assertLineStatuses(t, got, order.LineAllocated, order.LineAllocated)
	assertEventNames(t, f.events, "OrderLineAllocated", "OrderAllocated")

	// ONLY the backordered line was retried — the already-allocated line
	// was not reserved a second time.
	if got := len(f.inventory.reserveCalls) - reservesBeforeRetry; got != 1 {
		t.Fatalf("retry made %d reserve calls, want exactly 1 (the backordered line)", got)
	}
	if last := f.inventory.reserveCalls[len(f.inventory.reserveCalls)-1]; last.SKU != "SKU-2" {
		t.Fatalf("retry reserved %q, want SKU-2", last.SKU)
	}

	// The now-unblocked ship-complete order releases.
	if err := got.EnsureReleasable(); err != nil {
		t.Fatalf("EnsureReleasable after a cleared backorder: %v", err)
	}

	// The promise date was recomputed over both allocated lines.
	want := now().Add(24 * time.Hour)
	if d := got.PromiseDate(); d == nil || !d.Equal(want) {
		t.Fatalf("PromiseDate() = %v, want %v", d, want)
	}
}

func TestRetryAllocationLeavesAStillShortLineBackordered(t *testing.T) {
	f, o := backorderedFixture(t, false)

	got, err := f.retryAllocation().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got.Status() != order.StatusBackordered {
		t.Fatalf("Status() = %q, want %q", got.Status(), order.StatusBackordered)
	}
	assertLineStatuses(t, got, order.LineAllocated, order.LineBackordered)
	assertEventNames(t, f.events, "OrderLineBackordered")
}

func TestRetryAllocationPartialShipmentRepublishesPartiallyAllocated(t *testing.T) {
	f, o := backorderedFixture(t, true)

	got, err := f.retryAllocation().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Status() != order.StatusPartiallyAllocated {
		t.Fatalf("Status() = %q, want %q", got.Status(), order.StatusPartiallyAllocated)
	}
	assertEventNames(t, f.events, "OrderLineBackordered", "OrderPartiallyAllocated")
}

func TestRetryAllocationErrorPaths(t *testing.T) {
	t.Run("unknown order", func(t *testing.T) {
		f := newFixture()
		_, err := f.retryAllocation().Execute(context.Background(), "ord-missing")
		if !errors.Is(err, usecases.ErrOrderNotFound) {
			t.Fatalf("err = %v, want %v", err, usecases.ErrOrderNotFound)
		}
	})

	t.Run("repository lookup fails", func(t *testing.T) {
		f := newFixture()
		f.orders = &failingRepo{inner: f.orders, findErr: errBoom}
		_, err := f.retryAllocation().Execute(context.Background(), "ord-1")
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("an order with no backordered line is rejected", func(t *testing.T) {
		f := newFixture()
		o := f.mustReceive(t, false, line("SKU-1", 1, "pick"))
		if _, err := f.allocateOrder().Execute(context.Background(), o.ID()); err != nil {
			t.Fatalf("AllocateOrder: %v", err)
		}

		_, err := f.retryAllocation().Execute(context.Background(), o.ID())
		if !errors.Is(err, usecases.ErrNoBackorderedLines) {
			t.Fatalf("err = %v, want %v", err, usecases.ErrNoBackorderedLines)
		}
		if len(f.inventory.reserveCalls) != 1 {
			t.Fatalf("a rejected retry must not call inventory-storage again, calls = %d", len(f.inventory.reserveCalls))
		}
	})

	t.Run("fails closed on a non-business error", func(t *testing.T) {
		f, o := backorderedFixture(t, true)
		f.inventory.reserveErrBySKU["SKU-2"] = errBoom

		_, err := f.retryAllocation().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}

		stored, getErr := f.getOrder().Execute(context.Background(), o.ID())
		if getErr != nil {
			t.Fatalf("GetOrder: %v", getErr)
		}
		assertLineStatuses(t, stored, order.LineAllocated, order.LineBackordered)
	})

	t.Run("save fails", func(t *testing.T) {
		f, o := backorderedFixture(t, true)
		delete(f.inventory.reserveErrBySKU, "SKU-2")
		f.orders = &failingRepo{inner: f.orders, saveErr: errBoom}

		_, err := f.retryAllocation().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("save fails while persisting partial progress after a hard failure", func(t *testing.T) {
		f := newFixture()
		f.inventory.reserveErr = ports.ErrInsufficientStock
		o := f.mustReceive(t, true, line("SKU-1", 1, "pick"), line("SKU-2", 1, "pick"))
		if _, err := f.allocateOrder().Execute(context.Background(), o.ID()); err != nil {
			t.Fatalf("AllocateOrder: %v", err)
		}
		f.events.reset()

		// Line 1 now retries successfully; line 2 hits a hard failure.
		f.inventory.reserveErr = nil
		f.inventory.reserveErrBySKU["SKU-2"] = errBoom
		f.orders = &failingRepo{inner: f.orders, saveErr: errBoom}

		_, err := f.retryAllocation().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("publisher fails", func(t *testing.T) {
		f, o := backorderedFixture(t, true)
		delete(f.inventory.reserveErrBySKU, "SKU-2")
		f.events.err = errBoom

		_, err := f.retryAllocation().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})
}
