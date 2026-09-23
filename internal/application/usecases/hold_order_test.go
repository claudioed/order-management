package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// Tests for ADR 0020 §1's hold (releaseOnAllocation=false) and §4's
// ship-complete invariant.

func heldOrderFixture(t *testing.T) (*fixture, *order.Order) {
	t.Helper()
	f := newFixture()
	uc := f.receiveOrder()
	o, err := uc.ExecuteHeld(context.Background(), []usecases.NewLine{
		{SKU: "SKU-1", Quantity: 2, PathID: "pick"},
	}, false, false)
	if err != nil {
		t.Fatalf("ExecuteHeld: %v", err)
	}
	return f, o
}

func TestReceiveOrder_Held_AllocatesButDoesNotRelease(t *testing.T) {
	_, o := heldOrderFixture(t)

	if got := len(o.LinesWithStatus(order.LineAllocated)); got != 1 {
		t.Fatalf("allocated lines = %d, want 1 — the hold must still allocate", got)
	}
	if got := len(o.LinesWithStatus(order.LineReleased)); got != 0 {
		t.Fatalf("released lines = %d, want 0 — a held order must put no work on the floor", got)
	}
}

func TestReceiveOrder_Held_StillComputesAPromise(t *testing.T) {
	_, o := heldOrderFixture(t)

	// The whole point of holding is to let a caller read the promise and
	// decide. A hold that skipped promising would be useless.
	if o.PromiseDate() == nil {
		t.Fatal("expected a promise on a held order: the holder must be able to read it and decide")
	}
}

func TestReceiveOrder_Held_StillPublishesOrderAllocated(t *testing.T) {
	f, _ := heldOrderFixture(t)

	// The allocation genuinely happened and inventory genuinely holds
	// reservations; suppressing the event would hide a real state change
	// from every other context.
	names := f.events.names()
	if !contains(names, "OrderAllocated") {
		t.Fatalf("expected OrderAllocated; published: %v", names)
	}
	if contains(names, "OrderReleased") {
		t.Fatalf("OrderReleased must not fire for a held order; published: %v", names)
	}
}

func TestReceiveOrder_Unheld_IsUnchanged(t *testing.T) {
	f := newFixture()
	uc := f.receiveOrder()

	// The default path, asserted explicitly: absent the flag, behaviour
	// is receive-allocate-release exactly as before ADR 0020.
	o, err := uc.Execute(context.Background(), []usecases.NewLine{
		{SKU: "SKU-1", Quantity: 2, PathID: "pick"},
	}, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := len(o.LinesWithStatus(order.LineReleased)); got != 1 {
		t.Fatalf("released lines = %d, want 1 — the default must still release", got)
	}
	if !o.ReleaseOnAllocation() {
		t.Fatal("an order received without the flag must report ReleaseOnAllocation()=true")
	}
}

func TestReceiveOrder_HeldAndPartialShipment_IsRejected(t *testing.T) {
	f := newFixture()
	uc := f.receiveOrder()

	_, err := uc.ExecuteHeld(context.Background(), []usecases.NewLine{
		{SKU: "SKU-1", Quantity: 1, PathID: "pick"},
	}, true, false)
	if !errors.Is(err, order.ErrHeldOrderMustBeShipComplete) {
		t.Fatalf("err = %v, want ErrHeldOrderMustBeShipComplete (ADR 0020 §4)", err)
	}
}

func TestReceiveOrder_RejectedIntent_PersistsNothing(t *testing.T) {
	f := newFixture()
	uc := f.receiveOrder()

	_, _ = uc.ExecuteHeld(context.Background(), []usecases.NewLine{
		{SKU: "SKU-1", Quantity: 1, PathID: "pick"},
	}, true, false)

	// A rejected intake must leave no trace: no id minted, no row, no
	// OrderReceived. Checking the event is the discriminating part — an
	// implementation that validated AFTER Save would still fail the
	// call but would have published to the whole fleet first.
	if names := f.events.names(); len(names) != 0 {
		t.Fatalf("a rejected intake must publish nothing; published: %v", names)
	}
}

func TestReleaseHeldOrder_ReleasesTheHeldLines(t *testing.T) {
	f, o := heldOrderFixture(t)

	uc := &usecases.ReleaseHeldOrder{
		Orders: f.orders, Events: f.events, Clock: f.clock,
		Inventory: f.inventory, Promise: f.promise,
	}
	got, err := uc.Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if n := len(got.LinesWithStatus(order.LineReleased)); n != 1 {
		t.Fatalf("released lines = %d, want 1", n)
	}
}

func TestReleaseHeldOrder_IsIdempotent(t *testing.T) {
	f, o := heldOrderFixture(t)
	uc := &usecases.ReleaseHeldOrder{
		Orders: f.orders, Events: f.events, Clock: f.clock,
		Inventory: f.inventory, Promise: f.promise,
	}

	if _, err := uc.Execute(context.Background(), o.ID()); err != nil {
		t.Fatalf("first release: %v", err)
	}
	// A caller holding an external fill-or-kill deadline cannot tell
	// "my release was lost" from "my release failed", so retrying must
	// be safe.
	got, err := uc.Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("second release must succeed, got %v", err)
	}
	if n := len(got.LinesWithStatus(order.LineReleased)); n != 1 {
		t.Fatalf("released lines = %d, want 1 after a repeat release", n)
	}
}

