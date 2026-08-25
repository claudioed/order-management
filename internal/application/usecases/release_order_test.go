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

// allocatedFixture returns a fixture whose order is fully allocated.
func allocatedFixture(t *testing.T, allowPartialShipment bool, lines ...usecases.NewLine) (*fixture, *order.Order) {
	t.Helper()
	f := newFixture()
	o := f.mustReceive(t, allowPartialShipment, lines...)
	if _, err := f.allocateOrder().Execute(context.Background(), o.ID()); err != nil {
		t.Fatalf("AllocateOrder: %v", err)
	}
	f.events.reset()
	return f, o
}

func TestReleaseOrderHappyPath(t *testing.T) {
	f, o := allocatedFixture(t, false,
		usecases.NewLine{SKU: "SKU-1", Quantity: 2, PathID: "pick", GiftWrap: true},
		line("SKU-2", 1, "singles"),
	)

	got, err := f.releaseOrder().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got.Status() != order.StatusReleased {
		t.Fatalf("Status() = %q, want %q", got.Status(), order.StatusReleased)
	}
	assertLineStatuses(t, got, order.LineReleased, order.LineReleased)
	assertEventNames(t, f.events, "OrderLineReleased", "OrderLineReleased", "OrderReleased")

	if len(f.work.calls) != 2 {
		t.Fatalf("work calls = %d, want 2", len(f.work.calls))
	}

	promiseDate := now().Add(24 * time.Hour)
	first := f.work.calls[0]
	if first.PathID != "pick" || first.SKU != "SKU-1" || !first.GiftWrap {
		t.Fatalf("call 0 = %+v, want pick/SKU-1/giftWrap", first)
	}
	if first.Reference != o.ID() {
		t.Fatalf("call 0 reference = %q, want the order id %q", first.Reference, o.ID())
	}
	if !first.CPT.Equal(promiseDate) {
		t.Fatalf("call 0 cpt = %v, want the promise date %v", first.CPT, promiseDate)
	}
	if first.WorkUnitID != usecases.WorkUnitID(o.ID(), 1) {
		t.Fatalf("call 0 workUnitId = %q, want %q", first.WorkUnitID, usecases.WorkUnitID(o.ID(), 1))
	}

	second := f.work.calls[1]
	if second.PathID != "singles" || second.GiftWrap {
		t.Fatalf("call 1 = %+v, want singles without gift wrap", second)
	}
	if second.WorkUnitID == first.WorkUnitID {
		t.Fatal("each line must get a distinct work unit id")
	}
	// Both lines carry the ORDER's promise date, not a per-line one.
	if !second.CPT.Equal(promiseDate) {
		t.Fatalf("call 1 cpt = %v, want %v", second.CPT, promiseDate)
	}
}

func TestWorkUnitIDIsDeterministicAndLineScoped(t *testing.T) {
	if got, want := usecases.WorkUnitID("ord-77213", 1), "ord-77213-line-1"; got != want {
		t.Fatalf("WorkUnitID = %q, want %q", got, want)
	}
	if usecases.WorkUnitID("ord-1", 1) == usecases.WorkUnitID("ord-1", 2) {
		t.Fatal("two lines of the same order must not share a work unit id")
	}
	if usecases.WorkUnitID("ord-1", 1) == usecases.WorkUnitID("ord-2", 1) {
		t.Fatal("two orders must not share a work unit id")
	}
}

// Partial shipment: only the allocated lines are released; the backordered
// one is left alone and the order reads PartiallyReleased.
func TestReleaseOrderPartialShipmentReleasesOnlyAllocatedLines(t *testing.T) {
	f, o := backorderedFixture(t, true)

	got, err := f.releaseOrder().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got.Status() != order.StatusPartiallyReleased {
		t.Fatalf("Status() = %q, want %q", got.Status(), order.StatusPartiallyReleased)
	}
	assertLineStatuses(t, got, order.LineReleased, order.LineBackordered)
	// No OrderReleased: the order is not fully released.
	assertEventNames(t, f.events, "OrderLineReleased")

	if len(f.work.calls) != 1 {
		t.Fatalf("work calls = %d, want 1 (only the allocated line)", len(f.work.calls))
	}
}

// BR3 at the release boundary.
func TestReleaseOrderRejectsAShipCompleteOrderWithAnUnallocatedLine(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) (*fixture, *order.Order)
	}{
		{
			name: "a backordered line blocks release",
			setup: func(t *testing.T) (*fixture, *order.Order) {
				return backorderedFixture(t, false)
			},
		},
		{
			name: "a never-allocated order blocks release",
			setup: func(t *testing.T) (*fixture, *order.Order) {
				f := newFixture()
				o := f.mustReceive(t, false, line("SKU-1", 1, "pick"))
				return f, o
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, o := tt.setup(t)
			_, err := f.releaseOrder().Execute(context.Background(), o.ID())
			if !errors.Is(err, order.ErrShipCompleteBlocked) {
				t.Fatalf("err = %v, want %v", err, order.ErrShipCompleteBlocked)
			}
			if len(f.work.calls) != 0 {
				t.Fatalf("nothing may reach wes-work-planning, got %d calls", len(f.work.calls))
			}
			assertEventNames(t, f.events)
		})
	}
}

