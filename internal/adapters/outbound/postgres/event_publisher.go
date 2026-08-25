package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/order-management/internal/domain/shared"
)

// EventPublisher is a pgxpool-backed ports.EventPublisher that appends
// each domain event to the `events` table. It is the Postgres-backed
// counterpart to outbound/events.LogPublisher, and the natural read side
// for a future outbox-style Kafka relay (deferred in v1).
type EventPublisher struct {
	pool *pgxpool.Pool
}

// NewEventPublisher constructs an EventPublisher over pool.
func NewEventPublisher(pool *pgxpool.Pool) *EventPublisher {
	return &EventPublisher{pool: pool}
}

func (p *EventPublisher) Publish(ctx context.Context, event shared.DomainEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `
		INSERT INTO events (event_name, occurred_at, payload) VALUES ($1, $2, $3)
	`, event.EventName(), event.OccurredAt(), payload)
	return err
}
