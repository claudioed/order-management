package memory

import (
	"context"
	"sync"
)

// RepromiseProcessedEventsRepo is an in-memory implementation of
// ports.RepromiseProcessedEvents, used for tests and `go run ./cmd/order`
// with no DATABASE_URL set.
type RepromiseProcessedEventsRepo struct {
	mu        sync.Mutex
	processed map[string]bool
}

// NewRepromiseProcessedEventsRepo constructs an empty
// RepromiseProcessedEventsRepo.
func NewRepromiseProcessedEventsRepo() *RepromiseProcessedEventsRepo {
	return &RepromiseProcessedEventsRepo{processed: make(map[string]bool)}
}

// MarkProcessed records eventId if absent, returning true iff this call
// newly recorded it.
func (r *RepromiseProcessedEventsRepo) MarkProcessed(_ context.Context, eventId string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.processed[eventId] {
		return false, nil
	}
	r.processed[eventId] = true
	return true, nil
}
