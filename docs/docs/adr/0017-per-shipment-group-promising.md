---
id: 0017-per-shipment-group-promising
slug: /adr/0017-per-shipment-group-promising
title: 17. Per-shipment-group promising (ADR 0014 step B, the second half)
sidebar_label: 17. Per-shipment-group promising (ADR 0014 step B, second half)
description: "ADR 0017 — order.PromisePolicy gains PromiseGroups, grouping an AllowPartialShipment order's allocated lines by the cutoff each independently makes and giving every group its own Promise; a ship-complete order still gets exactly one group, computed by the unchanged Promise search, so its behaviour is byte-identical to before. Order gains a PromiseGroup breakdown alongside its legacy single-valued promise fields, which are re-derived as a latest-cutoff projection. Additive Postgres table and Kafka per-line fields; wes-work-planning needs no change."
---

# 17. Per-shipment-group promising (ADR 0014 step B, the second half)

## Status

Accepted.

## Context

ADR 0014 §3, verbatim: "`Order.AllowPartialShipment` already exists (ADR
0003). Its meaning is extended from 'may release lines separately' to
'may promise lines separately': when true, lines are grouped by the
cutoff they can make and each group gets its own `Promise`; the order's
`PromiseDate()` becomes the latest of them (so no existing reader sees an
earlier date than before). When false the whole order is one group and
the slowest line governs, exactly as today."

ADR 0016 shipped the OTHER half of what ADR 0014 §4 originally bundled
into "step B" — eligibility-driven `PathSelectionPolicy` — and explicitly
deferred this half, per its own §5: "Per-shipment-group promising...
[is] deferred to a future ADR/PR." That deferral was correct: per-group
promising is domain-invasive in a way routing is not. It changes what
`order.PromisePolicy.Promise(now, o)` computes (exactly ONE `Promise` for
the whole order, ignoring `AllowPartialShipment` entirely), what `Order`
stores (single-valued `promiseDate`/`promiseCptId`/`promiseBasis`), what
Postgres persists (single columns on `orders`), and what the Kafka wire
carries (`OrderAllocated`/`OrderPartiallyAllocated`'s order-level
`promise_date`/`promise_cpt_id`/`promise_basis`). Every one of those is a
real, separately-reviewable surface, and — critically — `AllowPartialShipment=false`
(ship-complete, the default per ADR 0003) is the overwhelming common
case. Any change here carries a hard backward-compatibility requirement:
every existing test for a ship-complete order's promise must keep
passing UNMODIFIED, and `wes-work-planning`'s real, already-shipped
consumer (verified by re-reading its actual `orderAllocatedData` decode
struct, not assumed) must need zero changes to keep working.

## Decision

### 1. `order.PromiseGroup` — the new value object

```go
type PromiseGroup struct {
	LineNos []int
	Promise Promise
}
```

A set of line numbers sharing one `Promise`. `LineNos` is a plain slice,
not a set — line order is already meaningful elsewhere on this aggregate
(`OrderLine.LineNo` is a stable 1-based position), and a small sorted
slice is simpler to persist and to read in a log line than a map.

### 2. `PromisePolicy.PromiseGroups` — reuse the existing search, add only the grouping step

```go
func (p PromisePolicy) PromiseGroups(now time.Time, o *Order) ([]PromiseGroup, bool)
```

- **`AllowPartialShipment=false`**: exactly one group covering every
  allocated line, computed by calling the EXISTING, UNCHANGED
  `Promise(now, o)` once and wrapping its result. This is the "exactly
  as today" case ADR 0014 §3 requires to be byte-identical —
  `Promise`'s own body, its window search, its capacity/eligibility
  checks, and its fallback delegation to `LeadTimePolicy` are not
  touched by this ADR at all.
