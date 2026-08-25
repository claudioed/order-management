# Order Management

> **⚠️ Study project.** This repository is an educational exercise in
> Domain-Driven Design applied to warehouse management/execution systems. It
> follows real industry-standard patterns and terminology (WMS/WES/WCS,
> chaotic storage, CloudEvents, RFC 7807, hexagonal architecture) but is
> **not a production system** and is **not affiliated with, endorsed by, or
> representative of Amazon or any other company**.

Order intake, allocation, promise-date calculation, and release — the
missing upstream Open Host Service for the `warehouse-systems` fleet
(`inventory-storage`, `wes-work-planning`, `fulfillment-execution`,
`workforce-management`, `facility-layout`).

This context owns **Order** and **OrderLine** as first-class, validated
aggregates — closing a gap where "an order" was previously just an
unowned, unvalidated string (`OrderRef` / `DemandRef` / `Reference`)
threaded independently through three other services.

## Bounded-context boundary (read this first)

This service is a **pure HTTP consumer** of `inventory-storage`'s and
`wes-work-planning`'s already-published REST APIs. It imports no Go code
from either repository and has no write access to their internal
aggregates (`Reservation`, `WorkPool`, etc.) — only to their published,
versioned HTTP contracts. Order Management is the **Customer**;
`inventory-storage` and `wes-work-planning` are the **Suppliers / Open Host
Services**, the same directional relationship the fleet's DDD reference
docs already use for WMS → WES.

This build is **100% additive** from those services' point of view: neither
repository is modified at all. See
[ADR 0002](docs/docs/adr/0002-http-consumer-of-inventory-and-wes-not-shared-code.md).

## Architecture

Hexagonal (ports & adapters), with a strict inward-only dependency rule —
**domain depends on nothing; application depends on domain; adapters depend
on application/domain** — identical in shape to the other five services in
the fleet. See
[ADR 0001](docs/docs/adr/0001-hexagonal-ports-and-adapters.md).

```
cmd/order/                        main.go — the only composition root
internal/
  domain/
    order/                        Order aggregate, OrderLine, statuses,
                                  invariants, LeadTimePolicy
    shared/                       OrderId, SKU, PathId, domain events, errors
  application/
    ports/                        OUT: OrderRepo, EventPublisher, Clock,
                                  InventoryReservationClient, WorkReleaseClient
    usecases/                     ReceiveOrder, AllocateOrder, RetryAllocation,
                                  ReleaseOrder, CancelOrder, GetOrder
  adapters/
    inbound/http/                 chi handlers, DTOs, RFC 7807 error mapping
    outbound/inventorystorage/    POST /reservations, DELETE /reservations/{id}
    outbound/weswork/             POST /paths/{pathId}/work-units
    outbound/postgres/            pgxpool repo + golang-migrate runner
    outbound/memory/              in-memory repo + clocks for tests
    outbound/events/              log publisher (Kafka-ready interface)
migrations/                       golang-migrate SQL files
apis/openapi.yaml                 OpenAPI 3.0 spec for all six endpoints
docs/docs/adr/                    Architecture Decision Records
```

The domain layer is pure Go: no `chi`, no `pgx`, no `net/http`. The
order-level `Status` is **derived from the line statuses on every read** and
is never stored, so it cannot drift out of sync with the lines it
summarises.

## Business rules worth knowing before you read the code

- **BR2 — fail closed on ambiguity.** A `409` from inventory-storage's
  `POST /reservations` is the business fact "no usable stock" and backorders
  that one line. A transport failure, a 5xx, or any other non-2xx is *not* a
  business fact: the whole `AllocateOrder` call fails and nothing is silently
  marked backordered.
- **BR3 — ship-complete by default.** `allowPartialShipment` defaults to
  `false`: any backordered line holds the WHOLE order back from release until
  `RetryAllocation` clears it. `RetryAllocation` is the only route from
  `Backordered` back to `Allocated`.
- **BR6 — the cancellation boundary is release.** Cancelling is legal only
  while no line is `Released`; the boundary is checked *before* any
  reservation is revoked. See
  [ADR 0004](docs/docs/adr/0004-cancellation-boundary-at-release.md), including
  its documented known gap.

BR2/BR3 are written up in
[ADR 0003](docs/docs/adr/0003-ship-complete-default-and-fail-closed-allocation.md).

## Running locally

### 1. Without a database (fastest)

With no `DATABASE_URL`, the service starts on the in-memory adapters and is
fully functional:

```bash
go run ./cmd/order
# {"level":"INFO","msg":"database url not configured; using in-memory adapters"}
# {"level":"INFO","msg":"http server listening","addr":":8080"}
```

### 2. With Postgres

```bash
docker compose up -d postgres          # Postgres 16 on localhost:5434

export DATABASE_URL='postgres://order:order@localhost:5434/order?sslmode=disable'
go run ./cmd/order                     # migrations run automatically at startup
```

