DROP TABLE IF EXISTS repromise_rollup;

ALTER TABLE funnel_rollup
    DROP COLUMN IF EXISTS promise_basis_capability,
    DROP COLUMN IF EXISTS promise_basis_lead_time,
    DROP COLUMN IF EXISTS orders_split_shipment,
    DROP COLUMN IF EXISTS promise_to_cutoff_gap_seconds_sum,
    DROP COLUMN IF EXISTS promise_to_cutoff_gap_samples;
