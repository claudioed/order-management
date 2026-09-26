---
title: Use Cases
sidebar_label: Use Cases
description: The six application-layer use cases, their HTTP and Kafka triggers, and their request/response shapes.
---

# Use Cases

Six use cases, one struct each, in `internal/application/usecases`. Each
depends only on the domain and on `application/ports` — never on an
adapter. Dependencies are plain struct fields, wired once in
`cmd/order/main.go`.

Per [ADR 0005](/docs/adr/0005-choreographed-release-via-kafka), allocation
and release are no longer separate public use cases (`AllocateOrder` and
`ReleaseOrder` are gone). They are folded into a single shared function,
`allocateAndRelease`, that `ReceiveOrder` and `RetryAllocation` both call —
a caller expresses ONE intent ("place an order", or "retry a stuck one"),
and this service internally attempts allocation and then release as one
flow.

| # | Use case | HTTP | Emits |
| --- | --- | --- | --- |
| 1 | `ReceiveOrder` | `POST /orders` | `OrderReceived`, then (best-effort) the allocation/release events below |
| 2 | `RetryAllocation` | `POST /orders/{id}/retry-allocation` | `OrderLineAllocated`, `OrderLineBackordered`, `OrderAllocated`/`OrderPartiallyAllocated` |
| 3 | `CancelOrder` | `DELETE /orders/{id}` | `OrderCancelled` |
| 4 | `GetOrder` | `GET /orders/{id}` | — (read model) |
| 5 | `ReleaseHeldOrder` | `POST /orders/{id}/release` | `OrderAllocated`/`OrderPartiallyAllocated` for the released lines |
| 6 | `RepromiseOrder` | Kafka: `TaskCPTMissed`/`PackageManifested` on `warehouse.fulfillment.events` | `OrderRepromised` |

## 1. ReceiveOrder(lines[], allowPartialShipment, releaseOnAllocation, requiredShipBy)

Intake. Validates every requested line (SKU non-empty, quantity > 0),
resolves each line's process path with `PathSelectionPolicy` and rejects
an inactive path (`400` `unknown-process-path`) or an ineligible line
(`422` `line-ineligible-for-resolved-path`) before anything is persisted
(ADR 0013/0016), mints an `OrderId`, persists the order in `Received` status, and publishes
`OrderReceived` **unconditionally** — a caller always sees that fact
regardless of what happens next.

**Request:**

```json
{
  "allowPartialShipment": false,
  "lines": [
    {"sku": "SKU-1", "quantity": 2},
    {"sku": "SKU-2", "quantity": 1, "giftWrap": true}
  ]
}
```

`pathId` is never part of this request — see
[Ubiquitous Language](/docs/business-context/ubiquitous-language): the
path is resolved internally (today always the default `pick`).

Two optional fields come from
[ADR 0020](/docs/adr/0020-network-originated-demand-hold-and-deadline-feasibility):
`releaseOnAllocation` (default `true`; `false` holds the order after
allocation — it must then be ship-complete, else `422`) and
`requiredShipBy` (the promise is constrained to a CPT window at or before
that instant; if none exists the order is still received and allocated,
but the response carries no `promiseDate`).

Immediately afterward, in the SAME call, `ReceiveOrder` attempts
allocation-then-release (see `allocateAndRelease` below) as a best-effort
next step. **A hard (non-business) failure in that step never fails
`ReceiveOrder` itself** — the order genuinely was received before
allocation was ever attempted, and telling the caller otherwise would be a
lie. The failure is still visible via the existing
`OrderAllocationPartiallyFailed` event whenever any line was genuinely
allocated first.

**Response — `201 Created`, `Location: /orders/{id}` — reflects whatever the
allocation pass achieved:**

```json
{
  "id": "ord-a1b2c3d4-0000-0000-0000-000000000001",
  "status": "Released",
  "allowPartialShipment": false,
  "releaseOnAllocation": true,
  "promiseDate": "2026-08-27T09:00:00Z",
  "lines": [
    {"lineNo": 1, "sku": "SKU-1", "quantity": 2, "pathId": "pick", "giftWrap": false, "status": "Released", "reservationId": "res-a1b2c3d4-0000-0000-0000-000000000009"},
    {"lineNo": 2, "sku": "SKU-2", "quantity": 1, "pathId": "pick", "giftWrap": true, "status": "Released", "reservationId": "res-a1b2c3d4-0000-0000-0000-000000000010"}
  ]
}
```

**Fails when:** a line's SKU is empty or the order has no lines (`400`),
the resolved path is not active (`400`), a line's quantity is not greater
than zero or a line is ineligible for its path (`422`), a held order allows
partial shipment (`422`), or the order itself could not be persisted
(`500`) — never because the implicit allocation pass hit a hard failure.

## 2. `allocateAndRelease` — the shared allocate-then-release flow

