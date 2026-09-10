# Architecture Decision Records (12 total, `docs/docs/adr/`)

1. **0001 — Hexagonal (ports & adapters) architecture.** The dependency
   rule this whole repo enforces (`internal/architecture/` fitness test).
2. **0002 — HTTP consumer of inventory-storage and wes-work-planning, not
   shared code.** The original Customer/Supplier boundary decision — see
   `bounded-context-boundary.md`. Partially superseded by 0005 for the
   release leg only (allocation via inventory-storage is unchanged).
3. **0003 — Ship-complete by default and fail-closed allocation.** BR2/BR3
   in ADR form — see `domain-model.md`.
4. **0004 — The cancellation boundary is release.** BR6 in ADR form,
   including the documented "no clawback of released work" known gap.
5. **0005 — Choreographed release via Kafka, folded allocate-then-release,
   and pathId goes internal-only.** The big one: deletes `/allocate` and
   `/release` REST verbs, deletes `ports.WorkReleaseClient` and
   `internal/adapters/outbound/weswork/` entirely, replaces the synchronous
   wes-work-planning call with Kafka choreography, folds the whole saga into
   `ReceiveOrder`/`RetryAllocation`. **Read this before touching anything in
   the allocation/release path.**
6. **0006 — Analytical data product.** The Order Funnel & Allocation Health
   report, `cmd/order-projector`/`cmd/order-reports`, isolation enforced by
   `arch-test`.
7. **0007 — Adopt the fleet micro-frontend console (`order-mgmt-mfe`).**
   `web/`'s existence, CORS wiring, the `warehouse-console` BFF's
   `GET /console/orders/{id}/lifecycle` fan-out hop. NOTE: this repo has TWO
   things legitimately called "ADR-0002" — its own (item 2 above, about the
   inventory-storage/wes-work-planning HTTP boundary) and the fleet-wide MFE
   decision numbered independently in `warehouse-ops-agent`. Don't confuse
   them.
8. **0008 — FulfillmentClass, a demand-shape classifier, not a process-path
   name.** `Single`/`SameSKUMulti`/`MultiLineMulti`, derive-don't-store,
   additive on the frozen Kafka `lines[]` payload. See `domain-model.md`.
9. **0009 — Standard metrics convention across the fleet.** Closed a real
   gap: `order-management` had ZERO telemetry (no `telemetry.Setup`, no
   `otelchi` RED middleware) before this ADR. Now emits Go-runtime metrics,
   HTTP RED via `otelchi`, and one business counter `order.orders.received`.
10. **0010 — MCP as an inbound adapter, not a new service.** `cmd/mcp`,
    one read-only tool (`get_order`), no write tool — see
    `api-contracts.md`.
11. **0011 — Adopt the fleet REST identity (static bearer keys, read/
    read-write scopes).** **Superseded by 0012** — do not re-implement this
    without checking 0012 first.
12. **0012 — Remove the REST/MCP bearer auth layer.** Fleet-wide rollback:
    `internal/adapters/inbound/auth/` deleted entirely, every REST and MCP
    route reachable with no `Authorization` header. Re-adopting auth later
    means adopting the fleet's NEXT iteration of that decision, not
    resurrecting this deleted package verbatim.

Other ADR-adjacent facts worth knowing without opening every file:

- Gateway API `HTTPRoute` chart template exists (`charts/order-management`
  `values.yaml` `gatewayApi:` block) but is additive/disabled by default —
  it is not itself the subject of a numbered ADR in this list; check the
  chart's own comments if you need its exact behavior.
