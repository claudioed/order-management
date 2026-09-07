//go:build integration

// Integration tests for the OLTP Postgres adapters (OrderRepo and the
// Postgres EventPublisher) against a real Postgres 16, gated behind the
// `integration` build tag. They are driven by CI's integration job (two
// postgres:16 service containers — one per database this repo owns) or run
// locally against `docker compose up -d` with DATABASE_URL exported; when
// DATABASE_URL is not set they skip, so `make test` stays hermetic.
package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/order-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

func requireDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set, skipping OLTP postgres integration test")
	}
	return url
}

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := requireDatabaseURL(t)
	if err := postgres.RunMigrations(url, "../../../../migrations"); err != nil {
		t.Fatalf("migrate OLTP: %v", err)
	}
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestOrderRepo_SaveAndFindByID_RoundTrip(t *testing.T) {
	pool := newPool(t)
	repo := postgres.NewOrderRepo(pool)
	ctx := context.Background()

	occurredAt := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	id := shared.OrderId("ord-int-" + time.Now().Format("150405.000000000"))

	// Build the aggregate through the domain's public API, not by
	// hand-crafting rows: what is stored must be exactly what the
	// aggregate produces.
	lines := mustLines(t, id)
	o, err := order.New(id, lines, true)
	if err != nil {
		t.Fatalf("order.New: %v", err)
	}

	if err := repo.Save(ctx, o); err != nil {
		t.Fatalf("Save (received): %v", err)
	}

	// Drive the same aggregate through its whole legal lifecycle,
	// persisting after each mutation — the repo must round-trip every
	// status transition the use cases can produce.
	if err := o.Allocate(1, "res-int-1"); err != nil {
		t.Fatalf("Allocate line 1: %v", err)
	}
	if err := o.Allocate(2, "res-int-2"); err != nil {
		t.Fatalf("Allocate line 2: %v", err)
	}
	o.SetPromiseDate(occurredAt.Add(48 * time.Hour))
	if err := repo.Save(ctx, o); err != nil {
		t.Fatalf("Save (allocated): %v", err)
	}

	assertRoundTrip(t, repo, id, func(reloaded *order.Order) {
		if reloaded.Status() != order.StatusAllocated {
			t.Errorf("status = %s, want Allocated", reloaded.Status())
		}
		if d := reloaded.PromiseDate(); d == nil || !d.Equal(occurredAt.Add(48*time.Hour)) {
			t.Errorf("promiseDate = %v, want %v", d, occurredAt.Add(48*time.Hour))
		}
		if r := reloaded.Lines()[0].ReservationID(); r == nil || *r != "res-int-1" {
			t.Errorf("line 1 reservationId = %v, want res-int-1", r)
		}
	})

	if err := o.Release(1); err != nil {
		t.Fatalf("Release line 1: %v", err)
	}
	if err := o.Release(2); err != nil {
		t.Fatalf("Release line 2: %v", err)
	}
	if err := repo.Save(ctx, o); err != nil {
		t.Fatalf("Save (released): %v", err)
	}

	assertRoundTrip(t, repo, id, func(reloaded *order.Order) {
		if reloaded.Status() != order.StatusReleased {
			t.Errorf("status = %s, want Released", reloaded.Status())
		}
		for _, l := range reloaded.Lines() {
			if l.Status() != order.LineReleased {
				t.Errorf("line %d status = %s, want Released", l.LineNo(), l.Status())
			}
		}
	})
}

