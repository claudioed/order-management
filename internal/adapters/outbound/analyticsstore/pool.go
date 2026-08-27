package analyticsstore

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool builds a pgxpool over the analytical database at databaseURL,
// mirroring the OLTP postgres.NewPool. It is used by the writer
// (cmd/order-projector).
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

// NewReadOnlyPool builds a pgxpool over the analytical database in which
// every connection is pinned to a read-only transaction default
// (default_transaction_read_only=on). The reader process
// (cmd/order-reports) uses this so a bug there cannot mutate the read model
// even if the database role itself is not read-only — defence in depth on top
// of the read-only ANALYTICS_DATABASE_URL role (ADR-0006).
func NewReadOnlyPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	return pgxpool.NewWithConfig(ctx, cfg)
}
