-- ADR 0020 §1: an order may be received with releaseOnAllocation=false,
-- meaning "allocate but do not release until told". The hold intent must
-- be persisted rather than derived from line statuses: a held order whose
-- lines are Allocated-but-not-Released is indistinguishable from an
-- ordinary ship-complete order awaiting a backordered line, so a later
-- RetryAllocation would otherwise release work onto the floor for an
-- order no one has committed to.
--
-- DEFAULT TRUE is what makes this additive: every existing row, and every
-- caller that never sends the field, keeps today's receive-allocate-release
-- behaviour byte for byte.
ALTER TABLE orders
    ADD COLUMN release_on_allocation BOOLEAN NOT NULL DEFAULT TRUE;
