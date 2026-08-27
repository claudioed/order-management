---
id: order-funnel-report
slug: /analytics/order-funnel-report
title: Order Funnel & Allocation Health report
sidebar_label: Order Funnel & Allocation Health
description: The order-management analytical data product — the Order Funnel & Allocation Health report, keyed per process path × hour bucket, served read-only by cmd/order-reports over REST.
---

# Order Funnel & Allocation Health report

This is order-management's **analytical data product**: a read model built from
this service's own domain events, projected into a separate analytical database
and served read-only by `cmd/order-reports`. It is defined and governed by
[ADR-0006](../adr/0006-analytical-data-product.md).

It is **eventually consistent** — a projection of the event stream, not a live
view of the OLTP database — to a freshness SLA of **p95 event-to-report lag <
30s**.

## What it measures

The report is keyed per **PathId × hour bucket** (UTC, truncated to the hour).
The order funnel is `received → allocated → released`, with cancellations and
hard allocation failures as leakage; the line-level counters track the same
lifecycle at OrderLine granularity.

| Field                       | Source event                       | Meaning |
| --------------------------- | ---------------------------------- | ------- |
| `ordersReceived`            | `OrderReceived`                    | Orders accepted into the building. Funnel entry. |
| `ordersAllocated`           | `OrderAllocated`                   | Orders whose every line was allocated. |
| `ordersPartiallyAllocated`  | `OrderPartiallyAllocated`          | Orders with some lines allocated, some backordered. |
| `ordersAllocationFailed`    | `OrderAllocationPartiallyFailed`   | Orders that hit a hard (non-business) allocation failure. Leakage. |
| `ordersReleased`            | `OrderReleased`                    | Orders whose every line was released as work. Funnel exit. |
| `ordersCancelled`           | `OrderCancelled`                   | Orders cancelled before release. Leakage. |
| `linesAllocated`            | `OrderLineAllocated`               | Order lines reserved against inventory-storage. |
| `linesBackordered`          | `OrderLineBackordered`             | Order lines with insufficient stock (409). Leakage. |
| `linesReleased`            | `OrderLineReleased`                | Order lines enqueued as wes-work-planning work. |

Each metric counts events per `(path_id, hour_bucket)`. Only these funnel-moving
event types populate the report; any other analytics event is acknowledged and
ignored.

## Path enrichment

Order-level events do not carry a process path, but the report is keyed by one.
The analytics publisher enriches each event with its path via an `OrderRepo`
lookup: an order's path is its first line's path (a v1 simplification that is
exact because intake places every line on the same default path), and
`OrderLineReleased` carries its line's path directly.

## REST API

Served by `cmd/order-reports` (default `:8092`), over a read-only Postgres pool.

### `GET /reports/funnel`

Query parameters:

- `from` (**required**, RFC3339) — window start, inclusive.
- `to` (**required**, RFC3339) — window end, exclusive.
- `pathId` (optional) — exact-match process-path filter.
- `granularity` (optional) — only `hour` is supported (the default).

```json
{
  "rows": [
    {
      "pathId": "pick",
      "hourBucket": "2026-06-01T14:00:00Z",
      "ordersReceived": 5,
      "ordersAllocated": 4,
      "ordersPartiallyAllocated": 0,
      "ordersAllocationFailed": 0,
      "ordersReleased": 3,
      "ordersCancelled": 1,
      "linesAllocated": 8,
      "linesBackordered": 2,
      "linesReleased": 6
    }
  ]
}
```

A missing or malformed `from`/`to` returns an RFC 7807
`application/problem+json` 400.

### `GET /reports/funnel/freshness`

Reports how far the read model trails real time — the age of the most recently
applied event. Zero when the read model is empty.

```json
{ "lagSeconds": 12.4 }
```

### `GET /healthz`

Liveness for the reports reader: `{"status":"ok"}`.

## Envelope v1

Every analytics event is published on `warehouse.order-management.analytics`
under the shared Envelope v1 wrapper, keyed by `order_id`:

```json
{
  "event_id": "…",
  "event_type": "OrderReleased",
  "occurred_at": "2026-06-01T14:03:12Z",
  "source": "order-management",
  "schema_version": 1,
  "data": { "order_id": "…", "path_id": "pick" }
}
```

The projector dedupes on `event_id` (idempotent, at-least-once) and ignores
unknown `event_type`s.
