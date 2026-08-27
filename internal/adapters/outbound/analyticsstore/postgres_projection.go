package analyticsstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/order-management/internal/analytics/report"
)

// PostgresProjection is the WRITER implementation of report.ProjectionStore,
// backed by a pgxpool over the analytical database. Every Apply* runs in a
// transaction that first claims the event id in analytics_processed_events
// (ON CONFLICT DO NOTHING); it only mutates the rollup when the claim is new,
// making each apply idempotent per eventId under Kafka's at-least-once
// delivery. It is the only writer of the analytical database.
type PostgresProjection struct {
	pool *pgxpool.Pool
}

// NewPostgresProjection constructs a PostgresProjection over pool.
func NewPostgresProjection(pool *pgxpool.Pool) *PostgresProjection {
	return &PostgresProjection{pool: pool}
}

// funnelColumn names the single rollup counter an Apply* method increments.
type funnelColumn string

const (
	colOrdersReceived           funnelColumn = "orders_received"
	colOrdersAllocated          funnelColumn = "orders_allocated"
	colOrdersPartiallyAllocated funnelColumn = "orders_partially_allocated"
	colOrdersAllocationFailed   funnelColumn = "orders_allocation_failed"
	colOrdersReleased           funnelColumn = "orders_released"
	colOrdersCancelled          funnelColumn = "orders_cancelled"
	colLinesAllocated           funnelColumn = "lines_allocated"
	colLinesBackordered         funnelColumn = "lines_backordered"
	colLinesReleased            funnelColumn = "lines_released"
)

// claim inserts eventId into analytics_processed_events, returning true iff
// this call newly recorded it (so the caller should apply the effect). It
// runs inside tx so the claim and the effect commit atomically.
func claim(ctx context.Context, tx pgx.Tx, eventId string, occurredAt time.Time) (bool, error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO analytics_processed_events (event_id, occurred_at)
		 VALUES ($1, $2) ON CONFLICT (event_id) DO NOTHING`,
		eventId, occurredAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// applyCount is the whole of every Apply* method: in one transaction, claim
// the eventId and — only if the claim is new — increment column on the
// (path_id, hour_bucket) row. col is a fixed value from the funnelColumn
// constants above (never model input), so interpolating it into the SQL is
// safe.
func (p *PostgresProjection) applyCount(ctx context.Context, eventId, pathId string, at time.Time, col funnelColumn) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	isNew, err := claim(ctx, tx, eventId, at)
	if err != nil {
		return fmt.Errorf("analyticsstore: claim event: %w", err)
	}
	if !isNew {
		committed = true
		return tx.Commit(ctx)
	}

	bucket := at.UTC().Truncate(time.Hour)
	// #nosec G201 -- col is a fixed funnelColumn constant, never user input.
	sql := fmt.Sprintf(
		`INSERT INTO funnel_rollup (path_id, hour_bucket, %[1]s)
		 VALUES ($1, $2, 1)
		 ON CONFLICT (path_id, hour_bucket) DO UPDATE SET
		 	%[1]s = funnel_rollup.%[1]s + 1`, col)
	if _, err := tx.Exec(ctx, sql, pathId, bucket); err != nil {
		return fmt.Errorf("analyticsstore: upsert rollup %s: %w", col, err)
	}

	committed = true
	return tx.Commit(ctx)
}

// ApplyOrderReceived counts one received order. Idempotent on eventId.
func (p *PostgresProjection) ApplyOrderReceived(ctx context.Context, eventId, pathId string, at time.Time) error {
	return p.applyCount(ctx, eventId, pathId, at, colOrdersReceived)
}

// ApplyOrderAllocated counts one fully-allocated order. Idempotent on eventId.
func (p *PostgresProjection) ApplyOrderAllocated(ctx context.Context, eventId, pathId string, at time.Time) error {
	return p.applyCount(ctx, eventId, pathId, at, colOrdersAllocated)
}

// ApplyOrderPartiallyAllocated counts one partially-allocated order.
// Idempotent on eventId.
func (p *PostgresProjection) ApplyOrderPartiallyAllocated(ctx context.Context, eventId, pathId string, at time.Time) error {
	return p.applyCount(ctx, eventId, pathId, at, colOrdersPartiallyAllocated)
}

// ApplyOrderAllocationFailed counts one hard allocation failure. Idempotent
// on eventId.
func (p *PostgresProjection) ApplyOrderAllocationFailed(ctx context.Context, eventId, pathId string, at time.Time) error {
	return p.applyCount(ctx, eventId, pathId, at, colOrdersAllocationFailed)
}

// ApplyOrderReleased counts one fully-released order. Idempotent on eventId.
func (p *PostgresProjection) ApplyOrderReleased(ctx context.Context, eventId, pathId string, at time.Time) error {
	return p.applyCount(ctx, eventId, pathId, at, colOrdersReleased)
}

// ApplyOrderCancelled counts one cancelled order. Idempotent on eventId.
func (p *PostgresProjection) ApplyOrderCancelled(ctx context.Context, eventId, pathId string, at time.Time) error {
	return p.applyCount(ctx, eventId, pathId, at, colOrdersCancelled)
}

// ApplyLineAllocated counts one allocated order line. Idempotent on eventId.
func (p *PostgresProjection) ApplyLineAllocated(ctx context.Context, eventId, pathId string, at time.Time) error {
	return p.applyCount(ctx, eventId, pathId, at, colLinesAllocated)
}

// ApplyLineBackordered counts one backordered order line. Idempotent on eventId.
func (p *PostgresProjection) ApplyLineBackordered(ctx context.Context, eventId, pathId string, at time.Time) error {
	return p.applyCount(ctx, eventId, pathId, at, colLinesBackordered)
}

// ApplyLineReleased counts one released order line. Idempotent on eventId.
func (p *PostgresProjection) ApplyLineReleased(ctx context.Context, eventId, pathId string, at time.Time) error {
	return p.applyCount(ctx, eventId, pathId, at, colLinesReleased)
}

// Compile-time assertion that PostgresProjection satisfies the write port.
var _ report.ProjectionStore = (*PostgresProjection)(nil)
