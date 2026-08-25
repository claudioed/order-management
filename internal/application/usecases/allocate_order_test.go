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

func TestAllocateOrderHappyPath(t *testing.T) {
	f := newFixture()
	o := f.mustReceive(t, false, line("SKU-1", 2, "pick"), line("SKU-2", 1, "singles"))

	got, err := f.allocateOrder().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got.Status() != order.StatusAllocated {
		t.Fatalf("Status() = %q, want %q", got.Status(), order.StatusAllocated)
	}
	assertLineStatuses(t, got, order.LineAllocated, order.LineAllocated)
	assertEventNames(t, f.events, "OrderLineAllocated", "OrderLineAllocated", "OrderAllocated")

	// The order's demandRef is its own OrderId — the identity this
	// bounded context exists to own.
	if len(f.inventory.reserveCalls) != 2 {
		t.Fatalf("reserve calls = %d, want 2", len(f.inventory.reserveCalls))
	}
	for i, call := range f.inventory.reserveCalls {
		if call.DemandRef != o.ID() {
			t.Fatalf("call %d demandRef = %q, want %q", i, call.DemandRef, o.ID())
		}
	}
	if f.inventory.reserveCalls[0].SKU != "SKU-1" || f.inventory.reserveCalls[0].Quantity != 2 {
		t.Fatalf("call 0 = %+v, want SKU-1 x2", f.inventory.reserveCalls[0])
	}

	// Promise date = now + the SLOWEST allocated path (pick, 24h).
	want := now().Add(24 * time.Hour)
	if d := got.PromiseDate(); d == nil || !d.Equal(want) {
		t.Fatalf("PromiseDate() = %v, want %v", d, want)
	}

	// The reservation ids must be persisted — CancelOrder needs them.
	for _, l := range got.Lines() {
		if l.ReservationID() == nil {
			t.Fatalf("line %d has no reservation id", l.LineNo())
		}
	}
}

// BR3 (ship-complete default): ANY backordered line puts the WHOLE order in
// Backordered, and no line proceeds to release.
func TestAllocateOrderShipCompleteBackordersTheWholeOrder(t *testing.T) {
	f := newFixture()
	f.inventory.reserveErrBySKU["SKU-2"] = ports.ErrInsufficientStock
	o := f.mustReceive(t, false, line("SKU-1", 1, "pick"), line("SKU-2", 1, "pick"))

	got, err := f.allocateOrder().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got.Status() != order.StatusBackordered {
		t.Fatalf("Status() = %q, want %q", got.Status(), order.StatusBackordered)
	}
	assertLineStatuses(t, got, order.LineAllocated, order.LineBackordered)
	assertEventNames(t, f.events, "OrderLineAllocated", "OrderLineBackordered")

	// And BR3's teeth: release is refused while the order is backordered.
	_, err = f.releaseOrder().Execute(context.Background(), o.ID())
	if !errors.Is(err, order.ErrShipCompleteBlocked) {
		t.Fatalf("ReleaseOrder err = %v, want %v", err, order.ErrShipCompleteBlocked)
	}
	if len(f.work.calls) != 0 {
		t.Fatalf("a blocked ship-complete order must not enqueue work, got %d calls", len(f.work.calls))
	}
}

// BR3 (partial shipment allowed): allocated lines stay independently
// eligible and the order reads PartiallyAllocated.
func TestAllocateOrderPartialShipmentIsPartiallyAllocated(t *testing.T) {
	f := newFixture()
	f.inventory.reserveErrBySKU["SKU-2"] = ports.ErrInsufficientStock
	o := f.mustReceive(t, true, line("SKU-1", 1, "singles"), line("SKU-2", 1, "pick"))

	got, err := f.allocateOrder().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got.Status() != order.StatusPartiallyAllocated {
		t.Fatalf("Status() = %q, want %q", got.Status(), order.StatusPartiallyAllocated)
	}
	assertLineStatuses(t, got, order.LineAllocated, order.LineBackordered)
	assertEventNames(t, f.events, "OrderLineAllocated", "OrderLineBackordered", "OrderPartiallyAllocated")

	// The promise date reflects only the allocated line's path (singles, 6h),
	// not the backordered one.
	want := now().Add(6 * time.Hour)
	if d := got.PromiseDate(); d == nil || !d.Equal(want) {
		t.Fatalf("PromiseDate() = %v, want %v", d, want)
	}
}

func TestAllocateOrderEveryLineBackorderedEmitsNoOrderLevelEvent(t *testing.T) {
	f := newFixture()
	f.inventory.reserveErr = ports.ErrInsufficientStock
	o := f.mustReceive(t, true, line("SKU-1", 1, "pick"), line("SKU-2", 1, "pick"))

	got, err := f.allocateOrder().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got.Status() != order.StatusBackordered {
		t.Fatalf("Status() = %q, want %q", got.Status(), order.StatusBackordered)
	}
	assertEventNames(t, f.events, "OrderLineBackordered", "OrderLineBackordered")
	if got.PromiseDate() != nil {
		t.Fatalf("a fully backordered order must have no promise date, got %v", got.PromiseDate())
	}
}

