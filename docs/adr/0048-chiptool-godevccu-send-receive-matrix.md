# ADR 0048 — Hermetic per-DP-type Matter send/receive suite (chip-tool ↔ godevccu)

- **Status**: Accepted
- **Date**: 2026-07-09
- **Related**:
  [ADR 0012 — Matter pure-Go implementation](./0012-matter-pure-go-implementation.md),
  [ADR 0013 — Matter commissioning bring-up](./0013-matter-commissioning-bring-up.md),
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  Matrix: [`docs/matter/chiptool-send-receive-matrix.md`](../matter/chiptool-send-receive-matrix.md),
  Apple pairing: [`docs/matter/apple-pair-test-guide.md`](../matter/apple-pair-test-guide.md)

## Context

Commissioning (Apple Home / Google Home / chip-tool) is a special case with
per-controller quirks. Everything **after** CASE, however, is uniform: a
controller reads, writes, invokes and subscribes attributes, and the bridge
translates each to/from a CCU parameter. That post-commissioning interaction is
where the substantive, DP-type-specific bugs live — and we were finding them one
at a time by hand against a live Apple Home:

- a thermostat setpoint write that failed with IM `Failure` because the Climate
  cluster asserted `value.(int16)` while the wire decoder delivers `int64`;
- external CCU changes never reaching a controller because custom-DP endpoints
  (dimmer, thermostat, cover, lock, siren) did not implement the
  `MatterChangeNotifier`, so the bridge never wired a CCU→Matter change listener;
- a setpoint that appeared to "revert" seconds after being set.

None of these were caught by the in-process parity suites (schema-correct, but
they never drive a real controller through the full read/write/subscribe path),
nor by the `chiptool` workflow, which until now covered commissioning and
protocol shape (subscribe, wildcard, descriptor, BasicInformation, events) but
not **functional** send/receive per cluster.

The `chiptool` CI workflow already runs a real chip-tool commissioner (north)
against the daemon backed by an in-process **godevccu** CCU simulator (south).
The machinery to exercise every DP type in both directions was therefore mostly
present; what was missing was (a) a controlled way to drive and observe the CCU
side, and (b) the per-cluster test matrix itself.

## Decision

Build a **hermetic, per-DP-type send/receive suite** on top of the existing
`tests/chiptool/` harness. For every Matter cluster the bridge can expose, cover
both directions:

- **SEND** (controller → CCU): a chip-tool `write`/invoke, asserted against the
  simulator's **ground truth** via `MockCCU.GetDPValue(address, param)` — not a
  second Matter read, so "the daemon forwarded south" is proven independently of
  "a read round-trips it".
- **RECEIVE** (CCU → controller): a simulated device-originated push via
  `MockCCU.FireDeviceEvent(address, param, value)`, asserted through a
  **proactive** Subscribe report using `harness.AwaitProactiveReport` — which
  subscribes *first*, then fires the change, then awaits the report. Firing
  before subscribing would let the subscribe's initial read reflect the value
  and pass even when the change-notifier is broken; the proactive ordering is
  what makes the suite catch the missing-notifier class of bug.

The authoritative coverage matrix (one row per cluster, with the exact commands,
CCU params, encodings and known gaps) lives in
[`docs/matter/chiptool-send-receive-matrix.md`](../matter/chiptool-send-receive-matrix.md),
implemented as one test file per cluster family under `tests/chiptool/`.

**godevccu gains the primitives this needs** (released as a new pinned version):

- `VirtualCCU.SimulateDeviceEvent(address, valueKey, value)` — frames a write as
  an unsolicited device-originated change (`force=true`, so read-only `ops=RE`
  telemetry params such as `ACTUAL_TEMPERATURE` are not rejected) and fires the
  computed follow-up events to registered callbacks exactly as a live device
  would. This is the RECEIVE cornerstone.
- a case-insensitive fleet loader and device-response mapping (marketing-cased
  `HmIP-*` fixture names such as `HmIP-PS` / `HmIP-SWDO` were being silently
  dropped), and explicit `ComputeEvents` telemetry entries for the fleet's
  read-only params so RECEIVE coverage cannot regress on a future tightening of
  the echo default.

Divergences from a live CCU are accepted where they do not affect
event-processing correctness (fidelity of specific values is covered on other
paths); the goal here is that Matter events are processed cleanly **for every DP
type**. Rows where the model layer has a genuine notifier gap (ColorControl
hue/sat/CT, ElectricalPower/Energy) are pinned as documented-limitation tests
(assert *no* proactive report, confirm read-on-demand) rather than omitted, so
closing the gap flips a red assertion instead of adding a new one.

## Consequences

- **The bugs above become standing regression guards.** A thermostat setpoint
  write, an external dimmer/thermostat change, and the setpoint-preservation
  invariant are now hermetic, per-DP-type CI tests instead of manual Apple-Home
  observations.
- **Execution is CI-only.** chip-tool needs Linux + its runtime; the suite
  compiles locally under `-tags=chiptool` but only *runs* in the `chiptool`
  workflow (arm64, `needs-chiptool` label). Local `make test` says nothing about
  it — the same constraint the commissioning tests already live under.
- **godevccu coupling.** The suite depends on a godevccu release carrying
  `SimulateDeviceEvent` and the loader/ComputeEvents fixes; loom pins that
  version. During development a temporary `replace => ../godevccu` bridges the
  gap.
- **The Apple/Google black boxes stay for interop only.** Post-CASE functional
  correctness is now proven hermetically; the real-controller guides remain for
  the commissioning quirks they alone exhibit
  ([`apple-pair-test-guide.md`](../matter/apple-pair-test-guide.md)).
- **The "setpoint revert" is reframed.** The hermetic invariant (a temperature
  push must not change the reported setpoint) holds with a well-behaved
  simulated thermostat; the field symptom is most likely device-mode behaviour
  (an eTRV in a schedule mode not holding a manual setpoint) rather than a
  Matter-reporting defect — to be confirmed and, if needed, fixed in the Climate
  mode-transition path.
