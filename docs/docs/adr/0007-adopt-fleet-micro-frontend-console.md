---
id: 0007-adopt-fleet-micro-frontend-console
slug: /adr/0007-adopt-fleet-micro-frontend-console
title: 0007. Adopt the fleet's micro-frontend console architecture (order-mgmt-mfe)
sidebar_label: 0007. Adopt fleet MFE console
description: ADR 0007 — an adoption record. This context joins the fleet-wide operator console decided in warehouse-ops-agent's ADR-0002 by owning an order-mgmt-mfe Module Federation remote in this repo's own web/ directory, consuming only this service's own REST API, plus the new GET /console/orders/{id}/lifecycle fan-out the console-bff performs against this service.
---

# 0007. Adopt the fleet's micro-frontend console architecture (`order-mgmt-mfe`)

## Status

Accepted.

## Context

`warehouse-ops-agent`'s [ADR-0002 — Micro-frontend console architecture over
per-service REST, with a thin BFF for cross-service reads](https://claudioed.github.io/warehouse-ops-agent/docs/adr/0002-micro-frontend-console-architecture)
is the fleet-wide decision: one Module Federation remote per bounded context,
owned and versioned inside that context's own repo; a separate
`warehouse-console` shell repo that composes them at runtime; a shared
`@warehouse/ui-kit` design-system repo; and, for the one genuinely
cross-cutting screen (Order Lifecycle), a thin Backend-for-Frontend hosted in
`warehouse-ops-agent` rather than as a new bounded context.

This record is this context's **adoption record**, not a re-litigation of
that decision. Order Management is a named participant in ADR-0002 on two
counts:

- It is one of the six bounded contexts expected to ship its own remote
  (`order-mgmt-mfe`) for its own operator screens (order intake, allocation
  and release status, backorder visibility).
- It is the **first hop** in the Order Lifecycle fan-out: the console-bff's
  `GET /console/orders/{id}/lifecycle` calls this service's `GET
  /orders/{id}` using the plain order id — the simplest of the three
  downstream hops ADR-0002 documents (unlike `fulfillment-execution`, this
  service needs no new lookup-by-reference endpoint; `GET /orders/{id}`
  already exists and already returns everything the BFF needs).

The forces specific to adopting this here, rather than treating ADR-0002 as
someone else's decision:

- **This context already has an existing HTTP Customer/Supplier boundary
  discipline** (this repo's own [ADR-0002 — HTTP consumer of
  inventory-storage and wes-work-planning, not shared
  code](./0002-http-consumer-of-inventory-and-wes-not-shared-code.md)).
  Adopting a browser client changes nothing about that discipline in the
  *outbound* direction — this service still calls Suppliers over HTTP only —
  but it does add a new *inbound* caller class (a browser origin, and the
  console-bff as a server-to-server caller) that the existing REST API had
  never needed to admit before.
- **No number collision, but a naming collision the reader must not
  confuse.** This repo's own `0002-...` is about the *inventory-storage
  and wes-work-planning* outbound boundary; the fleet-wide MFE decision is
  a *different* ADR-0002, numbered independently in `warehouse-ops-agent`'s
  own sequence. This record exists partly to make that distinction explicit
  in one place, since both are legitimately called "ADR-0002" depending on
  which repo you're standing in.
- **This context's own aggregate is a natural, self-contained remote.**
  `order-mgmt-mfe` needs no data this service doesn't already own and
  expose — `Order`, `OrderLine`, `Status`, `PromiseDate` are all already
  serialized on the existing REST surface (`apis/openapi.yaml`). No new
  domain-facing endpoint is required for the remote itself.
- **The console's cross-cutting screen does need one new, additive,
  side-effect-free capability from every hop it touches, and this service
  already had it.** ADR-0002 records that `inventory-storage`,
  `wes-work-planning`, and `fulfillment-execution` each needed a new
  lookup-by-reference GET endpoint. Order Management did **not** need one:
  `GET /orders/{id}` (already the identity lookup for the aggregate root)
  is exactly what the BFF's first hop needs, since the BFF has the order id
  from the start — it isn't looking up "everything with this reference," it
  already knows the aggregate's own primary key.
- **CORS was never previously a concern for this service.** Like the other
  five contexts, no browser had ever called this API. Per ADR-0002's
  "CORS: additive middleware, not a gateway" decision, this service must
  add the same `go-chi/cors` origin allow-list the other four fan-out
  targets already have.

## Decision

**Order Management adopts `warehouse-ops-agent`'s ADR-0002 in full, without
modification, as the fleet-wide micro-frontend console architecture. This
context's specific commitments under that decision are:**

1. **Own an `order-mgmt-mfe` remote in this repo's own `web/` directory.**
   A Vite + React Module Federation remote, built, tested, and released in
   this repo's own CI, on this repo's own schedule — no coordination with
   `warehouse-console` or any sibling remote required for a change scoped
   to this service's own screens. It talks only to this service's own REST
   API (`SERVICE_API_BASE`), the same as any other REST client of this
   service.
2. **Consume `@warehouse/ui-kit` for all shared presentation**, in
   particular `StatusPill` for rendering `Order.Status` /
   `OrderLine.LineStatus` — this service's domain enum is mapped once,
   centrally, so an operator sees the same visual language for "Backordered"
   here as for the equivalent state in any sibling remote. Hand-rolling a
   local status color mapping instead of consuming the shared component is
   treated as a defect against this decision, per ADR-0002's own framing.
3. **Add `go-chi/cors` middleware to this service's existing HTTP
   adapter**, gated by a `CORS_ALLOWED_ORIGINS` env var (default
   `localhost:5173` plus this remote's own dev port), permitting
   GET/POST/PUT/DELETE with no credentials — additive middleware only, not
   a gateway or reverse proxy, matching every other fan-out target in
   ADR-0002.
4. **No new endpoint is required to support the console-bff's Order
   Lifecycle fan-out.** The BFF's first hop is `GET /orders/{id}` against
   this service, which already exists, already returns the full `Order`
   aggregate (including per-line status), and needs no `demandRef`- or
   `reference`-style lookup-by-foreign-key the way the three downstream
   services did. This service's contribution to the console-bff's fan-out
   is therefore CORS-only, not a new domain-facing GET.
5. **This service's own bounded-context boundary is unchanged.** Per this
   repo's `CLAUDE.md` and its own ADR-0002 (HTTP consumer, not shared
   code), `order-mgmt-mfe` and the CORS middleware are purely additive to
   the *inbound* side of this service; they change nothing about how this
   service calls `inventory-storage` or `wes-work-planning` outbound, and
   they add no new dependency on either Supplier's code, database, or
   internal types.
6. **This is an adoption record, not a design record.** The architectural
   reasoning — why micro-frontends over one monolithic SPA, why a BFF
   instead of client-side fan-out, why the join key differs per downstream
   hop — lives in `warehouse-ops-agent`'s ADR-0002 and is not repeated or
   re-derived here. This record exists so a reader of *this* repo's ADR log
   can see that the decision was made fleet-wide, see exactly what this
   context's participation consists of, and follow the link to the source
   record rather than have it silently assumed.

## Consequences

### Easier

- **Ownership stays aligned with this service's existing REST API.** The
  same team/PR that owns `Order`'s response shape also owns the screen
  that renders it — a schema change and its UI update land together,
  exactly as ADR-0002 intends.
- **No new domain-facing endpoint, no new use case, no new port.** The
  Order Lifecycle screen's first hop is served by an endpoint this service
  already had before this record existed. The only backend change this
  record requires of this service is CORS middleware.
- **Visual consistency is enforced by `@warehouse/ui-kit`, not convention.**
  This service's `Status`/`LineStatus` values render identically to their
  counterparts in sibling remotes without this repo maintaining its own
  color mapping.
- **The existing outbound Customer/Supplier discipline (this repo's own
  ADR-0002) is completely untouched.** Nothing about adopting a browser
  client changes how this service talks to `inventory-storage` or
  `wes-work-planning`.

### Harder

- **CORS is now permanent surface on this service that never existed
  before.** The origin allow-list must be kept current as
  `order-mgmt-mfe`'s dev port, `warehouse-console`'s dev origin, or a real
  deployed console origin change — a forgotten update is a silent
  browser-side `Failed to fetch`, not a loud backend error, per ADR-0002's
  own noted risk.
- **This context now has a frontend build/release surface it did not have
  before** (`web/`'s own `package.json`, its own CI job, its own dependency
  on `@warehouse/ui-kit` as a Module Federation shared singleton) — a
  second technology stack (TypeScript/React alongside this repo's existing
  Go) for maintainers of this repo to keep current.
- **Two same-numbered ADRs across two repos can read as one decision if a
  reader isn't careful.** This repo's own `0002-http-consumer-of-inventory-
  and-wes-not-shared-code.md` and `warehouse-ops-agent`'s
  `0002-micro-frontend-console-architecture.md` are unrelated decisions
  that happen to share a number because each repo numbers its own ADR
  sequence independently. This record's title and cross-links exist
  specifically to prevent that confusion; no attempt was made to renumber
  either — ADR numbers are immutable once assigned per each repo's own ADR
  process.
- **This service now depends on `warehouse-ops-agent`'s BFF continuing to
  call `GET /orders/{id}` correctly.** No compile-time contract binds them;
  a future breaking change to this service's `Order` response shape must
  be coordinated the same way any other REST API consumer's would be — the
  console-bff is simply another Customer of this service's existing HTTP
  contract, not a special case.

## References

- [warehouse-ops-agent ADR-0002 — Micro-frontend console architecture over
  per-service REST, with a thin BFF for cross-service
  reads](https://claudioed.github.io/warehouse-ops-agent/docs/adr/0002-micro-frontend-console-architecture)
  (the fleet-wide decision this record adopts)
- [This repo's own ADR-0002 — HTTP consumer of inventory-storage and
  wes-work-planning, not shared
  code](./0002-http-consumer-of-inventory-and-wes-not-shared-code.md) (the
  pre-existing outbound boundary this record leaves unchanged)
- [Context Map](../ecosystem/context-map.md) (updated by this record to add
  the new inbound console-bff edge)
