# Order Management

Generic/Supporting bounded context: order intake, per-line stock allocation,
promise-date calculation, and choreographed release — the missing upstream
Open Host Service for the `warehouse-systems` fleet. Owns **Order** and
**OrderLine** as first-class aggregates. Before this context existed, "an
order" was just an unowned string (`OrderRef`/`DemandRef`/`Reference`)
independently reinvented by three other services.

Design source of truth: `/Users/claudioed/warehouse-systems/.hermes/plans/2026-08-25_023800-order-management.md`
and the fleet's shared DDD reference docs at `/Users/claudioed/docs/amazon-fulfillment-ddd.md`
and `/Users/claudioed/warehouse-systems-ddd.md`. Honor their ubiquitous
language and strategic classifications.

Study project — not a production system, not affiliated with Amazon or any
company (see README.md banner).

## Project Overview

- **Module:** `github.com/claudioed/order-management`, Go 1.26.
- **Four binaries, one module:** `cmd/order` (HTTP + Kafka-choreography
  OLTP service), `cmd/mcp` (MCP read server, ADR-0010), `cmd/order-projector`
  (analytics writer, ADR-0006), `cmd/order-reports` (analytics read-only API).
- **Public intent, one command.** Since ADR-0005, a caller expresses "place
  an order" via `POST /orders` alone — allocation and release happen inside
  that same call as a folded, best-effort saga. There is no public
  `/allocate` or `/release` verb anymore.
- **Suppliers:** `inventory-storage` (synchronous HTTP, `POST /reservations`)
  and `wes-work-planning` (asynchronous, via Kafka choreography — no HTTP
  call). This context is the **Customer**; both are **Suppliers / Open Host
  Services**. See `.claude/rules/bounded-context-boundary.md`.

## Architecture (NON-NEGOTIABLE — identical shape to the other fleet services)

Hexagonal / Ports & Adapters, enforced by an `arch-go` fitness test
(`internal/architecture/architecture_test.go`, CI job `arch-test`). Strict
dependency rule: **domain depends on nothing; application depends on
domain; adapters depend on application/domain.** No framework, HTTP, or SQL
type ever appears in the domain layer.

```
cmd/
  order/                       main.go — HTTP + Kafka-choreography OLTP service
  mcp/                         MCP server composition root (ADR-0010)
  order-projector/             analytics writer (ADR-0006), FirstOffset consumer
  order-reports/                analytics read-only REST API
internal/
  domain/
    order/                     Order aggregate, OrderLine, Status, invariants, LeadTimePolicy
    shared/                    OrderId, SKU, PathId, domain events, errors
  application/
    ports/                     OrderRepo, EventPublisher, Clock, OrderMetrics,
                                InventoryReservationClient (NO WorkReleaseClient — deleted, ADR-0005)
    usecases/                  ReceiveOrder, RetryAllocation, CancelOrder, GetOrder
                                (AllocateOrder/ReleaseOrder deleted as public types, ADR-0005;
                                shared allocate-then-release logic lives in allocation.go)
  adapters/
    inbound/http/               chi handlers, DTOs, RFC 7807 error mapping, CORS
    inbound/mcp/                MCP read-only tool (get_order), ADR-0010
    outbound/inventorystorage/  HTTP client: POST /reservations, DELETE /reservations/{id}
    outbound/kafka/             integration-events publisher (EVENT_PUBLISHER=kafka)
    outbound/events/            log publisher (default, EVENT_PUBLISHER=log)
    outbound/postgres/          pgxpool repo + golang-migrate runner
    outbound/memory/            in-memory repo for tests
    outbound/analyticsstore/    analytics read/write store (ADR-0006)
    outbound/telemetry/         OTel MeterProvider/TracerProvider, otelchi RED (ADR-0009)
  analytics/report/             analytical read model — depends on nothing internal (enforced)
migrations/                    golang-migrate SQL (OLTP)
migrations/analytics/          golang-migrate SQL (analytics store)
apis/openapi.yaml              REST contract (5 endpoints)
apis/asyncapi.yaml             Kafka contract (2 channels, 11 messages)
web/                           order-mgmt-mfe — Vite/React MFE remote (ADR-0007), NOT part of the Go module
docs/docs/adr/                 12 ADRs — see .claude/rules/adrs.md
```

