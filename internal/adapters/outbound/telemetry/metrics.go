package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/order-management/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/claudioed/order-management"

// orderCounterName is the business metric: how much demand is arriving at
// intake, and how much of it is immediately accepted vs. rejected before an
// Order aggregate is ever persisted. A rejected rate climbing relative to
// accepted means upstream callers (or an upstream integration) are sending
// order intake requests this service's own domain invariants refuse to
// honor (e.g. no lines, an invalid SKU/quantity) — worth alerting on
// independently of any HTTP-level 4xx rate, since a single POST /orders call
// can only ever produce one of these two outcomes.
const orderCounterName = "order.orders.received"

// outcomeKey distinguishes the two ends of order intake on the single
// counter, rather than splitting it into two instruments.
const outcomeKey = attribute.Key("outcome")

const (
	outcomeAccepted = "accepted"
	outcomeRejected = "rejected"
)

// OrderMetrics implements ports.OrderMetrics against the global
// MeterProvider. Until Setup installs a real provider, the global one is a
// no-op, so recording is cheap and safe in tests and local runs.
type OrderMetrics struct {
	counter metric.Int64Counter
}

var _ ports.OrderMetrics = (*OrderMetrics)(nil)

// NewOrderMetrics registers the order-intake counter. It only fails if the
// instrument name is invalid, which is a programming error, not a runtime
// condition — callers that would rather run un-instrumented than not at all
// can ignore the error and pass a nil ports.OrderMetrics instead.
func NewOrderMetrics() (*OrderMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		orderCounterName,
		metric.WithDescription("Orders received at intake, by outcome (accepted or rejected before persistence)."),
		metric.WithUnit("{order}"),
	)
	if err != nil {
		return nil, err
	}
	return &OrderMetrics{counter: counter}, nil
}

func (m *OrderMetrics) OrderAccepted(ctx context.Context) {
	m.record(ctx, outcomeAccepted)
}

func (m *OrderMetrics) OrderRejected(ctx context.Context) {
	m.record(ctx, outcomeRejected)
}

func (m *OrderMetrics) record(ctx context.Context, outcome string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcome)))
}
