package kafka_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/order-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// recordingPublisher counts the events it is asked to publish and can be made
// to fail, so the fan-out's ordering and fail-fast behaviour can be asserted.
type recordingPublisher struct {
	count int
	err   error
}

func (p *recordingPublisher) Publish(_ context.Context, _ shared.DomainEvent) error {
	if p.err != nil {
		return p.err
	}
	p.count++
	return nil
}

func TestFanOutPublisher_ForwardsToAll(t *testing.T) {
	a := &recordingPublisher{}
	b := &recordingPublisher{}
	f := kafka.NewFanOutPublisher(a, b)

	if err := f.Publish(context.Background(), shared.NewOrderReleased(time.Now(), "o1")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if a.count != 1 || b.count != 1 {
		t.Errorf("counts = %d/%d, want 1/1", a.count, b.count)
	}
}

func TestFanOutPublisher_FailFast(t *testing.T) {
	boom := errors.New("boom")
	a := &recordingPublisher{err: boom}
	b := &recordingPublisher{}
	f := kafka.NewFanOutPublisher(a, b)

	err := f.Publish(context.Background(), shared.NewOrderReleased(time.Now(), "o1"))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	// The second publisher must not run after the first fails.
	if b.count != 0 {
		t.Errorf("second publisher ran %d times, want 0", b.count)
	}
}
