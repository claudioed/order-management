-- ADR 0014 §3 / ADR 0017: per-shipment-group promising. Additive only —
-- orders.promise_date/promise_cpt_id/promise_basis (migration 0002) are
-- untouched and keep being written exactly as before (the "latest group"
-- projection Order.SetPromiseGroups derives). This table carries the
-- FULL per-group breakdown for orders whose AllowPartialShipment=true
-- lines were grouped by cutoff; a ship-complete order still gets exactly
-- one row here (group_no=1) mirroring its single legacy promise.
--
-- A normalized order_promise_group_lines join table (order_id, group_no,
-- line_no) was considered instead of the INT[] line_nos column below, to
-- mirror order_lines' own one-row-per-line shape more closely. line_nos
-- as a native Postgres array was chosen instead because a promise group
-- is read and rewritten as one atomic unit on every save (see
-- OrderRepo.Save: delete-then-reinsert the whole set), never queried or
-- joined line-by-line the way order_lines is — a join table would add a
-- second table and a second delete-then-reinsert loop for no query this
-- service actually runs.
CREATE TABLE order_promise_groups (
    order_id  TEXT NOT NULL REFERENCES orders(id),
    group_no  INT NOT NULL,
    line_nos  INT[] NOT NULL,
    cpt_id    TEXT,
    cutoff_at TIMESTAMPTZ NOT NULL,
    basis     TEXT NOT NULL,
    PRIMARY KEY (order_id, group_no)
);
