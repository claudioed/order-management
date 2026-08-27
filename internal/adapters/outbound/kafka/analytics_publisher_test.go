package kafka_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/claudioed/order-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// fakeOrderRepo is a minimal ports.OrderRepo whose FindByID returns a fixed
// order, so the analytics publisher's path_id enrichment can be asserted
// without a real repository. Only FindByID is exercised by the publisher; the
// rest satisfy the interface.
type fakeOrderRepo struct {
	order *order.Order
}

func (r fakeOrderRepo) FindByID(_ context.Context, _ shared.OrderId) (*order.Order, error) {
	return r.order, nil
}
func (fakeOrderRepo) Save(context.Context, *order.Order) error { return nil }
func (fakeOrderRepo) NextID(context.Context) (shared.OrderId, error) {
	return shared.OrderId("next"), nil
}

// orderOnPath builds a one-line order bound to path so enrichment lookups
// resolve to a known value.
func orderOnPath(t *testing.T, id, path string) *order.Order {
	t.Helper()
	sku, err := shared.NewSKU("SKU-1")
	if err != nil {
		t.Fatalf("NewSKU: %v", err)
	}
	line, err := order.NewOrderLine(1, sku, 1, shared.PathId(path), false)
	if err != nil {
		t.Fatalf("NewOrderLine: %v", err)
	}
	o, err := order.New(shared.OrderId(id), []*order.OrderLine{line}, false)
	if err != nil {
		t.Fatalf("order.New: %v", err)
	}
	return o
}

func decodeAnalytics(t *testing.T, raw []byte) (kafka.AnalyticsEnvelope, map[string]any) {
	t.Helper()
	var env kafka.AnalyticsEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	return env, data
}