func TestReleaseOrderErrorPaths(t *testing.T) {
	t.Run("unknown order", func(t *testing.T) {
		f := newFixture()
		_, err := f.releaseOrder().Execute(context.Background(), "ord-missing")
		if !errors.Is(err, usecases.ErrOrderNotFound) {
			t.Fatalf("err = %v, want %v", err, usecases.ErrOrderNotFound)
		}
	})

	t.Run("repository lookup fails", func(t *testing.T) {
		f := newFixture()
		f.orders = &failingRepo{inner: f.orders, findErr: errBoom}
		_, err := f.releaseOrder().Execute(context.Background(), "ord-1")
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("an order with nothing allocated is rejected", func(t *testing.T) {
		f := newFixture()
		f.inventory.reserveErr = ports.ErrInsufficientStock
		o := f.mustReceive(t, true, line("SKU-1", 1, "pick"))
		if _, err := f.allocateOrder().Execute(context.Background(), o.ID()); err != nil {
			t.Fatalf("AllocateOrder: %v", err)
		}

		_, err := f.releaseOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, usecases.ErrNoAllocatedLines) {
			t.Fatalf("err = %v, want %v", err, usecases.ErrNoAllocatedLines)
		}
	})

	t.Run("an already released order has nothing left to release", func(t *testing.T) {
		f, o := allocatedFixture(t, false, line("SKU-1", 1, "pick"))
		if _, err := f.releaseOrder().Execute(context.Background(), o.ID()); err != nil {
			t.Fatalf("first Execute: %v", err)
		}

		_, err := f.releaseOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, usecases.ErrNoAllocatedLines) {
			t.Fatalf("err = %v, want %v", err, usecases.ErrNoAllocatedLines)
		}
		if len(f.work.calls) != 1 {
			t.Fatalf("a line must never be released twice, work calls = %d", len(f.work.calls))
		}
	})

	t.Run("an allocated order with no promise date is rejected", func(t *testing.T) {
		f := newFixture()
		o := f.mustReceive(t, false, line("SKU-1", 1, "pick"))
		// Allocate the aggregate directly, bypassing the promise-date
		// policy AllocateOrder would have applied.
		if err := o.Allocate(1, "res-1"); err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		if err := f.orders.Save(context.Background(), o); err != nil {
			t.Fatalf("Save: %v", err)
		}

		_, err := f.releaseOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, usecases.ErrPromiseDateNotSet) {
			t.Fatalf("err = %v, want %v", err, usecases.ErrPromiseDateNotSet)
		}
	})

	t.Run("a downstream failure is hard and never a business fact", func(t *testing.T) {
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
				f.work.enqueueErr = tt.err

				_, err := f.releaseOrder().Execute(context.Background(), o.ID())
				if !errors.Is(err, tt.err) {
					t.Fatalf("err = %v, want %v", err, tt.err)
				}

				stored, getErr := f.getOrder().Execute(context.Background(), o.ID())
				if getErr != nil {
					t.Fatalf("GetOrder: %v", getErr)
				}
				assertLineStatuses(t, stored, order.LineAllocated)
			})
		}
	})

	t.Run("lines released before a mid-run failure are persisted", func(t *testing.T) {
		f, o := allocatedFixture(t, true, line("SKU-1", 1, "pick"), line("SKU-2", 1, "pick"))
		f.work.enqueueErrBySKU["SKU-2"] = errBoom

		_, err := f.releaseOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}

		stored, getErr := f.getOrder().Execute(context.Background(), o.ID())
		if getErr != nil {
			t.Fatalf("GetOrder: %v", getErr)
		}
		assertLineStatuses(t, stored, order.LineReleased, order.LineAllocated)
	})

	t.Run("save fails while persisting a partial release", func(t *testing.T) {
		f, o := allocatedFixture(t, true, line("SKU-1", 1, "pick"), line("SKU-2", 1, "pick"))
		f.work.enqueueErrBySKU["SKU-2"] = errBoom
		f.orders = &failingRepo{inner: f.orders, saveErr: errBoom}

		_, err := f.releaseOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("save fails after a full release", func(t *testing.T) {
		f, o := allocatedFixture(t, false, line("SKU-1", 1, "pick"))
		f.orders = &failingRepo{inner: f.orders, saveErr: errBoom}

		_, err := f.releaseOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("publisher fails on the line event", func(t *testing.T) {
		f, o := allocatedFixture(t, false, line("SKU-1", 1, "pick"))
		f.events.err = errBoom

		_, err := f.releaseOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("publisher fails on the order event", func(t *testing.T) {
		f, o := allocatedFixture(t, false, line("SKU-1", 1, "pick"))
		f.events.failAfter(1, errBoom)

		_, err := f.releaseOrder().Execute(context.Background(), o.ID())
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})
}
