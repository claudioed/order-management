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
                                  InventoryReservationClient
    usecases/                     ReceiveOrder (allocates+releases
                                  implicitly), RetryAllocation (retries+
                                  releases), CancelOrder, GetOrder
  adapters/
    inbound/http/                 chi handlers, DTOs, RFC 7807 error mapping
    outbound/inventorystorage/    POST /reservations, DELETE /reservations/{id}
    outbound/postgres/            pgxpool repo + golang-migrate runner
    outbound/memory/              in-memory repo + clocks for tests
    outbound/events/              log publisher (default, EVENT_PUBLISHER=log)
    outbound/kafka/               Kafka integration-events publisher
                                  (EVENT_PUBLISHER=kafka), topic
                                  warehouse.order-management.events
migrations/                       golang-migrate SQL files
apis/openapi.yaml                 OpenAPI 3.0 spec for every endpoint
docker-compose.kafka.yml          Local Kafka broker (KRaft, single node)
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

### 3. Wired to the real Supplier

The outbound inventory-storage client defaults to **permissive (no-op)
mode**, so tests and CI never reach the network. Permissive here does
*not* mean fail-open: allocating real stock must never appear to succeed
against a no-op, so a permissive client refuses the operation with a clear
`downstream-not-configured` problem response. Only `http` mode is suitable
for a real integration test or deployment:

```bash
export INVENTORY_STORAGE_MODE=http
export INVENTORY_STORAGE_BASE_URL=http://localhost:8080
export HTTP_ADDR=:8082
go run ./cmd/order
```

