# OpenCCU-Loom — open roadmap items

This file tracks deliverables that are scoped but deferred. Items land
here when we explicitly choose not to do them now but commit to
revisiting them later. Completed items are moved to `CHANGELOG.md`.

## Depth-parity execution plan (aiohomematic)

**Status**: prioritised, not started.

The structural port (phases 0–10) is complete; remaining work is
*depth* parity against the aiohomematic reference family. This section
fixes the order in which the open items are worked. **Matter parity is
a separate track against matter.js — out of scope here** (see
[`docs/matter-parity-contract.md`](./matter-parity-contract.md)).

Sequencing principle: protective guards first (they cover all later
model changes), then cheap high-certainty wins (model already done,
wiring only), then hardening, then the large Homegear track last — the
CCU is the primary fleet, Homegear is the secondary backend.

**Recommended order: Phase 0 → 1 (parallel) → 2 → 3a → 3b → 3c →
4 (on trigger).**

### Phase 0 — Parity-guard hardening (foundation, do first)

- **Code-enforce the model-snapshot drift baseline.** The cross-stack
  snapshot pipeline exists, but the "~270 architecturally-accepted
  drifts" baseline lives only in `CLAUDE.md` prose; regression
  detection is manual at the release gate. Promote it to a threshold
  constant + a CI step that fails when the drift count grows past the
  baseline without a matching `docs/parity/by_design.md` entry.
  *Effort: S. Risk: low.* Highest leverage per effort — it protects
  every model change in the phases below.
  **Gate:** `script/model_snapshot_diff.py` runs in CI and breaks on
  drift > baseline.

### Phase 1 — Cheap parity closes (model done, wire only)

Independent and parallelisable; both close real aiohomematic↔Loom gaps
where the model layer is complete and only the north-bound publisher
hook is missing. Follow the established publisher pattern
([ADR 0010](./adr/0010-discovery-payload-from-model.md),
[ADR 0011](./adr/0011-mqtt-topic-and-payload-architecture.md)).

- **MetricHubSensor → MQTT publishing.** Model present
  (`internal/model/hub/metrics.go`); REST exposes it, MQTT does not.
  *Effort: S. Risk: low.*
- **Inbox → MQTT publisher hook (`HUB_REFRESHED`).** Model + REST
  present (`internal/model/hub/payload.go`); the MQTT publisher hook is
  missing. *Effort: S. Risk: low.*
  **Gate:** MQTT topic-contract test + golden-replay of the hub event.

### Phase 2 — Resolver / correctness hardening

- **Discovery-snapshot SUBTYPE / `model_id` resolver.** A
  test-infrastructure gap (not a production defect) that masks the
  parity signal: SUBTYPE propagation for HmIP-PS/PSM variants and
  eTRV / SMO subtypes needs hardening in the translation resolver under
  `internal/ccudata/`. *Effort: M. Risk: medium.*
  **Gate:** `tests/integration/discovery_snapshot_field_diff_test.go`
  reports 0 `model_id` invariant failures.

### Phase 3 — Homegear depth-parity (largest block, own track)

The full detail lives in the **HomegearBackend depth-parity** section
below; sequenced here smallest / most-unblocking first. Today
`/api/v1/{programs,rooms,functions,sysvars}/...` return empty on a
Homegear backend.

- **3a — Sysvar adapter** (type coercion / persistence). First: removes
  SPA mis-renders, least model rework. *Effort: M. Risk: medium.*
- **3b — Rooms / Functions remodel** (break the CCU-shape assumption).
  *Effort: M–L. Risk: medium.*
- **3c — Programs (Homegear-flavoured ReGa adapter, JSON-RPC).** Largest
  single item. *Effort: L. Risk: high.*
  **Gate (per sub):** REST integration test against a Homegear backend
  returns non-empty, correct results.

### Phase 4 — Trigger-driven / opportunistic (low, non-blocking)

