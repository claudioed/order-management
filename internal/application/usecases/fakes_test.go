package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/order-management/internal/adapters/outbound/memory"
	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// errBoom stands in for any infrastructure failure that is not a business
// fact — the exact case AllocateOrder must NOT turn into a backorder.
var errBoom = errors.New("boom")

// fakeInventory is a scripted ports.InventoryReservationClient. Per-SKU
// scripts let one test express "SKU-1 reserves, SKU-2 is out of stock".
type fakeInventory struct {
	mu sync.Mutex

	// reserveErrBySKU maps a SKU to the error Reserve should return for
	// it. ports.ErrInsufficientStock models inventory-storage's 409.
	reserveErrBySKU map[shared.SKU]error
	// reserveErr applies to every SKU with no specific script.
	reserveErr error
	// revokeErr is returned by every RevokeReservation call.
	revokeErr error

	nextID       int
	reserveCalls []ports.ReservationRequest
	revokeCalls  []string
}

func newFakeInventory() *fakeInventory {
	return &fakeInventory{reserveErrBySKU: map[shared.SKU]error{}}
}

func (f *fakeInventory) Reserve(_ context.Context, req ports.ReservationRequest) (ports.ReservationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reserveCalls = append(f.reserveCalls, req)

	if err, ok := f.reserveErrBySKU[req.SKU]; ok {
		return ports.ReservationResult{}, err
	}
	if f.reserveErr != nil {
		return ports.ReservationResult{}, f.reserveErr
	}
	f.nextID++
	return ports.ReservationResult{ReservationID: reservationID(f.nextID)}, nil
}

func (f *fakeInventory) RevokeReservation(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokeCalls = append(f.revokeCalls, id)
	return f.revokeErr
}

func reservationID(n int) string {
	return fmt.Sprintf("res-%d", n)
}

// fakeWork is a scripted ports.WorkReleaseClient.
type fakeWork struct {
	mu sync.Mutex

	enqueueErrBySKU map[shared.SKU]error
	enqueueErr      error

	calls []ports.WorkUnitRequest
}

func newFakeWork() *fakeWork {
	return &fakeWork{enqueueErrBySKU: map[shared.SKU]error{}}
}

func (f *fakeWork) EnqueueWorkUnit(_ context.Context, req ports.WorkUnitRequest) (ports.WorkUnitResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)

	if err, ok := f.enqueueErrBySKU[req.SKU]; ok {
		return ports.WorkUnitResult{}, err
	}
	if f.enqueueErr != nil {
		return ports.WorkUnitResult{}, f.enqueueErr
	}
	return ports.WorkUnitResult{WorkUnitID: req.WorkUnitID}, nil
}

// recordingPublisher captures published events so a test can assert the
// exact facts a use case emitted, in order.
type recordingPublisher struct {
	mu     sync.Mutex
	events []shared.DomainEvent
	err    error
	// errAfter, when >= 0, makes Publish fail once that many events have
	// already been accepted — for asserting a failure on a later event in
	// a sequence rather than the first.
	errAfter int
}

func (p *recordingPublisher) Publish(_ context.Context, event shared.DomainEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil && len(p.events) >= p.errAfter {
		return p.err
	}
	p.events = append(p.events, event)
	return nil
}

// failAfter schedules err for the (n+1)th and every later Publish call.
func (p *recordingPublisher) failAfter(n int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errAfter = n
	p.err = err
}

func (p *recordingPublisher) names() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.events))
	for _, e := range p.events {
		out = append(out, e.EventName())
	}
	return out
}

// failingRepo wraps a real repo and fails a chosen operation, so the
// error-propagation paths of every use case are exercised.
type failingRepo struct {
	inner      ports.OrderRepo
	saveErr    error
	findErr    error
	nextIDErr  error
	saveCalls  int
	failSaveOn int // when > 0, only the Nth Save fails
}

func (r *failingRepo) Save(ctx context.Context, o *order.Order) error {
	r.saveCalls++
	if r.saveErr != nil && (r.failSaveOn == 0 || r.failSaveOn == r.saveCalls) {
		return r.saveErr
	}
	return r.inner.Save(ctx, o)
}

func (r *failingRepo) FindByID(ctx context.Context, id shared.OrderId) (*order.Order, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	return r.inner.FindByID(ctx, id)
}

func (r *failingRepo) NextID(ctx context.Context) (shared.OrderId, error) {
	if r.nextIDErr != nil {
		return "", r.nextIDErr
	}
	return r.inner.NextID(ctx)
}

// fixture bundles everything a use-case test needs, wired to in-memory and
// fake adapters. No test in this package touches a real network or DB.
type fixture struct {
	orders    ports.OrderRepo
	inventory *fakeInventory
	work      *fakeWork
	events    *recordingPublisher
	clock     *memory.FixedClock
	promise   order.LeadTimePolicy
}

func now() time.Time {
	return time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
}

func newFixture() *fixture {
	return &fixture{
		orders:    memory.NewOrderRepo(),
		inventory: newFakeInventory(),
		work:      newFakeWork(),
		events:    &recordingPublisher{},
		clock:     memory.NewFixedClock(now()),
		promise: order.NewLeadTimePolicy(24*time.Hour, map[shared.PathId]time.Duration{
			"singles": 6 * time.Hour,
		}),
	}
}

func (f *fixture) receiveOrder() *usecases.ReceiveOrder {
	return &usecases.ReceiveOrder{Orders: f.orders, Events: f.events, Clock: f.clock}
}

func (f *fixture) allocateOrder() *usecases.AllocateOrder {
	return &usecases.AllocateOrder{Orders: f.orders, Inventory: f.inventory, Events: f.events, Clock: f.clock, Promise: f.promise}
}

func (f *fixture) retryAllocation() *usecases.RetryAllocation {
	return &usecases.RetryAllocation{Orders: f.orders, Inventory: f.inventory, Events: f.events, Clock: f.clock, Promise: f.promise}
}

func (f *fixture) releaseOrder() *usecases.ReleaseOrder {
	return &usecases.ReleaseOrder{Orders: f.orders, Work: f.work, Events: f.events, Clock: f.clock}
}

func (f *fixture) cancelOrder() *usecases.CancelOrder {
	return &usecases.CancelOrder{Orders: f.orders, Inventory: f.inventory, Events: f.events, Clock: f.clock}
}

func (f *fixture) getOrder() *usecases.GetOrder {
	return &usecases.GetOrder{Orders: f.orders}
}

// mustReceive creates an order through the real ReceiveOrder use case.
func (f *fixture) mustReceive(t *testing.T, allowPartialShipment bool, lines ...usecases.NewLine) *order.Order {
	t.Helper()
	o, err := f.receiveOrder().Execute(context.Background(), lines, allowPartialShipment)
	if err != nil {
		t.Fatalf("ReceiveOrder: %v", err)
	}
	f.events.reset()
	return o
}

func (p *recordingPublisher) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = nil
}

func line(sku shared.SKU, qty int, pathID shared.PathId) usecases.NewLine {
	return usecases.NewLine{SKU: sku, Quantity: qty, PathID: pathID}
}

// assertLineStatuses checks the aggregate's per-line states in line order.
func assertLineStatuses(t *testing.T, o *order.Order, want ...order.LineStatus) {
	t.Helper()
	lines := o.Lines()
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d", len(lines), len(want))
	}
	for i, l := range lines {
		if l.Status() != want[i] {
			t.Fatalf("line %d status = %q, want %q", l.LineNo(), l.Status(), want[i])
		}
	}
}

func assertEventNames(t *testing.T, p *recordingPublisher, want ...string) {
	t.Helper()
	got := p.names()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}
