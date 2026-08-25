// Package weswork provides outbound WorkReleaseClient implementations: an
// HTTP client against wes-work-planning's published work-unit contract,
// and a permissive no-op selected by WES_WORK_PLANNING_MODE (default
// "permissive") so unit tests and CI never reach the network.
//
// As with the inventory-storage adapter, "permissive" here means "not
// configured", NOT "fail open": releasing real work must never appear to
// succeed against a no-op, so the permissive client fails loudly rather
// than fabricating a work unit id.
package weswork

import (
	"context"

	"github.com/claudioed/order-management/internal/application/ports"
)

// PermissiveClient is the default WorkReleaseClient: it never contacts
// wes-work-planning and never fabricates a work unit. EnqueueWorkUnit
// returns ports.ErrDownstreamNotConfigured, which ReleaseOrder surfaces
// unchanged.
type PermissiveClient struct{}

// NewPermissiveClient constructs a PermissiveClient.
func NewPermissiveClient() *PermissiveClient { return &PermissiveClient{} }

func (PermissiveClient) EnqueueWorkUnit(_ context.Context, _ ports.WorkUnitRequest) (ports.WorkUnitResult, error) {
	return ports.WorkUnitResult{}, ports.ErrDownstreamNotConfigured
}
