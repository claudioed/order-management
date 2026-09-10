// Package kafkacatalog is the outbound adapter that keeps a local,
// in-memory process-path catalogue up to date by consuming
// process-path-management's warehouse.process-path-management.events
// topic. It satisfies ports.ProcessPathCatalogue (IsActive) and a
// Ready()/WaitReady() gate so this service's composition root can block
// starting real work until the initial replay of the topic's full
// history completes.
//
// This package mirrors wes-work-planning's and fulfillment-execution's
// own internal/adapters/outbound/kafkacatalog byte-for-byte in its
// concurrency/readiness design (all three consume the SAME topic from
// the SAME upstream service) -- see those packages' doc comments for the
// full rationale, including two real bugs found and fixed there before
// this copy was made:
//
//  1. A readiness gate that only re-evaluates "am I caught up" on a NEW
//     message deadlocks forever on an ordinary restart where nothing new
//     has been published since the last run.
//  2. A FIXED, shared consumer group name lets a new process resume from
//     a PRIOR process's committed offset, reporting itself ready with an
//     empty local cache having never actually replayed anything. Fixed
//     fleet-wide (here too) by making every NewConsumer call use a
//     unique, process-scoped group id -- never a shared name.
//
// One real difference from WES/FE's copy: order-management only ever
// asks "is this path active" (see order-management ADR-0013's scope
// decision) -- it has no use for MatchPrefix/RequiredCapabilities/Direct
// beyond what's needed to answer that one question, so this consumer's
// local cache and internal/domain/processpath.PathDefinition are
// deliberately smaller than WES/FE's copies. Do not widen this shape
// speculatively; extend it only when a real attribute-driven routing
// phase needs the extra fields.
package kafkacatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/order-management/internal/domain/processpath"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// Topic is process-path-management's publish topic — this service has no
// business knowing anything else about that service beyond this topic
// name and the envelope/payload shape below.
const Topic = "warehouse.process-path-management.events"

// consumerGroupPrefix names this service's dedicated, PER-PROCESS
// consumer group on Topic. Deliberately NOT a fixed, shared name — see
// the package doc comment's bug (2) for why per-process uniqueness is a
// correctness requirement here, not a cosmetic choice.
const consumerGroupPrefix = "order-management-process-path-catalogue"

// Event types this consumer acts on — process-path-management's own
// past-tense domain events, verbatim.
const (
	eventTypeCreated     = "ProcessPathCreated"
	eventTypeUpdated     = "ProcessPathUpdated"
	eventTypeDeactivated = "ProcessPathDeactivated"
)

// envelope is the CloudEvents-like wrapper shared across every
// warehouse-systems publisher.
type envelope struct {
	EventType string          `json:"event_type"`
	Data      json.RawMessage `json:"data"`
}

// pathData is the payload shape for all three event types on Topic. Only
// PathId and MatchPrefix are decoded — Direct and RequiredCapabilities
// are read by WES/FE/WFM's copies of this consumer, not by
// order-management's (see package doc comment).
type pathData struct {
	PathId      string `json:"path_id"`
	MatchPrefix string `json:"match_prefix"`
}

// Reader is the subset of *kafkago.Reader this Consumer needs, so tests
// can substitute a fake without a live broker.
type Reader interface {
	ReadMessage(ctx context.Context) (kafkago.Message, error)
	Close() error
}

// Consumer maintains a local processpath.Catalogue by replaying Topic
// from its earliest offset (a fresh, per-process consumer group) and
// applying every ProcessPath* event as it arrives. It satisfies
// ports.ProcessPathCatalogue via IsActive, delegating to the current
// in-memory snapshot.
type Consumer struct {
	Reader Reader
	Logger *slog.Logger

	mu      sync.RWMutex
	paths   map[string]processpath.PathDefinition
	ready   bool
	readyCh chan struct{}
	target  targetOffsets
}

// targetOffsets is the per-partition "caught up" watermark captured once
// at startup (see newTargetOffsets), so Ready() reflects "has this
// consumer seen everything that existed in the topic at the moment it
// started", not "will it ever catch up to a topic that keeps growing".
type targetOffsets map[int]int64

// NewConsumer constructs a Consumer reading Topic from brokers under a
// fresh, PROCESS-UNIQUE consumer group, starting at the earliest offset.
// See the package doc comment for why the group must never be a fixed
// shared name.
func NewConsumer(ctx context.Context, brokers []string, logger *slog.Logger) (*Consumer, error) {
	return NewConsumerForTopic(ctx, brokers, Topic, logger)
}

// NewConsumerForTopic constructs the same full-replay consumer as
// NewConsumer, but against an explicit topic. It lets integration tests
// exercise production replay/readiness behavior against an isolated
// throwaway topic.
func NewConsumerForTopic(ctx context.Context, brokers []string, topic string, logger *slog.Logger) (*Consumer, error) {
	if logger == nil {
		logger = slog.Default()
	}

	target, err := newTargetOffsets(ctx, brokers, topic)
	if err != nil {
		return nil, fmt.Errorf("kafkacatalog: determine readiness target: %w", err)
	}

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     brokers,
		Topic:       topic,
		GroupID:     uniqueConsumerGroup(),
		StartOffset: kafkago.FirstOffset,
	})

	c := &Consumer{
		Reader:  reader,
		Logger:  logger,
		paths:   make(map[string]processpath.PathDefinition),
		readyCh: make(chan struct{}),
		target:  target,
	}
	if len(target) == 0 {
		// The topic has no partitions with any messages yet (a brand
		// new topic, or process-path-management has never published)
		// — there is nothing to catch up to, so this consumer is
		// trivially ready from the start.
		c.markReady()
	}
	return c, nil
}