- **`valve.Modulating` profile-registry wiring.** The type and
  constructor exist (`internal/model/custom/valve/`); only the profile
  registry does not map a device onto it. *Effort: S. **Blocked** until
  a device profile needs it.*
- **`GetProgramDataPointByStatePath` O(1) index.** Current O(n) scan is
  fine at ≤300 programs. *Effort: S. Deferred to a performance
  milestone.*

## HomegearBackend depth-parity

**Status**: backend abstraction + basic backend in place; depth-parity
deferred to a post-0.1.0 release.

The `internal/client/backends/homegear.go` backend speaks the same
XML-RPC surface as `CcuBackend` and works end-to-end for devices,
data points, and value writes. What is intentionally **not** ported
to 0.1.0:

- **Programs** — Homegear exposes its automation programs through a
  different JSON-RPC surface than the CCU's ReGa runtime; the
  per-program API surface, including the WS commands and REST
  routes, would need a Homegear-flavoured ReGa adapter.
- **Rooms / Functions** — the CCU's `Subsection.getAll` + `Room.getAll`
  JSON-RPC methods are CCU-specific; the Homegear analogue is a
  per-device metadata field rather than a top-level catalogue, and
  the daemon's room/function model currently assumes the CCU shape.
- **Sysvar parity** — Homegear's sysvar surface diverges from the CCU
  on type coercion and persistence; the ad-hoc handling needs a
  proper adapter to avoid silent mis-renders in the SPA.

These are scoped against the existing surfaces in
`internal/central/adapter/hub*.go` and the REST routes under
`/api/v1/programs/...`, `/api/v1/rooms/...`, `/api/v1/functions/...`,
`/api/v1/sysvars/...`. A Homegear-backed installation runs today
with sensors + actors working and the upper four surfaces returning
empty results — acceptable for v0.1.0; not acceptable long-term.

## Upstream pin: openccu-data

**Status**: steady state
**Owner**: upstream ([openccu-data](https://github.com/SukramJ/openccu-data))

The CCU metadata archives under `internal/ccudata/embedded/` are
mirrored from [openccu-data](https://github.com/SukramJ/openccu-data).
`make update-ccu-data` performs a one-shot resync; there is no longer
a plan to reimplement the extractors inside OpenCCU-Loom (see
[ADR 0003](./adr/0003-embed-occu-extracts.md)).

Open hygiene items:

- Periodically re-sync after openccu-data tags a new release.
- Consider promoting the pin to a GitHub-release artifact (e.g.
  `openccu-data-go-<ver>.tar.gz`) once a second Go consumer needs
  the archives.

## Optional: Go wrapper module for openccu-data

**Status**: not started
**Priority**: low
**Blocked by**: emergence of a second Go consumer (e.g. an externalised
`hmcli`, third-party tools).

### Context

Today OpenCCU-Loom vendors the openccu-data archives directly via
`go:embed`. If another Go project needs the same data, the right
answer is a lightweight companion module:

- `github.com/SukramJ/openccu-data-go` ships a single Go package with
  the `.json.gz` files under `go:embed`, mirroring the contents of
  openccu-data's `openccu_data/data/` tree.
- Released in sync with openccu-data's Python package (same version
  tag).

openccu-loom would then `go get` the module and drop its local embed
copy. Until that second consumer appears the overhead of a separate
repo is hard to justify.

### Deliverables (if/when we do it)

1. `openccu-data-go` repo under SukramJ with a CI pipeline that rebuilds
   the embed on every openccu-data release.
2. `openccu-loom` imports the module, removes the local mirror, drops
   `make update-ccu-data`.

## Dropped: native Go re-implementation of the CCU extractors

An earlier version of this roadmap proposed porting the Python
extractors to Go. That plan has been **cancelled** — openccu-data
is the single source of truth and covers the whole ecosystem. The
port would duplicate ~3500 LoC of curated heuristics with no
incremental benefit. See ADR 0003 for the reasoning.
