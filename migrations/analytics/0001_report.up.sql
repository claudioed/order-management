-- Order-management "Order Funnel & Allocation Health" analytics read model
-- (ADR-0006).
--
-- This is the ANALYTICAL database, separate from the OLTP database. It is
-- written only by cmd/order-projector and read (read-only) by
-- cmd/order-reports. The tables here are projections derived from the
-- analytics event stream, not sources of truth.

-- Idempotency + freshness: every applied analytics event id is recorded
-- here exactly once. applied_at is wall-clock insert time; occurred_at is
-- the event's business time, used to compute the projection's freshness lag.
CREATE TABLE analytics_processed_events (
    event_id    TEXT PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_analytics_processed_events_occurred_at
    ON analytics_processed_events (occurred_at DESC);

-- Consumer-level dedupe set, used by the inbound consumer's ProcessedEvents
-- gate. It is kept SEPARATE from analytics_processed_events (which the
-- projection UPSERT claims) so the two idempotency layers do not race to
-- claim the same event_id: the consumer gate admits the event, the projection
-- then records its effect.
CREATE TABLE analytics_consumed_events (
    event_id     TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The funnel rollup fact table: one row per (path_id, hour_bucket). Each
-- counter is UPSERTed (+1) as the matching analytics event arrives. The
-- funnel is orders_received -> orders_allocated -> orders_released, with
-- cancellations and backorders as leakage; the line-level counters track the
-- allocation lifecycle at OrderLine granularity.
CREATE TABLE funnel_rollup (
    path_id                    TEXT NOT NULL,
    hour_bucket                TIMESTAMPTZ NOT NULL,
    orders_received            BIGINT NOT NULL DEFAULT 0,
    orders_allocated           BIGINT NOT NULL DEFAULT 0,
    orders_partially_allocated BIGINT NOT NULL DEFAULT 0,
    orders_allocation_failed   BIGINT NOT NULL DEFAULT 0,
    orders_released            BIGINT NOT NULL DEFAULT 0,
    orders_cancelled           BIGINT NOT NULL DEFAULT 0,
    lines_allocated            BIGINT NOT NULL DEFAULT 0,
    lines_backordered          BIGINT NOT NULL DEFAULT 0,
    lines_released             BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (path_id, hour_bucket)
);

CREATE INDEX idx_funnel_rollup_hour_bucket
    ON funnel_rollup (hour_bucket);