Migrations live in `migrations/` and are applied by `golang-migrate` on boot;
there is no separate migrate step to remember.

### 3. Wired to the real Suppliers

Both outbound clients default to **permissive (no-op) mode**, so tests and CI
never reach the network. Permissive here does *not* mean fail-open: allocating
real stock or releasing real work must never appear to succeed against a
no-op, so a permissive client refuses the operation with a clear
`downstream-not-configured` problem response. Only `http` mode is suitable for
a real integration test or deployment:

```bash
export INVENTORY_STORAGE_MODE=http
export INVENTORY_STORAGE_BASE_URL=http://localhost:8080
export WES_WORK_PLANNING_MODE=http
export WES_WORK_PLANNING_BASE_URL=http://localhost:8081
export HTTP_ADDR=:8082
go run ./cmd/order
```

### Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | Listen address. |
| `DATABASE_URL` | *(unset)* | Postgres DSN. Unset ⇒ in-memory adapters. |
| `MIGRATIONS_PATH` | `migrations` | golang-migrate source directory. |
| `INVENTORY_STORAGE_MODE` | `permissive` | `http` or `permissive`. |
| `INVENTORY_STORAGE_BASE_URL` | *(unset)* | Required when mode is `http`. |
| `WES_WORK_PLANNING_MODE` | `permissive` | `http` or `permissive`. |
| `WES_WORK_PLANNING_BASE_URL` | *(unset)* | Required when mode is `http`. |
| `PROMISE_DEFAULT_LEAD_TIME` | `48h` | Promise-date lead time for any unlisted path. |
| `PROMISE_PATH_LEAD_TIMES` | *(unset)* | Per-path overrides, e.g. `pick=24h,singles=6h`. |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error`. |

## API

Six endpoints plus a liveness probe. The full contract, including the RFC 7807
error schema, is in [`apis/openapi.yaml`](apis/openapi.yaml).

| Method | Path | Use case |
| --- | --- | --- |
| `POST` | `/orders` | ReceiveOrder |
| `GET` | `/orders/{id}` | GetOrder |
| `POST` | `/orders/{id}/allocate` | AllocateOrder |
| `POST` | `/orders/{id}/retry-allocation` | RetryAllocation |
| `POST` | `/orders/{id}/release` | ReleaseOrder |
| `DELETE` | `/orders/{id}` | CancelOrder |
| `GET` | `/healthz` | Liveness probe |

Every error response is `application/problem+json` (RFC 7807), the same shape
the other five services emit.

### Curl walkthrough

**Health:**

```bash
curl -s localhost:8080/healthz
# {"status":"ok"}
```

**ReceiveOrder** — `pathId` defaults to `pick`; `allowPartialShipment`
defaults to `false` (ship-complete):

```bash
curl -s -X POST localhost:8080/orders \
  -H 'Content-Type: application/json' \
  -d '{
        "allowPartialShipment": false,
        "lines": [
          {"sku": "SKU-1", "quantity": 2, "pathId": "pick"},
          {"sku": "SKU-2", "quantity": 1, "giftWrap": true}
        ]
      }'
