package kafkacptschedule

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

type fakeReader struct {
	messages []kafkago.Message
	pos      int
}

func (r *fakeReader) ReadMessage(ctx context.Context) (kafkago.Message, error) {
	if r.pos < len(r.messages) {
		m := r.messages[r.pos]
		r.pos++
		return m, nil
	}
	<-ctx.Done()
	return kafkago.Message{}, ctx.Err()
}

func (r *fakeReader) Close() error { return nil }

func envelopeMsg(t *testing.T, partition int, offset int64, eventType string, data any) kafkago.Message {
	t.Helper()
	rawData, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	env := envelope{EventType: eventType, Data: rawData}
	rawEnv, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return kafkago.Message{Partition: partition, Offset: offset, Value: rawEnv}
}

func newTestConsumer(reader Reader, target targetOffsets) *Consumer {
	c := &Consumer{
		Reader:    reader,
		schedules: make(map[string]scheduleData),
		readyCh:   make(chan struct{}),
		target:    target,
	}
	if len(target) == 0 {
		c.markReady()
	}
	return c
}

func TestConsumer_NoTargetOffsets_IsReadyImmediately(t *testing.T) {
	c := newTestConsumer(&fakeReader{}, targetOffsets{})
	if !c.Ready() {
		t.Fatal("expected a consumer with no readiness target to be ready immediately")
	}
}

func TestConsumer_AppliesCPTScheduleChanged(t *testing.T) {
	reader := &fakeReader{
		messages: []kafkago.Message{
			envelopeMsg(t, 0, 0, eventTypeChanged, scheduleData{
				SiteId:   "site-1",
				Timezone: "UTC",
				Cutoffs: []cutoffData{
					{CptId: "sp1-1800", LocalTime: "18:00", EligiblePathIds: []string{"pick"}},
				},
			}),
		},
	}
	c := newTestConsumer(reader, targetOffsets{0: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	if err := c.WaitReady(ctx); err != nil {
		t.Fatalf("expected Ready before timeout, got: %v", err)
	}

	from := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	windows, known := c.NextCutoffs("site-1", from, 1)
	if !known {
		t.Fatal("expected known=true for a site with a replayed schedule")
	}
	if len(windows) != 1 || windows[0].CptId != "sp1-1800" {
		t.Fatalf("unexpected windows: %+v", windows)
	}
}

func TestConsumer_NextCutoffs_UnknownSite(t *testing.T) {
	c := newTestConsumer(&fakeReader{}, targetOffsets{})
	_, known := c.NextCutoffs("never-seen", time.Now(), 3)
	if known {
		t.Fatal("expected known=false for a site with no replayed schedule")
	}
}

func TestConsumer_LaterCPTScheduleChangedReplacesEarlierSnapshot(t *testing.T) {
	reader := &fakeReader{
		messages: []kafkago.Message{
			envelopeMsg(t, 0, 0, eventTypeChanged, scheduleData{
				SiteId: "site-1", Timezone: "UTC",
				Cutoffs: []cutoffData{{CptId: "old", LocalTime: "18:00"}},
			}),
			envelopeMsg(t, 0, 1, eventTypeChanged, scheduleData{
				SiteId: "site-1", Timezone: "UTC",
				Cutoffs: []cutoffData{{CptId: "new", LocalTime: "20:00"}},
			}),
		},
	}
	c := newTestConsumer(reader, targetOffsets{0: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	if err := c.WaitReady(ctx); err != nil {
		t.Fatalf("expected Ready before timeout, got: %v", err)
	}

	from := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	windows, known := c.NextCutoffs("site-1", from, 5)
	if !known {
		t.Fatal("expected known=true")
	}
	for _, w := range windows {
		if w.CptId == "old" {
			t.Fatalf("expected the later CPTScheduleChanged to fully replace the schedule, still found %q", w.CptId)
		}
	}
}

func TestConsumer_UnknownEventType_IsIgnored(t *testing.T) {
	reader := &fakeReader{
		messages: []kafkago.Message{
			envelopeMsg(t, 0, 0, "ProcessPathCreated", map[string]any{"path_id": "PICK"}),
		},
	}
	c := newTestConsumer(reader, targetOffsets{0: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	if err := c.WaitReady(ctx); err != nil {
		t.Fatalf("expected Ready before timeout even for an event type this consumer ignores, got: %v", err)
	}
}
