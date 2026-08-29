---
slug: /adr
title: Architecture Decision Records
sidebar_label: About ADRs
description: Why this service keeps ADRs, the template it uses, and how to propose a new one.
---

# Architecture Decision Records

## What these are

An **Architecture Decision Record (ADR)** captures a single
architecturally significant decision, its context, and its consequences —
at the moment it was made, in the words that were true then.

The value is not the decision; it is the **context**. Six months later,
the code shows *what* was chosen and can never show *why*, or what the
alternatives were, or what was known at the time. A reader who does not
know the why has two bad options: assume the decision was arbitrary and
change it, or assume it was sacred and work around it. An ADR gives them a
third.

## The format

These records use **Michael Nygard's template** — the de facto standard,
the same one every other `warehouse-systems` service uses:

```markdown
# NNNN. Title (a short noun phrase)

## Status
Accepted | Proposed | Deprecated | Superseded by ADR-XXXX

## Context
The forces at play: technical, business, operational. What is true that makes
this a decision rather than an obvious default? Written in full sentences and
in the present tense, describing the situation as it is.

## Decision
The response to those forces, stated actively: "We will…"

## Consequences
What becomes easier and what becomes harder — both. A record with only
positive consequences has not been thought about.
```

One decision per file, numbered `0001-`, `0002-`, … Numbers are never
reused and files are never deleted.

## Immutability

**An accepted ADR is never edited to change its decision.** If a decision
is reversed, write a new ADR that supersedes it, and update the old one's
*Status* line to point at the successor. The historical record of what was
believed at the time is the entire asset.

Typos, broken links and formatting are of course fair game.

## The records

| # | Title | Status |
| --- | --- | --- |
| [0001](./0001-hexagonal-ports-and-adapters.md) | Hexagonal (ports & adapters) architecture | Accepted |
| [0002](./0002-http-consumer-of-inventory-and-wes-not-shared-code.md) | HTTP consumer of inventory-storage and wes-work-planning, not shared code | Accepted |
| [0003](./0003-ship-complete-default-and-fail-closed-allocation.md) | Ship-complete by default and fail-closed allocation | Accepted |
| [0004](./0004-cancellation-boundary-at-release.md) | The cancellation boundary is release | Accepted, with a documented known gap |
| [0005](./0005-choreographed-release-via-kafka.md) | Choreographed release via Kafka, folded allocate-then-release, and pathId goes internal-only | Accepted |
| [0006](./0006-analytical-data-product.md) | Per-service analytical data product (report) via a separate analytics topic | Accepted |
| [0007](./0007-adopt-fleet-micro-frontend-console.md) | Adopt the fleet's micro-frontend console architecture (`order-mgmt-mfe`) | Accepted |

Each of these reconstructs a decision that is actually visible in this
repository's `CLAUDE.md` and code — none is a generic placeholder.

## Proposing a new one

1. Copy the template above into `docs/docs/adr/NNNN-short-kebab-title.md`,
   taking the next free number.
2. Open it with **Status: Proposed**.
3. Write the *Context* before the *Decision*. If the context does not make
   the decision feel inevitable, the context is incomplete — or the
   decision is wrong.
4. Fill in *Consequences* honestly, including the ones you dislike.
5. Add it to the table above and to `sidebars.ts`.
6. Raise it as a pull request; the discussion belongs on the PR, not
   inside the record.
7. On merge, flip the status to **Accepted**.

## What deserves an ADR

Something is architecturally significant if reversing it later would be
expensive — it constrains the structure of the code, pins a contract other
teams depend on, or is costly to undo.

**Yes:** the layering rule; how allocation and release are orchestrated
across two Suppliers; the fail-closed distinction between a business fact
and an infrastructure failure; the cancellation boundary at release.

**No:** which linter rules are enabled; a library upgrade; a refactor with
no external consequence. Padding the log with trivia makes the
significant records harder to find.