Release no longer calls any Supplier synchronously — see
[Kafka integration](#kafka-integration) below.

### Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | Listen address. |
| `DATABASE_URL` | *(unset)* | Postgres DSN. Unset ⇒ in-memory adapters. |
| `MIGRATIONS_PATH` | `migrations` | golang-migrate source directory. |
| `INVENTORY_STORAGE_MODE` | `permissive` | `http` or `permissive`. |
| `INVENTORY_STORAGE_BASE_URL` | *(unset)* | Required when mode is `http`. |
| `EVENT_PUBLISHER` | `log` | `log` (default, in-memory/Postgres publisher) or `kafka` — forwards `OrderAllocated`/`OrderPartiallyAllocated` to the integration topic **and** fans the full report-input event set out to the analytics topic (ADR 0006). |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated broker addresses. Only read when `EVENT_PUBLISHER=kafka`. |
| `ANALYTICS_DATABASE_URL` | *(unset)* | Analytical Postgres DSN. **Required** by `cmd/order-projector` (writer) and `cmd/order-reports` (read-only reader); never read by the OLTP binary. |
| `ANALYTICS_MIGRATIONS_PATH` | `migrations/analytics` | Analytical golang-migrate source directory (writer only). |
| `ADMIN_ADDR` | `:8091` | `cmd/order-projector` admin/health listen address. |
| `PROMISE_DEFAULT_LEAD_TIME` | `48h` | Promise-date lead time for any unlisted path. |
| `PROMISE_PATH_LEAD_TIMES` | *(unset)* | Per-path overrides, e.g. `pick=24h,singles=6h`. |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error`. |

## Kafka integration

Per [ADR 0005](docs/docs/adr/0005-choreographed-release-via-kafka.md),
release is no longer a synchronous HTTP call to `wes-work-planning`. It is
announced as a Kafka integration event, which that service (or any other
subscriber) consumes independently:

- **Topic:** `warehouse.order-management.events`
- **Forwarded events:** `OrderAllocated` and `OrderPartiallyAllocated`
  only — every other domain event (`OrderReceived`, `OrderLineAllocated`,
  `OrderLineBackordered`, `OrderLineReleased`, `OrderReleased`,
  `OrderCancelled`, `OrderAllocationPartiallyFailed`) stays local-only,
  mirroring `inventory-storage`'s own precedent of forwarding only two of
  its several domain events.
- **Payload (frozen, `data` field):**

  ```json
  {
    "order_id": "ord-a1b2c3d4-0000-0000-0000-000000000001",
    "promise_date": "2026-08-27T09:00:00Z",
    "lines": [
      {"line_no": 1, "sku": "SKU-1", "path_id": "pick", "gift_wrap": false}
    ]
  }
  ```

- **Opt-in, off by default.** Set `EVENT_PUBLISHER=kafka` (and, if not
  `localhost:9092`, `KAFKA_BROKERS`) to publish for real; the default
  `log` publisher requires no broker at all, so `go test ./...` and local
  runs are unaffected either way.
- **Local broker:** `docker compose -f docker-compose.kafka.yml up -d`
  starts a single-node KRaft Kafka on `localhost:9092`, the same
  `apache/kafka:3.8.0` image/config shape the fleet's shared
  `docker-compose.kafka.yml` uses (this repo has its own copy rather than
  a cross-repo reference).
- **Fire-and-forget, deliberately.** There is no release-confirmation
  reply event from `wes-work-planning` in v1 — see "Deferred (v1)" below.

## Analytics data product

Per [ADR 0006](docs/docs/adr/0006-analytical-data-product.md), this service
additionally owns an **analytical read model** — the *Order Funnel & Allocation
Health* report — built from its own domain events. It is a lightweight data mesh
with **no central data platform**: a separate analytics topic, a separate
analytical database, and two extra binaries, all owned by this repo. The report
contract is documented at
[`docs/docs/analytics/order-funnel-report.md`](docs/docs/analytics/order-funnel-report.md).

- **Separate topic:** `warehouse.order-management.analytics` — distinct from the
  integration topic, so widening the report's inputs never risks an integration
  consumer. A **new** analytics publisher emits the full report-input event set
  under Envelope v1 (keyed by `order_id`); the integration publisher is
  untouched. When `EVENT_PUBLISHER=kafka`, the OLTP binary fans out to both.
- **Separate analytical database:** its own `ANALYTICS_DATABASE_URL`, its own
  migrations in `migrations/analytics/`, and a **read-only role** for the reader.
- **Three processes, one writer:**
  - `cmd/order` — the OLTP binary (unchanged; fans out to the analytics topic
    when `EVENT_PUBLISHER=kafka`).
  - `cmd/order-projector` — the **only** writer. Consumes the analytics topic
    from the earliest offset, applies idempotent projections, runs the
    analytical migrations on start. Admin health on `:8091`.
  - `cmd/order-reports` — the **read-only** reader. Serves the report over REST
    on `:8092`; never writes, never migrates.

Run the writer and reader locally against the local Kafka and an analytical
database:

```bash
# Start the local broker (same as the Kafka integration section).
docker compose -f docker-compose.kafka.yml up -d

export ANALYTICS_DATABASE_URL='postgres://order:***@localhost:5434/order_analytics?sslmode=disable'
export KAFKA_BROKERS=localhost:9092

# 1. The writer — consumes the analytics topic, projects into the analytical DB,
#    and runs its migrations on start.
go run ./cmd/order-projector

# 2. The reader — serves the report read-only from the analytical DB.
go run ./cmd/order-reports
# then:
curl -s 'localhost:8092/reports/funnel?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z'
curl -s localhost:8092/reports/funnel/freshness

# 3. Drive an order lifecycle through the OLTP binary with kafka fan-out on,
#    and watch the report reflect it within the freshness SLA.
EVENT_PUBLISHER=kafka go run ./cmd/order
```

The report is **eventually consistent** by design — a projection of the event
stream to a freshness SLA (p95 event-to-report lag < 30s), not a real-time view.
There is **no MCP report tool** in this service: no MCP adapter exists in v1, so
the curated tool the estate's pilot added is a deferred follow-up.

## API

Four endpoints plus a liveness probe. The full contract, including the RFC 7807
error schema, is in [`apis/openapi.yaml`](apis/openapi.yaml).

| Method | Path | Use case |
| --- | --- | --- |
| `POST` | `/orders` | ReceiveOrder — allocates and releases automatically |
| `GET` | `/orders/{id}` | GetOrder |
| `POST` | `/orders/{id}/retry-allocation` | RetryAllocation — retries and releases automatically |
| `DELETE` | `/orders/{id}` | CancelOrder |
| `GET` | `/healthz` | Liveness probe |

`POST /orders/{id}/allocate` and `POST /orders/{id}/release` no longer
exist — see [ADR 0005](docs/docs/adr/0005-choreographed-release-via-kafka.md).
A caller expresses ONE intent, placing an order, and this service
internally attempts allocation-then-release automatically, right after
intake and again on retry.

Every error response is `application/problem+json` (RFC 7807), the same shape
the other five services emit.

### Curl walkthrough

**Health:**

```bash
curl -s localhost:8080/healthz
# {"status":"ok"}
```

**ReceiveOrder** — allocates and releases automatically, in the same call.
`pathId` is never part of the request (every line gets the internal
default); `allowPartialShipment` defaults to `false` (ship-complete):

```bash
curl -s -X POST localhost:8080/orders \
  -H 'Content-Type: application/json' \
  -d '{
        "allowPartialShipment": false,
        "lines": [
          {"sku": "SKU-1", "quantity": 2},
          {"sku": "SKU-2", "quantity": 1, "giftWrap": true}
        ]
      }'
