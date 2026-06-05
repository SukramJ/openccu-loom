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

### Phase 0 — Parity-guard hardening (foundation) — DONE

- **Code-enforce the model-snapshot drift baseline.** The threshold
  gate already existed (`script/model_snapshot_drift_check.py`,
  per-bucket baselines summing to ~280, run via `make snapshot-diff`);
  this phase hardened it rather than building it anew: removed the
  dangling `parity_audit.md` reference (the file no longer exists)
  and re-pointed it at `docs/parity/by_design.md`; fixed the broken
  env-override key derivation so the documented
  `OPENCCU_LOOM_DRIFT_GENERIC` / `_CHANNEL` / `_CUSTOM_ONLY_PY` /
  `_CALC` overrides actually resolve; added a TOTAL line and an
  unguarded-bucket guard that fails when the diff emits a drift bucket
  with no baseline (previously silently ignored).
  **Gate:** `make snapshot-diff` (release-time; the cross-stack diff
  is intentionally not in CI — the aiohomematic venv is absent there,
  by design per `.github/workflows/integration.yml`). CI dumps the Go
  side via `make snapshot-go`.

### Phase 1 — Cheap parity closes (model done, wire only) — ALREADY DONE

On inspection both items were already implemented and tested; the
`by_design.md` entries that listed them as deferred (A3-G5, A3-G9)
were stale and have been corrected to RESOLVED.

- **MetricHubSensor → MQTT publishing.** Wired in `wireOneCentral`
  (`internal/central/adapter/hub_mqtt_publisher.go`, `--- Metrics ---`
  block): System Health discovery + publish + `Metrics.OnUpdate`;
  Connection Latency via the per-interface
  `ConnectivityChangedEvent.LatencyMs` path. Tested by
  `hub_metric_sensors_test.go`. *Remaining: `MetricLastEventAgeSecs`
  has no production observer — a deferred scheduler job, not an MQTT
  gap; see `by_design.md`.*
- **Inbox → MQTT publishing.** Wired in the same file (`--- Inbox ---`
  block): `BuildInboxDiscovery` + initial publish + `Inbox.OnUpdate` →
  `PublishInbox`. Tested by `hub_mqtt_publisher_inbox_test.go`.

### Phase 2 — Resolver / correctness hardening — DONE

- **Discovery-snapshot SUBTYPE / `model_id` resolver.** The 25
  SUBTYPE-propagation failures (HmIP-PS/PSM uppercase, eTRV / SMO
  subtypes) were already resolved by the multi-stage
  `Translations.DeviceModelLabel` lookup (vendor-prefix strip + suffix
  strip + SUBTYPE fallback), covered by
  `internal/ccudata/translations_subtype_lookup_test.go`. Inspection of
  the committed snapshot showed only **2** residual devices with empty
  `model_id` — `HmIP-DLP` and `HmIP-UDI-SMI55` — and the cause was not
  the resolver but a missing device-model *label* in the upstream
  catalogue. Closed via the curated overlay
  (`internal/ccudata/embedded/translation_custom/device_models_{en,de}.json`),
  guarded by `internal/ccudata/device_models_overlay_test.go`. The
  stale "25 failures" doc comment on the field-diff test was corrected.
  **Gate:** regenerate the (gitignored) snapshot via `make snapshot-go`;
  the `model_id` invariant is expected to report 0 failures.

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
