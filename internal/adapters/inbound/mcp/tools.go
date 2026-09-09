package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/shared"
)

// tracerName is the OTel instrumentation scope for MCP tool spans.
const tracerName = "github.com/claudioed/order-management/internal/adapters/inbound/mcp"

// Deps is everything the MCP tools need, injected by the composition root.
// It carries the SAME read use case the HTTP adapter uses; the adapter
// never constructs an outbound adapter itself.
//
// order-management exposes no write use case over MCP: CancelOrder,
// ReceiveOrder, Allocate and RetryAllocate are all order-lifecycle
// commands with real business invariants (EnsureCancellable,
// EnsureReleasable) that this context's own domain enforces -- none of
// them is a decision an MCP-calling agent should make on this context's
// behalf, so none is wired here.
type Deps struct {
	// GetOrder is the existing read use case behind get_order, reused
	// unchanged.
	GetOrder *usecases.GetOrder
}

// --- get_order ------------------------------------------------------------

type getOrderInput struct {
	OrderId string `json:"orderId" jsonschema:"the id of the order to look up"`
}

func (d Deps) getOrder(ctx context.Context, in getOrderInput) (orderDTO, error) {
	orderID, err := shared.NewOrderId(in.OrderId)
	if err != nil {
		return orderDTO{}, err
	}
	o, err := d.GetOrder.Execute(ctx, orderID)
	if err != nil {
		return orderDTO{}, err
	}
	return toOrderDTO(o), nil
}

// --- registration -------------------------------------------------------------

// registerTools adds every tool to the server, each wrapped so its handler
// runs inside an OTel span named "mcp.tool <name>".
//
// order-management exposes no write use case over MCP (see Deps' own doc
// comment): the one registered tool is a read tool.
func (d Deps) registerTools(server *mcp.Server) {
	readOnly := true

	addTool(server, &mcp.Tool{
		Name:        "get_order",
		Description: "Return one order's current state by id: status, allow-partial-shipment flag, promise date, and every line's SKU/quantity/process-path/gift-wrap/status/reservation-id. Use it to answer 'what is the state of this order' questions -- allocation, backorder, release, or cancellation progress.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, d.getOrder)
}

// addTool registers one tool. It centralises the cross-cutting concern
// every tool shares: a span per call, and mapping a handler error onto the
// span before returning it.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		ctx, span := otel.Tracer(tracerName).Start(ctx, "mcp.tool "+tool.Name,
			trace.WithAttributes(
				attribute.String("mcp.tool.name", tool.Name),
			),
		)
		defer span.End()

		out, err := handle(ctx, in)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, zero, err
		}
		return nil, out, nil
	})
}
