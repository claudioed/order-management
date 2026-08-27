package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// OrderRepo is a pgxpool-backed implementation of ports.OrderRepo. The
// Order aggregate is stored across two tables (orders + order_lines) and
// always written in a single transaction, because a line's status and its
// order are one unit of consistency.
type OrderRepo struct {
	pool *pgxpool.Pool
}

// NewOrderRepo constructs an OrderRepo over pool.
func NewOrderRepo(pool *pgxpool.Pool) *OrderRepo {
	return &OrderRepo{pool: pool}
}

func (r *OrderRepo) Save(ctx context.Context, o *order.Order) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var promiseDate *time.Time
	if d := o.PromiseDate(); d != nil {
		promiseDate = d
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO orders (id, allow_partial_shipment, promise_date)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET promise_date = EXCLUDED.promise_date
	`, o.ID().String(), o.AllowPartialShipment(), promiseDate); err != nil {
		return err
	}

	for _, l := range o.Lines() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines (order_id, line_no, sku, quantity, path_id, gift_wrap, line_status, reservation_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (order_id, line_no) DO UPDATE
			  SET line_status = EXCLUDED.line_status,
			      reservation_id = EXCLUDED.reservation_id
		`, o.ID().String(), l.LineNo(), l.SKU().String(), l.Quantity(), l.PathID().String(), l.GiftWrap(), string(l.Status()), l.ReservationID()); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// FindByID returns (nil, nil) when no order has this id — "not found" is
// the application's concern, not the repository's.
func (r *OrderRepo) FindByID(ctx context.Context, id shared.OrderId) (*order.Order, error) {
	var allowPartialShipment bool
	var promiseDate *time.Time

	err := r.pool.QueryRow(ctx, `
		SELECT allow_partial_shipment, promise_date FROM orders WHERE id = $1
	`, id.String()).Scan(&allowPartialShipment, &promiseDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT line_no, sku, quantity, path_id, gift_wrap, line_status, reservation_id
		FROM order_lines WHERE order_id = $1 ORDER BY line_no
	`, id.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lines []*order.OrderLine
	for rows.Next() {
		var (
			lineNo        int
			sku           string
			quantity      int
			pathID        string
			giftWrap      bool
			lineStatus    string
			reservationID *string
		)
		if err := rows.Scan(&lineNo, &sku, &quantity, &pathID, &giftWrap, &lineStatus, &reservationID); err != nil {
			return nil, err
		}
		lines = append(lines, order.RehydrateOrderLine(
			lineNo, shared.SKU(sku), quantity, shared.PathId(pathID), giftWrap,
			order.LineStatus(lineStatus), reservationID,
		))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return order.Rehydrate(id, lines, allowPartialShipment, promiseDate), nil
}

// NextID mints an order id. The `ord-<uuid>` shape mirrors the
// `res-<uuid>` / `wu-<uuid>` conventions used elsewhere in the fleet.
func (r *OrderRepo) NextID(_ context.Context) (shared.OrderId, error) {
	return shared.OrderId("ord-" + uuid.NewString()), nil
}
