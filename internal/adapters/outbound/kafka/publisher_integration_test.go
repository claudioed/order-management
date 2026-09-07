//go:build integration

package kafka_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"

	adapter "github.com/claudioed/order-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// This verifies the real Kafka writer rather than a fake Writer. The broker is
// started by Testcontainers, never taken from KAFKA_BROKERS or localhost, so
// CI cannot silently skip the published-event contract.
func TestPublisherPublishesAllocationEnvelopeToKafka(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1",
		tckafka.WithClusterID("order-management-kafka-itest"),
	)
	if err != nil {
		t.Fatalf("start Kafka container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate Kafka container: %v", err)
		}
	})

	brokers, err := container.Brokers(ctx)
	if err != nil {
		t.Fatalf("resolve Kafka brokers: %v", err)
	}
	topic := fmt.Sprintf("warehouse.order-management.events.itest-%d", time.Now().UnixNano())
	createKafkaTopic(t, ctx, brokers, topic)

	writer := adapter.NewWriterForTopic(topic, brokers...)
	t.Cleanup(func() { _ = writer.Close() })
	publisher := adapter.NewPublisher(writer)
	occurredAt := time.Now().UTC().Truncate(time.Second)
	event := shared.NewOrderAllocated(occurredAt, "order-itest", occurredAt.Add(24*time.Hour), nil)
	if err := publisher.Publish(ctx, event); err != nil {
		t.Fatalf("publish allocation event: %v", err)
	}

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     brokers,
		Topic:       topic,
		StartOffset: kafkago.FirstOffset,
		MaxWait:     10 * time.Second,
	})
	t.Cleanup(func() { _ = reader.Close() })
	message, err := reader.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("read published message: %v", err)
	}

	var envelope struct {
		EventType string `json:"event_type"`
		Source    string `json:"source"`
	}
	if err := json.Unmarshal(message.Value, &envelope); err != nil {
		t.Fatalf("unmarshal published envelope: %v", err)
	}
	if envelope.EventType != "OrderAllocated" || envelope.Source != adapter.Source {
		t.Fatalf("published envelope = %+v, want OrderAllocated from %q", envelope, adapter.Source)
	}
}

func createKafkaTopic(t *testing.T, ctx context.Context, brokers []string, topic string) {
	t.Helper()
	conn, err := kafkago.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		t.Fatalf("dial Kafka broker: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create Kafka topic: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		partitions, err := conn.ReadPartitions(topic)
		if err == nil && len(partitions) > 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("Kafka topic %q never became ready", topic)
}
