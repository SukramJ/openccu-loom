# ADR 0008 — AggregatedState Default-Flip and Legacy-Path Removal

- **Status**: Accepted; topology direction superseded by [ADR 0011](./0011-mqtt-topic-and-payload-architecture.md)
- **Date**: 2026-04-30
- **Extends**: [ADR 0007 — Strong Model: `Source` Interface for Read + Write](./0007-strong-model-source-interface.md)

## Context

ADR 0007 phased the `Source` migration in 10 steps. After step 10 the
`discovery_aggregate.go` `buildX` functions still carried both the
legacy per-parameter wire topology (with inline Jinja templates) and
the new aggregated-state path. The dual shape was the only way to
land step 10 without breaking the ~30 existing HA-Discovery test
cases that pinned the legacy topology verbatim.

Wire→semantic translation logic (SET_POINT_MODE int → `"heat"/"auto"`,
lock numeric → `LOCKED/UNLOCKED/JAMMED/UNLOCKING`, light HSL Jinja, …)
duplicated knowledge that the model already encoded. The duplicate is
the principal cleanup target this ADR addresses.

## Decision

Adopt a **two-step retirement** of the legacy path:

- **Step A — Default-flip**: switch `BridgeConfig.AggregatedState`
  default to `true`; HA-Discovery references the aggregated state
  topic, per-parameter raw topics keep being published in parallel.
  Operators on the raw plane (Node-RED, InfluxDB) are unaffected.

- **Step B — Legacy-path removal**: delete the per-`buildX` `else`
  branch, the `resolve*` helpers, and the optional Reader interfaces
  that only the legacy branch consumed. The aggregated topology
  becomes the only mode; `discovery_aggregate.go` reaches the ADR
  0007 target of <200 LOC.

A soak window between Step A and Step B gives one full release of
real-traffic validation before the legacy fallback disappears, so
wire-shape regressions surface while the fallback still exists.

## Why split the steps

A single-PR retirement would compress test-fixture migration AND the
soak window into one merge, making the operator-visible impact
(HA-Discovery payload changes) untestable in production until merge
day. Splitting buys a quarter of real-traffic validation without
keeping engineering blocked.

## Trade-offs

- **Two PRs vs. one**: more bookkeeping, but the soak window between
  them is the actual safety buffer. Combining would lose that.
- **Operators on the raw plane**: no impact. The raw per-parameter
  state topics remain — the cleanup only affects HA-Discovery's
  `*_state_topic` references.
- **Aggregated-topic publishing cost**: minor. Channels with many
  parameters benefit (one retained payload instead of N); channels
  with few parameters break even.
- **Test-fixture maintenance**: Step A doubles the relevant test
  cases temporarily. Step B halves them and keeps only the new path.

## Consequences

- ADR 0007 step 10's <200 LOC target becomes reachable.
- Wire→semantic translation lives only in the model
  (`internal/model/custom/*/payload.go`). The MQTT bridge becomes a
  routing shell, matching the role aiohomematic2mqtt plays in the
  Python reference.
- Adding a new device type means: add a new Custom-DP with its
  payload methods, register service methods, ship. No bridge edits,
  no `discovery_aggregate.go` edits.
- The optional Reader interfaces deleted in Step B simplify the
  `device.Channel` consumer surface — `Channel` is read purely
  through `payload.Source` from the bridge's perspective.

## Status notes

Both steps are shipped: the `buildX` functions and the legacy `else`
branches are gone, the aggregated-state default is on, and the
optional Reader interfaces no longer exist. The aggregated channel
state topic itself was subsequently retired by [ADR 0011](./0011-mqtt-topic-and-payload-architecture.md),
which moved the canonical read surface back to per-DP topics with a
curated `custom/<kind>/state` aggregate for derived fields only. The
`Source` contract from ADR 0007 remains in force; only the MQTT
topology this ADR ratified shifted.

Any vestigial `WithAggregatedState` no-op call sites are mop-up
work and land as ordinary refactors when touched.
