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

See `CLAUDE.md` for the full ubiquitous language, aggregate invariants,
and architecture rules this codebase must honor.

## Status

v1 (MVP) scope. Full tooling parity with the other five services (Helm
chart, gremlins mutation gate, godog/BDD, MCP adapter, Kafka integration
events) is deliberately deferred — see `CLAUDE.md`'s "Explicitly deferred"
section.

## Running locally

```
docker compose up -d          # Postgres 16
# migrate up (see Makefile once scaffolded)
go run ./cmd/order
```

## License

MIT (or match the other repos' licensing — TBD).