**Note on `web/`:** owns its own `package.json`/build/dev-server (`:5181`),
talks only to this service's own REST API, and never participates in
`make check`/`check-all`. See `.claude/rules/frontend-mfe.md`.

## Key Commands

```bash
# Local quality gate (mirrors .github/workflows/ci.yml)
make check          # fmt-check + vet + build + lint + test — run after every change
make check-all       # check + coverage (90% gate on domain+application)
make integration     # Kafka adapter against a Testcontainers broker
make coverage         # go test -race -coverprofile + the 90% gate

# Run locally
go run ./cmd/order                    # in-memory adapters if DATABASE_URL unset
docker compose up -d postgres         # Postgres 16 on :5434
docker compose -f docker-compose.kafka.yml up -d   # local Kafka (KRaft, :9092)

# Docs site (Docusaurus — regenerate after ANY apis/openapi.yaml change)
cd docs && npm ci
npm run gen-api-docs   # docusaurus gen-api-docs order -> docs/docs/api-reference/rest/
npm run build          # onBrokenLinks / onBrokenAnchors are both 'throw'

# golangci-lint (CI-pinned version)
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1
```

Full CI job list, coverage/mutation gates, and the docs-drift check live in
`.claude/rules/ci-quality-gates.md` — read it before touching `.github/workflows/`
or `apis/*.yaml`.

## Code Standards / Testing

- gofmt/go vet clean; every package has a doc comment.
- `.golangci.yml` copied verbatim from `inventory-storage`'s own config.
- Table-driven tests: domain + application layers use the in-memory adapter
  and fake HTTP clients — **never hit a real network in a unit test.**
  Kafka-touching `-tags=integration` tests use **testcontainers**, never a
  skip-gated `KAFKA_BROKERS` env check (CI's `integration` job provisions no
  external broker; testcontainers is the only variant that actually runs).
- One httptest per REST endpoint: at least one success and one error path.
- Every named invariant in `.claude/rules/domain-model.md` needs a dedicated
  failing-path test.
- Coverage gate: **90%** on `./internal/domain/...,./internal/application/...`.
- Mutation gate (gremlins, weekly `mutation` CI job): baseline locked at
  efficacy 90.70% / mutant-coverage 81.13% on `internal/domain/order` — see
  `.gremlins.yaml`. Do not let a change silently regress below the pinned
  threshold.
- godog/BDD acceptance suite: `features/*.feature`, run via
  `go test ./... -run TestFeatures` (CI job `bdd`).

## Where the rest of the detail lives

This file is the index. Full DDD/tactical detail, API contracts, ADR
summaries, and CI mechanics are split into `.claude/rules/` so this file
stays short and current:

- **`.claude/rules/bounded-context-boundary.md`** — the Customer/Supplier
  rule, what this repo MUST NOT do (import Go packages, touch another
  service's DB).
- **`.claude/rules/domain-model.md`** — ubiquitous language, aggregate
  invariants (BR2/BR3/BR6), the 9 domain events, and the folded
  allocate-then-release use-case flow (ADR-0005).
- **`.claude/rules/api-contracts.md`** — the 5 REST endpoints, the exact
  outbound HTTP contract to inventory-storage, the Kafka integration/
  analytics channels (asyncapi.yaml), and the MCP `get_order` tool.
- **`.claude/rules/adrs.md`** — one-line summary + link for all 12 ADRs,
  including which ones supersede an earlier one.
- **`.claude/rules/ci-quality-gates.md`** — every CI job (`lint`, `test`,
  `integration`, `helm-lint`, `trivy-scan`, `docker-publish`, `release`,
  `codeql`, `scorecard`, `docs`), branch triggers, and the OpenAPI/AsyncAPI
  drift-regeneration procedure.
- **`.claude/rules/deferred-and-known-gaps.md`** — what is deliberately NOT
  built yet, and why each gap is a decision, not an oversight.
- **`.claude/rules/frontend-mfe.md`** — the `web/` micro-frontend remote,
  its isolation from the Go module, CORS config.

## Git workflow

GitFlow: `feature/*` branches off `develop`, PR into `develop`
(`gh pr create --base develop`); `develop` promotes to `main` for release.
Do not merge your own PR — leave it open for independent review. Do not
force-push over history once pushed.