// The fail-closed rule: a transport/5xx error is NOT a business fact and
// must never be recorded as a backorder.
func TestAllocateOrderFailsClosedOnANonBusinessError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "transport failure", err: errBoom},
		{name: "permissive (no-op) downstream", err: ports.ErrDownstreamNotConfigured},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.inventory.reserveErrBySKU["SKU-2"] = tt.err
			o := f.mustReceive(t, true, line("SKU-1", 1, "pick"), line("SKU-2", 1, "pick"))

			_, err := f.allocateOrder().Execute(context.Background(), o.ID())
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}

			// Line 2 must NOT be backordered: nothing in the system
			// asserted "no stock", only "we do not know".
			stored, getErr := f.getOrder().Execute(context.Background(), o.ID())
			if getErr != nil {
				t.Fatalf("GetOrder: %v", getErr)
			}
			assertLineStatuses(t, stored, order.LineAllocated, order.LinePending)

			// No order-level allocation event was published for a run
			// that did not complete.
			assertEventNames(t, f.events, "OrderLineAllocated")
		})
	}
}

// A hard failure still persists the reservations that genuinely happened,
// so nothing is stranded inside inventory-storage, and re-running resumes.
func TestAllocateOrderPersistsRealReservationsBeforeFailing(t *testing.T) {
	f := newFixture()
	f.inventory.reserveErrBySKU["SKU-2"] = errBoom
	o := f.mustReceive(t, true, line("SKU-1", 1, "pick"), line("SKU-2", 1, "pick"))

	if _, err := f.allocateOrder().Execute(context.Background(), o.ID()); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want %v", err, errBoom)
	}

	// Clearing the fault and re-running must allocate only the still-Pending
	// line — "cannot allocate the same line twice" doing its job.
	delete(f.inventory.reserveErrBySKU, "SKU-2")
	f.events.reset()

	got, err := f.allocateOrder().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	assertLineStatuses(t, got, order.LineAllocated, order.LineAllocated)
	assertEventNames(t, f.events, "OrderLineAllocated", "OrderAllocated")

	// SKU-1 was reserved exactly once across both runs.
	skuOneCalls := 0
	for _, call := range f.inventory.reserveCalls {
		if call.SKU == "SKU-1" {
			skuOneCalls++
		}
	}
	if skuOneCalls != 1 {
		t.Fatalf("SKU-1 was reserved %d times, want exactly 1", skuOneCalls)
	}
}

func TestAllocateOrderErrorPaths(t *testing.T) {
	t.Run("unknown order", func(t *testing.T) {
		f := newFixture()
		_, err := f.allocateOrder().Execute(context.Background(), "ord-missing")
		if !errors.Is(err, usecases.ErrOrderNotFound) {
			t.Fatalf("err = %v, want %v", err, usecases.ErrOrderNotFound)
		}
	})

	t.Run("repository lookup fails", func(t *testing.T) {
		f := newFixture()
		f.orders = &failingRepo{inner: f.orders, findErr: errBoom}
		_, err := f.allocateOrder().Execute(context.Background(), "ord-1")
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("save fails after a successful pass", func(t *testing.T) {
		f := newFixture()
		o := f.mustReceive(t, false, line("SKU-1", 1, "pick"))
		f.orders = &failingRepo{inner: f.orders, saveErr: errBoom}

		_, err := f.allocateOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("save fails while persisting partial progress after a hard failure", func(t *testing.T) {
		f := newFixture()
		o := f.mustReceive(t, true, line("SKU-1", 1, "pick"), line("SKU-2", 1, "pick"))
		f.inventory.reserveErrBySKU["SKU-2"] = errBoom
		f.orders = &failingRepo{inner: f.orders, saveErr: errBoom}

		_, err := f.allocateOrder().Execute(context.Background(), o.ID())
		// Both the allocation failure and the save failure are reported.
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("publisher fails on a line event", func(t *testing.T) {
		f := newFixture()
		o := f.mustReceive(t, false, line("SKU-1", 1, "pick"))
		f.events.err = errBoom

		_, err := f.allocateOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("publisher fails on a backordered line event", func(t *testing.T) {
		f := newFixture()
		o := f.mustReceive(t, false, line("SKU-1", 1, "pick"))
		f.inventory.reserveErr = ports.ErrInsufficientStock
		f.events.err = errBoom

		_, err := f.allocateOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("re-allocating an already allocated order is a no-op", func(t *testing.T) {
		f := newFixture()
		o := f.mustReceive(t, false, line("SKU-1", 1, "pick"))
		if _, err := f.allocateOrder().Execute(context.Background(), o.ID()); err != nil {
			t.Fatalf("first Execute: %v", err)
		}
		f.events.reset()

		got, err := f.allocateOrder().Execute(context.Background(), o.ID())
		if err != nil {
			t.Fatalf("second Execute: %v", err)
		}
		if got.Status() != order.StatusAllocated {
			t.Fatalf("Status() = %q, want %q", got.Status(), order.StatusAllocated)
		}
		assertEventNames(t, f.events)
		if len(f.inventory.reserveCalls) != 1 {
			t.Fatalf("reserve calls = %d, want 1 — an allocated line must not be reserved again", len(f.inventory.reserveCalls))
		}
	})
}
