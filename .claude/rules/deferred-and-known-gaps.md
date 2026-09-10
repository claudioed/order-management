# Deferred and known gaps (document them, don't skip silently)

Several items originally listed as "v1 scope — explicitly deferred" have
since SHIPPED (Helm chart + CI packaging jobs, gremlins mutation gate,
godog/BDD acceptance suite, MCP inbound adapter, Kafka integration +
analytics fan-out, Postgres integration suite, arch-go fitness tests,
Spectral api-lint, docs-api-drift, standard metrics/telemetry, fleet MFE
console, FulfillmentClass classifier). CI and the README's "Deferred"
section are the current source of truth; check them, not this list, if you
need to confirm something has NOT shipped yet before treating it as future
work.

Still deferred, as of the last verified pass:

- **Real carrier-rate promise-date calculation.** `LeadTimePolicy` computes
  the promise date from a configurable per-path lead time — real, tested
  domain logic, not a stub — but there is no live carrier integration, and
  no such service exists in this fleet to call.
- **Kafka release-confirmation reply events from wes-work-planning.**
  v1 (ADR-0005) ships fire-and-forget: this service publishes
  `OrderAllocated`/`OrderPartiallyAllocated` and never learns whether
  `wes-work-planning`'s consumer actually processed the event or
  successfully enqueued its own work. Mirrors `inventory-storage`'s own
  ADR-0004 stance on transactional guarantees, not an oversight.
- **Clawing back released work on cancellation.** BR6 (ADR-0004): once any
  line is `Released`, v1 does NOT claw it back on `CancelOrder`.
- **Killing the surviving `promise.go` boundary mutants.** The mutation
  gate is pinned at the measured baseline (efficacy 90.70%, mutant-coverage
  81.13% — see `.gremlins.yaml`); hardening the lead-time fallback tests to
  ratchet those numbers toward the fleet's 99/99 target is tracked
  follow-up work, not a currently-enforced requirement.
- **Any change to `inventory-storage` or `wes-work-planning`.** This
  repo's entire build is 100% additive from their point of view — neither
  repository is ever touched from here.

Keep the README's own "Deferred" section as the authoritative, most
current list — this file is a pointer/summary, not a replacement for it.
