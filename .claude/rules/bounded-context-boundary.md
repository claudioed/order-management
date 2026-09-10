# Bounded-context boundary (NON-NEGOTIABLE)

This service is a **pure HTTP consumer** of `inventory-storage`'s
already-published, already-stable REST API, and reacts to
`wes-work-planning` only indirectly via Kafka choreography (ADR-0005 — see
`adrs.md`). It is a **separate Go module in a separate repository**:

- MUST NOT import any Go package from `inventory-storage` or
  `wes-work-planning`.
- Gets **no write access** to either service's internal aggregates
  (`Reservation`, `WorkPool`, `WorkUnit`, etc.) — only to their published
  HTTP/Kafka contracts.
- Order Management is the **Customer**; `inventory-storage` and
  `wes-work-planning` are the **Suppliers / Open Host Services** — the same
  directional Customer/Supplier relationship the fleet's DDD reference docs
  use for WMS → WES.
- Do not weaken this boundary for convenience: no shared Go module, no
  direct DB access to either service's schema, ever.

This build is **100% additive** from `inventory-storage`'s and
`wes-work-planning`'s point of view — neither repository is modified at all
by anything in this repo.

## What changed with ADR-0005 (read before touching release logic)

The original v1 called `wes-work-planning`'s `POST /paths/{pathId}/work-units`
synchronously from a `ReleaseOrder` use case. That is **gone**:
`internal/adapters/outbound/weswork/` and `ports.WorkReleaseClient` were
deleted entirely. Release is now announced as a Kafka integration event
(`OrderAllocated`/`OrderPartiallyAllocated`) on
`warehouse.order-management.events`; `wes-work-planning`'s own consumer
reacts to it independently and derives the deterministic work-unit id
`{orderID}-line-{lineNo}` itself — that formula is a **frozen,
independently-reconstructed contract, never transmitted on the wire**, and
must match byte-for-byte on both sides or idempotent redelivery breaks.

`inventory-storage` remains a synchronous Supplier — allocation still calls
`POST /reservations` directly and waits for the response.
