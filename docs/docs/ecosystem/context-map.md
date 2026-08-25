---
title: Context Map
sidebar_label: Context Map
description: Order Management's Customer/Supplier relationship to inventory-storage and wes-work-planning — the exact HTTP contracts consumed and the boundary that is never crossed.
---

# Context Map

Order Management is the sixth service in the `warehouse-systems` platform,
and the only one so far whose only integrations are **synchronous HTTP
calls it makes outward** — it has no Kafka publisher, no Kafka consumer,
and no inbound integration from any sibling service.

## The platform, with this context's edges highlighted

```mermaid
flowchart TB
    subgraph NEW["Upstream front door"]
        OM["<b>order-management</b><br/>Generic/Supporting subdomain<br/>Order · OrderLine"]
    end
    subgraph WMS["WMS tier"]
        INV["<b>inventory-storage</b><br/>Core subdomain"]
    end
    subgraph WES["WES tier"]
        WP["<b>wes-work-planning</b><br/>Core subdomain"]
        FE["<b>fulfillment-execution</b><br/>Core subdomain"]
        WM["<b>workforce-management</b><br/>Supporting subdomain"]
    end
    subgraph GENERIC["Generic subdomain"]
        FL["<b>facility-layout</b>"]
    end

    OM ==>|"HTTP POST /reservations<br/>HTTP DELETE /reservations/{id}"| INV
    OM ==>|"HTTP POST /paths/{pathId}/work-units"| WP
    INV -->|"warehouse.inventory.events"| WP
    WM -->|"warehouse.workforce.events"| WP
    WP -->|"warehouse.work-planning.events"| FE
    FE -->|"warehouse.fulfillment.events"| WP

    classDef this fill:#1d4ed8,stroke:#1e3a8a,color:#fff,stroke-width:4px;
    classDef core fill:#1e3a8a,stroke:#1e293b,color:#fff;
    classDef supp fill:#6d28d9,stroke:#4c1d95,color:#fff;
    classDef gen fill:#475569,stroke:#94a3b8,color:#fff,stroke-dasharray: 6 4;
    class OM this;
    class INV,WP,FE core;
    class WM supp;
    class FL gen;
```

**Bold edges are this context's own live HTTP calls.** Thin edges are the
pre-existing Kafka topics between the other five services — this context
does not participate in any of them.

## This service's edges

### → `inventory-storage` (live, synchronous HTTP)

**Customer/Supplier.** Order Management is the Customer; inventory-storage
is the Supplier / Open Host Service. Per `CLAUDE.md`'s "Outbound HTTP
contracts" section, the exact contract consumed is:

| Call | Request | Response | Used by |
| --- | --- | --- | --- |
| `POST /reservations` | `{"sku":"...","quantity":N,"demandRef":"..."}` (this order's `OrderId` as `demandRef`) | `201`: `{"id":"...","sku":"...","quantity":N,"demandRef":"...","status":"...","allocations":[...],"expiresAt":"..."}` | `AllocateOrder`, `RetryAllocation` |
| `DELETE /reservations/{id}` | — | `204` on success | `CancelOrder` |

A `409` from `POST /reservations` means insufficient usable stock and maps
to `Backordered` for that line; any other non-2xx status or a transport
error propagates as a hard failure — never silently treated as
backordered.

### → `wes-work-planning` (live, synchronous HTTP)

**Customer/Supplier**, the same directional relationship the fleet's DDD
reference docs already use for WMS → WES, now applied one layer upstream.

| Call | Request | Response | Used by |
| --- | --- | --- | --- |
| `POST /paths/{pathId}/work-units` | `{"workUnitId":"...","cpt":"RFC3339","reference":"...","sku":"...","giftWrap":bool}` | `201`: `{"id":"...","pathId":"...","cpt":"RFC3339","reference":"...","state":"...","giftWrap":bool}` | `ReleaseOrder` |

`sku` and `giftWrap` are both optional fields already present on that
endpoint today — **no changes to wes-work-planning were needed or made**
for this context to exist.

## What this context explicitly does NOT do

Per `CLAUDE.md`'s "Bounded-context boundary" section (NON-NEGOTIABLE):

- **Imports no Go code from either Supplier.** This is a separate Go
  module in a separate repository. Not a type, not a constant, not a test
  helper.
- **Has no write access to their internal aggregates** — `Reservation`,
  `WorkPool`, `WorkUnit`, etc. — only to their published HTTP contracts
  above.
- **Has no database access to either Supplier's schema.**
- **This build is 100% additive from their point of view.** Neither
  repository is modified at all for this context to exist.

The only piece of Supplier state this context retains is the
`ReservationId` reference, held solely so `CancelOrder` can call
`DELETE /reservations/{id}` later. See
[ADR 0002](/docs/adr/0002-http-consumer-of-inventory-and-wes-not-shared-code)
for the full reasoning behind this boundary.

## No Kafka in v1

Per `CLAUDE.md`'s deferred scope, this context has no Kafka integration —
domain events are published through a local log publisher only (see
[Domain Events](/docs/ddd/domain-events)). Both outbound calls above are
therefore request/response HTTP, not asynchronous messages: `AllocateOrder`
and `ReleaseOrder` block on the Supplier's response and surface a `503`
with an RFC 7807 body if it cannot be reached or answers ambiguously.

## Failure-mode discipline (`MODE=http|permissive`)

Both outbound adapters follow the same permissive-by-default,
env-selected pattern already used three times elsewhere in this fleet
(inventory-storage → facility-layout, wes-work-planning →
inventory-storage, fulfillment-execution → inventory-storage):
`MODE=http|permissive` per adapter, defaulting to `permissive` so unit
tests never hit the network.

**Unlike those other adapters**, which fail-open because the lookups they
guard are soft/optional, calling `AllocateOrder`/`ReleaseOrder` while
wired to a permissive (no-op) client is NOT a soft concern here —
allocating real stock or releasing real work must never silently "succeed"
against a no-op. In permissive mode, both use cases return a clear
`ErrDownstreamNotConfigured` rather than fabricating a fake success. Only
`http` mode is suitable for any real integration test or deployment.
