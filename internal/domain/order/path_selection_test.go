package order

import (
	"testing"

	"github.com/claudioed/order-management/internal/domain/shared"
)

func TestPathSelectionPolicySelect(t *testing.T) {
	tests := []struct {
		name     string
		sku      shared.SKU
		quantity int
		giftWrap bool
		want     shared.PathId
	}{
		{name: "plain line resolves to the default path", sku: "SKU-1", quantity: 1, want: shared.DefaultPathId},
		{name: "gift-wrapped line still resolves to the default path in v1", sku: "SKU-2", quantity: 1, giftWrap: true, want: shared.DefaultPathId},
		{name: "quantity has no effect on v1's single rule", sku: "SKU-3", quantity: 50, want: shared.DefaultPathId},
	}

	var policy PathSelectionPolicy
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policy.Select(tt.sku, tt.quantity, tt.giftWrap)
			if got != tt.want {
				t.Fatalf("Select(%q, %d, %v) = %q, want %q", tt.sku, tt.quantity, tt.giftWrap, got, tt.want)
			}
		})
	}
}
