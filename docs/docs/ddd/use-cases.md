---
title: Use Cases
sidebar_label: Use Cases
description: The six application-layer use cases, their HTTP endpoints, and their request/response shapes.
---

# Use Cases

Six use cases, one struct each, in `internal/application/usecases`. Each
depends only on the domain and on `application/ports` — never on an
adapter. Dependencies are plain struct fields, wired once in
`cmd/order/main.go`.

| # | Use case | HTTP | Emits |
| --- | --- | --- | --- |
| 1 | `ReceiveOrder` | `POST /orders` | `OrderReceived` |
| 2 | `AllocateOrder` | `POST /orders/{id}/allocate` | `OrderLineAllocated`, `OrderLineBackordered`, `OrderAllocated`, `OrderPartiallyAllocated` |
| 3 | `RetryAllocation` | `POST /orders/{id}/retry-allocation` | same as `AllocateOrder`, scoped to backordered lines |
| 4 | `ReleaseOrder` | `POST /orders/{id}/release` | `OrderLineReleased`, `OrderReleased` |
| 5 | `CancelOrder` | `DELETE /orders/{id}` | `OrderCancelled` |
| 6 | `GetOrder` | `GET /orders/{id}` | — (read model) |

## 1. ReceiveOrder(lines[], allowPartialShipment)

Intake. Validates every requested line (SKU non-empty, quantity > 0),
mints an `OrderId`, and persists the order in `Received` status.

**Request:**

```json
{
  "allowPartialShipment": false,
  "lines": [
    {"sku": "SKU-1", "quantity": 2, "pathId": "pick"},
    {"sku": "SKU-2", "quantity": 1, "giftWrap": true}
  ]
}
```

**Response — `201 Created`, `Location: /orders/{id}`:**

```json
{
  "id": "ord-a1b2c3d4-0000-0000-0000-000000000001",
  "status": "Received",
  "allowPartialShipment": false,
  "lines": [
    {"lineNo": 1, "sku": "SKU-1", "quantity": 2, "pathId": "pick", "giftWrap": false, "status": "Pending"},
    {"lineNo": 2, "sku": "SKU-2", "quantity": 1, "pathId": "pick", "giftWrap": true, "status": "Pending"}
  ]
}
```

**Fails when:** a line's SKU is empty (`400`), a line's quantity is not
greater than zero (`422`).

Nothing is reserved and nothing is released here: allocation is a
separate, explicitly invoked command, so intake never depends on a
Supplier being reachable.

## 2. AllocateOrder(orderId)

Calls inventory-storage's `POST /reservations` once per `Pending` line,
using this order's id as the `demandRef`, and computes the order's promise
date from the domain lead-time policy.

**The 409-vs-everything-else distinction (BR2):** a `409` from
inventory-storage is the business fact "not enough usable stock" — that
ONE line becomes `Backordered` and allocation continues. Anything else (a
transport failure, a 5xx, any other non-2xx) fails the whole call with
`503` and no line is silently marked backordered. Reservations that
genuinely succeeded before such a failure are persisted, so re-issuing
this command resumes where it stopped.

**BR3:** with `allowPartialShipment: false` (the default), any backordered
line puts the WHOLE order in `Backordered`. With `allowPartialShipment:
true`, allocated lines stay independently releasable and the order reads
`PartiallyAllocated`.

**Response — `200 OK`:**

```json
{
  "id": "ord-a1b2c3d4-0000-0000-0000-000000000001",
  "status": "Backordered",
  "allowPartialShipment": false,
  "promiseDate": "2026-08-27T09:00:00Z",
  "lines": [
    {"lineNo": 1, "sku": "SKU-1", "quantity": 2, "pathId": "pick", "giftWrap": false, "status": "Allocated", "reservationId": "res-a1b2c3d4-0000-0000-0000-000000000009"},
    {"lineNo": 2, "sku": "SKU-2", "quantity": 1, "pathId": "pick", "giftWrap": true, "status": "Backordered"}
  ]
}
```

**Fails when:** order unknown (`404`), a downstream Supplier call fails
non-409 (`503`).

## 3. RetryAllocation(orderId)

Re-attempts allocation for every `Backordered` line only — the single
sanctioned route from `Backordered` back to `Allocated`, and the only
thing that can unblock a ship-complete order held back by BR3. Same
fail-closed rule as `AllocateOrder` applies to any non-409 failure.

**Fails when:** order unknown (`404`), the order has no backordered line
to retry (`409` `no-backordered-lines`), a downstream call fails non-409
(`503`).

## 4. ReleaseOrder(orderId)

Calls wes-work-planning's `POST /paths/{pathId}/work-units` per
`Allocated` line, sending the promise date as `cpt`, the order id as
`reference`, and the line's `sku` and `giftWrap`. The `workUnitId` is
deterministic — `{orderId}-line-{lineNo}` — so a duplicate release is a
detectable `409` upstream rather than a silently duplicated work unit.

**BR3 at the release boundary:** a ship-complete order releases NOTHING
while any line is still `Pending` or `Backordered` (`409`
`ship-complete-blocked`).

**Response — `200 OK`:**

```json
{
  "id": "ord-a1b2c3d4-0000-0000-0000-000000000001",
  "status": "Released",
  "allowPartialShipment": false,
  "promiseDate": "2026-08-27T09:00:00Z",
  "lines": [
    {"lineNo": 1, "sku": "SKU-1", "quantity": 2, "pathId": "pick", "giftWrap": false, "status": "Released", "reservationId": "res-a1b2c3d4-0000-0000-0000-000000000009"}
  ]
}
```

**Fails when:** order unknown (`404`), ship-complete order has an
unallocated line, no allocated line left to release, or no promise date
because the order was never allocated (all `409`), a downstream call
fails (`503`).

## 5. CancelOrder(orderId)

Revokes every allocated line's reservation via
`DELETE /reservations/{id}`; rejects with `ErrOrderAlreadyReleased`
(`409` `order-already-released`) if any line is already `Released` — the
boundary is checked BEFORE any reservation is revoked, so a rejected
cancellation leaves inventory-storage completely untouched.

**Response:** `204 No Content` on success.

**Fails when:** order unknown (`404`), any line already `Released`
(`409`), a downstream revoke call fails (`503`).

## 6. GetOrder(orderId)

Read-only. Returns current `Order` state, with the order-level `status`
computed from line statuses at read time — never a stored field.

**Response — `200 OK`:** same `Order` shape as the other endpoints above.

**Fails when:** order unknown (`404`).

## REST endpoint summary

| Method | Path | Use case |
| --- | --- | --- |
| `POST` | `/orders` | ReceiveOrder |
| `GET` | `/orders/{id}` | GetOrder |
| `POST` | `/orders/{id}/allocate` | AllocateOrder |
| `POST` | `/orders/{id}/retry-allocation` | RetryAllocation |
| `POST` | `/orders/{id}/release` | ReleaseOrder |
| `DELETE` | `/orders/{id}` | CancelOrder |
| `GET` | `/healthz` | Liveness probe |

The full contract, generated directly from `apis/openapi.yaml`, is on the
[API Reference](/docs/api-reference) pages.