func TestOrderRepo_BackorderedAndCancelledRoundTrip(t *testing.T) {
	pool := newPool(t)
	repo := postgres.NewOrderRepo(pool)
	ctx := context.Background()

	id := shared.OrderId("ord-int-bo-" + time.Now().Format("150405.000000000"))
	o, err := order.New(id, mustLines(t, id), false)
	if err != nil {
		t.Fatalf("order.New: %v", err)
	}
	if err := repo.Save(ctx, o); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A Backordered line stores no reservation id; a cancelled order
	// stores the derived Cancelled status on every line.
	if err := o.MarkBackordered(2); err != nil {
		t.Fatalf("MarkBackordered: %v", err)
	}
	if err := repo.Save(ctx, o); err != nil {
		t.Fatalf("Save (backordered): %v", err)
	}

	assertRoundTrip(t, repo, id, func(reloaded *order.Order) {
		if reloaded.Status() != order.StatusBackordered {
			t.Errorf("status = %s, want Backordered (ship-complete default)", reloaded.Status())
		}
		if r := reloaded.Lines()[1].ReservationID(); r != nil {
			t.Errorf("backordered line reservationId = %v, want nil", r)
		}
	})

	if err := o.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := repo.Save(ctx, o); err != nil {
		t.Fatalf("Save (cancelled): %v", err)
	}

	assertRoundTrip(t, repo, id, func(reloaded *order.Order) {
		if reloaded.Status() != order.StatusCancelled {
			t.Errorf("status = %s, want Cancelled", reloaded.Status())
		}
	})
}

// TestOrderRepo_FindByID_Missing covers the repository's documented
// "not found is the application's concern" contract: (nil, nil), not an
// error.
func TestOrderRepo_FindByID_Missing(t *testing.T) {
	pool := newPool(t)
	repo := postgres.NewOrderRepo(pool)

	got, err := repo.FindByID(context.Background(), shared.OrderId("ord-int-does-not-exist"))
	if err != nil {
		t.Fatalf("FindByID missing: err = %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("FindByID missing = %+v, want nil", got)
	}
}

// TestOrderRepo_NextID_Unique asserts every minted id is distinct — the
// `ord-<uuid>` convention's whole guarantee.
func TestOrderRepo_NextID_Unique(t *testing.T) {
	pool := newPool(t)
	repo := postgres.NewOrderRepo(pool)

	seen := make(map[shared.OrderId]struct{}, 100)
	for i := 0; i < 100; i++ {
		id, err := repo.NextID(context.Background())
		if err != nil {
			t.Fatalf("NextID: %v", err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NextID minted duplicate id %s", id)
		}
		seen[id] = struct{}{}
	}
}

// TestEventPublisher_AppendsToEventsTable covers the Postgres EventPublisher:
// every published event lands in the events table with its name, occurrence
// time, and JSON payload.
func TestEventPublisher_AppendsToEventsTable(t *testing.T) {
	pool := newPool(t)
	pub := postgres.NewEventPublisher(pool)
	ctx := context.Background()

	occurredAt := time.Date(2026, 9, 7, 11, 30, 0, 0, time.UTC).UTC()
	event := shared.NewOrderReceived(occurredAt, shared.OrderId("ord-int-evt"), 2)
	if err := pub.Publish(ctx, event); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var name string
	var payload []byte
	err := pool.QueryRow(ctx,
		`SELECT event_name, payload FROM events
		 WHERE event_name = 'OrderReceived' AND payload::text LIKE '%ord-int-evt%'
		 LIMIT 1`,
	).Scan(&name, &payload)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if name != "OrderReceived" {
		t.Errorf("event_name = %s, want OrderReceived", name)
	}
	if len(payload) == 0 {
		t.Error("payload = empty, want the marshalled event JSON")
	}
}

// mustLines builds two valid order lines on the default path.
func mustLines(t *testing.T, id shared.OrderId) []*order.OrderLine {
	t.Helper()
	_ = id // lines carry no order reference; the id is only for context
	l1, err := order.NewOrderLine(1, shared.SKU("SKU-INT-1"), 2, shared.DefaultPathId, false)
	if err != nil {
		t.Fatalf("NewOrderLine 1: %v", err)
	}
	l2, err := order.NewOrderLine(2, shared.SKU("SKU-INT-2"), 1, shared.DefaultPathId, true)
	if err != nil {
		t.Fatalf("NewOrderLine 2: %v", err)
	}
	return []*order.OrderLine{l1, l2}
}

// assertRoundTrip reloads id through the repo and hands the rehydrated
// aggregate to assert.
func assertRoundTrip(t *testing.T, repo *postgres.OrderRepo, id shared.OrderId, assert func(*order.Order)) {
	t.Helper()
	reloaded, err := repo.FindByID(context.Background(), id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if reloaded == nil {
		t.Fatalf("FindByID %s = nil, want the saved order", id)
	}
	assert(reloaded)
}
