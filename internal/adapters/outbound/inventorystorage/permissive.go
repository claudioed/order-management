// Package inventorystorage provides outbound InventoryReservationClient
// implementations: an HTTP client against inventory-storage's published
// reservation contract, and a permissive no-op selected by
// INVENTORY_STORAGE_MODE (default "permissive") so unit tests and CI never
// reach the network.
//
// This follows the env-selected adapter pattern already used three times
// in this fleet (inventory-storage -> facility-layout, wes-work-planning ->
// inventory-storage, fulfillment-execution -> inventory-storage), with one
// deliberate difference: those permissive adapters fail OPEN because a
// classification lookup is a soft, optional enrichment. Reserving real
// stock is not. A no-op that returned a fake reservation id would let
// AllocateOrder report success for stock nobody reserved, so this one
// fails loudly instead.
package inventorystorage

import (
	"context"

	"github.com/claudioed/order-management/internal/application/ports"
)

// PermissiveClient is the default InventoryReservationClient: it never
// contacts inventory-storage and never fabricates a reservation. Every
// operation returns ports.ErrDownstreamNotConfigured, which AllocateOrder
// and CancelOrder surface unchanged — it is not a 409, so it is never
// mistaken for a backorder.
type PermissiveClient struct{}

// NewPermissiveClient constructs a PermissiveClient.
func NewPermissiveClient() *PermissiveClient { return &PermissiveClient{} }

func (PermissiveClient) Reserve(_ context.Context, _ ports.ReservationRequest) (ports.ReservationResult, error) {
	return ports.ReservationResult{}, ports.ErrDownstreamNotConfigured
}

func (PermissiveClient) RevokeReservation(_ context.Context, _ string) error {
	return ports.ErrDownstreamNotConfigured
}
