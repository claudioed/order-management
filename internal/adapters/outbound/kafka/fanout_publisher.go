package kafka

import (
	"context"

	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// FanOutPublisher forwards each domain event to several EventPublishers in
// turn. It is how the OLTP composition root publishes one event stream to both
// the integration topic (via Publisher) and the analytics topic (via
// AnalyticsPublisher) without the use cases knowing there is more than one
// publisher: it itself satisfies ports.EventPublisher.
//
// Fan-out is fail-fast in publisher order: the first error stops the fan-out
// and is returned, so an integration-publish failure is never masked by a
// later analytics success (and vice versa).
type FanOutPublisher struct {
	publishers []ports.EventPublisher
}

// NewFanOutPublisher builds a FanOutPublisher over publishers, in the order
// they should receive each event.
func NewFanOutPublisher(publishers ...ports.EventPublisher) *FanOutPublisher {
	return &FanOutPublisher{publishers: publishers}
}

// Publish forwards event to every configured publisher, returning the first
// error encountered.
func (f *FanOutPublisher) Publish(ctx context.Context, event shared.DomainEvent) error {
	for _, p := range f.publishers {
		if err := p.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

// Compile-time assertion that FanOutPublisher satisfies the outbound
// event-publishing port.
var _ ports.EventPublisher = (*FanOutPublisher)(nil)
