---
slug: /api-reference
title: API Reference
sidebar_label: Overview
description: REST conventions, the endpoint matrix, and the RFC 7807 error catalog.
---

# API Reference

This service exposes one contract, kept as a source-of-truth artefact in
the repository and linted by Spectral locally:

| Contract | File | Rendered here |
| --- | --- | --- |
| REST (synchronous) | `apis/openapi.yaml` — OpenAPI 3.0.3 | **[REST API](./rest/order-management-api.info.mdx)** — generated directly from the spec |

The REST pages under **REST API** are *generated* from `apis/openapi.yaml`
by `docusaurus-plugin-openapi-docs` at build time. They are not
hand-transcribed, so they cannot drift from the spec the service ships.

## Endpoint matrix

All 7 routes registered in `internal/adapters/inbound/http` are
documented.

| Method | Path | Operation | Tag | Success | Errors |
| --- | --- | --- | --- | --- | --- |
| `GET` | `/healthz` | `getHealthz` | Health | `200` | — |
| `POST` | `/orders` | `receiveOrder` | Orders | `201` | `400` `422` `500` |
| `GET` | `/orders/{id}` | `getOrder` | Orders | `200` | `400` `404` `500` |
| `DELETE` | `/orders/{id}` | `cancelOrder` | Orders | `204` | `400` `404` `409` `503` `500` |
| `POST` | `/orders/{id}/allocate` | `allocateOrder` | Allocation | `200` | `400` `404` `503` `500` |
| `POST` | `/orders/{id}/retry-allocation` | `retryAllocation` | Allocation | `200` | `400` `404` `409` `503` `500` |
| `POST` | `/orders/{id}/release` | `releaseOrder` | Release | `200` | `400` `404` `409` `503` `500` |

## Status-code conventions

| Code | Used for | Example |
| --- | --- | --- |
| `200 OK` | A read, or a command whose result *is* the response body | `GET /orders/{id}`; `POST /orders/{id}/allocate` returns the order after the pass |
| `201 Created` | A new addressable resource, with a `Location` header | `POST /orders` → `Location: /orders/{id}` |
| `204 No Content` | A state transition with nothing useful to return | `DELETE /orders/{id}` |
| `400 Bad Request` | Malformed or missing input | empty SKU, unparseable JSON |
| `404 Not Found` | The addressed resource does not exist | unknown order |
| `409 Conflict` | Well-formed and addressable, but conflicts with current state | order already released (cancel), ship-complete blocked (release), no backordered lines (retry) |
| `422 Unprocessable Entity` | Well-formed but semantically invalid *values* | quantity ≤ 0 |
| `503 Service Unavailable` | A downstream Supplier could not be reached, answered ambiguously, or is wired in permissive (no-op) mode | any non-409 failure from inventory-storage or wes-work-planning during allocate/retry/release/cancel |

The `400` / `422` split is the one worth internalising: `400` means "I
could not understand the request," `422` means "I understood it perfectly
and it is not a legal thing to ask for." The `409` / `503` split matters
just as much here: `409` means this context's own state disagrees with
the request; `503` means a Supplier could not be trusted to answer,
never papered over with a fabricated success (BR2).

## Errors: RFC 7807 Problem Details

Every error response uses `application/problem+json`, the same shape the
other five services in this platform emit:

```json
{
  "type": "https://errors.order-management.warehouse-systems.dev/order-already-released",
  "title": "Order already has released lines and can no longer be cancelled",
  "status": 409,
  "detail": "order already has released lines and can no longer be cancelled",
  "instance": "/orders/ord-a1b2c3d4-0000-0000-0000-000000000001"
}
```

- `type` is a stable, unique URI per error **category**. It is an
  identifier — it does not have to resolve to a page.
- `title` is a fixed human string for the category.
- `detail` is the dynamic message from the underlying typed error.
- `instance` is the request path.

### Problem-type catalog

| `type` slug | Status | Raised by |
| --- | --- | --- |
| `order-not-found` | 404 | order id does not exist |
| `empty-sku` | 400 | a line's SKU is empty |
| `non-positive-quantity` | 422 | a line's quantity is not greater than zero |
| `order-already-released` | 409 | `CancelOrder` when any line is `Released` (BR6) |
| `ship-complete-blocked` | 409 | `ReleaseOrder` on a ship-complete order with an unallocated line (BR3) |
| `no-backordered-lines` | 409 | `RetryAllocation` on an order with nothing backordered |
| `downstream-not-configured` | 503 | a Supplier client is running in permissive (no-op) mode |
| `internal-error` | 500 | anything unmapped |

The domain never knows about any of this. It returns typed errors; the
inbound adapter is the only layer that translates them.

## DTOs never leak domain types

Request and response bodies are adapter-local structs in
`internal/adapters/inbound/http/dto.go`. An `orderResponse` is not an
`order.Order`. That indirection is what lets the domain model evolve
without breaking the wire contract.

## Authentication

None. `security: []` in the spec is deliberate and explicit: this is an
internal, cluster-local service reached through the platform's gateway,
which owns authentication and authorisation. Declaring `security: []`
states "no auth at this layer" rather than leaving it ambiguous.
