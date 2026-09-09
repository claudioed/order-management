// Command mcp is the composition root for the Order Management MCP
// server: it wires env config to outbound adapters, adapters to the
// GetOrder read use case, and that to the inbound MCP adapter, then
// serves MCP over Streamable HTTP. It is a second, independent
// deployable alongside cmd/order (the HTTP service), per ADR-0010.
//
// order-management exposes no write use case over MCP: CancelOrder,
// ReceiveOrder, Allocate and RetryAllocate are all order-lifecycle
// commands with real business invariants enforced by the domain --
// none is a decision an MCP-calling agent should make on this context's
// behalf. This server therefore wires only the read side (GetOrder) and
// exposes only a read tool.
//
// The server is mounted unauthenticated: the fleet-wide REST/MCP
// static-bearer auth layer has been removed.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	inboundmcp "github.com/claudioed/order-management/internal/adapters/inbound/mcp"
	"github.com/claudioed/order-management/internal/adapters/outbound/memory"
	"github.com/claudioed/order-management/internal/adapters/outbound/postgres"
	"github.com/claudioed/order-management/internal/adapters/outbound/telemetry"
	"github.com/claudioed/order-management/internal/application/ports"
	"github.com/claudioed/order-management/internal/application/usecases"
)

func main() {
	if err := run(); err != nil {
		slog.Error("mcp server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	serviceName := getenv("OTEL_SERVICE_NAME", "order-management-mcp")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel(getenv("LOG_LEVEL", "info"))}))
	slog.SetDefault(logger)

	// Same non-blocking telemetry setup as the HTTP service: an unreachable
	// Collector degrades to dropped telemetry, never a server that won't start.
	shutdownTelemetry, err := telemetry.Setup(
		context.Background(),
		serviceName,
		getenv("SERVICE_VERSION", telemetry.DefaultServiceVersion),
		getenv("OTEL_EXPORTER_OTLP_ENDPOINT", telemetry.DefaultEndpoint),
	)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(shutdownCtx); err != nil {
			logger.Warn("telemetry shutdown reported an error", "error", err)
		}
	}()

	httpAddr := getenv("MCP_ADDR", ":8090")
	databaseURL := os.Getenv("DATABASE_URL")
	migrationsPath := getenv("MIGRATIONS_PATH", "migrations")

	orders, closeAdapters, err := buildAdapters(context.Background(), databaseURL, migrationsPath, logger)
	if err != nil {
		return err
	}
	defer closeAdapters()

	// The MCP adapter reuses the SAME read use case the HTTP adapter uses:
	// GetOrder, over the same repo. No write use case is wired -- see this
	// file's own package doc comment.
	deps := inboundmcp.Deps{
		GetOrder: &usecases.GetOrder{Orders: orders},
	}
	server := inboundmcp.NewServer(deps)
	handler := inboundmcp.Handler(server)

	srv := &http.Server{Addr: httpAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		logger.Info("mcp server listening (Streamable HTTP)", "addr", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("mcp server failed", "error", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// buildAdapters wires the Postgres OrderRepo when DATABASE_URL is set, or
// falls back to the in-memory repo for local development without a
// database -- exactly as cmd/order/main.go's buildRepoAdapters does
// (minus the event publisher, which only the write-side OLTP binary
// needs).
func buildAdapters(ctx context.Context, databaseURL, migrationsPath string, logger *slog.Logger) (ports.OrderRepo, func(), error) {
	noop := func() {}

	if databaseURL == "" {
		logger.Info("database url not configured; using in-memory adapters")
		return memory.NewOrderRepo(), noop, nil
	}

	if err := postgres.RunMigrations(databaseURL, migrationsPath); err != nil {
		return nil, noop, err
	}

	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return nil, noop, err
	}

	return postgres.NewOrderRepo(pool), pool.Close, nil
}

// logLevel mirrors cmd/order/main.go's newLogger level parsing, kept local
// to this binary so both composition roots stay free-standing.
func logLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
