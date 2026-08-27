---
title: Aggregates & Invariants
sidebar_label: Aggregates & Invariants
description: The Order aggregate root, OrderLine, and every invariant they enforce.
---

# Aggregates & Invariants

One aggregate root, one entity, one value-object package. Every invariant
below is enforced **in the domain layer**, per `CLAUDE.md`'s "Aggregates &
invariants" section, and has a dedicated failing-path unit test.

```mermaid
classDiagram
  class Order {
    <<Aggregate Root>>
    -id OrderId
    -lines OrderLine[]
    -allowPartialShipment bool
    -promiseDate *time.Time
    +Status() Status
    +Allocate(lineNo, reservationId) error
    +Backorder(lineNo) error
    +Release(lineNo, workUnitId) error
    +Cancel() error
    +EnsureReleasable() error
    +EnsureCancellable() error
  }
  class OrderLine {
    <<Entity>>
    -lineNo int
    -sku SKU
    -quantity int
    -pathId PathId
    -giftWrap bool
    -status LineStatus
    -reservationId *string
  }
  Order "1" *-- "1..*" OrderLine : lines
```

## Order

**The aggregate root.** Owns `OrderId`, `OrderLine[]`,
`AllowPartialShipment bool`, `Status`, and `PromiseDate *time.Time`.

### Invariants

| Invariant | Enforcement |
| --- | --- |
| **Cannot allocate the same line twice.** | `Allocate` rejects a line not in `Pending` or `Backordered`. |
| **Cannot release a line that isn't `Allocated`.** | `Release` rejects any other line status. |
| **Cannot cancel once ANY line is `Released`.** | `EnsureCancellable` returns `ErrOrderAlreadyReleased`. |
| **Order-level `Status` is always computed from line statuses, never stored redundantly.** | `Status()` derives the value on every call; there is no backing field. |

### Status derivation

```mermaid
stateDiagram-v2
    [*] --> Received: ReceiveOrder
    Received --> Allocated: every line Allocated
    Received --> PartiallyAllocated: allowPartialShipment=true,<br/>mixed Allocated/Backordered
    Received --> Backordered: allowPartialShipment=false (BR3),<br/>any line Backordered
    Allocated --> Released: every line Released
    PartiallyAllocated --> PartiallyReleased: some lines Released
    Backordered --> Allocated: RetryAllocation clears every backorder
    Received --> Cancelled: CancelOrder (pre-release)
    Allocated --> Cancelled: CancelOrder (pre-release)
    Backordered --> Cancelled: CancelOrder (pre-release)
    Released --> [*]: cancellation no longer legal (BR6)
```

## OrderLine

**A single requested item within an `Order`.**

| Field | Meaning |
| --- | --- |
| `SKU` | The stock keeping unit ordered. |
| `Quantity` | Must be > 0. |
| `PathId` | wes-work-planning process path this line's work is enqueued onto; defaults to `"pick"` if not supplied. |
| `GiftWrap` | Whether the requester asked for gift packaging. |
| `LineStatus` | `Pending` / `Allocated` / `Backordered` / `Released` / `Cancelled`. |
| `ReservationId` | Set once allocated; needed to cancel. |

### Invariants

| Invariant | Enforcement |
| --- | --- |
| **`Quantity` must be > 0.** | Rejected at construction (`ReceiveOrder`). |
| **`SKU` must be non-empty.** | Rejected at construction. |
| **A `Backordered` line may transition back to `Allocated` ONLY via `RetryAllocation`.** | No other use case touches a `Backordered` line's status. |

## BR3 — ship-complete default

`AllowPartialShipment=false` (the default): if ANY line ends up
`Backordered` during `AllocateOrder`, the WHOLE order's status is
`Backordered` (no line proceeds to release) until a human/caller issues
`RetryAllocation`. `AllowPartialShipment=true`: allocated lines are
independently eligible for release; order status becomes
`PartiallyAllocated`. See
[ADR 0003](/docs/adr/0003-ship-complete-default-and-fail-closed-allocation).

## BR6 — cancellation boundary

`CancelOrder` is legal ONLY while no line has reached `Released`. Legal
cancellation revokes every allocated line's reservation via
`DELETE /reservations/{id}` on inventory-storage. Once ANY line is
`Released`, v1 does NOT claw back released work — this is a documented,
deliberate known gap (same honesty pattern as inventory-storage's
ADR-0003 "no expiry sweeper" section), not an oversight to silently paper
over. See
[ADR 0004](/docs/adr/0004-cancellation-boundary-at-release).

## Value objects (`internal/domain/shared`)

| Type | Rule |
| --- | --- |
| `OrderId` | Identity, minted by `OrderRepo.NextID` |
| `SKU` | Non-empty string |
| `PathId` | Non-empty string; defaults to `pick` when a line omits it |

Every failing-path test named in `CLAUDE.md`'s "Aggregates & invariants"
section — bin-capacity-style rejections, double-allocation, releasing an
unallocated line, cancelling after release, backordering via any route
other than `RetryAllocation` — has a dedicated unit test in
`internal/domain/order` and `internal/application/usecases`.