// uniqueConsumerGroup builds a group id unique to this process instance
// (hostname + PID + a nanosecond timestamp) — see the package doc
// comment for why per-process uniqueness is a correctness requirement,
// not a cosmetic choice.
func uniqueConsumerGroup() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("%s-%s-%d-%d", consumerGroupPrefix, host, os.Getpid(), time.Now().UnixNano())
}

// newTargetOffsets dials Topic directly (no consumer group) and reads
// each partition's current last offset, returning only the partitions
// that actually have at least one message.
func newTargetOffsets(ctx context.Context, brokers []string, topic string) (targetOffsets, error) {
	if len(brokers) == 0 {
		return nil, fmt.Errorf("kafkacatalog: no brokers configured")
	}
	conn, err := kafkago.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		return nil, fmt.Errorf("kafkacatalog: dial %s: %w", brokers[0], err)
	}
	defer func() { _ = conn.Close() }()

	partitions, err := conn.ReadPartitions(topic)
	if err != nil {
		return nil, fmt.Errorf("kafkacatalog: read partitions for %s: %w", topic, err)
	}

	out := make(targetOffsets, len(partitions))
	for _, p := range partitions {
		pconn, err := kafkago.DialLeader(ctx, "tcp", brokers[0], topic, p.ID)
		if err != nil {
			return nil, fmt.Errorf("kafkacatalog: dial leader for partition %d: %w", p.ID, err)
		}
		first, last, err := pconn.ReadOffsets()
		closeErr := pconn.Close()
		if err != nil {
			return nil, fmt.Errorf("kafkacatalog: read offsets for partition %d: %w", p.ID, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("kafkacatalog: close leader conn for partition %d: %w", p.ID, closeErr)
		}
		if last > first {
			// last is exclusive (the offset of the NEXT message to be
			// written) — a consumer has caught up once it has
			// processed the message at offset last-1.
			out[p.ID] = last
		}
	}
	return out, nil
}

// Close releases the underlying Kafka reader.
func (c *Consumer) Close() error {
	return c.Reader.Close()
}

// Ready reports whether this consumer has processed every message that
// existed in Topic at the moment it started. Safe to call concurrently.
func (c *Consumer) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready
}

// WaitReady blocks until Ready() would return true or ctx is done,
// whichever comes first. Used by the composition root to gate starting
// real work on catalogue completeness.
func (c *Consumer) WaitReady(ctx context.Context) error {
	if c.Ready() {
		return nil
	}
	select {
	case <-c.readyCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Consumer) markReady() {
	c.mu.Lock()
	alreadyReady := c.ready
	c.ready = true
	c.mu.Unlock()
	if !alreadyReady {
		close(c.readyCh)
	}
}

// IsActive satisfies ports.ProcessPathCatalogue against this consumer's
// current in-memory snapshot. Matching semantics are byte-for-byte
// identical to processpath.Catalogue.Lookup — this consumer builds a real
// processpath.Catalogue on every read rather than reimplementing the
// match rule, so the two can never drift.
func (c *Consumer) IsActive(pathID shared.PathId) bool {
	c.mu.RLock()
	defs := make([]processpath.PathDefinition, 0, len(c.paths))
	for _, d := range c.paths {
		defs = append(defs, d)
	}
	c.mu.RUnlock()
	_, err := processpath.New(defs).Lookup(pathID.String())
	return err == nil
}

// Run consumes Topic until ctx is cancelled or the reader returns a
// fatal error. A handling error is logged and the loop continues, so one
// malformed message cannot wedge this consumer.
func (c *Consumer) Run(ctx context.Context) error {
	for {
		msg, err := c.Reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := c.handle(msg); err != nil {
			c.Logger.ErrorContext(ctx, "process-path catalogue message handling failed",
				"topic", msg.Topic, "offset", msg.Offset, "partition", msg.Partition, "error", err)
		}
		c.checkReady(msg)
	}
}

func (c *Consumer) checkReady(msg kafkago.Message) {
	c.mu.RLock()
	target, tracked := c.target[msg.Partition]
	alreadyReady := c.ready
	c.mu.RUnlock()
	if alreadyReady || !tracked {
		return
	}
	if msg.Offset+1 < target {
		return
	}

	c.mu.Lock()
	delete(c.target, msg.Partition)
	allCaughtUp := len(c.target) == 0
	c.mu.Unlock()

	if allCaughtUp {
		c.markReady()
	}
}

func (c *Consumer) handle(msg kafkago.Message) error {
	var env envelope
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		return fmt.Errorf("kafkacatalog: unmarshal envelope: %w", err)
	}

	switch env.EventType {
	case eventTypeCreated, eventTypeUpdated:
		var data pathData
		if err := json.Unmarshal(env.Data, &data); err != nil {
			return fmt.Errorf("kafkacatalog: unmarshal %s data: %w", env.EventType, err)
		}
		c.applyUpsert(data)
	case eventTypeDeactivated:
		var data pathData
		if err := json.Unmarshal(env.Data, &data); err != nil {
			return fmt.Errorf("kafkacatalog: unmarshal %s data: %w", env.EventType, err)
		}
		c.applyDeactivated(data.PathId)
	default:
		// An event type outside this consumer's contract — ignored,
		// same convention as every other consumer in this fleet that
		// reads a shared/fan-out-shaped topic.
	}
	return nil
}

func (c *Consumer) applyUpsert(data pathData) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paths[strings.ToUpper(data.PathId)] = processpath.PathDefinition{
		Id:          data.PathId,
		MatchPrefix: data.MatchPrefix,
	}
}

func (c *Consumer) applyDeactivated(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.paths, strings.ToUpper(id))
}

// WaitReadyTimeout bounds how long the composition root waits for the
// initial replay before giving up and failing startup outright.
const WaitReadyTimeout = 60 * time.Second
