---
id: 0001-hexagonal-ports-and-adapters
slug: /adr/0001-hexagonal-ports-and-adapters
title: 0001. Hexagonal (ports & adapters) architecture
sidebar_label: 0001. Hexagonal architecture
description: ADR 0001 — adopt ports & adapters with a strict inward-only dependency rule.
---

# 0001. Hexagonal (ports & adapters) architecture

## Status

Accepted. Established with the initial implementation of this bounded context.

## Context

Order Management is a **Generic/Supporting subdomain**, not a Core one: the
value here is not a clever algorithm, it is that the order lifecycle is
*correct and legible* — that a line cannot be allocated twice, that a
ship-complete order does not leak half-released work into the warehouse, and
that a cancellation either revokes every reservation or changes nothing. The
rules are simple to state and easy to get quietly wrong.

Several forces push against keeping them clean:

- **This context is defined by its calls to other services.** Almost every
  interesting operation here — allocate, release, cancel — is a conversation
  with `inventory-storage` or `wes-work-planning`. Written naively, an HTTP
  client type ends up inside the business logic and the rules become
  untestable without a network.
- **The rules must be testable without infrastructure.** "A backordered line
  returns to Allocated only via RetryAllocation" and "cancellation is illegal
  once any line is released" each deserve a dedicated failing-path test. That
  investment only pays if tests run in milliseconds and need no Postgres, no
  Supplier, and no fixtures.
- **The order-level status is derived, not stored.** That decision (see
  [ADR 0003](./0003-ship-complete-default-and-fail-closed-allocation.md)) only
  survives if the derivation lives in one place that nothing can route around
  — not in a SQL projection some later query forgets to apply.
- **The service must run without a database.** A developer poking at the API
  and the whole `httptest` suite need a fully functional service with no
  Postgres.
- **The rest of the platform already does this.** The five sibling services
  are all hexagonal. A sixth shaped differently would be a tax on every
  reader who moves between them.

## Decision

**We will structure the service as a hexagon (ports & adapters), with a strict
inward-only dependency rule:**

> **domain depends on nothing; application depends on domain; adapters depend
> on application/domain.**

Concretely:

- `internal/domain/` is **pure Go**. No `chi`, no `pgx`, no `net/http`, no
  struct tags for serialisation. The `Order` aggregate root and its
  `OrderLine` entities enforce their own invariants on every mutation, and
  `LeadTimePolicy` is a domain policy — the promise date is a business
  decision, not a formatting concern.
- `internal/application/ports/` declares the **outbound interfaces** the
  application needs: `OrderRepo`, `EventPublisher`, `Clock`,
  `InventoryReservationClient`, `WorkReleaseClient`. They are owned by the
  application and expressed in *this* context's types — never in a Supplier's
  types (see [ADR 0002](./0002-http-consumer-of-inventory-and-wes-not-shared-code.md)).
- `internal/application/usecases/` holds **one struct per use case**, with
  collaborators as plain fields. No use case imports an adapter package.
- `internal/adapters/` implements the ports: `inbound/http` (chi, DTOs, RFC
  7807 error mapping), `outbound/inventorystorage`, `outbound/weswork`,
  `outbound/postgres`, `outbound/memory`, `outbound/events`.
- `cmd/order/main.go` is the **only** composition root — the only file that
  reads environment variables and the only file that knows both a port and its
  implementation.

`Clock` is a port for the same reason the repository is: the promise date is a
*domain* output computed from "now", so time is injected rather than read from
`time.Now()` inside a policy. That is what makes promise-date assertions exact
instead of tolerance-based.

## Consequences

### Easier

- **Invariants are unit-testable in microseconds.** The domain has no I/O, so
  every failing path (`ErrLineAlreadyAllocated`, `ErrLineNotBackordered`,
  `ErrOrderAlreadyReleased`, `ErrShipCompleteBlocked`) is a table-driven test.
  This is what made 100% domain coverage affordable.
- **The Suppliers are fakes in every test.** `AllocateOrder`'s hardest
  behaviour — "409 is a business fact, everything else is not" — is asserted
  against a scripted `InventoryReservationClient`, with no HTTP anywhere. The
  real HTTP client is then tested separately against an `httptest` server for
  wire-shape fidelity.
- **Two storage backends, no domain change.** `memory` and `postgres`
  implement the same port. `go run ./cmd/order` with no `DATABASE_URL` starts
  a working service.
- **The wire contract and the model evolve independently.** DTOs live in the
  HTTP adapter; an `orderResponse` is not an `order.Order`. The derived
  `status` field is computed at serialisation time from the aggregate.
- **The deferred work is additive.** Kafka publishing is a second
  `EventPublisher`; a real carrier-rate promise date is a second
  `LeadTimePolicy`. Neither touches a use case.

### Harder

- **More files and more indirection.** Adding a field end-to-end touches the
  aggregate, the port, the two adapters and the DTO. For a CRUD service this
  would be over-engineering; here the mapping is the boundary that keeps a
  Supplier's wire shape out of the aggregate.
- **Mapping code is real work, and it is where wire bugs live.** Each outbound
  adapter converts between this context's types and the Supplier's JSON. That
  mapping is exactly what the `httptest`-server adapter tests exist to pin
  down.
- **Cross-service orchestration is an explicit use-case concern.** `CancelOrder`
  must revoke reservations *before* it mutates the aggregate, and
  `AllocateOrder` must persist partial progress *before* it returns a hard
  error. Nothing in the compiler enforces that ordering; the tests do.
- **The rule is easy to violate under deadline pressure.** Nothing stops a use
  case importing `net/http`. The sibling services close that gap with arch-go
  fitness tests; adopting them here is deferred (see the README), and until
  then the rule is upheld by review.
