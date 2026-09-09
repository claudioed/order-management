package mcp

import (
	"context"
	"testing"

	"github.com/claudioed/order-management/internal/adapters/outbound/memory"
	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// harness wires the read use case the MCP adapter needs over an in-memory
// repo, seeding orders directly via the domain's own Rehydrate
// constructor (mirroring internal/domain/order's own test suite) rather
// than through ReceiveOrder -- ReceiveOrder needs a real
// InventoryReservationClient and LeadTimePolicy to exercise allocation,
// which is out of scope for this adapter's own tests: what matters here
// is that get_order correctly reads back whatever the repo holds, not
// how an order comes to hold a given state.
type harness struct {
	t *testing.T

	orders *memory.OrderRepo

	deps Deps
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	orders := memory.NewOrderRepo()

	h := &harness{
		t:      t,
		orders: orders,
	}
	h.deps = Deps{
		GetOrder: &usecases.GetOrder{Orders: orders},
	}
	return h
}

func (h *harness) ctx() context.Context { return context.Background() }

// mustSaveOrder rehydrates and saves an Order directly into the repo.
func (h *harness) mustSaveOrder(id string, lines []*order.OrderLine, allowPartialShipment bool) {
	h.t.Helper()
	orderID, err := shared.NewOrderId(id)
	if err != nil {
		h.t.Fatalf("order id %q: %v", id, err)
	}
	o := order.Rehydrate(orderID, lines, allowPartialShipment, nil)
	if err := h.orders.Save(h.ctx(), o); err != nil {
		h.t.Fatalf("saving order %q: %v", id, err)
	}
}

func TestGetOrder(t *testing.T) {
	tests := []struct {
		name    string
		orderID string
		wantErr bool
		assert  func(t *testing.T, out orderDTO)
	}{
		{
			name:    "empty orderId rejected",
			orderID: "",
			wantErr: true,
		},
		{
			name:    "unknown order rejected",
			orderID: "ORD-NOPE",
			wantErr: true,
		},
		{
			name:    "order with an allocated and a backordered line",
			orderID: "ORD-1",
			assert: func(t *testing.T, out orderDTO) {
				if out.ID != "ORD-1" {
					t.Fatalf("unexpected order id %q", out.ID)
				}
				if out.Status != "PartiallyAllocated" {
					t.Fatalf("unexpected status %q", out.Status)
				}
				if len(out.Lines) != 2 {
					t.Fatalf("expected 2 lines, got %d", len(out.Lines))
				}
				allocated := out.Lines[0]
				if allocated.Status != "Allocated" || allocated.ReservationID == nil || *allocated.ReservationID != "RES-1" {
					t.Fatalf("unexpected allocated line %+v", allocated)
				}
				backordered := out.Lines[1]
				if backordered.Status != "Backordered" || backordered.ReservationID != nil {
					t.Fatalf("unexpected backordered line %+v", backordered)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			resID := "RES-1"
			h.mustSaveOrder("ORD-1", []*order.OrderLine{
				order.RehydrateOrderLine(1, "SKU-1", 2, "pick", false, order.LineAllocated, &resID),
				order.RehydrateOrderLine(2, "SKU-2", 1, "pick", true, order.LineBackordered, nil),
			}, true)

			out, err := h.deps.getOrder(h.ctx(), getOrderInput{OrderId: tc.orderID})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.assert(t, out)
		})
	}
}
