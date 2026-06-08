# Matter Behavioral-Parity Contract

!!! info "Who this page is for"
    Contributors and AI agents working on the Matter bridge. End users
    and administrators do not need this page — see [Matter](user/matter.md)
    for the operator-facing guide.

OpenCCU-Loom's Matter side is a deliberate, behaviour-level port of
[matter.js](https://github.com/project-chip/matter.js) HEAD. This page
defines what "parity" means in practice and which standing guards keep
the port honest, so that a change which merely *looks* right but drifts
from the gold standard cannot land unnoticed.

**Audience:** every contributor and AI agent that touches Matter-side code in
OpenCCU-Loom. This is a standing contract, not an audit you run once. Read it
before your first Matter change and treat it as binding.

It complements the [`matter.js as the Matter Gold Standard`](../CLAUDE.md#matterjs-as-the-matter-gold-standard)
section of `CLAUDE.md`: that section tells you matter.js HEAD is the gold
standard; this document tells you what *parity* means, why **behaviour** parity
(not just schema parity) is the bar, and which standing guards enforce it.

---

## 1. The principle

> **Think for yourself, and always verify against matter.js / connectedhomeip.
> Mirror behaviour, not just shape.**

Two halves, both mandatory:

1. **Reason about the change.** Understand the cluster, the command, the state
   machine, the failure modes. Do not cargo-cult.
2. **Verify against the gold standard.** Before you write a Matter-side fix or
   feature, read the corresponding matter.js source (and connectedhomeip for
   wire-truth). Your implementation mirrors theirs — same defaults, same
   constraints, same status codes, same order, same wire shape — unless a
   divergence is deliberate and recorded.

A change that "looks right" but was never checked against matter.js is not
acceptable, no matter how reasonable it seems. The protocol's edge cases were
encoded into matter.js through real interop testing against Apple Home, Google
Home, and Alexa; we mirror that hard-won behaviour rather than re-deriving it.

## 2. Why matter.js / chip, and why *behaviour*

matter.js HEAD is a certified, production-tested, continuously-evolving Matter
stack. connectedhomeip (chip) is the CSA reference. Together they are the
authority for:

- cluster IDs, revisions, attribute / command / event IDs (schema), **and**
- defaults, constraint enforcement on writes, command semantics, status codes,
  conformance gating, subscribe / report cadence, commissioning and CASE state
  machines (**behaviour**), **and**
- the byte-level wire shape (TLV, IM messages, sigma).

**Schema parity is necessary but not sufficient.** It is easy to advertise the
right attribute IDs and revisions and still get the behaviour wrong — accept a
write matter.js rejects, return the wrong status, skip a constraint, drop a
session that must survive. Those defects pass every schema test and surface as
silent Apple / Google pair-aborts or mis-behaving devices that take days to
attribute back. Behaviour parity is the bar.

This is not hypothetical: a parity sweep found a whole class of write-constraint
and command-semantics defects (wrong setpoint limits, an unenforced
percent-max, a `SupportedOperatingModes` bitmap that marked a mandatory mode
unsupported, a `MoveToColorTemperature` that never moved, an `UpdateNOC` that
tore down its own response session) — every one of which a schema test would
have waved through.

## 3. The workflow for every Matter-side change

1. **Read the matter.js source first.** Likely paths:
   - schema constant / revision / id → `../matter.js/packages/model/src/standard/elements/<name>.element.ts`
   - cluster behaviour (defaults, mandatory attrs, conformance, write
     constraints, command logic) → `../matter.js/packages/node/src/behaviors/<name>/<Name>Server.ts`
   - device type → `../matter.js/packages/node/src/devices/<name>.ts`
   - wire codec / IM / sigma → `../matter.js/packages/types/src/tlv/`, `../matter.js/packages/protocol/src/`
   - wire-truth cross-check → `../connectedhomeip/src/app/...`, `../connectedhomeip/src/messaging/...`
2. **Mirror the behaviour** in Go idiom (struct-with-methods, `context.Context`,
   goroutines for TS decorators / mixins / `Promise<T>`). Keep the same
   defaults, constraints, status codes, and order.
3. **Cite the source in the Go code:**
   `// Mirrors matter.js packages/node/src/behaviors/.../FooServer.ts:bar`
   (or the chip path). Provenance must survive drift. `TestDocPurity` permits
   matter.js / chip `path:line` references in comments; it forbids legacy-CCU
   project names and audit-tracking codes.
4. **Add a parity test — behaviour, not just schema.** A new constraint adds a
   row to the negative-write parity table (§4). A new cluster server adds a
   schema parity case. A wire change adds / updates a TLV fixture. PRs without
   parity coverage are rejected.
5. **Record deliberate divergences** in [`docs/parity/by_design.md`](./parity/by_design.md)
   (the living catalogue). A non-trivial divergence also gets an ADR. Valid
   divergences are TypeScript-only optimisations that fight Go's GC, or
   decorator patterns with no Go equivalent. Invalid divergences are
   hand-coding cluster revisions, attribute IDs, constraint defaults, status
   codes, or Apple-required tag patterns — those go verbatim from matter.js.

## 4. The standing guards (enforcement)

Parity is held by build- and test-time guards, **not** by periodically
regenerated audit reports. These are the mechanism; keep them green and extend
them with every change:

| Guard | Location | Locks |
| --- | --- | --- |
| **Schema parity** | `internal/north/matter/**/parity_matterjs_test.go`; pin `docs/parity/matter/matter-schema-snapshot.json` (regen via `docs/parity/matter/extract-from-matter-js.ts`) | cluster / device-type IDs, revisions, attribute / command / event IDs vs matter.js HEAD |
| **Behavioural negative-write parity** | `internal/north/matter/cluster/matter_negative_write_parity_test.go` | a write/invoke matter.js *rejects* is rejected by Loom with the matching IM status (ConstraintError 0x87 / InvalidCommand 0x85), plus boundary positive controls against over-rejection. Add one row per new constraint. |
| **Wire-codec parity** | `internal/north/matter/tlv/parity_matterjs_test.go`; fixtures `docs/parity/matter/tlv-wire-fixtures.json` | byte-level TLV / IM shape |
| **Wiring-capability pins** | `tests/contract/wiring_pins/dormant_capability_wiring_test.go` | every capability gate / setter is actually wired on the production path — fails the build the moment a wiring is removed, even though the capability keeps passing its own unit test (the "implemented but never wired" bug class) |
| **Reference-controller validation** | `tests/chiptool/` (`//go:build chiptool`), `internal/north/matter/conformance/` | end-to-end behaviour against the real `chip-tool` commissioner |
| **Divergence catalogue** | [`docs/parity/by_design.md`](./parity/by_design.md) | every intentional deviation, with rationale |

When a parity sweep is genuinely warranted, prefer extending these guards over
producing a throw-away report: a guard catches the *next* regression, a report
catches only today's.

## 5. The aiohomematic relationship — different, on purpose

The CCU side and the Matter side have **different** gold standards and
**different** lifecycles:

- **aiohomematic (and its sibling family)** is the gold standard for the **CCU
  side** — transports, devices, paramsets, custom-DP composition, visibility,
  grouping. OpenCCU-Loom has matured to deep parity here (the cross-stack
  model-snapshot is the authoritative measure, at a documented accepted-drift
  steady state), and is already a **superset in scope**: the standalone-daemon
  surface — MQTT, REST, WebSocket, the config UI, the Matter bridge — has no
  aiohomematic counterpart. aiohomematic is therefore consulted as **reference
  prior-art** when a specific CCU-semantics question arises ("how does
  aiohomematic do X?"), not swept wholesale as an ongoing audit target. Where
  Loom deliberately advances beyond it, `by_design.md` records the divergence.
  OpenCCU-Loom is a real evolution, not a port frozen to its source.

- **matter.js / chip** is the gold standard for the **Matter side**, and it is a
  **living, certified, evolving** standard. matter.js HEAD bumps cluster and
  device-type revisions; the wire shape is interop-critical. Parity here is a
  **permanent discipline**, re-verified on every Matter change and whenever the
  matter.js pin is advanced — not a one-time audit. This contract exists because
  that asymmetry is real: the CCU port converges, the Matter mirror never
  "finishes".

The two reference layers do not overlap. CCU wire knowledge stays in
aiohomematic; Matter wire knowledge stays in matter.js. When a single bridge
feature spans both (a HmIP DataPoint mapped onto a Matter cluster) the boundary
is the `internal/model/custom/<dp>/matter.go` file — left side mirrors
aiohomematic, right side mirrors matter.js.

## 6. The non-negotiable checklist

Before you open a Matter-side PR:

- [ ] I read the matter.js (and, for wire-truth, chip) source for this
      cluster / behaviour / device-type.
- [ ] My implementation mirrors its defaults, constraints, status codes, and
      order — or the divergence is recorded in `by_design.md` (+ ADR if
      non-trivial).
- [ ] I cited the matter.js / chip `path:line` in the Go code.
- [ ] I added or extended a **behaviour** parity test (a constraint row, a
      command-semantics case, a wire fixture) — not only a schema assertion.
- [ ] The standing guards in §4 are green.

If you cannot check every box, the change is not ready.
