---
id: 0012-remove-rest-mcp-bearer-auth
title: 12. Remove the REST/MCP bearer auth layer
sidebar_label: 12. Remove REST/MCP bearer auth
sidebar_position: 12
description: "Fleet-wide decision to remove the static-bearer REST/MCP auth layer adopted in ADR 0011 for now: the internal/adapters/inbound/auth package, its wiring in cmd/order, cmd/order-reports and cmd/mcp, and the outbound inventory-storage bearer are all removed, and every route (REST and MCP) is reachable with no Authorization header."
---

# 12. Remove the REST/MCP bearer auth layer

## Status

**Accepted.** This is a fleet-wide rollback decision, applied here: the
static-bearer REST/MCP identity layer adopted in
[ADR 0011](0011-adopt-fleet-rest-identity.md) (itself an adoption of
warehouse-ops-agent ADR 0005) is removed across the fleet for now.
**Supersedes ADR 0011.**

## Decision, as applied here

The `internal/adapters/inbound/auth` package is deleted entirely, along
with its wiring: the MCP adapter's `Handler` no longer takes an
`Authenticator` and requires no bearer key; the HTTP router
(`NewRouter`/`NewReportsRouter`) no longer takes an `auth.Middleware` and
mounts every route, including every mutating one, with no auth
middleware; `cmd/order`, `cmd/order-reports` and `cmd/mcp` no longer parse
`AUTH_MODE` or construct a `StaticKeyAuth`, and the "REST auth is OFF"
startup log line is gone because there is no more auth to configure. The
outbound `inventorystorage` client drops its `BearerToken`/`WithBearerToken`
plumbing and `INVENTORY_STORAGE_API_KEY` is no longer read anywhere in this
repository. The Helm chart's `auth` values block, the `*_API_KEY` /
`AUTH_MODE` Secret and ConfigMap entries are removed. `apis/openapi.yaml`
drops `components.securitySchemes.bearerAuth` and every `security:` key
(there was no MCP/asyncapi security scheme to remove here).

## Consequences

Every REST and MCP route in this service is now reachable with **no**
Authorization header — this is a deliberate, temporary rollback of the
fleet's identity posture, not an oversight. Re-adopting authentication
later is expected to mean re-adopting the fleet's next iteration of that
decision (whatever supersedes both ADR 0011 and this ADR), not resurrecting
the deleted `auth` package verbatim.
