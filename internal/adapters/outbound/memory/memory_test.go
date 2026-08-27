package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/order-management/internal/adapters/outbound/memory"
	"github.com/claudioed/order-management/internal/domain/order"
)

func TestOrderRepoRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewOrderRepo()

	id, err := repo.NextID(ctx)
	if err != nil {
		t.Fatalf("NextID: %v", err)
	}
	if id == "" {
		t.Fatal("NextID returned an empty id")
	}

	second, err := repo.NextID(ctx)
	if err != nil {
		t.Fatalf("NextID: %v", err)
	}
	if second == id {
		t.Fatalf("NextID returned %q twice", id)
	}

	line, err := order.NewOrderLine(1, "SKU-1", 2, "pick", false)
	if err != nil {
		t.Fatalf("NewOrderLine: %v", err)
	}
	o, err := order.New(id, []*order.OrderLine{line}, false)
	if err != nil {
		t.Fatalf("order.New: %v", err)
	}
	if err := repo.Save(ctx, o); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := repo.FindByID(ctx, id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got == nil || got.ID() != id {
		t.Fatalf("FindByID = %v, want the saved order", got)
	}
}

func TestOrderRepoFindByIDOnAnUnknownIDIsNotAnError(t *testing.T) {
	got, err := memory.NewOrderRepo().FindByID(context.Background(), "ord-missing")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got != nil {
		t.Fatalf("FindByID = %v, want nil — 'not found' is the application's concern", got)
	}
}

func TestFixedClock(t *testing.T) {
	start := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	clock := memory.NewFixedClock(start)

	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
	clock.Advance(90 * time.Minute)
	if got, want := clock.Now(), start.Add(90*time.Minute); !got.Equal(want) {
		t.Fatalf("after Advance, Now() = %v, want %v", got, want)
	}
}

func TestSystemClockMovesForward(t *testing.T) {
	clock := memory.SystemClock{}
	if clock.Now().IsZero() {
		t.Fatal("SystemClock returned the zero time")
	}
}
