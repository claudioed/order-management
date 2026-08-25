package events_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/claudioed/order-management/internal/adapters/outbound/events"
	"github.com/claudioed/order-management/internal/domain/shared"
)

func TestLogPublisherLogsTheEventAsJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	publisher := events.NewLogPublisher(logger)

	at := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	if err := publisher.Publish(context.Background(), shared.NewOrderReceived(at, "ord-1", 2)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, buf.String())
	}
	if record["event_name"] != "OrderReceived" {
		t.Fatalf("event_name = %v, want OrderReceived", record["event_name"])
	}
	payload, ok := record["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %v, want an object", record["payload"])
	}
	if payload["eventName"] != "OrderReceived" || payload["OrderID"] != "ord-1" {
		t.Fatalf("payload lost detail: %v", payload)
	}
}

func TestLogPublisherDefaultsItsLogger(t *testing.T) {
	if err := events.NewLogPublisher(nil).Publish(context.Background(), shared.NewOrderReleased(time.Now(), "ord-1")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}