- **`AllowPartialShipment=true`**: each allocated line is evaluated
  INDEPENDENTLY, as if it were the sole line of a single-line order
  (reusing the identical window-search/fallback logic via a new
  unexported helper, `promiseForLines`, parameterized on an explicit
  line slice instead of deriving lines from `o` internally — the
  minimum refactor needed to reuse the search per line without changing
  `Promise`'s own external behaviour or signature). Lines whose
  independently-computed `Promise` value is identical (same `Basis`,
  same `CptId`, same `CutoffAt`) are partitioned into the same group,
  in first-occurrence order for determinism. This directly implements
  "lines are grouped by the cutoff they can make."

  The per-line fallback (`fallbackForLines`) still delegates to
  `LeadTimePolicy.PromiseDate`, which is NOT modified — it keeps its
  existing whole-order, slowest-line-governs signature. When the group
  being evaluated is every allocated line (the common single-group
  case), `fallbackForLines` calls `p.fallback(now, o)` directly, so
  that path is provably identical to `Promise`'s own fallback. When
  evaluating a genuine subset (a specific line, for the
  multi-group case), a temporary `Order` is rehydrated containing only
  that subset via the existing `Rehydrate`/`RehydrateOrderLine`
  constructors — no new constructor, no change to `LeadTimePolicy`
  itself — so `LeadTimePolicy`'s "slowest allocated line governs" rule
  applies correctly to just that subset.

### 3. `Order` widens additively: `promiseGroups []PromiseGroup` alongside the legacy fields

```go
func (o *Order) PromiseGroups() []PromiseGroup
func (o *Order) SetPromiseGroups(groups []PromiseGroup)
```

`SetPromiseGroups` stores the full breakdown as the new source of truth
AND re-derives the legacy `promiseDate`/`promiseCptId`/`promiseBasis`
fields from it, so every EXISTING reader of those three fields (the
Postgres repo's write path, the wire event publisher,
`PromiseDate()`/`PromiseCptId()`/`PromiseBasis()` themselves) keeps
working unchanged.

**The projection rule** — this is the one place ADR 0014 §3's text needed
an explicit extension, and it is documented here rather than left
implicit:

- `PromiseDate()` = the **latest `CutoffAt`** among all groups. This is
  ADR 0014 §3's own stated rule verbatim ("the order's `PromiseDate()`
  becomes the latest of them") — no existing reader ever sees an
  earlier date than the single-promise behaviour would have produced.
- ADR 0014 does not specify an aggregation rule for `CptId`/`Basis` — a
  single string cannot represent N different departures. This ADR makes
  the honest choice: `PromiseCptId()`/`PromiseBasis()` are projected
  from the SAME group whose `CutoffAt` is that latest one. All three
  legacy fields therefore describe "the group with the latest cutoff",
  consistently, rather than three independently-chosen groups that
  could disagree with each other about which departure they describe.

Calling `SetPromiseGroups` with a single group covering every allocated
line reproduces `SetPromise`'s own field assignment byte for byte — this
is what makes the ship-complete path's behaviour provably identical, not
merely "should be the same" (see `TestSetPromiseGroups_SingleGroup_MatchesSetPromise`).
`SetPromise` itself is left completely untouched: it remains the
narrower, existing method every pre-ADR-0017 test exercises directly.

`Rehydrate` keeps its exact 6-argument signature — every existing call
site and test compiles and behaves unchanged. A new
`RehydrateWithGroups` (7 arguments: the same six plus `promiseGroups
[]PromiseGroup`) is the widened constructor a repository adapter uses to
round-trip the full breakdown; `Rehydrate` itself is now sugar for
`RehydrateWithGroups(..., nil)`.

### 4. `allocationDeps.setPromiseDate` always calls the new path

```go
func (deps allocationDeps) setPromiseDate(o *order.Order) {
	if groups, ok := deps.Promise.PromiseGroups(deps.Clock.Now(), o); ok {
		o.SetPromiseGroups(groups)
	}
}
```

**Design decision, and why**: this could have branched on
`o.AllowPartialShipment()` and kept calling the old
`Promise`/`SetPromise` path for ship-complete orders, only invoking
`PromiseGroups`/`SetPromiseGroups` for partial-shipment orders. That
was rejected in favour of always calling the new path, because
`PromisePolicy.PromiseGroups` degrades to exactly one group for
`AllowPartialShipment=false`, computed via the unchanged `Promise`
search, and `SetPromiseGroups` with that single group reproduces
`SetPromise`'s assignment exactly (§3, and its dedicated unit test). The
OUTCOME for a ship-complete order is therefore provably identical either
way — calling one method here, rather than two branches that would need
to be kept in sync by hand as this code evolves, is the smaller and more
obviously-correct diff against `allocateAndRelease`, which is exactly
this fleet's stated preference for extending over inventing parallel
paths.

### 5. Postgres: one additive table, `orders`' existing columns untouched

`migrations/0003_promise_groups.up.sql`:

```sql
CREATE TABLE order_promise_groups (
    order_id  TEXT NOT NULL REFERENCES orders(id),
    group_no  INT NOT NULL,
    line_nos  INT[] NOT NULL,
    cpt_id    TEXT,
    cutoff_at TIMESTAMPTZ NOT NULL,
    basis     TEXT NOT NULL,
    PRIMARY KEY (order_id, group_no)
);
```

`orders.promise_date`/`promise_cpt_id`/`promise_basis` (migration 0002)
are **not modified, not repurposed** — `OrderRepo.Save` keeps writing
them exactly as before, from `Order.PromiseDate()`/`PromiseCptId()`/
`PromiseBasis()`, which are now `SetPromiseGroups`'s projection (§3).

A normalized `order_promise_group_lines(order_id, group_no, line_no)`
join table — mirroring `order_lines`' own one-row-per-line shape more
closely — was considered instead of the `INT[] line_nos` column. The
array column was chosen because a promise group is always read and
rewritten as one atomic unit (see below: delete-then-reinsert on every
save), never queried or joined line-by-line the way `order_lines` is; a
join table would add a second table and a second delete/reinsert loop
for a query pattern this service does not have.

**Save strategy: delete-then-reinsert the order's full group set on
every save**, rather than diff/upsert a breakdown whose shape (how many
groups, which lines land in which) can change entirely between
allocation passes — a line moving from one cutoff to another, or a
previously-solo line joining a shared group once a second line
allocates, is a structural change to the whole set, not a per-row
update. This mirrors this repo's existing `order_lines` convention of
one row per line rewritten on every save, just widened to "one row per
group, the whole set rewritten."

`FindByID` reads both the existing single columns AND the new
`order_promise_groups` rows, and reconstructs the aggregate via
`RehydrateWithGroups`.

**Round-tripped for real against a live Postgres 16** (see
`TestOrderRepo_PromiseGroups_RoundTrip`, run against `docker compose up
-d postgres`, not just asserted against an in-memory fake): a
partial-shipment order with two groups (one shared by two lines with a
real CPT cutoff, one solo line on a LeadTime fallback) round-trips its
exact group count, line numbers per group, and `CptId`/`CutoffAt`/
`Basis` per group; overwriting with a differently-shaped single-group
breakdown on a second save correctly replaces the stale rows rather than
accumulating them.

### 6. Kafka wire: additive per-line fields only

`shared.ReleasedLine` gains three new pointer fields:

```go
type ReleasedLine struct {
	LineNo           int
	SKU              SKU
	PathID           PathId
	GiftWrap         bool
	FulfillmentClass string
	PromiseCptId     *string
	PromiseBasis     *string
	PromiseCutoffAt  *time.Time
}
```

All three are pointers, nil when the released line has no group
breakdown to attribute it to — mirroring this repo's existing
pointer-field convention for "may not apply" (`OrderLine.ReservationID`).
`allocateAndRelease` populates them from whichever `PromiseGroup`
(indexed via a new `promiseGroupByLine` helper) each released line
belongs to. On the wire (`internal/adapters/outbound/kafka/publisher.go`'s
`releasedLineData`), the three new JSON fields are `omitempty`:
`promise_cpt_id`, `promise_basis`, `promise_cutoff_at`.

The existing top-level `OrderAllocated`/`OrderPartiallyAllocated` fields
— `promise_date` (FROZEN, unchanged shape), `promise_cpt_id`,
`promise_basis` — are completely unchanged: they continue to carry the
"latest group" order-level projection §3 describes, exactly as ADR 0014
shipped them.

**`wes-work-planning` needs zero changes for this phase** — verified by
re-reading its actual, currently-shipped consumer decode struct (not
assumed): `orderAllocatedData{OrderId, PromiseDate time.Time, Lines
[]orderLineData}`. It decodes neither the existing order-level
`promise_cpt_id`/`promise_basis` (ADR 0014's own fields, already live
and already unread by that consumer) nor anything about per-line promise
identity. Adding three new, always-optional per-line fields that
`orderLineData` has no field to receive changes nothing about how that
struct decodes the payload — extra JSON keys are silently ignored by
`encoding/json`'s default unmarshal behaviour. A future phase may teach
`wes-work-planning` to read the new per-line fields (so its own per-CPT
work grouping can use the SAME group identity this service now
computes); that is out of scope here, exactly as ADR 0014's rollout plan
already sequenced it ("wes-work-planning: read `promise_cpt_id` when
present" was step 3, already done; a further step to read per-line
promise identity is not yet scheduled).

### 7. What this ADR deliberately does NOT touch

- `RepromiseOrder`/`OrderRepromised` — ADR 0014 §5, sequenced as "This
  service, step C", its own future ADR after fulfillment-execution ships
  `TaskCPTMissed`/`PackageManifested` under its own ADR. Not implemented
  here.
- Promise KPIs / analytics — ADR 0014 §6, "Phase 6" per this fleet's own
  phase numbering. Not implemented here.
- `wes-work-planning` — not touched at all, per §6 above.
- `LeadTimePolicy`'s own signature or body — untouched; only called
  against differently-scoped temporary aggregates (§2).
- `PathSelectionPolicy`/routing — ADR 0016's own concern, already
  shipped, unrelated to this phase.

## Alternatives considered

- **Branch `setPromiseDate` on `AllowPartialShipment` and keep two
  separate call paths** (old `Promise`/`SetPromise` for ship-complete,
  new `PromiseGroups`/`SetPromiseGroups` for partial). Rejected — see
  §4: the single-path version is provably equivalent for the
  ship-complete case and is the smaller diff to maintain going forward.
- **A normalized `order_promise_group_lines` join table** instead of an
  `INT[]` column. Rejected — see §5: no query in this service ever
  touches a group's lines independently of the whole group, so the join
  table's only benefit (line-level querying) is unused, at the cost of a
  second table and a second delete/reinsert loop.
- **Diff/upsert the group breakdown row-by-row on save**, instead of
  delete-then-reinsert. Rejected — see §5: the shape of the breakdown
  (group count, line membership) can change structurally between
  allocation passes, so there is no stable `group_no` identity to upsert
  against; delete-then-reinsert is simpler and correct by construction.
- **Aggregate `PromiseCptId`/`PromiseBasis` independently of
  `PromiseDate`'s "latest cutoff" group** (e.g. pick the group with the
  most lines, or the earliest cutoff). Rejected: ADR 0014 §3 only
  specifies the `PromiseDate` rule; picking a DIFFERENT group for
  `CptId`/`Basis` would let the three legacy fields describe three
  different departures simultaneously, which is a more confusing and
  less honest summary than "all three describe the same one group,
  consistently."