Not a public use case, but the core of this redesign
(`internal/application/usecases/allocation.go`), shared by `ReceiveOrder`
and `RetryAllocation` exactly the way `allocateLines` was already shared
between the pre-redesign `AllocateOrder` and `RetryAllocation`.

1. Calls inventory-storage's `POST /reservations` once per eligible line,
   using this order's id as the `demandRef`.
   **The 409-vs-everything-else distinction (BR2):** a `409` is the
   business fact "not enough usable stock" — that ONE line becomes
   `Backordered` and allocation continues. Anything else is ambiguous, not
   a business fact: the pass hard-fails and no line is silently marked
   backordered.
2. On success, computes the promise (`PromisePolicy`: capability-derived
   CPT window, per shipment group when partial shipment is allowed, lead
   time as fallback — ADR 0014/0017; `FeasibleBy` when `requiredShipBy`
   is set — ADR 0020). If the order is held (`releaseOnAllocation=false`)
   it stops here. Otherwise it checks `EnsureReleasable()`
   (**BR3**: a ship-complete order releases NOTHING while any line is
   still unallocated). When it passes, every currently-`Allocated` line is
   released via the pure domain transition `Order.Release` — no
   synchronous call to any Supplier.
3. Saves the order ONCE, covering both allocation and release state.
4. Publishes `OrderAllocated` or `OrderPartiallyAllocated` — now carrying
   a `lines[]` payload of exactly what was released this pass — which is
   also forwarded to Kafka as an integration event (see
   [Domain Events](/docs/ddd/domain-events)).

## 3. RetryAllocation(orderId)

Re-attempts allocation for every `Backordered` line only — the single
sanctioned route from `Backordered` back to `Allocated`. On success, it
ALSO attempts release in the same call via `allocateAndRelease`. UNLIKE
`ReceiveOrder`'s best-effort treatment, this is an explicit, on-purpose
operator/recovery action: a hard failure here DOES propagate to its
caller.

**Fails when:** order unknown (`404`), the order has no backordered line
to retry (`409` `no-backordered-lines`), a downstream call fails non-409
(`503`).

## 4. CancelOrder(orderId)

Revokes every allocated line's reservation via
`DELETE /reservations/{id}`; rejects with `ErrOrderAlreadyReleased`
(`409` `order-already-released`) if any line is already `Released` — the
boundary is checked BEFORE any reservation is revoked, so a rejected
cancellation leaves inventory-storage completely untouched.

**Response:** `204 No Content` on success.

**Fails when:** order unknown (`404`), any line already `Released`
(`409`), a downstream revoke call fails (`503`).

## 5. GetOrder(orderId)

Read-only. Returns current `Order` state, with the order-level `status`
computed from line statuses at read time — never a stored field.

**Response — `200 OK`:** same `Order` shape as the other endpoints above.

**Fails when:** order unknown (`404`).

## 6. ReleaseHeldOrder(orderId)

The second half of the ADR 0020 hold. Reuses `allocateAndRelease`'s
release leg with an empty set of lines to allocate, so BR3, the promise
recompute and the outcome events are exactly those of every other
release. Idempotent for a held order whose lines are already released
(`200`, no state change, no event); an order that was never held answers
`409` `order-not-held`. The hold flag is never cleared — it records what
the order was received as.

**Fails when:** order unknown (`404`), not held (`409` `order-not-held`),
ship-complete with an unallocated line (`409` `ship-complete-blocked`).

## 7. RepromiseOrder (Kafka-driven)

Not reachable over HTTP. The `RepromiseConsumer`
(`internal/adapters/inbound/kafka`, group `order-management-repromise`)
decodes `TaskCPTMissed`/`PackageManifested` from
`warehouse.fulfillment.events`, maps the work-unit id back to an order line
(`ParseWorkUnitID`), and `RepromiseOrder` recomputes that line's
shipment-group promise, publishing `OrderRepromised` only if it moved
([ADR 0018](/docs/adr/0018-repromise-order-consumer-and-order-repromised)).
Each inbound `event_id` is processed at most once
(`ports.RepromiseProcessedEvents`).

## REST endpoint summary

| Method | Path | Use case |
| --- | --- | --- |
| `POST` | `/orders` | ReceiveOrder (allocates + releases automatically) |
| `GET` | `/orders/{id}` | GetOrder |
| `POST` | `/orders/{id}/retry-allocation` | RetryAllocation (retries + releases automatically) |
| `POST` | `/orders/{id}/release` | ReleaseHeldOrder (held orders only, ADR 0020) |
| `DELETE` | `/orders/{id}` | CancelOrder |
| `GET` | `/healthz` | Liveness probe |

`POST /orders/{id}/allocate` no longer exists, and the pre-ADR-0005
general-purpose `POST /orders/{id}/release` is gone — see
[ADR 0005](/docs/adr/0005-choreographed-release-via-kafka). The current
`/release` path is ADR 0020's held-order release only.

The full contract, generated directly from `apis/openapi.yaml`, is on the
[API Reference](/docs/api-reference) pages.
