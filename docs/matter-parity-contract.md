# Matter Parity — the host side

!!! info "Who this page is for"
    Contributors and AI agents working on the Matter bridge. End users
    and administrators do not need this page — see [Matter](user/matter.md)
    for the operator-facing guide.

The Matter wire stack is no longer part of this repository. The TLV codec, the
Interaction Model, PASE / CASE, MRP, DNS-SD, the cluster servers and the
endpoint assembler live in the
[go-fabric](https://github.com/SukramJ/go-fabric) module, which this daemon
embeds — and so does the contract that governs them:

> **[go-fabric — Matter Behavioural-Parity Contract](https://github.com/SukramJ/go-fabric/blob/main/docs/matter-parity-contract.md)**
>
> Read it before your first Matter-side change, in either repository. It
> defines what parity means, why *behaviour* parity (not just schema parity)
> is the bar, and which standing guards enforce it.

What stays here is the **host half of a bridge**: the model walk in
`internal/north/matteradapter/`, the endpoint store in
`internal/store/matterendpoint/`, and the per-device projections under
`internal/model/custom/<dp>/matter.go`. That half has its own gold standard and
its own guards, and they are listed below.

---

## The boundary

go-fabric's `contract/` package is the seam. Everything on its far side mirrors
matter.js and is covered by that module's contract. Everything on this side —
**which** data point becomes **which** cluster attribute, which product maps to
which device type, how many endpoints a physical device gets — is this
repository's decision.

The `internal/model/custom/<dp>/matter.go` files are where the two meet: the
left side of each file mirrors `aiohomematic` (the CCU-side gold standard), the
right side mirrors matter.js. A projection defect produces a device that pairs
successfully and then misbehaves — the same class of failure go-fabric's
contract is about, and one its guards cannot see.

## Host-side standing guards

| Guard | Location | Locks |
| --- | --- | --- |
| **Schema pin** | `TestMatterSchemaSnapshotInSync` (`tests/contract/`) against `notes/parity/matter/matter-schema-snapshot.json`, refreshed with `make sync-matter-schema` | that a Matter schema change cannot arrive unnoticed inside a go-fabric version bump |
| **Scenario corpus** | `tests/scenario/` over `notes/parity/matter/scenarios/`, with `tests/contract/matter_scenario_gate_test.go` as the coverage gate | end-to-end behaviour of a real projection against a live bridge; every custom-DP type with a `matter.go` must have at least one tagged scenario |
| **Wiring-capability pins** | `tests/contract/wiring_pins/dormant_capability_wiring_test.go` | every capability gate / setter is actually wired on the production path — the "implemented but never wired" bug class |
| **Reference-controller validation** | `tests/chiptool/` (`//go:build chiptool`) | end-to-end behaviour of this daemon against the real `chip-tool` commissioner |
| **Projection divergences** | [`notes/parity/by_design.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/parity/by_design.md), `## Matter / matter.js Divergences` | every intentional deviation in how the model projects onto Matter |

Wire-level divergences — anything about the codec, the IM, sessions or the
cluster servers themselves — are recorded in
[go-fabric's `by_design.md`](https://github.com/SukramJ/go-fabric/blob/main/notes/parity/by_design.md),
not here.

## The aiohomematic relationship — different, on purpose

The CCU side and the Matter side have **different** gold standards and
**different** lifecycles:

- **aiohomematic (and its sibling family)** is the gold standard for the **CCU
  side** — transports, devices, paramsets, custom-DP composition, visibility,
  grouping. OpenCCU-Loom has matured to deep parity here (the cross-stack
  model-snapshot is the authoritative measure, at a documented accepted-drift
  steady state), and is already a **superset in scope**: the standalone-daemon
  surface — MQTT, REST, WebSocket, the config UI, the Matter bridge — has no
  aiohomematic counterpart. aiohomematic is therefore consulted as **reference
  prior-art** when a specific CCU-semantics question arises, not swept
  wholesale as an ongoing audit target. Where Loom deliberately advances beyond
  it, `by_design.md` records the divergence.

- **matter.js / chip** is the gold standard for the **Matter side**, and it is
  a **living, evolving** standard: matter.js HEAD bumps cluster and device-type
  revisions, and the wire shape is interop-critical. A port of a CCU stack
  converges, because its source stands still; the Matter mirror never
  "finishes".

The two reference layers do not overlap. CCU wire knowledge stays in
aiohomematic; Matter wire knowledge stays in matter.js.
