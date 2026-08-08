# ADR 0036 — Matter `Bridge`: defer the CommissioningSession/IMEngine facade split

- **Status**: accepted (defer with plan)
- **Date**: 2026-06-15
- **Related**:
  `internal/north/matter/bridge/bridge.go`,
  the analysis item Area 7 [W2]/[P2] in
  `notes/audits/architecture-analysis-2026-06-15.md`,
  [matter.js as the Matter gold standard](https://github.com/SukramJ/openccu-loom/blob/main/CLAUDE.md)

## Context

The architecture analysis (Area 7 [W2]) flagged `Bridge` as a 46-field
god-object and proposed ([P2, L]) decomposing it into a
`CommissioningSession` (PASE/CASE/OpCreds/window/`sigma1Replied`) and an
`IMEngine` (subscriptions/event log/timed gates), leaving `Bridge` a
composing coordinator.

Examined against the code, the picture is more nuanced than "god-object
with inline logic".

## Finding

**`Bridge` already delegates its heavy logic to extracted collaborators.**
The 46 fields are largely *references* to already-separate types plus the
coordinator's own transport/lifecycle state, not inline implementation:

- Commissioning: `paseHandler PaseHandler`, `caseHandler CaseHandler`
  (interface-typed, separate implementations with their own providers),
  `commissioningWindow *CommissioningWindow` (own type), `sigma1Replied`
  map.
- IM: `subManager *subscription.Manager` (own package), `eventLog
  *im.EventLog` (own type), the timed-gate / status-response sync.Maps.
- Transport/MRP: `ackTracker *mrp.AckTracker`, `listener *udp.Listener`,
  `topology *endpoint.Topology`, `dispatcher *endpoint.TopologyDispatcher`,
  `assembler *endpoint.Assembler` — all own types.

So the proposed `CommissioningSession` / `IMEngine` would mostly be
**facades grouping references** to components that are already separated,
plus thin coordination methods. The genuine win (extracting the heavy
PASE/CASE/subscription/event-log logic into its own units) has **already
happened**.

**A facade split is high-churn on commissioning-critical code.** The
remaining fields are accessed pervasively across `receive.go`,
`subscribe.go`, the dispatch helpers, and `bridge.go` (`b.paseHandler`,
`b.subManager`, `b.timedDeadlines`, `b.sigma1Replied`, …). Moving them
behind `b.commissioning.*` / `b.im.*` renames a large number of call
sites in the most safety-critical Matter path — where a subtle slip
produces silent Apple/Google pairing aborts that, per CLAUDE.md, take
days to attribute back.

**Validation tooling for that risk is not available here.** matter.js is
not checked out in this environment (so the gold-standard cross-check the
CLAUDE.md workflow requires cannot run), and the decisive integration
signal — the chip-tool / Apple-pairing sweep — is a live-CCU, hardware
test, not part of unit CI. A big-bang facade move verified only by unit
tests would ship un-cross-checked.

## Decision

**Defer** the CommissioningSession/IMEngine facade split. The cost (a
large rename across commissioning-critical code) is not justified by the
benefit (grouping references to already-separated collaborators), and the
change cannot be validated here to the standard this code demands.

When pursued, the safe path is incremental, not big-bang, and gated on
the matter.js cross-check + a chip-tool sweep between steps:

1. Group the field *references* into named embedded sub-structs
   (`commissioning`, `im`) so field promotion keeps call sites unchanged
   — a pure grouping with no behaviour change — and land it on its own.
2. Migrate methods onto the sub-structs one cluster at a time (PASE/CASE,
   then subscriptions, then timed gates), running the chip-tool brief
   (`notes/contributor/chip-tool-test-brief.md`) after each.
3. Only then narrow `Bridge` to a composing coordinator.

## Alternatives considered

- **Full facade split now.** Rejected — high-churn on commissioning-
  critical code, marginal structural gain (logic already extracted), and
  unvalidatable here (no matter.js, no chip-tool in unit CI).
- **Field-cluster comments only.** Considered as a navigability aid but
  not landed: unlike the adapter package (ADR 0034), `Bridge`'s fields
  are already grouped and commented by concern in `bridge.go`; comment
  churn would add little.

## Consequences

- The Area 7 [W2] item is closed as "deferred with an incremental,
  validation-gated plan" rather than a big-bang refactor.
- The heavy-logic extraction the proposal targeted is already in place
  (`subscription.Manager`, `mrp.AckTracker`, `im.EventLog`,
  `CommissioningWindow`, the PASE/CASE handlers); `Bridge`'s remaining
  size is its inherent role as the transport/commissioning coordinator.
- A future incremental split starts from the documented step plan and
  runs under the gold-standard workflow once matter.js + chip-tool are
  available.