func TestAnalyticsPublisher_PublishesEachEventType(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sku, _ := shared.NewSKU("SKU-1")

	tests := []struct {
		name          string
		event         shared.DomainEvent
		wantType      string
		wantKey       string
		wantPath      string
		wantDataField string
		wantDataValue any
	}{
		{
			name:          "OrderReceived",
			event:         shared.NewOrderReceived(at, "o1", 2),
			wantType:      "OrderReceived",
			wantKey:       "o1",
			wantPath:      "pick",
			wantDataField: "line_count",
			wantDataValue: float64(2),
		},
		{
			name:          "OrderAllocated",
			event:         shared.NewOrderAllocated(at, "o1", at, []shared.ReleasedLine{{LineNo: 1, SKU: sku, PathID: "singles"}}),
			wantType:      "OrderAllocated",
			wantKey:       "o1",
			wantPath:      "singles", // taken from the carried released line
			wantDataField: "order_id",
			wantDataValue: "o1",
		},
		{
			name:          "OrderPartiallyAllocated",
			event:         shared.NewOrderPartiallyAllocated(at, "o1", 1, 1, at, []shared.ReleasedLine{{LineNo: 1, SKU: sku, PathID: "singles"}}),
			wantType:      "OrderPartiallyAllocated",
			wantKey:       "o1",
			wantPath:      "singles",
			wantDataField: "backordered_lines",
			wantDataValue: float64(1),
		},
		{
			name:          "OrderAllocationPartiallyFailed",
			event:         shared.NewOrderAllocationPartiallyFailed(at, "o1", 1, 2, "boom"),
			wantType:      "OrderAllocationPartiallyFailed",
			wantKey:       "o1",
			wantPath:      "pick",
			wantDataField: "remaining_lines",
			wantDataValue: float64(2),
		},
		{
			name:          "OrderReleased",
			event:         shared.NewOrderReleased(at, "o1"),
			wantType:      "OrderReleased",
			wantKey:       "o1",
			wantPath:      "pick",
			wantDataField: "order_id",
			wantDataValue: "o1",
		},
		{
			name:          "OrderCancelled",
			event:         shared.NewOrderCancelled(at, "o1", 3),
			wantType:      "OrderCancelled",
			wantKey:       "o1",
			wantPath:      "pick",
			wantDataField: "revoked_reservations",
			wantDataValue: float64(3),
		},
		{
			name:          "OrderLineAllocated",
			event:         shared.NewOrderLineAllocated(at, "o1", 1, sku, 2, "res-1"),
			wantType:      "OrderLineAllocated",
			wantKey:       "o1",
			wantPath:      "pick", // looked up by line number on the order
			wantDataField: "sku",
			wantDataValue: "SKU-1",
		},
		{
			name:          "OrderLineBackordered",
			event:         shared.NewOrderLineBackordered(at, "o1", 1, sku, 2),
			wantType:      "OrderLineBackordered",
			wantKey:       "o1",
			wantPath:      "pick",
			wantDataField: "line_no",
			wantDataValue: float64(1),
		},
		{
			name:          "OrderLineReleased",
			event:         shared.NewOrderLineReleased(at, "o1", 1, "singles", "wu-1"),
			wantType:      "OrderLineReleased",
			wantKey:       "o1",
			wantPath:      "singles", // carried on the event directly
			wantDataField: "work_unit_id",
			wantDataValue: "wu-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &fakeWriter{}
			p := kafka.NewAnalyticsPublisher(nil, fakeOrderRepo{order: orderOnPath(t, "o1", "pick")}, func() string { return "evt-fixed" })
			p.Writer = w

			if err := p.Publish(context.Background(), tt.event); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if len(w.messages) != 1 {
				t.Fatalf("expected 1 message, got %d", len(w.messages))
			}
			msg := w.messages[0]
			if string(msg.Key) != tt.wantKey {
				t.Errorf("key = %q, want %q", string(msg.Key), tt.wantKey)
			}

			env, data := decodeAnalytics(t, msg.Value)
			if env.EventType != tt.wantType {
				t.Errorf("event_type = %q, want %q", env.EventType, tt.wantType)
			}
			if env.EventID != "evt-fixed" {
				t.Errorf("event_id = %q, want evt-fixed", env.EventID)
			}
			if env.Source != "order-management" {
				t.Errorf("source = %q, want order-management", env.Source)
			}
			if env.SchemaVersion != 1 {
				t.Errorf("schema_version = %d, want 1", env.SchemaVersion)
			}
			if !env.OccurredAt.Equal(at) {
				t.Errorf("occurred_at = %v, want %v", env.OccurredAt, at)
			}
			if data["path_id"] != tt.wantPath {
				t.Errorf("path_id = %v, want %q", data["path_id"], tt.wantPath)
			}
			if got := data[tt.wantDataField]; got != tt.wantDataValue {
				t.Errorf("data[%q] = %v (%T), want %v (%T)", tt.wantDataField, got, got, tt.wantDataValue, tt.wantDataValue)
			}
		})
	}
}

func TestAnalyticsPublisher_SkipsUnknownEvents(t *testing.T) {
	w := &fakeWriter{}
	p := kafka.NewAnalyticsPublisher(nil, fakeOrderRepo{}, func() string { return "evt" })
	p.Writer = w

	if err := p.Publish(context.Background(), unknownAnalyticsEvent{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(w.messages) != 0 {
		t.Fatalf("expected unknown event to be skipped, got %d messages", len(w.messages))
	}
}

type unknownAnalyticsEvent struct{}

func (unknownAnalyticsEvent) EventName() string     { return "Unknown" }
func (unknownAnalyticsEvent) OccurredAt() time.Time { return time.Time{} }

// TestAnalyticsPublisher_MissingOrderLeavesPathEmpty asserts enrichment is
// best-effort: an order the repo cannot find yields an empty path rather than
// a failed publish.
func TestAnalyticsPublisher_MissingOrderLeavesPathEmpty(t *testing.T) {
	w := &fakeWriter{}
	p := kafka.NewAnalyticsPublisher(nil, fakeOrderRepo{order: nil}, func() string { return "evt" })
	p.Writer = w

	if err := p.Publish(context.Background(), shared.NewOrderReceived(time.Now(), "missing", 1)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	_, data := decodeAnalytics(t, w.messages[0].Value)
	if data["path_id"] != "" {
		t.Errorf("path_id = %v, want empty", data["path_id"])
	}
}
