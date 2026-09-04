// Package kafka publishes cross-service integration events to the shared
// warehouse-systems Kafka broker. It implements ports.EventPublisher, so it
// drops in wherever the log or Postgres outbox publisher is used today.
//
// Only OrderAllocated and OrderPartiallyAllocated are part of the
// published integration contract (see CLAUDE.md's Kafka integration
// section); every other domain event is a local concern and is not
// forwarded here — mirroring inventory-storage's own precedent of
// forwarding only two of its several domain events.
package kafka

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/order-management/internal/domain/shared"
)

// Topic is the integration events topic this service publishes to.
const Topic = "warehouse.order-management.events"

// Source identifies this service in the event envelope.
const Source = "order-management"

// tracerName scopes the publish spans this adapter emits.
const tracerName = "github.com/claudioed/order-management/internal/adapters/outbound/kafka"

// spanName follows the fleet-wide convention for messaging spans:
// "kafka.publish <topic>" on the producer side, "kafka.consume <topic>" on
// the consumer side. This service only publishes.
const spanName = "kafka.publish " + Topic

// Writer is the subset of *kafkago.Writer the Publisher depends on, so unit
// tests can substitute a fake without a real broker.
type Writer interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// envelope is the integration event wrapper shared across all
// warehouse-systems services.
type envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Source     string          `json:"source"`
	Data       json.RawMessage `json:"data"`
}

// releasedLineData is the `data.lines[]` entry shape — frozen, and shared
// verbatim with wes-work-planning's Kafka consumer (see CLAUDE.md's Kafka
// integration section). Field names are snake_case per the fleet's
// existing wire convention (e.g. inventory-storage's demand_ref).
//
// fulfillment_class is additive: it carries Order.FulfillmentClass()
// (SINGLE / SAME_SKU_MULTI / MULTI_LINE_MULTI), the same value for every
// line released in one pass since it classifies the whole order, not the
// individual line. Consumers that predate this field can safely ignore
// it — see ADR-0008.
type releasedLineData struct {
	LineNo           int    `json:"line_no"`
	SKU              string `json:"sku"`
	PathID           string `json:"path_id"`
	GiftWrap         bool   `json:"gift_wrap"`
	FulfillmentClass string `json:"fulfillment_class"`
}

// allocationData is the `data` payload shape for both OrderAllocated and
// OrderPartiallyAllocated, per CLAUDE.md. Frozen: field names and shape
// must match wes-work-planning's consumer expectations exactly.
type allocationData struct {
	OrderID     string             `json:"order_id"`
	PromiseDate string             `json:"promise_date"`
	Lines       []releasedLineData `json:"lines"`
}

// Publisher publishes OrderAllocated and OrderPartiallyAllocated domain
// events as integration events on Topic.
type Publisher struct {
	writer Writer
}

// NewPublisher builds a Publisher over writer.
func NewPublisher(writer Writer) *Publisher {
	return &Publisher{writer: writer}
}

// NewWriter builds a *kafkago.Writer addressed at Topic on the given broker
// addresses.
func NewWriter(brokers ...string) *kafkago.Writer {
	return &kafkago.Writer{
		Addr:                   kafkago.TCP(brokers...),
		Topic:                  Topic,
		Balancer:               &kafkago.LeastBytes{},
		AllowAutoTopicCreation: true,
	}
}

func toReleasedLineData(lines []shared.ReleasedLine) []releasedLineData {
	out := make([]releasedLineData, 0, len(lines))
	for _, l := range lines {
		out = append(out, releasedLineData{
			LineNo: l.LineNo, SKU: l.SKU.String(), PathID: l.PathID.String(), GiftWrap: l.GiftWrap,
			FulfillmentClass: l.FulfillmentClass,
		})
	}
	return out
}

func (p *Publisher) Publish(ctx context.Context, event shared.DomainEvent) error {
	var data allocationData

	switch e := event.(type) {
	case shared.OrderAllocated:
		data = allocationData{
			OrderID:     e.OrderID.String(),
			PromiseDate: e.PromiseDate.UTC().Format(time.RFC3339),
			Lines:       toReleasedLineData(e.Lines),
		}
	case shared.OrderPartiallyAllocated:
		data = allocationData{
			OrderID:     e.OrderID.String(),
			PromiseDate: e.PromiseDate.UTC().Format(time.RFC3339),
			Lines:       toReleasedLineData(e.Lines),
		}
	default:
		return nil
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}

	env := envelope{
		EventID:    uuid.NewString(),
		EventType:  event.EventName(),
		OccurredAt: event.OccurredAt(),
		Source:     Source,
		Data:       payload,
	}

	msg, err := json.Marshal(env)
	if err != nil {
		return err
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			semconv.MessagingSystemKafka,
			semconv.MessagingDestinationName(Topic),
			semconv.MessagingOperationName("publish"),
			semconv.MessagingMessageID(env.EventID),
			attribute.String("messaging.message.event_type", env.EventType),
		),
	)
	defer span.End()

	// Inject after starting the span so the headers carry *this* span as the
	// parent: that is what stitches the downstream consumer's trace onto
	// this one.
	headers := []kafkago.Header{}
	otel.GetTextMapPropagator().Inject(ctx, headerCarrier{headers: &headers})

	if err := p.writer.WriteMessages(ctx, kafkago.Message{Value: msg, Headers: headers}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	return nil
}
