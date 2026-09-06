package mcp

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/order-management/internal/domain/order"
)

// TestScopeGating_DeniesWithoutReadScope proves that a context carrying no
// (or insufficient) scope is rejected by the handler guard. The single
// registered tool is a read tool, so ScopeRead is the minimum the guard
// enforces; the transport test always presents a valid key, so this
// white-box test is what exercises the denial branch.
func TestScopeGating_DeniesWithoutReadScope(t *testing.T) {
	// No scope in context -> scopeFromContext returns "" -> denied.
	unauth := context.Background()

	t.Run("empty-scope context denied at the read guard", func(t *testing.T) {
		if scopeAllows(scopeFromContext(unauth), ScopeRead) {
			t.Fatal("empty-scope context must not satisfy ScopeRead")
		}
	})

	t.Run("read scope satisfies read", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), scopeKey{}, ScopeRead)
		if !scopeAllows(scopeFromContext(ctx), ScopeRead) {
			t.Fatal("read scope must satisfy ScopeRead")
		}
	})

	t.Run("read scope does not satisfy the future write seam", func(t *testing.T) {
		// The read-write scope class exists for a future write tool; a
		// read-only key must not clear ScopeReadWrite.
		ctx := context.WithValue(context.Background(), scopeKey{}, ScopeRead)
		if scopeAllows(scopeFromContext(ctx), ScopeReadWrite) {
			t.Fatal("read scope must NOT satisfy ScopeReadWrite")
		}
	})
}

// TestToolCallDeniedWithoutScope drives the real registered tool through an
// in-memory client/server pair whose request context lacks a scope (no HTTP
// auth middleware runs), asserting the handler's own scope guard rejects it.
func TestToolCallDeniedWithoutScope(t *testing.T) {
	h := newHarness(t)
	h.mustSaveOrder("ORD-1", []*order.OrderLine{
		order.RehydrateOrderLine(1, "SKU-1", 1, "pick", false, order.LinePending, nil),
	}, false)
	server := NewServer(h.deps)

	client := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil)
	ct, st := sdk.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.CallTool(ctx, &sdk.CallToolParams{
		Name:      "get_order",
		Arguments: map[string]any{"orderId": "ORD-1"},
	})
	if err != nil {
		t.Fatalf("call tool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("tool call without scope must be denied by the handler guard")
	}
}
