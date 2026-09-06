package mcp

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// scopeKey is the context key under which the authenticated scope is carried
// from the auth middleware into tool/resource handlers.
type scopeKey struct{}

// scopeFromContext returns the scope stored by the auth middleware, or the
// empty scope if none is present (which scopeAllows treats as unauthorized).
func scopeFromContext(ctx context.Context) Scope {
	if s, ok := ctx.Value(scopeKey{}).(Scope); ok {
		return s
	}
	return ""
}

// NewServer builds the MCP server for this bounded context with the
// get_order read tool registered. Handlers read the authenticated scope
// from their context (placed there by Handler's middleware).
//
// order-management exposes no resource or prompt: its only meaningful
// read is a single order looked up by id, which the get_order tool
// already covers directly -- a resource template would just be the same
// lookup behind a second, redundant surface, and there is no multi-step
// workflow here worth a prompt's operational SOP. No write tool is
// registered either: CancelOrder, ReceiveOrder, Allocate and
// RetryAllocate are all order-lifecycle commands with real business
// invariants (see internal/domain/order's own EnsureCancellable/
// EnsureReleasable), not decisions an MCP-calling agent should make on
// this context's behalf.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "order-management-mcp", Version: "1.0.0"},
		&mcp.ServerOptions{
			Instructions: "Read-only access to order-management: look up one order's current state (status, lines, allocation/release/cancellation progress) by id.",
		},
	)

	deps.registerTools(server, scopeFromContext)

	return server
}

// Handler returns the Streamable HTTP handler for the MCP server, wrapped in
// the auth middleware. Every request must carry a valid bearer key; the scope
// it grants is placed in the request context for handlers to enforce per-tool.
//
// This is the single seam described in ADR-0010 ("MCP inbound adapter"):
// replacing StaticKeyAuth with an OAuth 2.1 resource-server Authenticator
// changes only what is passed here, not any handler.
func Handler(server *mcp.Server, auth Authenticator) http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, ok := auth.Authenticate(r)
		if !ok {
			// Signal how to authenticate without leaking any detail about why
			// the credential failed.
			w.Header().Set("WWW-Authenticate", `Bearer realm="order-management-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), scopeKey{}, scope)
		streamable.ServeHTTP(w, r.WithContext(ctx))
	})
}
