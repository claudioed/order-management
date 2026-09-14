# Architecture Decision Records (17 total, `docs/docs/adr/`)

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
13. **0013 — Process-path selection as a real domain policy, validated
    against a live catalogue.** `order.PathSelectionPolicy` (v1: one rule,
    `PICK`) plus `ports.ProcessPathCatalogue.IsActive` backed by a Kafka-fed
    `kafkacatalog` cache (`PATH_CATALOGUE_SOURCE=none|kafka`); unknown path
    -> synchronous 400 before anything persists.
14. **0014 — ACCEPTED: the promise is a CPT window derived from fulfillment
    capability.** Replaces `now + PROMISE_PATH_LEAD_TIMES` with a
    `PromisePolicy` over process-path-management's capability contract
    (their ADR 0010: `cycleTimeP95`, `eligibility`, site `CPTSchedule`)
    and wes-work-planning capacity; `LeadTimePolicy` stays as the tagged
    fallback (`basis=LeadTime`); per-shipment-group promising when
    `AllowPartialShipment`; `OrderRepromised` closes the loop. Not
    implemented yet — read it before touching `promise.go` or
    `path_selection.go`.
15. **0015 — ACCEPTED: wes-work-planning's PathCapacityChanged wired as
    the real PathCapacity adapter.** Closes ADR-0014 step 3:
    `kafkapathcapacity.Consumer`, a third Kafka consumer (own
    per-process-unique group) on a NEW topic
    (`warehouse.work-planning.events`), replaces `UnknownPathCapacity`
    as the `PATH_CATALOGUE_SOURCE=kafka` default. `ports.PathCapacity`/
    `order.CapacitySource.Remaining` widened to accept `cutoffAt
    time.Time` alongside `cptId` — the caller (`PromisePolicy.
    linesFitWindow`) already has it in scope, so the cache is keyed on
    `(PathId, CutoffAt)` with an exact match, not `cptId` string
    matching. `UnknownPathCapacity` stays available for
    `PATH_CATALOGUE_SOURCE=none`/dev mode. Read it before touching
    `ports.PathCapacity`, `promise_policy.go`, or adding a fourth Kafka
    consumer.
16. **0016 — ACCEPTED: eligibility-driven process-path selection (ADR
    0014 step B, ROUTING ONLY).** `order.PathSelectionPolicy.Select`
    widened to `(sku, quantity, giftWrap, productAttributes,
    EligibilitySource) (PathId, bool)` and now actually evaluates
    `shared.DefaultPathId`'s declared `Eligibility` against the line
    (`MaxUnitsPerLine`, required/excluded product attributes, gift wrap
    folded in as the attribute string `"giftWrap"`). New
    `ports.ProductClassificationLookup` port +
    `internal/adapters/outbound/productclassification` (HTTP client +
    fail-open `PermissiveLookup`, `PRODUCT_CLASSIFICATION_MODE=http|
    permissive`) mirror wes-work-planning's own ADR-0009 pattern exactly
    — independently written, no cross-service Go import. An ineligible
    line is rejected with a new `shared.ErrLineIneligibleForResolvedPath`
    (422) before anything persists. Per-shipment-group promising — ADR
    0014's OTHER half of "step B" — is explicitly OUT OF SCOPE here and
    deferred to a future ADR/PR: this phase never touches `Order`'s
    promise fields, `PromisePolicy`'s single-`Promise`-per-order
    contract, or `SetPromise`. Honest v1 limitation, documented in the
    ADR: `ports.ProcessPathCatalogue` has no "list active paths" method,
    so this policy can only evaluate `shared.DefaultPathId` itself, not
    choose among multiple real paths — read the ADR before extending
    `path_selection.go` to a real multi-path decision.
17. **0017 — ACCEPTED: per-shipment-group promising (ADR 0014 step B,
    the second half).** `order.PromisePolicy` gains `PromiseGroups(now,
    o) ([]PromiseGroup, bool)`: for `AllowPartialShipment=false`, exactly
    one group covering every allocated line, computed via the UNCHANGED
    `Promise(now, o)` — byte-identical to pre-ADR-0017 behaviour. For
    `AllowPartialShipment=true`, each allocated line is evaluated
    independently and lines with an identical resulting `Promise`
    (same `Basis`/`CptId`/`CutoffAt`) are grouped together.
    `Order.SetPromiseGroups` stores the full `[]PromiseGroup` breakdown
    AND re-derives the legacy single-valued `promiseDate`/`promiseCptId`/
    `promiseBasis` as a "latest cutoff" projection (ADR 0014 §3's stated
    rule, extended here to cover CptId/Basis too — documented in the
    ADR). New additive Postgres table `order_promise_groups`
    (delete-then-reinsert on every save; `orders`' existing columns
    untouched); new additive `shared.ReleasedLine` pointer fields
    (`PromiseCptId`/`PromiseBasis`/`PromiseCutoffAt`) carrying per-line
    group attribution on the Kafka wire, `omitempty`. wes-work-planning
    needs zero changes (verified against its real consumer decode
    struct) — read the ADR before extending `promise_policy.go`,
    `order.go`'s promise fields, or the Postgres/Kafka promise wiring.

Other ADR-adjacent facts worth knowing without opening every file:

- Gateway API `HTTPRoute` chart template exists (`charts/order-management`
  `values.yaml` `gatewayApi:` block) but is additive/disabled by default —
  it is not itself the subject of a numbered ADR in this list; check the
  chart's own comments if you need its exact behavior.