# 201 Created, Location: /orders/ord-...
# Both lines allocate and release in this SAME call when inventory-storage
# has stock, so a ship-complete order typically comes back already
# Released:
# {"id":"ord-...","status":"Released","allowPartialShipment":false,
#  "promiseDate":"2026-08-27T09:00:00Z",
#  "lines":[{"lineNo":1,"sku":"SKU-1","quantity":2,"pathId":"pick",
#            "giftWrap":false,"status":"Released","reservationId":"res-..."}, ...]}
```

A `409` from inventory-storage backorders that one line instead — a
business fact, not a call failure — and, per BR3, a ship-complete order
with any backordered line stays `Backordered` and releases nothing until
`retry-allocation` clears it:

```bash
# {"id":"ord-...","status":"Backordered","allowPartialShipment":false,
#  "lines":[..., {"lineNo":2,"sku":"SKU-2","quantity":1,"pathId":"pick",
#                 "giftWrap":true,"status":"Backordered"}]}
```

**GetOrder:**

```bash
ORDER_ID=ord-...   # from the response above
curl -s localhost:8080/orders/$ORDER_ID
```

**RetryAllocation** — re-attempts the backordered lines and only those,
then releases whatever it newly clears:

```bash
curl -s -X POST localhost:8080/orders/$ORDER_ID/retry-allocation
# 200 OK — status becomes Released (or PartiallyReleased) once every
# backorder clears and BR3 permits release
```

**CancelOrder** — revokes every allocated line's reservation, then cancels:

```bash
curl -s -i -X DELETE localhost:8080/orders/$ORDER_ID
# 204 No Content
```

Rejected by BR6 once any line has been released — which, since a
ship-complete order that allocates cleanly is released automatically
inside `POST /orders`, can happen right after intake:

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
- **Kafka release-confirmation reply events from wes-work-planning.**
  This service publishes `OrderAllocated`/`OrderPartiallyAllocated` to
  Kafka fire-and-forget — it never learns whether wes-work-planning's
  consumer actually processed the event or successfully enqueued its own
  work. This is a deliberate v1 choice (mirroring inventory-storage's own
  ADR 0004 stance on transactional guarantees), not an oversight: a
  confirmation-loop pattern (e.g. this service subscribing to a
  `WorkEnqueued` reply event) is real, scoped-down future work. See
  [ADR 0005](docs/docs/adr/0005-choreographed-release-via-kafka.md).
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

**The Docusaurus site now exists**, mirroring the other five
`warehouse-systems` repositories' exact structure (Docusaurus 3.10.2,
`docusaurus-plugin-openapi-docs`/`docusaurus-theme-openapi-docs`, the same
six-category sidebar minus "AI Ecosystem (MCP)", which is deferred — no MCP
adapter exists yet). It lives under `docs/` and builds cleanly:

```bash
cd docs
npm ci
npm run gen-api-docs   # generates docs/docs/api-reference/rest/ from apis/openapi.yaml
npm run build          # onBrokenLinks / onBrokenAnchors are both 'throw'
```

The four ADRs under `docs/docs/adr/` are wired into the sidebar's
"Architecture Decision Records" category alongside a new `adr/about.md`
index page. `.github/workflows/docs.yml` builds and deploys the site to
GitHub Pages on every push to `main` that touches `docs/**`, publishing to
**https://claudioed.github.io/order-management/**.

## Architecture Decision Records

1. [0001 — Hexagonal (ports & adapters) architecture](docs/docs/adr/0001-hexagonal-ports-and-adapters.md)
2. [0002 — HTTP consumer of inventory-storage and wes-work-planning, not shared code](docs/docs/adr/0002-http-consumer-of-inventory-and-wes-not-shared-code.md) (partially superseded by 0005 for release)
3. [0003 — Ship-complete by default and fail-closed allocation](docs/docs/adr/0003-ship-complete-default-and-fail-closed-allocation.md)
4. [0004 — The cancellation boundary is release](docs/docs/adr/0004-cancellation-boundary-at-release.md)
5. [0005 — Choreographed release via Kafka, folded allocate-then-release, and pathId goes internal-only](docs/docs/adr/0005-choreographed-release-via-kafka.md)

## License

MIT (or match the other repos' licensing — TBD).
