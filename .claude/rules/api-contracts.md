# API contracts

## REST API (5 endpoints — `apis/openapi.yaml`, no more `/allocate`/`/release`)

- `POST   /orders`                       → `receiveOrder` — intake; folds
  allocation-then-release into the same call (ADR-0005). 201 always, even
  if the implicit allocation pass hits a hard failure.
- `GET    /orders/{id}`                  → `getOrder`
- `POST   /orders/{id}/retry-allocation` → `retryAllocation` — 503 on a
  genuine hard failure (this IS an explicit ask for allocation now).
- `DELETE /orders/{id}`                  → `cancelOrder` — 409 if any line
  already `Released` (BR6); 204 on success.
- `GET    /healthz`                      → `getHealthz` — liveness only,
  does not check Postgres/inventory-storage.

JSON DTOs live in the http adapter; never leak domain structs. Every error
response is RFC 7807 `application/problem+json` (identical shape to the
other five services).

**No auth on any route** (ADR-0012 — the fleet-wide static-bearer rollout
from ADR-0011 was rolled back; `apis/openapi.yaml` carries no
`securitySchemes`/`security:` keys at all as of that ADR). Re-adopting auth
later means adopting whatever supersedes both ADR-0011 and ADR-0012, not
resurrecting the deleted `internal/adapters/inbound/auth` package verbatim.

CORS middleware (`go-chi/cors`) is enabled on every route, allowing
`CORS_ALLOWED_ORIGINS` (env, default
`http://localhost:5173,http://localhost:5181` — the `warehouse-console`
shell and this repo's own `order-mgmt-mfe` remote), for the fleet's browser
console (ADR-0007).

## Analytics REST API (`cmd/order-reports`, default `:8092`)

- `GET /reports/funnel` — Order Funnel & Allocation Health report, keyed
  per `pathId × hour`. Query params `from`/`to` (required, RFC3339),
  `pathId` (optional filter), `granularity` (only `hour`).
- `GET /reports/funnel/freshness` — projection lag (`lagSeconds`).
- `GET /healthz`

## Outbound HTTP contract to inventory-storage (EXACT — verified against live openapi.yaml)

Base URL via env `INVENTORY_STORAGE_BASE_URL`, mode via
`INVENTORY_STORAGE_MODE` (default `permissive`):

- `POST /reservations` — request
  `{"sku":"...","quantity":N,"demandRef":"..."}` (this Order's `OrderId` as
  `demandRef`) — 201 response:
  `{"id":"...","sku":"...","quantity":N,"demandRef":"...","status":"...","allocations":[{"stockUnitId":"...","quantity":N}],"expiresAt":"..."}`.
  A 409 (RFC 7807) means insufficient usable stock → `Backordered` for that
  line (BR2). Any other non-2xx or transport error propagates as a hard
  failure — never silently backordered.
- `DELETE /reservations/{id}` — 204 on success. Used by `CancelOrder`.

**Permissive mode is NOT soft here.** Unlike other fleet adapters that
fail-open on optional/soft lookups, allocating real stock is never allowed
to silently "succeed" against a no-op client. In permissive mode,
allocation returns a clear `ErrDownstreamNotConfigured`. Only `http` mode
is suitable for a real integration test or deployment.

## Kafka contract (`apis/asyncapi.yaml`, AsyncAPI 2.6.0 — Spectral-gated, `api-lint` CI job)

Fleet envelope, NOT CloudEvents. Two envelope variants:

- **Integration envelope** on `warehouse.order-management.events`:
  `{event_id, event_type, occurred_at, source, data}`, unkeyed, at-least-once.
- **Analytics envelope** on `warehouse.order-management.analytics`: adds
  `schema_version: 1`, keyed by `OrderId`.

### Channel: `warehouse.order-management.events` (integration, frozen)

- `subscribe` operationId `consumeOrderManagementEvents`.
- Only `OrderAllocated` / `OrderPartiallyAllocated` messages. `data.lines[]`
  entry shape is shared verbatim with `wes-work-planning`'s consumer and
  MUST NOT change without coordinating both sides.
  `fulfillment_class` is additive (ADR-0008).

### Channel: `warehouse.order-management.analytics`

- `publish` operationId `publishOrderAnalytics` (this service is the
  producer).
- `subscribe` operationId `projectOrderAnalytics` (`cmd/order-projector` is
  the sole consumer/writer — FirstOffset, idempotent on `event_id`).
- Carries all 9 analytics-relevant event types (see `domain-model.md`'s
  event table) as 9 distinct messages.

### Deterministic work-unit id (frozen, zero wire-level enforcement)

`WorkUnitID(orderID, lineNo) = "{orderID}-line-{lineNo}"`
(`internal/application/usecases/allocation.go`). `wes-work-planning`'s
consumer independently reconstructs the SAME formula from
`(order_id, lines[].line_no)` — never transmitted on the wire. Must match
byte-for-byte or idempotent redelivery breaks. Nothing catches a drift
except manual review and cross-repo test discipline.

## MCP server (`cmd/mcp`, ADR-0010) — read-only, one tool

- `internal/adapters/inbound/mcp/` — second driving adapter over the
  **existing** `GetOrder` use case, calling the same use case struct the
  HTTP handler calls (never a parallel code path).
- Exactly **one tool, `get_order`**, no resource, no prompt, **no write
  tool** — every write use case here (`ReceiveOrder`, `CancelOrder`,
  `RetryAllocation`) enforces a real domain invariant an MCP-calling agent
  should not trigger directly.
- No auth (ADR-0012 rolled back the bearer-key layer this adapter
  originally had per ADR-0011).
