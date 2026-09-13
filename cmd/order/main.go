// Command order is the composition root: it wires env config into
// adapters, adapters into use cases, and use cases into the HTTP router.
// It is the only file that reads environment variables and the only file
// that knows both a port and its implementation.
package main

import (
	"context"
	"fmt"
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
	"github.com/claudioed/order-management/internal/adapters/outbound/kafkacatalog"
	"github.com/claudioed/order-management/internal/adapters/outbound/kafkacptschedule"
	"github.com/claudioed/order-management/internal/adapters/outbound/memory"
	"github.com/claudioed/order-management/internal/adapters/outbound/pathcapacity"
	"github.com/claudioed/order-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/order-management/internal/adapters/outbound/telemetry"
	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/order"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// DefaultSiteId is used when DEFAULT_SITE_ID is unset. ADR 0014 step A
// does not yet model which site an order ships from (a known
// simplification for this phase — see order.PromisePolicy's doc
// comment); every promise in this phase is computed against one
// configured site.
const DefaultSiteId = "site-1"

func main() {
	if err := run(); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	ctx := context.Background()

	serviceName := getenv("OTEL_SERVICE_NAME", inboundhttp.DefaultServiceName)
	otlpEndpoint := getenv("OTEL_EXPORTER_OTLP_ENDPOINT", telemetry.DefaultEndpoint)

	// Telemetry comes up before any adapter, so every subsequent adapter is
	// built against the real providers rather than the no-op globals.
	// Export is non-blocking: an unreachable Collector costs telemetry,
	// never availability.
	shutdownTelemetry, err := telemetry.Setup(ctx, serviceName, getenv("SERVICE_VERSION", telemetry.DefaultServiceVersion), otlpEndpoint)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(shutdownCtx); err != nil {
			logger.Warn("telemetry shutdown did not flush cleanly", "error", err)
		}
	}()
	logger.Info("telemetry configured",
		"service_name", serviceName,
		"service_version", getenv("SERVICE_VERSION", telemetry.DefaultServiceVersion),
		"environment", getenv("ENVIRONMENT", telemetry.DefaultEnvironment),
		"otlp_endpoint", otlpEndpoint,
	)

	orderMetrics, err := telemetry.NewOrderMetrics()
	if err != nil {
		return err
	}

	httpAddr := getenv("HTTP_ADDR", ":8080")
	databaseURL := os.Getenv("DATABASE_URL")
	migrationsPath := getenv("MIGRATIONS_PATH", "migrations")

	orders, publisher, closeAdapters, err := buildRepoAdapters(databaseURL, migrationsPath, getenv("EVENT_PUBLISHER", "log"), logger)
	if err != nil {
		return err
	}
	defer closeAdapters()

	inventory := buildInventoryClient(getenv("INVENTORY_STORAGE_MODE", "permissive"), os.Getenv("INVENTORY_STORAGE_BASE_URL"), logger)

	// The process-path catalogue's SOURCE is selectable, defaulting to
	// "none" (validation skipped -- a nil ports.ProcessPathCatalogue is
	// this fleet's established "not yet wired" convention, see
	// ReceiveOrder's doc comment). Set PATH_CATALOGUE_SOURCE=kafka for a
	// real deployment, mirroring wes-work-planning/fulfillment-execution/
	// workforce-management's identical PATH_CATALOGUE_SOURCE convention.
	// Unlike those three services, order-management never had a
	// file-based catalogue to begin with (see ADR-0013's scope decision),
	// so there is no "file" mode here -- only "none" (skip) and "kafka"
	// (real validation).
	//
	// ADR-0014 step A extends this SAME switch: the CPT schedule cache
	// (kafkacptschedule) is a SEPARATE consumer instance on the SAME
	// topic/broker as the catalogue, so it is gated identically -- it
	// only runs when the catalogue does, since both need KAFKA_BROKERS.
	catalogueSource := getenv("PATH_CATALOGUE_SOURCE", "none")
	var catalogue ports.ProcessPathCatalogue
	var cptSchedule ports.CPTScheduleCache
	// catalogueConsumerCtx/cancelCatalogueConsumer are declared here
	// (rather than inline in the switch below) because the Kafka
	// catalogue source needs its own Run goroutine started BEFORE
	// WaitReady is called -- otherwise nothing would ever be consuming
	// messages while this process waits, guaranteeing a deadlock until
	// WaitReadyTimeout.
	catalogueConsumerCtx, cancelCatalogueConsumer := context.WithCancel(context.Background())
	defer cancelCatalogueConsumer()

	switch catalogueSource {
	case "kafka":
		kafkaBrokers := os.Getenv("KAFKA_BROKERS")
		if kafkaBrokers == "" {
			return fmt.Errorf("PATH_CATALOGUE_SOURCE=kafka requires KAFKA_BROKERS to be set")
		}
		brokerList := strings.Split(kafkaBrokers, ",")

		kafkaCatalogue, err := kafkacatalog.NewConsumer(context.Background(), brokerList, logger)
		if err != nil {
			return fmt.Errorf("failed to start the Kafka-sourced process-path catalogue: %w", err)
		}
		catalogue = kafkaCatalogue
		logger.Info("process-path catalogue source configured", "source", "kafka", "topic", kafkacatalog.Topic)
		go func() {
			logger.Info("process-path catalogue consumer running", "topic", kafkacatalog.Topic)
			if err := kafkaCatalogue.Run(catalogueConsumerCtx); err != nil {
				logger.Error("process-path catalogue consumer stopped", "error", err)
			}
		}()

		// A SEPARATE consumer instance, own per-process-unique consumer
		// group, on the SAME topic -- see kafkacptschedule's package
		// doc comment for why this is safe (independent event-type
		// filters, independent groups).
		cptScheduleConsumer, err := kafkacptschedule.NewConsumer(context.Background(), brokerList, logger)
		if err != nil {
			return fmt.Errorf("failed to start the Kafka-sourced CPT schedule cache: %w", err)
		}
		cptSchedule = cptScheduleConsumer
		logger.Info("CPT schedule cache source configured", "source", "kafka", "topic", kafkacptschedule.Topic)
		go func() {
			logger.Info("CPT schedule consumer running", "topic", kafkacptschedule.Topic)
			if err := cptScheduleConsumer.Run(catalogueConsumerCtx); err != nil {
				logger.Error("CPT schedule consumer stopped", "error", err)
			}
		}()

		logger.Info("waiting for the process-path catalogue to replay its initial history before accepting traffic")
		waitCtx, waitCancel := context.WithTimeout(context.Background(), kafkacatalog.WaitReadyTimeout)
		err = kafkaCatalogue.WaitReady(waitCtx)
		waitCancel()
		if err != nil {
			return fmt.Errorf("process-path catalogue did not become ready within %s: %w", kafkacatalog.WaitReadyTimeout, err)
		}

		logger.Info("waiting for the CPT schedule cache to replay its initial history before accepting traffic")
		scheduleWaitCtx, scheduleWaitCancel := context.WithTimeout(context.Background(), kafkacptschedule.WaitReadyTimeout)
		err = cptScheduleConsumer.WaitReady(scheduleWaitCtx)
		scheduleWaitCancel()
		if err != nil {
			return fmt.Errorf("CPT schedule cache did not become ready within %s: %w", kafkacptschedule.WaitReadyTimeout, err)
		}
	default:
		logger.Warn("process-path catalogue source not configured; ReceiveOrder will skip path validation",
			"hint", "set PATH_CATALOGUE_SOURCE=kafka for a real deployment")
	}

	leadTime := order.NewLeadTimePolicy(
		durationEnv("PROMISE_DEFAULT_LEAD_TIME", order.DefaultLeadTime, logger),
		perPathLeadTimes(os.Getenv("PROMISE_PATH_LEAD_TIMES"), logger),
	)

	// PromisePolicy (ADR 0014) is the primary policy; leadTime is its
	// fallback, unchanged in its own logic. Capability/Schedule are nil
	// when the Kafka catalogue source is not configured, in which case
	// PromisePolicy always falls back to leadTime -- exactly this
	// service's pre-ADR-0014 behaviour.
	promise := order.PromisePolicy{
		Schedule:   cptSchedule,
		Capability: catalogue,
		Capacity:   pathcapacity.NewUnknown(),
		Fallback:   leadTime,
		SiteId:     getenv("DEFAULT_SITE_ID", DefaultSiteId),
	}

	clock := memory.SystemClock{}
	server := &inboundhttp.Server{
		ReceiveOrder:    &usecases.ReceiveOrder{Orders: orders, Events: publisher, Clock: clock, Inventory: inventory, Promise: promise, Catalogue: catalogue, Metrics: orderMetrics},
		RetryAllocation: &usecases.RetryAllocation{Orders: orders, Inventory: inventory, Events: publisher, Clock: clock, Promise: promise},
		CancelOrder:     &usecases.CancelOrder{Orders: orders, Inventory: inventory, Events: publisher, Clock: clock},
		GetOrder:        &usecases.GetOrder{Orders: orders},
	}

	httpServer := &http.Server{
		Addr:              httpAddr,
		Handler:           inboundhttp.NewRouter(server, logger, serviceName),
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
