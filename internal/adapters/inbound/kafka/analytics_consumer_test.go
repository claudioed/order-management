package kafka_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	inboundkafka "github.com/claudioed/order-management/internal/adapters/inbound/kafka"
)

// projCall captures one projection-store method invocation.
type projCall struct {
	method  string
	eventID string
	pathID  string
	at      time.Time
}

// fakeProjection records the calls the consumer makes so a test can assert the
// envelope was routed to the right method with the right fields. It implements
// report.ProjectionStore.
type fakeProjection struct {
	calls []projCall
}

func (f *fakeProjection) record(method, eventID, pathID string, at time.Time) error {
	f.calls = append(f.calls, projCall{method, eventID, pathID, at})
	return nil
}

func (f *fakeProjection) ApplyOrderReceived(_ context.Context, e, p string, at time.Time) error {
	return f.record("received", e, p, at)
}
func (f *fakeProjection) ApplyOrderAllocated(_ context.Context, e, p string, at time.Time) error {
	return f.record("allocated", e, p, at)
}
func (f *fakeProjection) ApplyOrderPartiallyAllocated(_ context.Context, e, p string, at time.Time) error {
	return f.record("partially", e, p, at)
}
func (f *fakeProjection) ApplyOrderAllocationFailed(_ context.Context, e, p string, at time.Time) error {
	return f.record("failed", e, p, at)
}
func (f *fakeProjection) ApplyOrderReleased(_ context.Context, e, p string, at time.Time) error {
	return f.record("released", e, p, at)
}
func (f *fakeProjection) ApplyOrderCancelled(_ context.Context, e, p string, at time.Time) error {
	return f.record("cancelled", e, p, at)
}
func (f *fakeProjection) ApplyLineAllocated(_ context.Context, e, p string, at time.Time) error {
	return f.record("line-allocated", e, p, at)
}
func (f *fakeProjection) ApplyLineBackordered(_ context.Context, e, p string, at time.Time) error {
	return f.record("line-backordered", e, p, at)
}
func (f *fakeProjection) ApplyLineReleased(_ context.Context, e, p string, at time.Time) error {
	return f.record("line-released", e, p, at)
}

// fakeProcessed is an in-memory ProcessedEvents.
type fakeProcessed struct {
	seen map[string]bool
}

func newFakeProcessed() *fakeProcessed { return &fakeProcessed{seen: map[string]bool{}} }

func (p *fakeProcessed) MarkProcessed(_ context.Context, eventID string) (bool, error) {
	if p.seen[eventID] {
		return false, nil
	}
	p.seen[eventID] = true
	return true, nil
}

func analyticsEnvelope(t *testing.T, eventID, eventType string, at time.Time, data map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	env := map[string]any{
		"event_id":       eventID,
		"event_type":     eventType,
		"occurred_at":    at.Format(time.RFC3339Nano),
		"source":         "order-management",
		"schema_version": 1,
		"data":           json.RawMessage(raw),
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return b
}

func TestAnalyticsConsumer_RoutesEachEventType(t *testing.T) {
	at := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		eventType  string
		wantMethod string
	}{
		{"received", "OrderReceived", "received"},
		{"allocated", "OrderAllocated", "allocated"},
		{"partially", "OrderPartiallyAllocated", "partially"},
		{"failed", "OrderAllocationPartiallyFailed", "failed"},
		{"released", "OrderReleased", "released"},
		{"cancelled", "OrderCancelled", "cancelled"},
		{"line-allocated", "OrderLineAllocated", "line-allocated"},
		{"line-backordered", "OrderLineBackordered", "line-backordered"},
		{"line-released", "OrderLineReleased", "line-released"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proj := &fakeProjection{}
			processed := newFakeProcessed()
			c := &inboundkafka.AnalyticsConsumer{Projection: proj, Processed: processed, Logger: slog.Default()}

			raw := analyticsEnvelope(t, "e-"+tt.name, tt.eventType, at, map[string]any{"order_id": "o1", "path_id": "pick"})
			if err := c.HandleMessage(context.Background(), raw); err != nil {
				t.Fatalf("HandleMessage: %v", err)
			}
			if len(proj.calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(proj.calls))
			}
			got := proj.calls[0]
			if got.method != tt.wantMethod {
				t.Errorf("method = %q, want %q", got.method, tt.wantMethod)
			}
			if got.pathID != "pick" {
				t.Errorf("pathID = %q, want pick", got.pathID)
			}
			if !got.at.Equal(at) {
				t.Errorf("at = %v, want %v", got.at, at)
			}
		})
	}
}

func TestAnalyticsConsumer_Idempotent(t *testing.T) {
	at := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	proj := &fakeProjection{}
	processed := newFakeProcessed()
	c := &inboundkafka.AnalyticsConsumer{Projection: proj, Processed: processed, Logger: slog.Default()}

	raw := analyticsEnvelope(t, "dup", "OrderReceived", at, map[string]any{"order_id": "o1", "path_id": "pick"})
	for range 2 {
		if err := c.HandleMessage(context.Background(), raw); err != nil {
			t.Fatalf("HandleMessage: %v", err)
		}
	}
	if len(proj.calls) != 1 {
		t.Fatalf("expected 1 apply for duplicate delivery, got %d", len(proj.calls))
	}
}

func TestAnalyticsConsumer_IgnoresUnknownEventType(t *testing.T) {
	proj := &fakeProjection{}
	processed := newFakeProcessed()
	c := &inboundkafka.AnalyticsConsumer{Projection: proj, Processed: processed, Logger: slog.Default()}

	raw := analyticsEnvelope(t, "e1", "SomethingElse", time.Now(), map[string]any{"order_id": "o1"})
	if err := c.HandleMessage(context.Background(), raw); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if len(proj.calls) != 0 {
		t.Fatalf("expected unknown event to make no call, got %d", len(proj.calls))
	}
	// An event with no projection method must NOT be marked processed, so a
	// later contract change could reprocess it.
	if processed.seen["e1"] {
		t.Error("non-projecting event should not be marked processed")
	}
}
