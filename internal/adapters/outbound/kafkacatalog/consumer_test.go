package kafkacatalog

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/order-management/internal/domain/processpath"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// fakeReader replays a fixed sequence of messages, then blocks until ctx
// is cancelled -- mirrors a real Kafka reader that has caught up and is
// now waiting for new messages.
type fakeReader struct {
	messages []kafkago.Message
	pos      int
	closed   bool
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

func (r *fakeReader) Close() error {
	r.closed = true
	return nil
}

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
		Reader:  reader,
		paths:   make(map[string]processpath.PathDefinition),
		readyCh: make(chan struct{}),
		target:  target,
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

func TestConsumer_Run_BecomesReadyAfterCatchingUpSinglePartition(t *testing.T) {
	reader := &fakeReader{
		messages: []kafkago.Message{
			envelopeMsg(t, 0, 0, eventTypeCreated, pathData{PathId: "PICK", MatchPrefix: "pick"}),
			envelopeMsg(t, 0, 1, eventTypeCreated, pathData{PathId: "PACK", MatchPrefix: "pack"}),
		},
	}
	// target[0] = 2 means "caught up once offset 1 has been processed"
	// (last is exclusive: 2 messages at offsets 0 and 1).
	c := newTestConsumer(reader, targetOffsets{0: 2})

	if c.Ready() {
		t.Fatal("expected not ready before Run starts")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = c.Run(ctx)
		close(done)
	}()

	if err := c.WaitReady(ctx); err != nil {
		t.Fatalf("expected Ready before timeout, got: %v", err)
	}
	cancel()
	<-done

	if !c.IsActive(shared.PathId("pick")) {
		t.Fatal("expected PICK to be active")
	}
	if !c.IsActive(shared.PathId("pack")) {
		t.Fatal("expected PACK to be active")
	}
}

func TestConsumer_MultiPartition_ReadyOnlyAfterBothCaughtUp(t *testing.T) {
	reader := &fakeReader{
		messages: []kafkago.Message{
			envelopeMsg(t, 0, 0, eventTypeCreated, pathData{PathId: "PICK", MatchPrefix: "pick"}),
			// Partition 1 not yet caught up (target[1]=1, need offset 0).
		},
	}
	c := newTestConsumer(reader, targetOffsets{0: 1, 1: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	if err := c.WaitReady(ctx); err == nil {
		t.Fatal("expected WaitReady to time out -- partition 1 never caught up")
	}
}

func TestConsumer_Deactivated_RemovesPathFromCache(t *testing.T) {
	reader := &fakeReader{
		messages: []kafkago.Message{
			envelopeMsg(t, 0, 0, eventTypeCreated, pathData{PathId: "PICK", MatchPrefix: "pick"}),
			envelopeMsg(t, 0, 1, eventTypeDeactivated, pathData{PathId: "PICK"}),
		},
	}
	c := newTestConsumer(reader, targetOffsets{0: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	if err := c.WaitReady(ctx); err != nil {
		t.Fatalf("expected Ready before timeout, got: %v", err)
	}

	if c.IsActive(shared.PathId("pick")) {
		t.Fatal("expected PICK to be inactive after deactivation")
	}
}

func TestConsumer_Revised_UpdatesMatchPrefix(t *testing.T) {
	reader := &fakeReader{
		messages: []kafkago.Message{
			envelopeMsg(t, 0, 0, eventTypeCreated, pathData{PathId: "PICK", MatchPrefix: "pick"}),
			envelopeMsg(t, 0, 1, eventTypeUpdated, pathData{PathId: "PICK", MatchPrefix: "pick-zone-a"}),
		},
	}
	c := newTestConsumer(reader, targetOffsets{0: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	if err := c.WaitReady(ctx); err != nil {
		t.Fatalf("expected Ready before timeout, got: %v", err)
	}

	if !c.IsActive(shared.PathId("pick-zone-a")) {
		t.Fatal("expected pick-zone-a to resolve as active after revision")
	}
	// The revision REPLACED matchPrefix from "pick" to "pick-zone-a", so
	// the bare old prefix no longer matches -- this is expected, not a
	// bug: MatchPrefix is a full replacement on ProcessPathUpdated, not
	// an additive widening.
	if c.IsActive(shared.PathId("pick")) {
		t.Fatal("expected bare pick to no longer resolve after its matchPrefix was replaced")
	}
}

func TestConsumer_UnknownEventType_IsIgnored(t *testing.T) {
	reader := &fakeReader{
		messages: []kafkago.Message{
			envelopeMsg(t, 0, 0, "SomeFutureEventType", map[string]any{}),
		},
	}
	c := newTestConsumer(reader, targetOffsets{0: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	if err := c.WaitReady(ctx); err != nil {
		t.Fatalf("expected Ready before timeout even for an unrecognized event type, got: %v", err)
	}
}

func TestConsumer_IsActive_UnknownPath_ReturnsFalse(t *testing.T) {
	c := newTestConsumer(&fakeReader{}, targetOffsets{})
	if c.IsActive(shared.PathId("never-declared")) {
		t.Fatal("expected an undeclared path id to be inactive")
	}
}

// TestConsumer_ListActive_EmptyCache_ReturnsEmptySlice (ADR-0021): a
// not-yet-populated cache must fail open with an empty slice, never nil
// treated as an error and never a panic on the caller's range loop.
func TestConsumer_ListActive_EmptyCache_ReturnsEmptySlice(t *testing.T) {
	c := newTestConsumer(&fakeReader{}, targetOffsets{})
	got := c.ListActive()
	if len(got) != 0 {
		t.Fatalf("ListActive() = %v, want empty", got)
	}
}

// TestConsumer_ListActive_ReturnsEveryUpsertedPath (ADR-0021): every path
// upserted via ProcessPathCreated/Updated is enumerable, carrying the
// same CycleTimeP95/Eligibility IsActive/CycleTimeP95/Eligibility would
// each report individually for that id.
func TestConsumer_ListActive_ReturnsEveryUpsertedPath(t *testing.T) {
	c := newTestConsumer(&fakeReader{}, targetOffsets{})

	if err := c.handle(envelopeMsg(t, 0, 0, eventTypeCreated, pathData{
		PathId: "PICK", MatchPrefix: "pick", CycleTimeP95: "30m0s",
		Eligibility: &eligibilityData{},
	})); err != nil {
		t.Fatalf("handle PICK: %v", err)
	}
	if err := c.handle(envelopeMsg(t, 0, 1, eventTypeCreated, pathData{
		PathId: "SINGLES", MatchPrefix: "singles", CycleTimeP95: "10m0s",
		Eligibility: &eligibilityData{},
	})); err != nil {
		t.Fatalf("handle SINGLES: %v", err)
	}

	got := c.ListActive()
	if len(got) != 2 {
		t.Fatalf("ListActive() returned %d entries, want 2: %+v", len(got), got)
	}
	byID := map[shared.PathId]shared.ActivePathCandidate{}
	for _, a := range got {
		byID[a.PathId] = a
	}
	pick, ok := byID["PICK"]
	if !ok || !pick.CycleTimeKnown || pick.CycleTimeP95 != 30*time.Minute {
		t.Fatalf("ListActive() PICK entry = %+v, want cycleTime=30m known=true", pick)
	}
	singles, ok := byID["SINGLES"]
	if !ok || !singles.CycleTimeKnown || singles.CycleTimeP95 != 10*time.Minute {
		t.Fatalf("ListActive() SINGLES entry = %+v, want cycleTime=10m known=true", singles)
	}
}

// TestConsumer_ListActive_DeactivatedPath_IsExcluded (ADR-0021): a
// deactivated path must disappear from ListActive, the same as it already
// disappears from IsActive/CycleTimeP95/Eligibility.
func TestConsumer_ListActive_DeactivatedPath_IsExcluded(t *testing.T) {
	c := newTestConsumer(&fakeReader{}, targetOffsets{})

	if err := c.handle(envelopeMsg(t, 0, 0, eventTypeCreated, pathData{
		PathId: "PICK", MatchPrefix: "pick", CycleTimeP95: "30m0s",
		Eligibility: &eligibilityData{},
	})); err != nil {
		t.Fatalf("handle create: %v", err)
	}
	if err := c.handle(envelopeMsg(t, 0, 1, eventTypeDeactivated, pathData{PathId: "PICK"})); err != nil {
		t.Fatalf("handle deactivate: %v", err)
	}

	if got := c.ListActive(); len(got) != 0 {
		t.Fatalf("ListActive() = %v, want empty after deactivation", got)
	}
}
