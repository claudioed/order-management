package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

func TestReceiveOrder(t *testing.T) {
	t.Run("creates an order in Received status and publishes OrderReceived", func(t *testing.T) {
		f := newFixture()

		o, err := f.receiveOrder().Execute(context.Background(), []usecases.NewLine{
			line("SKU-1", 2, "pick"),
			{SKU: "SKU-2", Quantity: 1, PathID: "singles", GiftWrap: true},
		}, false)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}

		if o.Status() != order.StatusReceived {
			t.Fatalf("Status() = %q, want %q", o.Status(), order.StatusReceived)
		}
		if o.AllowPartialShipment() {
			t.Fatal("AllowPartialShipment must default to false (ship-complete, BR3)")
		}
		assertLineStatuses(t, o, order.LinePending, order.LinePending)
		assertEventNames(t, f.events, "OrderReceived")

		if !o.Lines()[1].GiftWrap() {
			t.Fatal("gift wrap was lost between the request and the aggregate")
		}
		if o.Lines()[0].PathID() != "pick" || o.Lines()[1].PathID() != "singles" {
			t.Fatalf("path ids were lost: %q, %q", o.Lines()[0].PathID(), o.Lines()[1].PathID())
		}

		// The order must be readable straight back out of the repo.
		stored, err := f.getOrder().Execute(context.Background(), o.ID())
		if err != nil {
			t.Fatalf("GetOrder: %v", err)
		}
		if stored.ID() != o.ID() {
			t.Fatalf("stored id = %q, want %q", stored.ID(), o.ID())
		}
	})

	t.Run("nothing is reserved or released at intake", func(t *testing.T) {
		f := newFixture()
		f.mustReceive(t, false, line("SKU-1", 1, "pick"))

		if len(f.inventory.reserveCalls) != 0 {
			t.Fatalf("intake called inventory-storage %d times, want 0", len(f.inventory.reserveCalls))
		}
		if len(f.work.calls) != 0 {
			t.Fatalf("intake called wes-work-planning %d times, want 0", len(f.work.calls))
		}
	})

	t.Run("rejects invalid lines before touching the repository", func(t *testing.T) {
		tests := []struct {
			name    string
			lines   []usecases.NewLine
			wantErr error
		}{
			{
				name:    "empty sku",
				lines:   []usecases.NewLine{{SKU: "", Quantity: 1, PathID: "pick"}},
				wantErr: shared.ErrEmptySKU,
			},
			{
				name:    "zero quantity",
				lines:   []usecases.NewLine{{SKU: "SKU-1", Quantity: 0, PathID: "pick"}},
				wantErr: shared.ErrNonPositiveQuantity,
			},
			{
				name:    "negative quantity on a later line",
				lines:   []usecases.NewLine{line("SKU-1", 1, "pick"), {SKU: "SKU-2", Quantity: -1}},
				wantErr: shared.ErrNonPositiveQuantity,
			},
			{
				name:    "no lines at all",
				lines:   nil,
				wantErr: order.ErrNoLines,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f := newFixture()
				_, err := f.receiveOrder().Execute(context.Background(), tt.lines, false)
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				assertEventNames(t, f.events)
			})
		}
	})

	t.Run("propagates repository failures", func(t *testing.T) {
		tests := []struct {
			name string
			repo *failingRepo
		}{
			{name: "NextID fails", repo: &failingRepo{nextIDErr: errBoom}},
			{name: "Save fails", repo: &failingRepo{saveErr: errBoom}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f := newFixture()
				tt.repo.inner = f.orders
				f.orders = tt.repo

				_, err := f.receiveOrder().Execute(context.Background(), []usecases.NewLine{line("SKU-1", 1, "pick")}, false)
				if !errors.Is(err, errBoom) {
					t.Fatalf("err = %v, want %v", err, errBoom)
				}
			})
		}
	})

	t.Run("propagates publisher failures", func(t *testing.T) {
		f := newFixture()
		f.events.err = errBoom

		_, err := f.receiveOrder().Execute(context.Background(), []usecases.NewLine{line("SKU-1", 1, "pick")}, false)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})
}

func TestGetOrder(t *testing.T) {
	t.Run("returns the stored order", func(t *testing.T) {
		f := newFixture()
		o := f.mustReceive(t, true, line("SKU-1", 1, "pick"))

		got, err := f.getOrder().Execute(context.Background(), o.ID())
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if got.ID() != o.ID() || !got.AllowPartialShipment() {
			t.Fatalf("got %+v, want the received order", got)
		}
	})

	t.Run("an unknown id is ErrOrderNotFound", func(t *testing.T) {
		f := newFixture()
		_, err := f.getOrder().Execute(context.Background(), "ord-missing")
		if !errors.Is(err, usecases.ErrOrderNotFound) {
			t.Fatalf("err = %v, want %v", err, usecases.ErrOrderNotFound)
		}
	})

	t.Run("propagates repository failures", func(t *testing.T) {
		f := newFixture()
		f.orders = &failingRepo{inner: f.orders, findErr: errBoom}

		_, err := f.getOrder().Execute(context.Background(), "ord-1")
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})
}
