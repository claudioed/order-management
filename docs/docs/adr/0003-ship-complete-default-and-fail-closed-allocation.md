---
id: 0003-ship-complete-default-and-fail-closed-allocation
slug: /adr/0003-ship-complete-default-and-fail-closed-allocation
title: 0003. Ship-complete by default and fail-closed allocation
sidebar_label: 0003. Ship-complete and fail-closed
description: ADR 0003 — BR2/BR3 — treat only a 409 as a backorder, and hold a ship-complete order back until every line is allocated.
---

# 0003. Ship-complete by default and fail-closed allocation

## Status

Accepted. Established with the initial implementation of this bounded context.

## Context

`AllocateOrder` calls `inventory-storage`'s `POST /reservations` once per
order line. Each of those calls can come back in three materially different
ways, and the whole correctness of this context turns on not confusing them:

1. **201** — stock is reserved. The line is `Allocated` and carries a
   `reservationId`.
2. **409** — inventory-storage has *decided*, authoritatively, that there is
   not enough usable stock. This is a **business fact**, and the business has
   a name for it: the line is `Backordered`.
3. **Anything else** — a connection refused, a timeout, a 500, a 502 from a
   proxy, a 400 from a contract drift. This is **not** a fact about stock. It
   is an absence of information: the reservation may exist upstream, or may
   not, and this context cannot tell.

The dangerous shortcut is to write `if err != nil { markBackordered() }`. It
looks tidy and it is wrong in the worst possible direction: a network blip
would silently tell an operator "we are out of stock" for goods sitting on the
shelf, and — because a backordered line is one a human is expected to retry
later — it would do so in a way that looks like a normal, expected outcome
rather than an incident.

The second question is what a backordered line means for the *order*. Retail
fulfilment has two legitimate answers, and they are business decisions, not
technical ones:

- **Ship-complete**: the customer gets one shipment. If any line is short, the
  order waits. This is the safe default — it never surprises a customer with a
  partial delivery they did not ask for, and it never spends pick labour on
  work that cannot complete the order.
- **Partial shipment**: send what is available now. Faster for the customer
  who opted in, but it commits real warehouse labour to a fraction of an order.

Whichever applies has to be visible in the order's *status*, and that status
must not be able to lie. A stored `status` column that is updated by whichever
code path last touched an order will eventually disagree with the lines it
claims to summarise — most likely at exactly the moment someone is debugging
why an order did not ship.

## Decision

**Three connected rules.**

### BR2 — only a 409 is a backorder; everything else fails the call

The outbound port is defined so that the adapter, not the use case, makes this
distinction: `InventoryReservationClient.Reserve` returns
`ports.ErrInsufficientStock` **only** for a 409, and a distinct error for a
transport failure or any other non-2xx status. `AllocateOrder` then:

- on `ErrInsufficientStock` — marks that ONE line `Backordered`, publishes
  `OrderLineBackordered`, and continues to the next line;
- on any other error — **fails the whole call**, marking nothing.

The permissive (no-op) outbound clients participate in this rule rather than
sidestepping it. Unlike the fail-*open* permissive adapters elsewhere in this
platform — where a classification lookup is a soft enrichment and "unknown" is
a safe answer — reserving real stock has no safe fake. So the permissive
clients return `ports.ErrDownstreamNotConfigured`, which is not a 409 and
therefore, by the rule above, fails the call loudly. Only `MODE=http` is
suitable for a real integration test or deployment.

A hard failure mid-pass still **persists the reservations that genuinely
succeeded** before returning the error. Discarding them would strand real
reservations inside inventory-storage with nothing left in this context able
to revoke them. Lines still `Pending` stay `Pending`, so re-issuing
`AllocateOrder` resumes where it stopped — the "cannot allocate the same line
twice" invariant is what makes that safe.

### BR3 — ship-complete is the default

`allowPartialShipment` defaults to `false`. With it false, ANY `Backordered`
line puts the WHOLE order in `Backordered` and **no line proceeds to release**
until `RetryAllocation` clears it. The domain enforces this at the release
boundary itself (`Order.EnsureReleasable` → `ErrShipCompleteBlocked`), not in
the use case, so there is no route to release that can skip the check.

With `allowPartialShipment` true, allocated lines are independently eligible
for release and the order reads `PartiallyAllocated`.

`RetryAllocation` is the ONLY transition from `Backordered` back to
`Allocated`, and it re-attempts backordered lines exclusively. It is therefore
also the only thing that can unblock a ship-complete order.

### The order-level status is derived, never stored

`Order.Status()` is computed from the line statuses on every read. There is no
`status` column in the `orders` table, and no field on the aggregate. A status
that is computed cannot drift out of sync with the lines it summarises, and
BR3 becomes a property of that one derivation rather than a rule that every
write path has to remember.

## Consequences

### Easier

- **An operator can trust a backorder.** Seeing `Backordered` means
  inventory-storage said so. Infrastructure trouble surfaces as a `503` with
  an RFC 7807 body, which is an incident, not a business outcome.
- **The dangerous case is directly testable.** "A transport failure must not
  backorder a line" is a first-class test at both the use-case and the
  adapter level, including an explicit assertion that the two error kinds
  never collapse into one.
- **BR3 cannot be bypassed.** The check lives on the aggregate, so any future
  caller of release gets it for free.
- **Status is always consistent.** There is no code path that can update lines
  without updating the summary, because there is no separate summary.
- **A misconfigured deployment fails loudly.** Booting with the default
  permissive clients and calling allocate returns a clear
  `downstream-not-configured` problem instead of a fake success.

### Harder

- **Deriving status on every read costs a loop.** Over an order's handful of
  lines this is irrelevant; it would matter if orders had thousands of lines,
  and that would be the moment to add a projection *alongside* the derivation,
  never in place of it.
- **"Status" cannot be queried in SQL.** A future "find all backordered
  orders" report needs a projection built from the same rule, and keeping the
  two in agreement is real work.
- **Partial progress after a hard failure is a state a reader must understand.**
  An order can genuinely sit with line 1 `Allocated` and line 2 `Pending`.
  That is deliberate — the alternative strands reservations — but it means
  "allocated" is not always all-or-nothing, and the retry semantics have to be
  documented (they are, on the endpoint).
- **A stuck backorder needs a human or a scheduler.** Nothing in v1 retries
  automatically; `RetryAllocation` is an explicit command. That is the honest
  v1 position, not an oversight.

## Update (operational visibility for the partial-allocation-on-failure outcome)

The "harder" consequence above — *"Partial progress after a hard failure is a
state a reader must understand"* — was, until now, discoverable only by
reading the code comment on `AllocateOrder`. The decision itself is
unchanged: a hard failure mid-pass still persists whatever was genuinely
reserved and still fails the call. What changed is that this outcome is now
also a first-class fact: when `AllocateOrder` returns a hard failure after
persisting at least one newly-allocated line, it publishes
`OrderAllocationPartiallyFailed` (`AllocatedLines`, `RemainingLines`, and a
truncated `Cause`) alongside the existing `OrderLineAllocated` events. This
does not change the fail-closed/idempotent behaviour above — deleting/
compensating the partial reservations was considered and rejected again here
for the same reason as BR2's "Easier" section: it would only move the
failure risk to a second, itself-fallible call (`DELETE /reservations/{id}`)
and would discard state a retry could otherwise use. Publishing this event is
best-effort: if the publish itself fails, that failure is joined onto the
original allocation error rather than replacing it, so the caller's real
failure is never masked.
