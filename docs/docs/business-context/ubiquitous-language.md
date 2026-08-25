---
title: Ubiquitous Language
sidebar_label: Ubiquitous Language
description: The exact vocabulary of the Order Management bounded context, with definitions and where each term lives in code.
---

# Ubiquitous Language

These are the terms this bounded context uses, with the definitions it
uses them with. They are not synonyms for each other and are not
interchangeable with the same English words used in inventory-storage or
wes-work-planning.

## Core terms

### Order

The **aggregate root**. Carries `OrderId`, `OrderLine[]`,
`AllowPartialShipment bool`, `Status`, and `PromiseDate *time.Time`.

*Code:* `internal/domain/order.Order`

### OrderLine

A single requested item within an `Order`. Carries `SKU`, `Quantity`,
`PathId` (ALWAYS the internal default `"pick"` — see the note below;
never caller-supplied), `GiftWrap bool`, `LineStatus`
(`Pending`/`Allocated`/`Backordered`/`Released`/`Cancelled`), and
`ReservationId *string` (set once allocated; needed to cancel).

*Code:* `internal/domain/order.OrderLine`

### Status (order-level)

**Always derived from line statuses — never a redundant field that can
drift out of sync.** `Received` → `Allocated` | `PartiallyAllocated` |
`Backordered` → `Released` | `PartiallyReleased` → `Cancelled` (only
reachable from a pre-release state).

*Code:* computed on every read, never stored on the aggregate or in the
`orders` table.

### Allocation

Reserving stock for one line via inventory-storage's `POST /reservations`.
This service does **not** model a local `Reservation` aggregate — it only
stores the `ReservationId` reference. inventory-storage remains the sole
owner/source-of-truth for reservation state.

*Code:* `internal/application/usecases.allocateLines` /
`allocateAndRelease`, called from `ReceiveOrder` and `RetryAllocation` —
no longer a standalone public use case (see
[ADR 0005](/docs/adr/0005-choreographed-release-via-kafka)).

### Release

Marking an allocated line `Released` (`Order.Release`, a pure domain
transition) once it clears BR3's `EnsureReleasable` check, then announcing
that fact on the enriched `OrderAllocated`/`OrderPartiallyAllocated` Kafka
integration event. **No longer a synchronous call to
wes-work-planning** — see
[ADR 0005](/docs/adr/0005-choreographed-release-via-kafka). Release is
folded into the same `allocateAndRelease` flow as Allocation, run
automatically right after a successful allocation pass inside
`ReceiveOrder`/`RetryAllocation` — never a public verb of its own.

*Code:* `internal/application/usecases.allocateAndRelease`,
`internal/adapters/outbound/kafka`

### Promise date

Computed at allocation time by a domain policy function using a
configurable per-path lead time (no live carrier integration exists —
intentionally simple, but real code with real tests, never a stub or
hardcoded field).

*Code:* `internal/domain/order.LeadTimePolicy`

### Backordered

A line-level state set when inventory-storage's `POST /reservations`
returns `409` (insufficient usable stock). This is a **business fact**,
distinct from a transport/5xx error, which is NOT a business fact and must
fail the call outright rather than silently marking a line backordered
(fail-closed on ambiguity).

*Code:* `order.LineStatusBackordered`

## Value objects

| Term | Rule |
| --- | --- |
| `OrderId` | The order's identity — this bounded context's contribution to the platform. |
| `SKU` | Non-empty string identifying a stock keeping unit. |
| `PathId` | Non-empty string identifying the wes-work-planning process path a line's work is enqueued onto. ALWAYS the internal default (`pick`) — never caller-supplied on intake (see [ADR 0005](/docs/adr/0005-choreographed-release-via-kafka)). |
| `Quantity` | Must be > 0 for every requested line. |

## States

**`OrderLine.LineStatus`**

| Status | Meaning |
| --- | --- |
| `Pending` | Received but not yet allocated. |
| `Allocated` | inventory-storage reserved stock for this line; `ReservationId` is set. |
| `Backordered` | inventory-storage returned 409 — no usable stock right now. |
| `Released` | wes-work-planning accepted this line as a work unit. |
| `Cancelled` | The line was cancelled before release. |

A `Backordered` line may transition back to `Allocated` **only** via
`RetryAllocation` — no other path.

**`Order.Status`** — see the [Core terms](#status-order-level) entry above;
the full transition diagram is on the
[Aggregates & Invariants](/docs/ddd/aggregates-and-invariants) page.

## Words that mean something different elsewhere

| Word | Here (Order Management) | Elsewhere |
| --- | --- | --- |
| **Reservation** | *not modelled* — only a `ReservationId` reference is held | `inventory-storage`: the aggregate itself, a revocable binding of quantity to demand |
| **Release** | `ReleaseOrder` — enqueuing allocated lines as work units | `wes-work-planning`: the act of accepting and scheduling a `WorkUnit` |
| **Status** | order-level, always derived from line statuses | `inventory-storage`'s `Reservation.Status` (`ACTIVE`/`CONFIRMED`/`REVOKED`/`EXPIRED`) is a completely different state machine on a completely different aggregate |
| **Order reference string** | this context's real `OrderId` | previously: `demandRef` on inventory-storage's `Reservation`, `reference` on wes-work-planning's `WorkUnit`, `Reference` on fulfillment-execution's `Task` — three independently-reinvented strings this context now supplies as one real identity |

Do not share a DTO or type across those boundaries. Translate at the
Anti-Corruption Layer instead — see
[ADR 0002](/docs/adr/0002-http-consumer-of-inventory-and-wes-not-shared-code).
