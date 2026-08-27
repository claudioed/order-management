// Package kafka holds the inbound Kafka adapters. This file implements the
// analytics projector's consumer: it reads the order-management analytics
// topic and applies each report-moving event to the funnel ProjectionStore,
// exactly once per event_id despite Kafka's at-least-once delivery (ADR-0006).
package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/order-management/internal/analytics/report"
)

// AnalyticsConsumerGroup is the Kafka consumer group the analytics projector
// reads under. Distinct from any OLTP consumer group so the pipelines track
// their offsets independently.
const AnalyticsConsumerGroup = "order-management-analytics"

// tracerName scopes the consume spans this adapter emits.
const tracerName = "github.com/claudioed/order-management/internal/adapters/inbound/kafka"

// ProcessedEvents is the consumer's idempotency gate: it records which
// event_ids have been admitted, so an at-least-once redelivery is a no-op. It
// is declared here — where it is consumed — so the analytics side owns its own
// port and the OLTP application layer stays untouched (ADR-0006). The Postgres
// implementation lives in the analyticsstore outbound adapter.
type ProcessedEvents interface {
	// MarkProcessed records eventId if absent, returning true iff this call
	// newly recorded it.
	MarkProcessed(ctx context.Context, eventId string) (bool, error)
}

// analyticsEnvelope is the inbound decode shape of the Envelope v1 wrapper on
// the analytics topic. The data payload is left as a RawMessage and decoded
// per event_type. It is declared here (rather than imported from the outbound
// publisher) so this inbound adapter does not depend on an outbound adapter.
type analyticsEnvelope struct {
	EventID       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Source        string          `json:"source"`
	SchemaVersion int             `json:"schema_version"`
	Data          json.RawMessage `json:"data"`
}

// analyticsData is the union of fields the projecting event payloads carry.
// Every report-moving event carries path_id (enriched by the publisher); the
// rest of the fields are unused by the projection and present only so the
// decode is total.
type analyticsData struct {
	OrderID string `json:"order_id"`
	PathID  string `json:"path_id"`
	LineNo  int    `json:"line_no"`
}

// AnalyticsConsumer reads analytics events off the analytics topic and applies
// each to the funnel ProjectionStore, exactly once per event_id.
type AnalyticsConsumer struct {
	Reader     *kafkago.Reader
	Projection report.ProjectionStore
	Processed  ProcessedEvents
	Logger     *slog.Logger
}

// NewAnalyticsConsumer constructs an AnalyticsConsumer reading topic from
// brokers under AnalyticsConsumerGroup.
func NewAnalyticsConsumer(brokers []string, topic string, projection report.ProjectionStore, processed ProcessedEvents, logger *slog.Logger) *AnalyticsConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: AnalyticsConsumerGroup,
		// Start a brand-new consumer group at the EARLIEST offset. The
		// analytics projection must see the full history of the topic (it is a
		// replayable read model, not a live integration reaction), so a fresh
		// projector — or a backfill into a new group — reads from the
		// beginning rather than kafka-go's default of the latest offset, which
		// would silently drop every event produced before the group first
		// committed an offset. Once the group has committed offsets, those take
		// precedence and this only affects the first join.
		StartOffset: kafkago.FirstOffset,
	})
	return &AnalyticsConsumer{Reader: reader, Projection: projection, Processed: processed, Logger: logger}
}

// Run reads and handles messages until ctx is cancelled or the reader returns
// a fatal error. A handling error is logged and the loop continues so one bad
// message cannot wedge the projector.
func (c *AnalyticsConsumer) Run(ctx context.Context) error {
	for {
		msg, err := c.Reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if err := c.Handle(ctx, msg); err != nil {
			c.Logger.ErrorContext(ctx, "analytics message handling failed", "error", err)
		}
	}
}

// Close releases the underlying Kafka reader.
func (c *AnalyticsConsumer) Close() error {
	return c.Reader.Close()
}

