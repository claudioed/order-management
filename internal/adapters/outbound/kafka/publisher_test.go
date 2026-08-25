package kafka_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/claudioed/order-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// fakeWriter captures every message it's asked to write, so tests can assert
// on the exact envelope shape without a real broker.
type fakeWriter struct {
	messages []kafkago.Message
	err      error
}

func (w *fakeWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	if w.err != nil {
		return w.err
	}
	w.messages = append(w.messages, msgs...)
	return nil
}

type envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Source     string          `json:"source"`
	Data       json.RawMessage `json:"data"`
}

type releasedLineData struct {
	LineNo   int    `json:"line_no"`
	SKU      string `json:"sku"`
	PathID   string `json:"path_id"`
	GiftWrap bool   `json:"gift_wrap"`
}

type allocationData struct {
	OrderID     string             `json:"order_id"`
	PromiseDate string             `json:"promise_date"`
	Lines       []releasedLineData `json:"lines"`
}

func TestPublisher_OrderAllocated_EnvelopeShape(t *testing.T) {
	writer := &fakeWriter{}
	pub := kafka.NewPublisher(writer)

	occurredAt := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	promiseDate := occurredAt.Add(24 * time.Hour)
	lines := []shared.ReleasedLine{
		{LineNo: 1, SKU: "SKU-1", PathID: "pick", GiftWrap: true},
		{LineNo: 2, SKU: "SKU-2", PathID: "singles", GiftWrap: false},
	}
	event := shared.NewOrderAllocated(occurredAt, "ord-42", promiseDate, lines)

	if err := pub.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	if len(writer.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(writer.messages))
	}

	var env envelope
	if err := json.Unmarshal(writer.messages[0].Value, &env); err != nil {
		t.Fatalf("failed to unmarshal envelope: %v", err)
	}

	if env.EventType != "OrderAllocated" {
		t.Errorf("EventType = %q, want OrderAllocated", env.EventType)
	}
	if env.Source != kafka.Source {
		t.Errorf("Source = %q, want %q", env.Source, kafka.Source)
	}
	if !env.OccurredAt.Equal(occurredAt) {
		t.Errorf("OccurredAt = %v, want %v", env.OccurredAt, occurredAt)
	}
	if env.EventID == "" {
		t.Error("EventID must not be empty")
	}

	var data allocationData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	if data.OrderID != "ord-42" {
		t.Errorf("data.order_id = %q, want ord-42", data.OrderID)
	}
	if data.PromiseDate != promiseDate.Format(time.RFC3339) {
		t.Errorf("data.promise_date = %q, want %q", data.PromiseDate, promiseDate.Format(time.RFC3339))
	}
	if len(data.Lines) != 2 {
		t.Fatalf("data.lines = %v, want 2 entries", data.Lines)
	}
	if data.Lines[0].LineNo != 1 || data.Lines[0].SKU != "SKU-1" || data.Lines[0].PathID != "pick" || !data.Lines[0].GiftWrap {
		t.Errorf("data.lines[0] = %+v, want {1 SKU-1 pick true}", data.Lines[0])
	}
	if data.Lines[1].LineNo != 2 || data.Lines[1].SKU != "SKU-2" || data.Lines[1].PathID != "singles" || data.Lines[1].GiftWrap {
		t.Errorf("data.lines[1] = %+v, want {2 SKU-2 singles false}", data.Lines[1])
	}
}

func TestPublisher_OrderPartiallyAllocated_EnvelopeShape(t *testing.T) {
	writer := &fakeWriter{}
	pub := kafka.NewPublisher(writer)

	occurredAt := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	promiseDate := occurredAt.Add(6 * time.Hour)
	lines := []shared.ReleasedLine{{LineNo: 1, SKU: "SKU-1", PathID: "pick", GiftWrap: false}}
	event := shared.NewOrderPartiallyAllocated(occurredAt, "ord-7", 1, 1, promiseDate, lines)

	if err := pub.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	if len(writer.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(writer.messages))
	}

	var env envelope
	if err := json.Unmarshal(writer.messages[0].Value, &env); err != nil {
		t.Fatalf("failed to unmarshal envelope: %v", err)
	}
	if env.EventType != "OrderPartiallyAllocated" {
		t.Errorf("EventType = %q, want OrderPartiallyAllocated", env.EventType)
	}

	var data allocationData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	if data.OrderID != "ord-7" || len(data.Lines) != 1 {
		t.Errorf("data = %+v, want order_id ord-7 with 1 line", data)
	}
}

