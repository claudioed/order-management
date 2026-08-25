CREATE TABLE orders (
    id                     TEXT PRIMARY KEY,
    allow_partial_shipment BOOLEAN NOT NULL,
    promise_date           TIMESTAMPTZ
);

-- Order-level status is deliberately NOT a column: it is derived from the
-- line statuses by the Order aggregate, so there is nothing here that can
-- drift out of sync with the lines it would summarise.
CREATE TABLE order_lines (
    order_id       TEXT    NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    line_no        INTEGER NOT NULL CHECK (line_no > 0),
    sku            TEXT    NOT NULL,
    quantity       INTEGER NOT NULL CHECK (quantity > 0),
    path_id        TEXT    NOT NULL,
    gift_wrap      BOOLEAN NOT NULL DEFAULT FALSE,
    line_status    TEXT    NOT NULL,
    -- inventory-storage's reservation id. A reference only: this context
    -- models no local Reservation aggregate, because inventory-storage
    -- remains the sole owner of reservation state.
    reservation_id TEXT,
    PRIMARY KEY (order_id, line_no)
);

CREATE INDEX idx_order_lines_sku ON order_lines (sku);
CREATE INDEX idx_order_lines_status ON order_lines (line_status);

CREATE TABLE events (
    id          BIGSERIAL PRIMARY KEY,
    event_name  TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    payload     JSONB       NOT NULL
);
