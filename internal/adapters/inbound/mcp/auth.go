package mcp

import (
	"github.com/claudioed/order-management/internal/adapters/inbound/auth"
)

// The MCP adapter used to carry its own Scope/Authenticator/StaticKeyAuth
// (ADR-0010). Fleet ADR 0005 (warehouse-ops-agent) lifted that
// implementation into internal/adapters/inbound/auth so the REST and MCP
// surfaces share ONE implementation per repository; this file keeps the
// MCP package's public names stable as aliases over that package, so the
// composition root and the tests read the same way as before.
//
// The Authenticator seam is unchanged: StaticKeyAuth today, an OAuth 2.1
// resource-server implementation tomorrow, behind the same interface.

// Scope is a coarse authorization class carried by an API key.
type Scope = auth.Scope

const (
	ScopeRead      = auth.ScopeRead
	ScopeReadWrite = auth.ScopeReadWrite
)

// Authenticator validates a request's bearer credential and reports the scope
// it grants.
type Authenticator = auth.Authenticator

// StaticKeyAuth authenticates against a fixed set of bearer keys.
type StaticKeyAuth = auth.StaticKeyAuth

// NewStaticKeyAuth builds a StaticKeyAuth from token->scope pairs.
func NewStaticKeyAuth(keys map[string]Scope) *StaticKeyAuth {
	return auth.NewStaticKeyAuth(keys)
}

// scopeAllows reports whether a granted scope may call a tool requiring the
// given minimum scope. read-write satisfies everything; read satisfies only
// read.
func scopeAllows(granted, required Scope) bool {
	return auth.Allows(granted, required)
}