func TestPublisher_OrderAllocated_EmptyLines(t *testing.T) {
	writer := &fakeWriter{}
	pub := kafka.NewPublisher(writer)

	event := shared.NewOrderAllocated(time.Now(), "ord-1", time.Now(), nil)
	if err := pub.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	var env envelope
	if err := json.Unmarshal(writer.messages[0].Value, &env); err != nil {
		t.Fatalf("failed to unmarshal envelope: %v", err)
	}
	var data allocationData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}
	if len(data.Lines) != 0 {
		t.Errorf("data.lines = %v, want empty", data.Lines)
	}
}

func TestPublisher_IgnoresOtherDomainEvents(t *testing.T) {
	writer := &fakeWriter{}
	pub := kafka.NewPublisher(writer)

	event := shared.NewOrderReceived(time.Now(), "ord-1", 2)

	if err := pub.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if len(writer.messages) != 0 {
		t.Errorf("expected no message written for a non-integration event, got %d", len(writer.messages))
	}
}

// TestPublisher_InjectsTraceContextIntoHeaders proves the published message
// carries the W3C traceparent for the publish span, which is what lets
// wes-work-planning's consumer parent its span onto this one. It also pins
// the span's name and messaging attributes, since those are the fleet-wide
// convention the other services follow.
func TestPublisher_InjectsTraceContextIntoHeaders(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	installTracing(t, sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))

	writer := &fakeWriter{}
	pub := kafka.NewPublisher(writer)

	event := shared.NewOrderAllocated(time.Now(), "ord-1", time.Now(), nil)

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("TraceIDFromHex: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("SpanIDFromHex: %v", err)
	}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}))

	if err := pub.Publish(ctx, event); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	if len(writer.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(writer.messages))
	}

	var traceparent string
	for _, h := range writer.messages[0].Headers {
		if h.Key == "traceparent" {
			traceparent = string(h.Value)
		}
	}
	if traceparent == "" {
		t.Fatalf("no traceparent header on the published message: %+v", writer.messages[0].Headers)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	published := spans[0]

	if published.Name() != "kafka.publish "+kafka.Topic {
		t.Errorf("span name = %q, want %q", published.Name(), "kafka.publish "+kafka.Topic)
	}
	if published.SpanKind() != trace.SpanKindProducer {
		t.Errorf("span kind = %v, want producer", published.SpanKind())
	}
	if published.Parent().SpanID() != spanID {
		t.Errorf("publish span parent = %s, want the caller's span %s", published.Parent().SpanID(), spanID)
	}

	want := "00-" + traceID.String() + "-" + published.SpanContext().SpanID().String() + "-01"
	if traceparent != want {
		t.Errorf("traceparent = %q, want %q", traceparent, want)
	}

	attrs := map[string]string{}
	for _, attr := range published.Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	if attrs["messaging.system"] != "kafka" {
		t.Errorf("messaging.system = %q, want kafka", attrs["messaging.system"])
	}
	if attrs["messaging.destination.name"] != kafka.Topic {
		t.Errorf("messaging.destination.name = %q, want %q", attrs["messaging.destination.name"], kafka.Topic)
	}
	if attrs["messaging.message.event_type"] != "OrderAllocated" {
		t.Errorf("messaging.message.event_type = %q, want OrderAllocated", attrs["messaging.message.event_type"])
	}
}

// errWriterBoom stands in for a broker write failure.
var errWriterBoom = errors.New("writer boom")

func installTracing(t *testing.T, tp trace.TracerProvider) {
	t.Helper()

	previousTracer := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	t.Cleanup(func() {
		otel.SetTracerProvider(previousTracer)
		otel.SetTextMapPropagator(previousPropagator)
	})
}

// TestPublisher_NoTraceContextWithoutASpan covers the un-instrumented case —
// no Setup, so the global provider is the no-op one. Publishing must still
// work and must leave the headers clean rather than stamping on an all-zero
// traceparent that a consumer would try to parent onto.
func TestPublisher_NoTraceContextWithoutASpan(t *testing.T) {
	installTracing(t, noop.NewTracerProvider())

	writer := &fakeWriter{}
	pub := kafka.NewPublisher(writer)

	event := shared.NewOrderAllocated(time.Now(), "ord-1", time.Now(), nil)

	if err := pub.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	for _, h := range writer.messages[0].Headers {
		if h.Key == "traceparent" {
			t.Errorf("unexpected traceparent header with no active span: %q", h.Value)
		}
	}
}

func TestPublisher_MarshalErrorPropagates(t *testing.T) {
	writer := &fakeWriter{err: errWriterBoom}
	pub := kafka.NewPublisher(writer)

	event := shared.NewOrderAllocated(time.Now(), "ord-1", time.Now(), nil)
	if err := pub.Publish(context.Background(), event); err == nil {
		t.Fatal("Publish: want error from a failing writer, got nil")
	}
}
