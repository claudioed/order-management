// Package report holds the order-management "Order Funnel & Allocation
// Health" read model: the shapes of the analytical report the data product
// serves, the query that selects it, and the outbound ports the writer and
// reader adapters implement. It is a read-model region that depends on
// nothing else in this module — the OLTP domain and application layers must
// not import it, and it must not import them (ADR-0006).
package report

import "time"

// Granularity is the time-bucket resolution a report is rolled up to. Only
// hourly buckets are modelled for this round.
type Granularity string

const (
	// GranularityHour rolls rows up into UTC hour buckets.
	GranularityHour Granularity = "hour"
)

// RowKey identifies a single funnel row: the wes-work-planning process path
// (PathId) the order/lines are bound to, and the UTC hour bucket the row
// aggregates. HourBucket is the bucket start, truncated to the hour in UTC.
type RowKey struct {
	PathId     string
	HourBucket time.Time
}

// Row is one aggregated funnel row for a (pathId, hourBucket) key. It
// captures the order-level funnel — received → allocated → released — with
// cancellations and backorders as leakage, plus the line-level allocation
// counters. Every field is a simple per-bucket count.
type Row struct {
	Key RowKey

	// --- order-level funnel counters -------------------------------------

	// OrdersReceived is the number of OrderReceived events in this bucket.
	OrdersReceived int
	// OrdersAllocated is the number of OrderAllocated events (every line
	// allocated) in this bucket.
	OrdersAllocated int
	// OrdersPartiallyAllocated is the number of OrderPartiallyAllocated
	// events (some lines allocated, some backordered) in this bucket.
	OrdersPartiallyAllocated int
	// OrdersAllocationFailed is the number of OrderAllocationPartiallyFailed
	// events (a hard, non-business allocation failure) in this bucket.
	OrdersAllocationFailed int
	// OrdersReleased is the number of OrderReleased events (every line
	// released as work) in this bucket.
	OrdersReleased int
	// OrdersCancelled is the number of OrderCancelled events in this bucket —
	// funnel leakage before release.
	OrdersCancelled int

	// --- line-level counters ---------------------------------------------

	// LinesAllocated is the number of OrderLineAllocated events in this
	// bucket.
	LinesAllocated int
	// LinesBackordered is the number of OrderLineBackordered events in this
	// bucket — line-level leakage.
	LinesBackordered int
	// LinesReleased is the number of OrderLineReleased events in this bucket.
	LinesReleased int
}

// FunnelReport is the full result of a report query: the matching rows.
type FunnelReport struct {
	Rows []Row
}

// ReportQuery selects and filters the rows a report covers. From is
// inclusive and To is exclusive, both compared against a row's HourBucket.
// PathId is an optional exact-match filter (empty means "no filter on this
// dimension").
type ReportQuery struct {
	From        time.Time
	To          time.Time
	PathId      string
	Granularity Granularity
}
