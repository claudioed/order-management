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

	// ApplyOrderAllocated counts one fully-allocated order AND its promise
	// KPI facts (ADR 0014 §6 / ADR 0019), in the SAME idempotent apply.
	//
	// DESIGN DECISION: basis/cutoffAt/splitShipment are added as new
	// parameters on this EXISTING method rather than exposed through a
	// separate ApplyPromise call keyed by the same eventId. Every Apply*
	// method here claims eventId exactly once (via the
	// analytics_processed_events ON CONFLICT DO NOTHING table) before
	// applying its effect; a second Apply* call for the SAME eventId would
	// find it already claimed and silently skip its own effect. Since the
	// promise facts are read off the exact same OrderAllocated event as the
	// funnel counter, they MUST be recorded in the same claim+update
	// transaction, not a second one. basis is the wire value of
	// order.PromiseBasis ("Capability"/"LeadTime", or "" for an event
	// published before ADR 0014). cutoffAt is the promise's CPT cutoff
	// instant, non-nil only when known. splitShipment is true when the
	// order had more than one order.PromiseGroup at allocation time (ADR
	// 0014 §3 / ADR 0017).
	ApplyOrderAllocated(ctx context.Context, eventId, pathId string, at time.Time, basis string, cutoffAt *time.Time, splitShipment bool) error

	// ApplyOrderPartiallyAllocated counts one partially-allocated order AND
	// its promise KPI facts — see ApplyOrderAllocated's doc comment for why
	// these parameters were added here rather than to a separate method.
	ApplyOrderPartiallyAllocated(ctx context.Context, eventId, pathId string, at time.Time, basis string, cutoffAt *time.Time, splitShipment bool) error

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

	// --- promise KPIs (ADR 0014 §6 / ADR 0019) -----------------------------

	// ApplyOrderRepromised counts one OrderRepromised event (ADR 0018).
	// Deliberately NOT pathId-dimensioned — see Row.OrdersRepromised's doc
	// comment for the design choice and why. Every call lands on the
	// (""-path, hour) row, with its own independent eventId claim (a
	// different Kafka message from any OrderAllocated event, so there is
	// no double-claim risk with ApplyOrderAllocated above).
	ApplyOrderRepromised(ctx context.Context, eventId string, at time.Time) error
}