// Handle processes one consumed message inside a "kafka.consume <topic>" span
// whose parent is the producer's span, read from the message headers. It is
// exported separately from Run so the propagation can be tested without a live
// broker.
func (c *AnalyticsConsumer) Handle(ctx context.Context, msg kafkago.Message) error {
	ctx = otel.GetTextMapPropagator().Extract(ctx, headerCarrier{headers: &msg.Headers})

	ctx, span := otel.Tracer(tracerName).Start(ctx,
		"kafka.consume "+msg.Topic,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			semconv.MessagingSystemKafka,
			semconv.MessagingDestinationName(msg.Topic),
			semconv.MessagingOperationName("process"),
		),
	)
	defer span.End()

	if err := c.HandleMessage(ctx, msg.Value); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

// HandleMessage decodes raw as an analyticsEnvelope and applies the matching
// projection method for its event_type. Event types outside the projection
// contract are ignored (and not marked processed). For a projecting event it
// dedupes on event_id via ProcessedEvents before applying, so a redelivery is
// a no-op. It is exported separately from Run so tests can feed raw envelopes
// without a live broker.
func (c *AnalyticsConsumer) HandleMessage(ctx context.Context, raw []byte) error {
	var env analyticsEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("analytics: decode envelope: %w", err)
	}

	// Only the funnel-moving events project. Every other analytics event type
	// is acknowledged without touching the read model or the processed set, so
	// a later contract change could reprocess it.
	if !isProjecting(env.EventType) {
		return nil
	}

	isNew, err := c.Processed.MarkProcessed(ctx, env.EventID)
	if err != nil {
		return fmt.Errorf("analytics: mark processed: %w", err)
	}
	if !isNew {
		return nil
	}

	var data analyticsData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return fmt.Errorf("analytics: decode data: %w", err)
	}

	return c.apply(ctx, env.EventType, env.EventID, data.PathID, env.OccurredAt)
}

// isProjecting reports whether eventType moves a funnel counter.
func isProjecting(eventType string) bool {
	switch eventType {
	case "OrderReceived", "OrderAllocated", "OrderPartiallyAllocated",
		"OrderAllocationPartiallyFailed", "OrderReleased", "OrderCancelled",
		"OrderLineAllocated", "OrderLineBackordered", "OrderLineReleased":
		return true
	default:
		return false
	}
}

// apply routes a decoded event to the projection method for its type.
func (c *AnalyticsConsumer) apply(ctx context.Context, eventType, eventID, pathID string, at time.Time) error {
	switch eventType {
	case "OrderReceived":
		return c.Projection.ApplyOrderReceived(ctx, eventID, pathID, at)
	case "OrderAllocated":
		return c.Projection.ApplyOrderAllocated(ctx, eventID, pathID, at)
	case "OrderPartiallyAllocated":
		return c.Projection.ApplyOrderPartiallyAllocated(ctx, eventID, pathID, at)
	case "OrderAllocationPartiallyFailed":
		return c.Projection.ApplyOrderAllocationFailed(ctx, eventID, pathID, at)
	case "OrderReleased":
		return c.Projection.ApplyOrderReleased(ctx, eventID, pathID, at)
	case "OrderCancelled":
		return c.Projection.ApplyOrderCancelled(ctx, eventID, pathID, at)
	case "OrderLineAllocated":
		return c.Projection.ApplyLineAllocated(ctx, eventID, pathID, at)
	case "OrderLineBackordered":
		return c.Projection.ApplyLineBackordered(ctx, eventID, pathID, at)
	case "OrderLineReleased":
		return c.Projection.ApplyLineReleased(ctx, eventID, pathID, at)
	default:
		return nil
	}
}

// headerCarrier adapts a kafka-go header slice to propagation.TextMapCarrier,
// so the W3C traceparent the publisher injected can be extracted here, making
// this consume span a child of the publish span. It mirrors the outbound
// adapter's carrier; it is duplicated rather than shared so the inbound adapter
// does not import the outbound one.
type headerCarrier struct {
	headers *[]kafkago.Header
}

var _ propagation.TextMapCarrier = headerCarrier{}

func (c headerCarrier) Get(key string) string {
	for _, h := range *c.headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func (c headerCarrier) Set(key, value string) {
	for i := range *c.headers {
		if (*c.headers)[i].Key == key {
			(*c.headers)[i].Value = []byte(value)
			return
		}
	}
	*c.headers = append(*c.headers, kafkago.Header{Key: key, Value: []byte(value)})
}

func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(*c.headers))
	for _, h := range *c.headers {
		keys = append(keys, h.Key)
	}
	return keys
}