## Consequences

**Easier**

- `AllowPartialShipment=true` orders now get the real per-shipment
  promise behaviour ADR 0014 always intended — the Amazon-style "two
  dates for a book and a bag of dog food" behaviour is finally live, not
  just documented as a future step.
- `PromiseGroups()` is the new full-fidelity source of truth for any
  future consumer (KPIs, `RepromiseOrder`'s eventual per-group
  recompute) without those future phases needing to re-derive grouping
  logic — it already exists here.
- The ship-complete floor (`AllowPartialShipment=false`, the default) is
  provably, not just intuitively, unchanged: the exact same `Promise`
  method, the exact same `SetPromise` field assignment, reached through
  one extra, transparent wrapping step.

**Harder**

- `PromisePolicy.PromiseGroups`'s multi-group branch evaluates the
  window search once PER allocated line, rather than once for the whole
  order — for an order with many lines and a wide CPT horizon, this is
  more `Schedule.NextCutoffs`/`Capability.CycleTimeP95`/
  `Capacity.Remaining` calls than before. Every existing outbound port
  adapter this policy calls through (`ports.CPTScheduleCache`,
  `ports.ProcessPathCatalogue`, `ports.PathCapacity`) is a local,
  Kafka-fed in-memory cache today (ADR 0013/0014/0015), so this is not a
  new network cost, but it is a real, measurable increase in per-order
  CPU work for a partial-shipment order with many lines — worth
  revisiting if profiling ever shows it matters.