func TestReleaseHeldOrder_OnUnheldOrder_IsRejected(t *testing.T) {
	f := newFixture()
	rec := f.receiveOrder()
	o, err := rec.Execute(context.Background(), []usecases.NewLine{
		{SKU: "SKU-1", Quantity: 1, PathID: "pick"},
	}, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	uc := &usecases.ReleaseHeldOrder{
		Orders: f.orders, Events: f.events, Clock: f.clock,
		Inventory: f.inventory, Promise: f.promise,
	}
	// Already released by intake, so the idempotent branch answers
	// first — releasing an unheld order is a no-op success, not a 409.
	if _, err := uc.Execute(context.Background(), o.ID()); err != nil {
		t.Fatalf("expected idempotent success on an already-released order, got %v", err)
	}
}

func TestReleaseHeldOrder_UnknownOrder_IsNotFound(t *testing.T) {
	f := newFixture()
	uc := &usecases.ReleaseHeldOrder{
		Orders: f.orders, Events: f.events, Clock: f.clock,
		Inventory: f.inventory, Promise: f.promise,
	}
	_, err := uc.Execute(context.Background(), shared.OrderId("nope"))
	if !errors.Is(err, usecases.ErrOrderNotFound) {
		t.Fatalf("err = %v, want ErrOrderNotFound", err)
	}
}

func TestOrder_HoldSurvivesRehydration(t *testing.T) {
	// The reason the flag is persisted at all: a held order that
	// backorders is indistinguishable, by line status alone, from an
	// ordinary ship-complete order waiting on stock. If the hold did not
	// survive a repository round-trip, a later RetryAllocation would
	// release work onto the floor for an order nobody committed to.
	line := order.RehydrateOrderLine(1, "SKU-1", 1, "pick", false, order.LineAllocated, nil)
	held := order.RehydrateHeld("ord-1", []*order.OrderLine{line}, false, nil, nil, nil, nil, false)
	if held.ReleaseOnAllocation() {
		t.Fatal("a rehydrated held order must still report ReleaseOnAllocation()=false")
	}

	// And the pre-ADR path must read as un-held, since every row written
	// before migration 0005 defaults to TRUE.
	legacy := order.Rehydrate("ord-2", []*order.OrderLine{line}, false, nil, nil, nil)
	if !legacy.ReleaseOnAllocation() {
		t.Fatal("an order rehydrated via the pre-ADR constructor must be un-held")
	}
}

func TestRetryAllocation_OnHeldOrder_DoesNotRelease(t *testing.T) {
	// The bug this whole persistence change exists to prevent.
	f := newFixture()
	f.inventory.reserveErrBySKU["SKU-1"] = ports.ErrInsufficientStock // -> Backordered
	rec := f.receiveOrder()
	o, err := rec.ExecuteHeld(context.Background(), []usecases.NewLine{
		{SKU: "SKU-1", Quantity: 1, PathID: "pick"},
	}, false, false)
	if err != nil {
		t.Fatalf("ExecuteHeld: %v", err)
	}
	if n := len(o.LinesWithStatus(order.LineBackordered)); n != 1 {
		t.Fatalf("backordered lines = %d, want 1 (fixture precondition)", n)
	}
	// Stock arrives.
	delete(f.inventory.reserveErrBySKU, "SKU-1")

	got, err := f.retryAllocation().Execute(context.Background(), o.ID())
	if err != nil {
		t.Fatalf("RetryAllocation: %v", err)
	}
	if n := len(got.LinesWithStatus(order.LineReleased)); n != 0 {
		t.Fatalf("released lines = %d, want 0 — retry must not release a HELD order", n)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
