package productclassification

import (
	"context"

	"github.com/claudioed/order-management/internal/application/ports"
)

// PermissiveLookup is the default ports.ProductClassificationLookup: it
// never contacts inventory-storage and always reports Known=false, which
// order.PathSelectionPolicy's caller (ReceiveOrder) treats as "no derived
// product attributes available" -- the line is evaluated with whatever
// attributes it already carries (gift wrap only), same fail-open
// convention as wes-work-planning's own PermissiveLookup. Selected via
// PRODUCT_CLASSIFICATION_MODE (default "permissive"), so existing tests,
// CI and deployments that do not set the env var see identical behaviour
// to before this feature existed.
type PermissiveLookup struct{}

// NewPermissiveLookup constructs a PermissiveLookup.
func NewPermissiveLookup() *PermissiveLookup {
	return &PermissiveLookup{}
}

func (PermissiveLookup) GetClassification(_ context.Context, sku string) (ports.ProductClassification, error) {
	return ports.ProductClassification{SKU: sku, Known: false}, nil
}
