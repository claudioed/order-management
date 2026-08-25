# Project: Order Management (Generic/Supporting Bounded Context — order intake, allocation, release)

The missing upstream Open Host Service for the warehouse-systems fleet. Owns
**Order** and **OrderLine** as first-class aggregates: intake, per-line stock
allocation (via inventory-storage), promise-date calculation, release of
allocated work (via wes-work-planning), and cancellation up to the release
boundary. Before this context existed, "an order" was just an unowned,
unvalidated string (`OrderRef`/`DemandRef`/`Reference`) independently
reinvented by three different services.

Source of truth for the design: `/Users/claudioed/warehouse-systems/.hermes/plans/2026-08-25_023800-order-management.md`
and the fleet's shared reference docs at `/Users/claudioed/docs/amazon-fulfillment-ddd.md`
and `/Users/claudioed/warehouse-systems-ddd.md`. Honor their ubiquitous
language and DDD strategic classifications.

## Bounded-context boundary (NON-NEGOTIABLE — read this before writing any code)

This service is a **pure HTTP consumer** of `inventory-storage`'s and
`wes-work-planning`'s already-published, already-stable REST APIs. It is a
**separate Go module in a separate repository** — it MUST NOT import any Go
package from those repos, and it gets no write access to their internal
aggregates (`Reservation`, `WorkPool`, `WorkUnit`, etc.), only to their
published HTTP contracts below. Order Management is the **Customer**;
inventory-storage and wes-work-planning are the **Suppliers / Open Host
Services** — the same directional Customer/Supplier relationship the fleet's
DDD reference docs already use for WMS → WES. Do not weaken this boundary
for convenience (e.g. no shared Go module, no direct DB access to either
service's schema).

## Architecture (NON-NEGOTIABLE — identical shape to the other five services)

Hexagonal / Ports & Adapters. Strict dependency rule: **domain depends on
nothing; application depends on domain; adapters depend on
application/domain.** No framework, HTTP, or SQL types in the domain layer.

```
cmd/order/                    main.go — composition root
internal/
  domain/
    order/                    Order aggregate, OrderLine, Status, invariants
    shared/                   value objects: OrderId, SKU, PathId, events, errors
  application/
    ports/                    OUT: OrderRepo, EventPublisher, Clock,
                               InventoryReservationClient, WorkReleaseClient
    usecases/                 ReceiveOrder, AllocateOrder, RetryAllocation,
                               ReleaseOrder, CancelOrder, GetOrder
  adapters/
    inbound/http/             chi handlers, DTOs, RFC7807 error mapping
    outbound/inventorystorage/  HTTP client: POST /reservations,
                                 DELETE /reservations/{id}
    outbound/weswork/           HTTP client: POST /paths/{pathId}/work-units
    outbound/postgres/        pgxpool repo + migrations
    outbound/memory/          in-memory repo for tests
    outbound/events/          log publisher (Kafka-ready interface, unused in v1)
migrations/                   golang-migrate SQL files
apis/openapi.yaml
```

## Ubiquitous Language (use these exact names)

- **Order** — the aggregate root. `OrderId`, `OrderLine[]`,
  `AllowPartialShipment bool`, `Status`, `PromiseDate *time.Time`.
- **OrderLine** — `SKU`, `Quantity`, `PathId` (defaults to `"pick"` if not
  supplied — a documented v1 simplification, same spirit as
  fulfillment-execution's `path_id`-prefix convention), `GiftWrap bool`,
  `LineStatus` (`Pending`/`Allocated`/`Backordered`/`Released`/`Cancelled`),
  `ReservationId *string` (set once allocated; needed to cancel).
- **Status** (order-level, ALWAYS derived from line statuses — never a
  redundant field that can drift out of sync): `Received` →
  `Allocated` | `PartiallyAllocated` | `Backordered` → `Released` |
  `PartiallyReleased` → `Cancelled` (only reachable from a pre-release
  state).
- **Allocation** — reserving stock for one line via inventory-storage's
  `POST /reservations`. This service does NOT model a local Reservation
  aggregate — it only stores the `ReservationId` reference.
  inventory-storage remains the sole owner/source-of-truth for reservation
  state.
- **Release** — enqueuing an allocated line as work via wes-work-planning's
  `POST /paths/{pathId}/work-units`.
- **Promise date** — computed at allocation time by a domain policy
  function using a configurable per-path lead time (no live carrier
  integration exists — intentionally simple, but real code with real
  tests, never a stub/hardcoded field).
- **Backordered** — a line-level state set when inventory-storage's
  `POST /reservations` returns 409 (insufficient usable stock). This is a
  BUSINESS FACT, distinct from a transport/5xx error, which is NOT a
  business fact and must fail the call outright rather than silently
  marking a line backordered (fail-closed on ambiguity).

## Aggregates & invariants (enforce in domain, unit-tested — each needs a failing-path test)

- **Order**: cannot allocate the same line twice; cannot release a line
  that isn't `Allocated`; cannot cancel once ANY line is `Released`
  (`ErrOrderAlreadyReleased`); order-level `Status` is always computed from
  line statuses, never stored redundantly.
- **OrderLine**: `Quantity` must be > 0; `SKU` must be non-empty; a
  `Backordered` line may transition back to `Allocated` ONLY via
  `RetryAllocation` — no other path.
- **BR3 (ship-complete default)**: `AllowPartialShipment=false` (the
  default): if ANY line ends up `Backordered` during `AllocateOrder`, the
  WHOLE order's status is `Backordered` (no line proceeds to release) until
  a human/caller issues `RetryAllocation`. `AllowPartialShipment=true`:
  allocated lines are independently eligible for release; order status
  becomes `PartiallyAllocated`.
- **BR6 (cancellation boundary)**: `CancelOrder` is legal ONLY while no
  line has reached `Released`. Legal cancellation revokes every allocated
  line's reservation via `DELETE /reservations/{id}` on inventory-storage.
  Once ANY line is `Released`, v1 does NOT claw back released work — this
  is a documented, deliberate known gap (same honesty pattern as
  inventory-storage's ADR-0003 "no expiry sweeper" section), not an
  oversight to silently paper over.

## Domain events (past tense — local log publisher only in v1, no Kafka)

OrderReceived, OrderLineAllocated, OrderLineBackordered, OrderAllocated,
OrderPartiallyAllocated, OrderLineReleased, OrderReleased, OrderCancelled.

## Use cases (application layer)

1. ReceiveOrder(lines[], allowPartialShipment) -> Order in Received status
2. AllocateOrder(orderId) -> calls inventory-storage POST /reservations per
   line; 409 -> that line Backordered (business fact); any other
   non-2xx/transport error -> the whole call fails, nothing is silently
   marked backordered
3. RetryAllocation(orderId) -> re-attempts allocation for every Backordered
   line only
4. ReleaseOrder(orderId) -> calls wes-work-planning POST
   /paths/{pathId}/work-units per Allocated line (cpt=PromiseDate,
   reference=orderId, sku, giftWrap); rejects a line that isn't Allocated
5. CancelOrder(orderId) -> revokes every allocated line's reservation via
   DELETE /reservations/{id}; rejects with ErrOrderAlreadyReleased if any
   line is already Released
6. GetOrder(orderId) -> current Order state (read)

## REST API (inbound adapter)

- POST   /orders                          -> ReceiveOrder
- GET    /orders/{id}                     -> GetOrder
- POST   /orders/{id}/allocate            -> AllocateOrder
- POST   /orders/{id}/retry-allocation    -> RetryAllocation
- POST   /orders/{id}/release             -> ReleaseOrder
- DELETE /orders/{id}                     -> CancelOrder
- GET    /healthz

JSON DTOs live in the http adapter; never leak domain structs.

## Outbound HTTP contracts this service consumes (EXACT — verified against the live repos, do not guess a different shape)

**inventory-storage** (base URL via env `INVENTORY_STORAGE_BASE_URL`):
- `POST /reservations` — request `{"sku":"...","quantity":N,"demandRef":"..."}`
  (use this Order's `OrderId` as `demandRef`) — 201 response:
  `{"id":"...","sku":"...","quantity":N,"demandRef":"...","status":"...","allocations":[{"stockUnitId":"...","quantity":N}],"expiresAt":"..."}`.
  A 409 response (RFC 7807 problem+json) means insufficient usable stock —
  map to `Backordered` for that line. Any other non-2xx status or a
  transport error must propagate as a hard failure of `AllocateOrder` —
  never silently treated as backordered.
- `DELETE /reservations/{id}` — 204 on success. Used by `CancelOrder`.

**wes-work-planning** (base URL via env `WES_WORK_PLANNING_BASE_URL`):
- `POST /paths/{pathId}/work-units` — request
  `{"workUnitId":"...","cpt":"RFC3339","reference":"...","sku":"...","giftWrap":bool}`
  — 201 response:
  `{"id":"...","pathId":"...","cpt":"RFC3339","reference":"...","state":"...","giftWrap":bool}`.
  `sku` and `giftWrap` are both optional fields already present on this
  endpoint today — no changes to wes-work-planning are needed or permitted.

Both outbound adapters follow the SAME permissive-by-default env-selected
pattern already used three times elsewhere in this fleet
(inventory-storage→facilitylayout, wes-work-planning→inventory-storage,
fulfillment-execution→inventory-storage): `MODE=http|permissive` per
adapter, defaulting to `permissive` so unit tests never hit the network.
UNLIKE those other adapters (which fail-open because classification lookups
are soft/optional), calling `AllocateOrder`/`ReleaseOrder` while wired to a
permissive (no-op) client is NOT a soft concern — allocating real stock or
releasing real work must never silently "succeed" against a no-op. In
permissive mode, `AllocateOrder`/`ReleaseOrder` must return a clear
`ErrDownstreamNotConfigured` rather than fabricating a fake success. Only
`http` mode is suitable for any real integration test or deployment.

## Tech & standards

- Go 1.26, modules. Module path: `github.com/claudioed/order-management`.
- chi (github.com/go-chi/chi/v5), pgx/v5 + pgxpool, golang-migrate SQL migrations.
- Config via env (DATABASE_URL, HTTP_ADDR, INVENTORY_STORAGE_BASE_URL,
  WES_WORK_PLANNING_BASE_URL, INVENTORY_STORAGE_MODE=http|permissive,
  WES_WORK_PLANNING_MODE=http|permissive — both default to `permissive`).
  docker-compose.yml for Postgres 16.
- Typed domain errors mapped to HTTP status in the adapter, RFC 7807
  problem+json for every error response (identical shape to the other five
  services — copy their `problemDetails` struct and error-mapping pattern
  exactly).
- Table-driven tests: domain + application (in-memory adapter + fake HTTP
  clients for the two outbound ports — never hit a real network in unit
  tests); one httptest per REST endpoint covering at least one success and
  one error path each.
- gofmt/go vet clean; every package has a doc comment.
- golangci-lint: copy `.golangci.yml` verbatim from
  `/Users/claudioed/warehouse-systems/inventory-storage/.golangci.yml`.

## v1 scope — explicitly deferred (do NOT build these; document them, don't skip silently)

- Helm chart / warehouse-infra kind-cluster wiring.
- Gremlins mutation testing gate.
- godog/BDD acceptance tests.
- MCP inbound adapter.
- Kafka integration events / async publishing (log publisher only).
- Real carrier-rate promise-date calculation (a configurable static lead
  time is correct for v1).
- Any change to inventory-storage or wes-work-planning — this build must
  be 100% additive from THEIR point of view; they are not touched at all.

Write a short "Deferred (v1)" section in the README listing these
explicitly, so a reader never mistakes an absence for an oversight.

## Local quality gate (mirror the other five repos' Makefile/lefthook shape, minus DB/mutation-dependent targets)

Targets: `build` (go build ./...), `vet`, `fmt-check` (gofmt -l . empty),
`lint` (golangci-lint run ./...), `test` (go test ./... -race), `coverage`
(gate: 90% on `./internal/domain/...,./internal/application/...`), `check`
(fmt-check + vet + build + lint + test), `check-all` (check + coverage).
NO `integration`/`mutation`/`mutation-full`/`bdd` targets in v1 — those
require Postgres/gremlins/godog setup that is out of scope this round.

lefthook.yml: pre-commit runs fmt-check/vet/lint; pre-push runs `check`.
Copy the structural shape from
`/Users/claudioed/warehouse-systems/inventory-storage/lefthook.yml`, dropping
anything mutation/integration-specific.

GitHub Actions CI (`.github/workflows/ci.yml`): `lint` and `test` jobs only
in v1 (mirror the job shape/style of the other repos' workflows for these
two jobs specifically — same golangci-lint version pin `v2.13.1`, same Go
setup action version). Do NOT add `mutation`, `bdd`, `helm-lint`,
`docker-publish`, `release`, or `integration` jobs — those are deferred.

## Definition of done

- `go build ./...`, `go vet ./...`, `go test ./... -race` all green.
- `golangci-lint run ./...` reports 0 issues.
- Coverage on `internal/domain/...,internal/application/...` >= 90%
  (verify with `go test -race -coverprofile=coverage.out -coverpkg=./internal/domain/...,./internal/application/... ./...`
  then `go tool cover -func=coverage.out`).
- Every named invariant in "Aggregates & invariants" above has a dedicated
  failing-path test.
- Every REST endpoint has at least one httptest success case and one error
  case.
- README.md: run steps (compose up, migrate, go run), curl example per
  endpoint, hexagonal-layering note, and the "Deferred (v1)" section.
- `apis/openapi.yaml` covers all 6 endpoints plus the RFC 7807 error schema
  (spectral-lint clean if spectral is available locally; if not installed,
  note that in your summary rather than skipping validation silently).
- Four ADRs under `docs/docs/adr/` (Docusaurus-style frontmatter matching
  the other repos' ADR files exactly — check
  `/Users/claudioed/warehouse-systems/inventory-storage/docs/docs/adr/0001-hexagonal-ports-and-adapters.md`
  for the exact frontmatter shape to copy):
  1. `0001-hexagonal-ports-and-adapters.md`
  2. `0002-http-consumer-of-inventory-and-wes-not-shared-code.md` — the
     bounded-context-boundary decision above, in ADR form.
  3. `0003-ship-complete-default-and-fail-closed-allocation.md` — BR2/BR3
     in ADR form.
  4. `0004-cancellation-boundary-at-release.md` — BR6 in ADR form,
     including the documented known-gap section.
- Do NOT attempt a full Docusaurus site build in v1 unless time permits
  trivially — the ADR markdown files existing with correct content matters
  more than a working `npm run build` for this first pass. Note the
  Docusaurus site's status honestly in your final summary either way.

## Git workflow

- Work on a branch named `feature/order-management-v1` off `develop`.
- `develop` branch must exist (create it from `main`/initial commit if this
  is the first commit to the repo).
- Commit frequently with clear messages.
- Push and open a PR into `develop` via `gh pr create --base develop`.
- Do NOT merge the PR yourself — leave it open for independent review.
- Do NOT force-push over history once pushed.
