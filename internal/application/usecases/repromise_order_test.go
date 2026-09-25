package usecases_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// fakeProcessedEvents is a scripted ports.RepromiseProcessedEvents. By
// default every event_id is new; alreadyProcessed pre-seeds ids that
// must report isNew=false, and err makes every call fail (an
// infrastructure failure, distinct from "already processed").
type fakeProcessedEvents struct {
	mu               sync.Mutex
	alreadyProcessed map[string]bool
	err              error
	calls            []string
}

func newFakeProcessedEvents() *fakeProcessedEvents {
	return &fakeProcessedEvents{alreadyProcessed: map[string]bool{}}
}

func (f *fakeProcessedEvents) MarkProcessed(_ context.Context, eventId string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, eventId)
	if f.err != nil {
		return false, f.err
	}
	if f.alreadyProcessed[eventId] {
		return false, nil
	}
	f.alreadyProcessed[eventId] = true
	return true, nil
}

// repromiseFixture bundles a real ReceiveOrder-created, fully-released
// order plus the fake adapters RepromiseOrder needs, so tests exercise
// the real domain PromiseGroups() breakdown rather than a hand-built
// stub.
type repromiseFixture struct {
	f         *fixture
	processed *fakeProcessedEvents
}

func newRepromiseFixture() *repromiseFixture {
	return &repromiseFixture{f: newFixture(), processed: newFakeProcessedEvents()}
}

func (rf *repromiseFixture) repromiseOrder(promise order.PromisePolicy) *usecases.RepromiseOrder {
	return &usecases.RepromiseOrder{
		Orders:    rf.f.orders,
		Promise:   promise,
		Events:    rf.f.events,
		Clock:     rf.f.clock,
		Processed: rf.processed,
	}
}

