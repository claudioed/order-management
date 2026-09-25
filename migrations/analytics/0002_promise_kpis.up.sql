-- Promise KPIs on the Order Funnel & Allocation Health data product
-- (ADR 0014 §6 / ADR 0019). Additive only: new columns on the existing
-- funnel_rollup table, at the SAME (path_id, hour_bucket) dimensional grain
-- as the pre-existing funnel counters, plus a new sibling table for the
-- re-promise-rate counter (which is deliberately NOT path_id-dimensioned —
-- see ADR 0019 for the design choice).

ALTER TABLE funnel_rollup
    ADD COLUMN promise_basis_capability   BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN promise_basis_lead_time    BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN orders_split_shipment      BIGINT NOT NULL DEFAULT 0,
    -- Sum + count columns for promise-to-cutoff-gap's MEAN, divided
    -- client-side in the reader (the same "sum and count, divide in the
    -- reader" pattern fulfillment-execution's own throughput_rollup uses
    -- for AvgClaimToCompleteSeconds). Only a Capability-basis promise with
    -- a real cutoffAt contributes a sample.
    ADD COLUMN promise_to_cutoff_gap_seconds_sum   DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN promise_to_cutoff_gap_samples       BIGINT NOT NULL DEFAULT 0;

-- The re-promise-rate counter: OrderRepromised (ADR 0018) carries no
-- process path, and this data product deliberately does not add an
-- OrderRepo lookup to resolve one for it (see ADR 0019). Rather than force
-- it onto funnel_rollup with an empty-string path_id sentinel colliding
-- with that table's PRIMARY KEY (path_id, hour_bucket) semantics, it gets
-- its own small rollup at (hour_bucket) grain only.
CREATE TABLE repromise_rollup (
    hour_bucket        TIMESTAMPTZ NOT NULL,
    orders_repromised  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (hour_bucket)
);

CREATE INDEX idx_repromise_rollup_hour_bucket
    ON repromise_rollup (hour_bucket);
