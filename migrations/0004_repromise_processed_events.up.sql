-- ADR 0018 (ADR 0014 §5): RepromiseOrder's own OLTP-side idempotency
-- gate, keyed on the Kafka message's event_id alone (see
-- ports.RepromiseProcessedEvents' doc comment for why event_id-only is
-- the deliberate, documented simplification of ADR 0014's stated
-- "(orderId, sourceEventId)" key). Shape mirrors labor-performance's own
-- processed_events table (its migrations/0001_init.up.sql) — the proven
-- precedent for an OLTP-side Kafka-consumer idempotency table in this
-- fleet.
--
-- Named repromise_processed_events, not processed_events, so a future
-- OLTP-side consumer added to this service never collides with this
-- one's table by accident.
CREATE TABLE repromise_processed_events (
    event_id     TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
