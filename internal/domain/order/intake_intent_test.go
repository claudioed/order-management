package order_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/order-management/internal/domain/order"
)

func TestValidateIntakeIntent(t *testing.T) {
	cases := []struct {
		name                string
		allowPartial        bool
		releaseOnAllocation bool
		wantErr             error
	}{
		{"ordinary ship-complete order", false, true, nil},
		{"ordinary partial-shipment order", true, true, nil},
		{"held ship-complete order", false, false, nil},
		{"held partial-shipment order is contradictory", true, false, order.ErrHeldOrderMustBeShipComplete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := order.ValidateIntakeIntent(tc.allowPartial, tc.releaseOnAllocation)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestOrder_ReleaseOnAllocation_DefaultsTrue(t *testing.T) {
	// The additive guarantee, asserted on the constructor every existing
	// caller uses: an order built without any mention of the hold must
	// release on allocation exactly as before ADR 0020.
	line, err := order.NewOrderLine(1, "SKU-1", 1, "pick", false)
	if err != nil {
		t.Fatalf("NewOrderLine: %v", err)
	}
	o, err := order.New("ord-1", []*order.OrderLine{line}, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !o.ReleaseOnAllocation() {
		t.Fatal("a newly-constructed order must release on allocation")
	}

	o.Hold()
	if o.ReleaseOnAllocation() {
		t.Fatal("after Hold() the order must report ReleaseOnAllocation()=false")
	}
}

func TestOrder_RequiredShipBy_AbsentByDefault(t *testing.T) {
	line, err := order.NewOrderLine(1, "SKU-1", 1, "pick", false)
	if err != nil {
		t.Fatalf("NewOrderLine: %v", err)
	}
	o, err := order.New("ord-1", []*order.OrderLine{line}, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Absent, not a zero time.Time: "no deadline" must stay
	// distinguishable from "a deadline at the zero instant", which would
	// make every ordinary order look infeasible.
	if o.RequiredShipBy() != nil {
		t.Fatal("a newly-constructed order must carry no deadline")
	}

	deadline := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	o.SetRequiredShipBy(deadline)
	if got := o.RequiredShipBy(); got == nil || !got.Equal(deadline) {
		t.Fatalf("requiredShipBy = %v, want %v", got, deadline)
	}
}
