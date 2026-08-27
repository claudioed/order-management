// Command order is the composition root: it wires env config into
// adapters, adapters into use cases, and use cases into the HTTP router.
// It is the only file that reads environment variables and the only file
// that knows both a port and its implementation.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	inboundhttp "github.com/claudioed/order-management/internal/adapters/inbound/http"
	"github.com/claudioed/order-management/internal/adapters/outbound/events"
	"github.com/claudioed/order-management/internal/adapters/outbound/inventorystorage"
	kafkaadapter "github.com/claudioed/order-management/internal/adapters/outbound/kafka"
	"github.com/claudioed/order-management/internal/adapters/outbound/memory"
	"github.com/claudioed/order-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

func main() {
	if err := run(); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	httpAddr := getenv("HTTP_ADDR", ":8080")
	databaseURL := os.Getenv("DATABASE_URL")
	migrationsPath := getenv("MIGRATIONS_PATH", "migrations")

	orders, publisher, closeAdapters, err := buildRepoAdapters(databaseURL, migrationsPath, getenv("EVENT_PUBLISHER", "log"), logger)
	if err != nil {
		return err
	}
	defer closeAdapters()

	inventory := buildInventoryClient(getenv("INVENTORY_STORAGE_MODE", "permissive"), os.Getenv("INVENTORY_STORAGE_BASE_URL"), logger)

	promise := order.NewLeadTimePolicy(
		durationEnv("PROMISE_DEFAULT_LEAD_TIME", order.DefaultLeadTime, logger),
		perPathLeadTimes(os.Getenv("PROMISE_PATH_LEAD_TIMES"), logger),
	)

	clock := memory.SystemClock{}
	server := &inboundhttp.Server{
		ReceiveOrder:    &usecases.ReceiveOrder{Orders: orders, Events: publisher, Clock: clock, Inventory: inventory, Promise: promise},
		RetryAllocation: &usecases.RetryAllocation{Orders: orders, Inventory: inventory, Events: publisher, Clock: clock, Promise: promise},
		CancelOrder:     &usecases.CancelOrder{Orders: orders, Inventory: inventory, Events: publisher, Clock: clock},
		GetOrder:        &usecases.GetOrder{Orders: orders},
	}

	httpServer := &http.Server{
		Addr:              httpAddr,
		Handler:           inboundhttp.NewRouter(server, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

// newLogger builds the process-wide structured logger. LOG_LEVEL maps
// debug|info|warn|error (case-insensitive) to the matching slog.Level,
// defaulting to Info for unset or unrecognized values.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// buildRepoAdapters wires the Postgres adapters when DATABASE_URL is set,
// or falls back to the in-memory adapters for local development without a
// database. The event publisher defaults to that same memory/Postgres
// choice ("log"), or can be switched to the Kafka integration-events
// publisher via eventPublisher="kafka" (EVENT_PUBLISHER env), independent
// of which repos are in use — mirroring inventory-storage's
// EVENT_PUBLISHER=kafka|log pattern exactly.
func buildRepoAdapters(databaseURL, migrationsPath, eventPublisher string, logger *slog.Logger) (
	ports.OrderRepo, ports.EventPublisher, func(), error,
) {
	noop := func() {}

	var (
		orders     ports.OrderRepo
		defaultPub ports.EventPublisher
		closeRepos = noop
	)

	if databaseURL == "" {
		logger.Info("database url not configured; using in-memory adapters")
		orders = memory.NewOrderRepo()
		defaultPub = events.NewLogPublisher(logger)
	} else {
		if err := postgres.RunMigrations(databaseURL, migrationsPath); err != nil {
			return nil, nil, noop, err
		}
		pool, err := postgres.NewPool(context.Background(), databaseURL)
		if err != nil {
			return nil, nil, noop, err
		}
		orders = postgres.NewOrderRepo(pool)
		defaultPub = postgres.NewEventPublisher(pool)
		closeRepos = pool.Close
	}

	if !strings.EqualFold(eventPublisher, "kafka") {
		return orders, defaultPub, closeRepos, nil
	}

	brokers := strings.Split(getenv("KAFKA_BROKERS", "localhost:9092"), ",")

	// Integration publisher: forwards OrderAllocated/OrderPartiallyAllocated
	// onto warehouse.order-management.events. Left exactly as-is.
	writer := kafkaadapter.NewWriter(brokers...)
	integration := kafkaadapter.NewPublisher(writer)

	// Analytics publisher: forwards the full report-input event set onto the
	// SEPARATE warehouse.order-management.analytics topic for the data product
	// (ADR-0006). It enriches each event with its process path via the order
	// repo. A single OLTP event stream fans out to both publishers.
	analytics := kafkaadapter.NewAnalyticsPublisher(brokers, orders, uuid.NewString)

	logger.Info("event publisher configured", "publisher", "kafka",
		"integration_topic", kafkaadapter.Topic, "analytics_topic", kafkaadapter.AnalyticsTopic, "brokers", brokers)

	fanOut := kafkaadapter.NewFanOutPublisher(integration, analytics)

	closeAll := func() {
		if err := analytics.Close(); err != nil {
			logger.Error("error closing analytics kafka writer", "error", err)
		}
		if err := writer.Close(); err != nil {
			logger.Error("error closing kafka writer", "error", err)
		}
		closeRepos()
	}

	return orders, fanOut, closeAll, nil
}

// buildInventoryClient selects the outbound InventoryReservationClient via
// INVENTORY_STORAGE_MODE (http|permissive), defaulting to "permissive" so
// unit tests and CI never reach the network. Permissive does NOT mean
// fail-open: it refuses to allocate rather than fabricating a reservation.
func buildInventoryClient(mode, baseURL string, logger *slog.Logger) ports.InventoryReservationClient {
	if !strings.EqualFold(mode, "http") {
		logger.Warn("inventory-storage client in permissive (no-op) mode; allocation will refuse to run",
			"hint", "set INVENTORY_STORAGE_MODE=http and INVENTORY_STORAGE_BASE_URL for a real deployment")
		return inventorystorage.NewPermissiveClient()
	}
	logger.Info("inventory-storage client configured", "mode", "http", "base_url", baseURL)
	return inventorystorage.NewClient(baseURL, nil)
}

// durationEnv reads a Go duration (e.g. "48h", "90m") from key, falling
// back to fallback for an unset or unparseable value.
func durationEnv(key string, fallback time.Duration, logger *slog.Logger) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		logger.Warn("ignoring invalid duration env var", "key", key, "value", raw, "fallback", fallback.String())
		return fallback
	}
	return d
}

// perPathLeadTimes parses PROMISE_PATH_LEAD_TIMES, a comma-separated list
// of pathId=duration pairs (e.g. "pick=24h,singles=6h"). Malformed entries
// are skipped with a warning rather than failing startup: a bad promise
// override should degrade to the default lead time, not take the service
// down.
func perPathLeadTimes(raw string, logger *slog.Logger) map[shared.PathId]time.Duration {
	if raw == "" {
		return nil
	}
	out := make(map[shared.PathId]time.Duration)
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, value, found := strings.Cut(pair, "=")
		d, err := time.ParseDuration(strings.TrimSpace(value))
		if !found || strings.TrimSpace(name) == "" || err != nil || d <= 0 {
			logger.Warn("ignoring invalid entry in PROMISE_PATH_LEAD_TIMES", "entry", pair)
			continue
		}
		out[shared.PathId(strings.TrimSpace(name))] = d
	}
	return out
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
