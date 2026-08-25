---
title: Quickstart
sidebar_label: Quickstart
description: Run the service locally and walk every endpoint with curl.
---

# Quickstart

## Run it

### Option A — in-memory, no database (fastest)

```bash
go run ./cmd/order
# {"level":"INFO","msg":"database url not configured; using in-memory adapters"}
# {"level":"INFO","msg":"http server listening","addr":":8080"}
```

With no `DATABASE_URL`, the service starts on the in-memory adapters and is
fully functional.

### Option B — with Postgres

```bash
docker compose up -d postgres          # Postgres 16 on localhost:5434

export DATABASE_URL='postgres://order:***@localhost:5434/order?sslmode=disable'
go run ./cmd/order                     # migrations run automatically at startup
```

Migrations live in `migrations/` and are applied by `golang-migrate` on
boot; there is no separate migrate step to remember.

### Option C — wired to the real Suppliers

Both outbound clients default to **permissive (no-op) mode**, so tests and
CI never reach the network. Permissive here does not mean fail-open:
allocating real stock or releasing real work must never appear to succeed
against a no-op, so a permissive client refuses the operation with a clear
`downstream-not-configured` problem response. Only `http` mode is suitable
for a real integration test or deployment:

```bash
export INVENTORY_STORAGE_MODE=http
export INVENTORY_STORAGE_BASE_URL=http://localhost:8080
export WES_WORK_PLANNING_MODE=http
export WES_WORK_PLANNING_BASE_URL=http://localhost:8081
export HTTP_ADDR=:8082
go run ./cmd/order
```

## Walk the API

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
backordered line is a successful outcome reported in the body, not an
error:

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

**ReleaseOrder** — enqueues each allocated line as work on its process
path:

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

**CancelOrder** — revokes every allocated line's reservation, then
cancels:

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

## Tests

```bash
go build ./...
go vet ./...
go test ./...
go test ./... -race

# Coverage gate on domain + application
go test -race -coverprofile=coverage.out \
  -coverpkg=./internal/domain/...,./internal/application/... ./...
go tool cover -func=coverage.out
```

`make check` runs `fmt-check`, `vet`, `build`, `lint`, `test` in one pass —
the same feedback CI gives you post-push, available locally pre-commit. See
the [Quality gate section of the README](https://github.com/claudioed/order-management#quality-gate).
