---
id: 0011-adopt-fleet-rest-identity
title: 11. Adopt the fleet REST identity — static bearer keys with read/read-write scopes
sidebar_label: 11. Fleet REST identity (adoption)
sidebar_position: 11
description: "order-management adopts warehouse-ops-agent ADR 0005: every REST route except /healthz sits behind the same static-bearer, read/read-write-scope middleware the MCP adapter already carried, rolled out through AUTH_MODE=enforce|log|off, with the outbound inventory-storage client presenting its own bearer."
---

# 11. Adopt the fleet REST identity — static bearer keys with read/read-write scopes

## Status

**Accepted.** This is an adoption record: the decision itself is the
fleet-wide [warehouse-ops-agent ADR 0005 — Fleet REST identity: static
bearer keys with read/read-write scopes, no IdP](https://github.com/claudioed/warehouse-ops-agent/blob/develop/docs/docs/adr/0005-rest-identity-static-bearer-scopes.md).
Read that for the context, the alternatives (OIDC now, gateway-only auth, a
shared Go module) and the rollout plan; this page records only what
order-management did with it.

## Decision, as applied here

One `auth` package, `internal/adapters/inbound/auth`, copied verbatim from
the fleet template (copy-not-share, per ADR-0002). It owns `Scope`,
`Authenticator`, `StaticKeyAuth`, `Middleware` and `KeysFromEnv`. The MCP
adapter from ADR-0010 no longer carries its own copy of those types; its
public names are now aliases over this package, so this repository has
exactly one implementation serving both the REST and MCP surfaces, behind
the same OAuth-ready `Authenticator` seam.

Route policy: `GET`/`HEAD`/`OPTIONS` require `read`, everything else
requires `read-write`; `/healthz` on both `cmd/order` and `cmd/order-reports`
stays outside the middleware (a chi `Group`), and the reports router pins
every route to `read`. Failures are RFC 7807 problems under this service's
existing `https://errors.order-management.warehouse-systems.dev/` base:
`401 unauthenticated` (with `WWW-Authenticate: Bearer`) and
`403 insufficient-scope`.

Keys are `API_READ_KEY` / `API_READWRITE_KEY`, falling back to
`MCP_READ_KEY` / `MCP_READWRITE_KEY`, so one Secret serves both surfaces.
`AUTH_MODE=enforce|log|off`; the composition roots default to `enforce`
when a key is configured and to `off` — with a loud WARN — when none is,
so local development and every existing handler test are unaffected. The
outbound inventory-storage client sends `Authorization: Bearer
$INVENTORY_STORAGE_API_KEY` when that variable is set and no header
otherwise. The chart exposes `auth.mode`, `auth.readKey`,
`auth.readWriteKey`, `auth.existingSecret` and `inventoryStorage.apiKey`.

## Consequences

Additive only: no handler, use case or domain type changed. The static
keys are not identities (no per-user attribution, no rotation beyond
re-applying Terraform) — exactly the gap the `Authenticator` seam leaves
open for an OAuth 2.1 resource server later, without touching any router.