func TestRepromiseOrder_PromiseMoved_PublishesOrderRepromisedAndUpdatesGroups(t *testing.T) {
	rf := newRepromiseFixture()
	o := rf.f.mustReceive(t, false, line("SKU-1", 1, "pick"))
	if o.Status() != order.StatusReleased {
		t.Fatalf("Status() = %q, want %q", o.Status(), order.StatusReleased)
	}
	original := o.PromiseGroups()
	if len(original) != 1 {
		t.Fatalf("original PromiseGroups = %d, want 1: %+v", len(original), original)
	}
	originalCutoff := original[0].Promise.CutoffAt

	// A capability input change since intake: "pick" now has a SHORTER
	// lead time (as if a faster path/schedule were now in force). Same
	// shape of PromisePolicy (fallback only, no Schedule/Capability —
	// exactly what a real order-management deployment has today), a
	// genuinely different result.
	movedLeadTime := order.NewLeadTimePolicy(6*time.Hour, nil)
	movedPromise := order.PromisePolicy{Fallback: movedLeadTime}

	err := rf.repromiseOrder(movedPromise).Execute(context.Background(), usecases.RepromiseOrderRequest{
		SourceEventId: "evt-1", OrderId: o.ID(), LineNo: 1, Reason: "TaskCPTMissed",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stored, err := rf.f.orders.FindByID(context.Background(), o.ID())
	if err != nil || stored == nil {
		t.Fatalf("FindByID: %v, %v", stored, err)
	}
	updated := stored.PromiseGroups()
	if len(updated) != 1 {
		t.Fatalf("updated PromiseGroups = %d, want 1: %+v", len(updated), updated)
	}
	wantCutoff := rf.f.clock.Now().Add(6 * time.Hour)
	if !updated[0].Promise.CutoffAt.Equal(wantCutoff) {
		t.Errorf("updated cutoff = %v, want %v", updated[0].Promise.CutoffAt, wantCutoff)
	}
	if updated[0].Promise.CutoffAt.Equal(originalCutoff) {
		t.Fatal("cutoff did not actually move; test fixture is not discriminating")
	}

	repromised := findOrderRepromised(t, rf.f.events)
	if repromised.OrderID != o.ID() {
		t.Errorf("OrderRepromised.OrderID = %q, want %q", repromised.OrderID, o.ID())
	}
	if repromised.Reason != "TaskCPTMissed" {
		t.Errorf("OrderRepromised.Reason = %q, want %q", repromised.Reason, "TaskCPTMissed")
	}
	// Both old and new promises are LeadTime-basis: neither has a CPT
	// identity, so both are empty strings — the move is visible via
	// the domain's persisted CutoffAt, not via this event's CptId
	// fields, exactly as ADR 0014's Promise value object documents.
	if repromised.CptIdOld != "" || repromised.CptIdNew != "" {
		t.Errorf("OrderRepromised CptIdOld/New = %q/%q, want both empty for a LeadTime-basis promise", repromised.CptIdOld, repromised.CptIdNew)
	}
}

func TestRepromiseOrder_PromiseUnchanged_NoEventPublished(t *testing.T) {
	rf := newRepromiseFixture()
	o := rf.f.mustReceive(t, false, line("SKU-1", 1, "pick"))

	// Identical policy shape to what produced the order's promise at
	// intake: nothing has actually changed, so nothing should move.
	err := rf.repromiseOrder(rf.f.promise).Execute(context.Background(), usecases.RepromiseOrderRequest{
		SourceEventId: "evt-1", OrderId: o.ID(), LineNo: 1, Reason: "PackageManifested",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertEventNames(t, rf.f.events) // no events published
}

func TestRepromiseOrder_AlreadyProcessed_NoOp(t *testing.T) {
	rf := newRepromiseFixture()
	o := rf.f.mustReceive(t, false, line("SKU-1", 1, "pick"))
	rf.processed.alreadyProcessed["evt-1"] = true

	movedPromise := order.PromisePolicy{Fallback: order.NewLeadTimePolicy(1*time.Hour, nil)}
	err := rf.repromiseOrder(movedPromise).Execute(context.Background(), usecases.RepromiseOrderRequest{
		SourceEventId: "evt-1", OrderId: o.ID(), LineNo: 1, Reason: "TaskCPTMissed",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertEventNames(t, rf.f.events) // no events published, no re-evaluation
}

func TestRepromiseOrder_OrderNotFound_NoOp(t *testing.T) {
	rf := newRepromiseFixture()
	err := rf.repromiseOrder(order.PromisePolicy{}).Execute(context.Background(), usecases.RepromiseOrderRequest{
		SourceEventId: "evt-1", OrderId: "ord-does-not-exist", LineNo: 1, Reason: "TaskCPTMissed",
	})
	if err != nil {
		t.Fatalf("Execute: %v, want nil (fail-soft)", err)
	}
	assertEventNames(t, rf.f.events)
}

func TestRepromiseOrder_LineNotInAnyCurrentGroup_NoOp(t *testing.T) {
	rf := newRepromiseFixture()
	o := rf.f.mustReceive(t, false, line("SKU-1", 1, "pick"))

	movedPromise := order.PromisePolicy{Fallback: order.NewLeadTimePolicy(1*time.Hour, nil)}
	// Line 99 does not exist on this order at all.
	err := rf.repromiseOrder(movedPromise).Execute(context.Background(), usecases.RepromiseOrderRequest{
		SourceEventId: "evt-1", OrderId: o.ID(), LineNo: 99, Reason: "TaskCPTMissed",
	})
	if err != nil {
		t.Fatalf("Execute: %v, want nil (fail-soft)", err)
	}
	assertEventNames(t, rf.f.events)
}

func TestRepromiseOrder_NoFreshPromiseAvailable_NoOp(t *testing.T) {
	rf := newRepromiseFixture()

	// Build an order directly (not through ReceiveOrder) whose line is
	// still Pending — never allocated — but which already carries a
	// (now stale) persisted PromiseGroup breakdown covering that line,
	// mirroring the only way PromisePolicy.PromiseGroups' own ok=false
	// branch is reachable: no line currently allocated at all.
	l, err := order.NewOrderLine(1, "SKU-1", 1, "pick", false)
	if err != nil {
		t.Fatalf("NewOrderLine: %v", err)
	}
	staleCutoff := rf.f.clock.Now().Add(24 * time.Hour)
	o, err := order.New("ord-stale-1", []*order.OrderLine{l}, false)
	if err != nil {
		t.Fatalf("order.New: %v", err)
	}
	o.SetPromiseGroups([]order.PromiseGroup{{LineNos: []int{1}, Promise: order.Promise{CutoffAt: staleCutoff, Basis: order.BasisLeadTime}}})
	if err := rf.f.orders.Save(context.Background(), o); err != nil {
		t.Fatalf("Save: %v", err)
	}

	err = rf.repromiseOrder(order.PromisePolicy{Fallback: order.NewLeadTimePolicy(1*time.Hour, nil)}).
		Execute(context.Background(), usecases.RepromiseOrderRequest{
			SourceEventId: "evt-1", OrderId: o.ID(), LineNo: 1, Reason: "TaskCPTMissed",
		})
	if err != nil {
		t.Fatalf("Execute: %v, want nil (fail-soft)", err)
	}
	assertEventNames(t, rf.f.events)

	// The stale group breakdown must be untouched — nothing was saved.
	stored, err := rf.f.orders.FindByID(context.Background(), o.ID())
	if err != nil || stored == nil {
		t.Fatalf("FindByID: %v, %v", stored, err)
	}
	if got := stored.PromiseGroups()[0].Promise.CutoffAt; !got.Equal(staleCutoff) {
		t.Errorf("PromiseGroups()[0].Promise.CutoffAt = %v, want unchanged %v", got, staleCutoff)
	}
}

func TestRepromiseOrder_ProcessedMarkError_ReturnsError(t *testing.T) {
	rf := newRepromiseFixture()
	rf.processed.err = errBoom
	err := rf.repromiseOrder(order.PromisePolicy{}).Execute(context.Background(), usecases.RepromiseOrderRequest{
		SourceEventId: "evt-1", OrderId: "ord-1", LineNo: 1, Reason: "TaskCPTMissed",
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Execute err = %v, want errBoom", err)
	}
}

func TestRepromiseOrder_SaveError_ReturnsError(t *testing.T) {
	rf := newRepromiseFixture()
	o := rf.f.mustReceive(t, false, line("SKU-1", 1, "pick"))

	failing := &failingRepo{inner: rf.f.orders, saveErr: errBoom}
	uc := &usecases.RepromiseOrder{
		Orders: failing, Promise: order.PromisePolicy{Fallback: order.NewLeadTimePolicy(1*time.Hour, nil)},
		Events: rf.f.events, Clock: rf.f.clock, Processed: rf.processed,
	}
	err := uc.Execute(context.Background(), usecases.RepromiseOrderRequest{
		SourceEventId: "evt-1", OrderId: o.ID(), LineNo: 1, Reason: "TaskCPTMissed",
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Execute err = %v, want errBoom", err)
	}
}

func TestRepromiseOrder_PublishError_ReturnsError(t *testing.T) {
	rf := newRepromiseFixture()
	o := rf.f.mustReceive(t, false, line("SKU-1", 1, "pick"))
	rf.f.events.failAfter(0, errBoom)

	uc := &usecases.RepromiseOrder{
		Orders: rf.f.orders, Promise: order.PromisePolicy{Fallback: order.NewLeadTimePolicy(1*time.Hour, nil)},
		Events: rf.f.events, Clock: rf.f.clock, Processed: rf.processed,
	}
	err := uc.Execute(context.Background(), usecases.RepromiseOrderRequest{
		SourceEventId: "evt-1", OrderId: o.ID(), LineNo: 1, Reason: "TaskCPTMissed",
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Execute err = %v, want errBoom", err)
	}
}

// findOrderRepromised returns the first shared.OrderRepromised event
// published to p, failing the test if none was published.
func findOrderRepromised(t *testing.T, p *recordingPublisher) shared.OrderRepromised {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.events {
		if r, ok := e.(shared.OrderRepromised); ok {
			return r
		}
	}
	t.Fatalf("no OrderRepromised event was published; events = %v", p.names())
	return shared.OrderRepromised{}
}

var _ ports.RepromiseProcessedEvents = (*fakeProcessedEvents)(nil)
