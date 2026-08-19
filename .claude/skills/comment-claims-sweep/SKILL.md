---
name: comment-claims-sweep
description: Fan out read-only agents that verify code-comment claims against the actual code — comments naming consumers, "not wired yet" notes, file-header inventories, ratchet justifications. Run before every release. Use when asked for a comment sweep, a claims audit, or pre-release verification.
---

# Comment-claims sweep

Mechanical guards cover only two shapes of prose:
`TestDeclaredSilentEventDocsClaimNoConsumers` and
`TestRatchetReasonsAreNotDeferrals`. Everything else a comment asserts is
unverified. The 0.54.4 sweep found one live delivery bug and a dozen refuted
claims behind seven green PRs.

## What to look for

A comment is a **claim**. These four shapes go stale silently:

1. **Comments naming a consumer** — "for the MQTT/webhook consumers", "so
   MQTT/WS subscribers receive it", "consumed by X". Resolve the event type
   or symbol and check that a subscriber actually exists in production code
   (test files do not count).
2. **"stub" / "not wired yet" / "TODO: hook up"** — either still true (then
   the seam is dead and that is a finding) or long since done (then the
   comment misleads).
3. **File-header inventories** — "this file holds A, B and C". Check the file
   still holds exactly that.
4. **Ratchet justifications** in `tests/contract/` —
   `wiringSettersWithoutCaller`, `wiringSeamsUnderInvestigation`,
   `registryWalkersWithoutAdoptSeam`, `eventsWithoutSubscriber`. Each entry
   claims a reason; verify the reason still holds.

## How to run it

Size the fan-out from the host (see the root `CLAUDE.md`): these are
read-only `sweep` agents, so 6–8 in parallel is fine. Give each one a
disjoint tree — `internal/central`, `internal/north/mqtt`, `internal/north/rest`,
`internal/north/matter`, `internal/model`, `internal/store` + `internal/client`,
`tests/contract` — and this brief:

> Find every comment in <tree> that makes a checkable claim of the four shapes
> above. For each, resolve the claim against production code (exclude `_test.go`
> when checking whether a consumer exists). Report a table: file:line, the
> claim, VERIFIED / REFUTED, and the evidence line you checked. Do not edit
> anything. ≤ 250 words; longer tables to the scratchpad as a path.

## Accepting the result

A report is a claim too. For every REFUTED finding, read the cited lines
yourself before rewording or fixing — a refuted claim about a dead consumer is
usually a **delivery bug**, not a comment bug. Fix the code first, then the
comment.