- The aggregate's promise state genuinely grew (as ADR 0014's own
  Consequences section warned it would): `Order` now carries both the
  legacy summary and the full group breakdown, and any future change to
  either must keep them consistent — `SetPromiseGroups` is the only
  place that invariant is enforced, so a future change must route
  through it rather than mutating the legacy fields directly.
- Coverage and mutation gates on `internal/domain/order` needed real new
  tests, as ADR 0014's own Consequences section predicted ("The
  coverage and mutation gates... will need real new tests, not a
  threshold adjustment") — delivered here (see Verification), not
  deferred.
- A ship-complete order and a partial-shipment order whose lines all
  happen to share one cutoff are now indistinguishable by their
  `PromiseGroups()` shape alone (both report exactly one group) — a
  future analytics consumer wanting "is this order eligible for
  per-group promising" as a signal must still read
  `AllowPartialShipment()` directly, not infer it from group count.

## Verification

Ran for real, in this worktree, against the exact fleet toolchain
(gofmt, `go vet`, `golangci-lint run ./...` pinned v2.13.1,
`gremlins` v0.6.0, and a real `docker compose up -d postgres` — not
simulated):

```
$ gofmt -l .
(empty)

$ go vet ./...
(clean)

$ golangci-lint run ./...
0 issues.

$ make coverage
...
Coverage: 93.7% (gate: 90%)

$ make arch-test
--- PASS: TestHexagonalDependencyRule (5 subtests)
--- PASS: TestAnalyticsIsolation
--- PASS: TestPortsAreCustomerOwned
ok

$ make bdd
ok (all packages, no regressions)

$ DATABASE_URL=postgres://order:order@localhost:5434/order?sslmode=disable \
    go test -tags=integration ./internal/adapters/outbound/postgres/... -v
--- PASS: TestOrderRepo_SaveAndFindByID_RoundTrip
--- PASS: TestOrderRepo_BackorderedAndCancelledRoundTrip
--- PASS: TestOrderRepo_FindByID_Missing
--- PASS: TestOrderRepo_NextID_Unique
--- PASS: TestOrderRepo_PromiseGroups_RoundTrip   (new, ADR 0017)
--- PASS: TestEventPublisher_AppendsToEventsTable
ok

$ make mutation-fast
Killed: 63, Lived: 6, Not covered: 10
Test efficacy: 91.30%  (gate: 89%, improved from the pre-ADR-0017 baseline)
Mutator coverage: 87.34%  (gate: 83%, improved from the pre-ADR-0017 baseline)
```

Every pre-existing test in `internal/domain/order`,
`internal/application/usecases`, and
`internal/adapters/outbound/{kafka,postgres}` — including every
ship-complete-order promise test (`LeadTimePolicy`, `PromisePolicy.Promise`,
`ReceiveOrder`'s single-promise assertions) — passes UNMODIFIED, proving
ADR 0014 §3's stated backward-compatibility guarantee by construction,
not by inspection.
