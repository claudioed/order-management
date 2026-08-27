package report

import (
	"context"
	"time"
)

// ReportStore is the read side of the funnel data product: the reader
// process queries it to serve reports. It is read-only by contract — the
// Postgres implementation runs over a pool pinned to a read-only role.
type ReportStore interface {
	// Query returns the funnel rows matching q.
	Query(ctx context.Context, q ReportQuery) (FunnelReport, error)
	// FreshnessLag reports how far the read model lags real time: the age
	// of the most recently applied event. A larger lag means the projection
	// is further behind the event stream.
	FreshnessLag(ctx context.Context) (time.Duration, error)
}

// ProjectionStore is the write side of the funnel data product: the
// projector process applies each consumed event to it. Every Apply* method
// is idempotent on eventId — applying the same eventId twice records the
// effect once, so the at-least-once Kafka stream can be projected exactly
// once.
//
// The methods take the derivation-relevant fields already extracted from the
// analytics envelope (rather than a domain event) so this port stays free of
// any OLTP domain dependency. Every method is keyed by pathId — the report's
// single dimension — plus the event's occurrence time, which the store
// truncates to the hour bucket.
type ProjectionStore interface {
	// ApplyOrderReceived counts one received order onto (pathId, hour).
	ApplyOrderReceived(ctx context.Context, eventId, pathId string, at time.Time) error
	// ApplyOrderAllocated counts one fully-allocated order.
	ApplyOrderAllocated(ctx context.Context, eventId, pathId string, at time.Time) error
	// ApplyOrderPartiallyAllocated counts one partially-allocated order.
	ApplyOrderPartiallyAllocated(ctx context.Context, eventId, pathId string, at time.Time) error
	// ApplyOrderAllocationFailed counts one hard allocation failure.
	ApplyOrderAllocationFailed(ctx context.Context, eventId, pathId string, at time.Time) error
	// ApplyOrderReleased counts one fully-released order.
	ApplyOrderReleased(ctx context.Context, eventId, pathId string, at time.Time) error
	// ApplyOrderCancelled counts one cancelled order (funnel leakage).
	ApplyOrderCancelled(ctx context.Context, eventId, pathId string, at time.Time) error

	// ApplyLineAllocated counts one allocated order line.
	ApplyLineAllocated(ctx context.Context, eventId, pathId string, at time.Time) error
	// ApplyLineBackordered counts one backordered order line (leakage).
	ApplyLineBackordered(ctx context.Context, eventId, pathId string, at time.Time) error
	// ApplyLineReleased counts one released order line.
	ApplyLineReleased(ctx context.Context, eventId, pathId string, at time.Time) error
}