# 201 Created, Location: /orders/ord-...
# {"id":"ord-...","status":"Received","allowPartialShipment":false,
#  "lines":[{"lineNo":1,"sku":"SKU-1","quantity":2,"pathId":"pick",
#            "giftWrap":false,"status":"Pending"}, ...]}
```

**GetOrder:**

```bash
ORDER_ID=ord-...   # from the response above
curl -s localhost:8080/orders/$ORDER_ID
```

**AllocateOrder** — reserves each pending line against inventory-storage. A
backordered line is a successful outcome reported in the body, not an error:

```bash
curl -s -X POST localhost:8080/orders/$ORDER_ID/allocate
# 200 OK
# {"id":"ord-...","status":"Allocated","promiseDate":"2026-08-27T09:00:00Z",
#  "lines":[{"lineNo":1,...,"status":"Allocated","reservationId":"res-..."}, ...]}
```

**RetryAllocation** — re-attempts the backordered lines and only those:

```bash
curl -s -X POST localhost:8080/orders/$ORDER_ID/retry-allocation
```

**ReleaseOrder** — enqueues each allocated line as work on its process path:

```bash
curl -s -X POST localhost:8080/orders/$ORDER_ID/release
# 200 OK — status becomes Released (or PartiallyReleased)
```

Blocked by BR3 while a ship-complete order still has an unallocated line:

```bash
curl -s -X POST localhost:8080/orders/$ORDER_ID/release
# 409 Conflict, application/problem+json
# {"type":"https://errors.order-management.warehouse-systems.dev/ship-complete-blocked",
#  "title":"Ship-complete order cannot be released while any line is unallocated",
#  "status":409,"detail":"...","instance":"/orders/ord-.../release"}
```

**CancelOrder** — revokes every allocated line's reservation, then cancels:

```bash
curl -s -i -X DELETE localhost:8080/orders/$ORDER_ID
# 204 No Content
```

Rejected by BR6 once any line has been released:

```bash
curl -s -X DELETE localhost:8080/orders/$ORDER_ID
# 409 Conflict, application/problem+json
# {"type":"https://errors.order-management.warehouse-systems.dev/order-already-released",
#  "title":"Order already has released lines and can no longer be cancelled",
#  "status":409,...}
```

## Quality gate

Every `make` target mirrors a step in `.github/workflows/ci.yml`, so the same
feedback CI gives you post-push is available locally, pre-commit:

```bash
make check       # fmt-check + vet + build + lint + test -race
make check-all   # check + coverage (gate: 90% on domain + application)
```

| Target | What it runs |
| --- | --- |
| `build` | `go build ./...` |
| `vet` | `go vet ./...` |
| `fmt` / `fmt-check` | `gofmt -w .` / fail if `gofmt -l .` is non-empty |
| `lint` | `golangci-lint run ./...` (CI pins `v2.13.1`) |
| `test` | `go test ./... -race` |
| `coverage` | coverage profile + the 90% gate |

Git hooks are wired through [lefthook](https://github.com/evilmartians/lefthook)
— `pre-commit` runs fmt-check/vet/lint, `pre-push` runs `make check`. Hooks are
not tracked by git, so activate them once per clone:

```bash
brew install lefthook   # or: go install github.com/evilmartians/lefthook@latest
lefthook install
```

CI runs exactly two jobs in v1: **`lint`** and **`test`**.

## Deferred (v1)

The following are **deliberately out of scope for this first pass**. They are
listed so an absence is never mistaken for an oversight — each is a decision,
not a gap someone forgot about.

- **Helm chart / `warehouse-infra` kind-cluster wiring.** No `charts/`
  directory and no `helm-lint` CI job. Deployment for now is
  `go run ./cmd/order` or a hand-built container.
- **Gremlins mutation-testing gate.** The sibling services gate on mutation
  score; there is no `.gremlins.yaml` and no `mutation` job here yet. The 90%
  coverage gate is the only test-quality sensor in v1.
- **godog / BDD acceptance tests.** No `features/` directory and no `bdd` job.
  The behavioural rules are covered by table-driven unit tests and the
  `httptest` suite instead.
- **MCP inbound adapter.** HTTP is the only inbound adapter.
- **Kafka integration events.** Domain events are published through the log
  publisher only. `ports.EventPublisher` is deliberately the shape a Kafka
  producer satisfies, so adding a broker later is purely additive.
- **Postgres integration tests.** The `postgres` adapter has no
  `-tags=integration` suite and there is no `integration` CI job; that needs a
  live database in CI, which is a separate round of work.
- **arch-go architecture fitness tests.** The dependency rule in ADR 0001 is
  upheld by review here, not yet by an executable test.
- **Spectral / OpenAPI linting in CI.** `apis/openapi.yaml` is
  spectral-clean against `.spectral.yaml` locally, but there is no `api-lint`
  job in v1.
- **Docker image publishing and releases.** No `docker-publish` or `release`
  job, no `Dockerfile`.
- **Real carrier-rate promise dates.** `LeadTimePolicy` computes the promise
  date from a configurable per-path lead time. That is real, tested domain
  logic — not a hardcoded field — but it is not a live carrier integration,
  and no such service exists in this fleet to call.
- **Clawing back released work on cancellation.** Documented in detail in
  [ADR 0004](docs/docs/adr/0004-cancellation-boundary-at-release.md).
- **Any change to `inventory-storage` or `wes-work-planning`.** This build is
  100% additive from their point of view; neither repository is touched.

### Docusaurus site

The four ADRs under `docs/docs/adr/` are written with the same Docusaurus
frontmatter (`id`, `slug`, `title`, `sidebar_label`, `description`) the sibling
repositories use, so they drop straight into a site when one is set up here.
**There is no Docusaurus project in this repository yet** — no
`docusaurus.config.ts`, no `package.json`, no `sidebars.ts` — so `npm run build`
does not apply. The ADR markdown is the deliverable for v1.

## Architecture Decision Records

1. [0001 — Hexagonal (ports & adapters) architecture](docs/docs/adr/0001-hexagonal-ports-and-adapters.md)
2. [0002 — HTTP consumer of inventory-storage and wes-work-planning, not shared code](docs/docs/adr/0002-http-consumer-of-inventory-and-wes-not-shared-code.md)
3. [0003 — Ship-complete by default and fail-closed allocation](docs/docs/adr/0003-ship-complete-default-and-fail-closed-allocation.md)
4. [0004 — The cancellation boundary is release](docs/docs/adr/0004-cancellation-boundary-at-release.md)

## License

MIT (or match the other repos' licensing — TBD).
