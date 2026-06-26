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

### Phase 3 — Homegear depth-parity — DONE (sysvars) / AT PARITY (rest)

The parity target is aiohomematic's *Homegear* support, not the CCU —
aiohomematic itself has no full CCU feature parity for Homegear. Against
that target, three of the four hub surfaces were already at parity and
only sysvars were a real gap.

- **Programs / Rooms / Functions — already at parity, no work.**
  aiohomematic's `HOMEGEAR_CAPABILITIES` sets `programs=False`,
  `rooms=False`, `functions=False` and its `backends/homegear.py` has no
  methods for them (Homegear has no ReGa engine and no
  room/function metadata RPC). openccu-loom returns `ErrUnsupported`
  for programs and derives rooms/functions from device fields — the same
  behaviour. Confirmed, not a gap.
- **3a — Sysvar adapter — DONE.** Homegear is XML-RPC-only, so the
  JSON-RPC hub bootstrap (`WireHub` → `loadSysvars` via `SysVar.getAll`)
  fails at login and never populated the hub — `/api/v1/sysvars` was
  empty. Added an XML-RPC sysvar load + refresh path
  (`internal/central/adapter/homegear_hub_wiring.go`) that calls the
  Homegear backend's `getAllSystemVariables`, infers the value type from
  the Go value (Homegear ships name+value only), reconciles into the hub
  model, and serves the `hub.sysvar_refresh` job — mirroring
  aiohomematic's `get_all_system_variables` path. Value writes route
  through `setSystemVariable`. Tested by `homegear_hub_wiring_test.go`
  (unit, full type matrix) and `tests/integration/homegear_sysvar_test.go`
  (real XML-RPC wire path against godevccu Homegear mode).
  *Sysvar create/update-metadata/delete via the SPA stays nil for
  Homegear: those carry ReGa-only metadata Homegear does not model.*

**Concluded (2026-06).** Homegear is **at parity with the defined target**
(aiohomematic's Homegear support) and the topic is closed. The
`HomegearBackend` implements the full operations surface — devices, paramsets,
get/set value, links, sysvars, metadata, device name, `determineParameter` —
and the only `ErrUnsupported` methods are ReGa-only concepts Homegear has no
engine for (programs, rooms, functions, inbox, system-update, sysvar-create),
exactly matching aiohomematic. Going **beyond** this target (full CCU-like
depth, non-HomeMatic Homegear families such as Z-Wave/EnOcean, a Homegear-native
sysvar-create path, validation against a live Homegear) is the explicit
non-goal recorded in `SPECIFICATION.md` §2.2 ("No Homegear depth-parity (full)")
and is **not** planned. No further Homegear work is scheduled.

### Phase 4 — Trigger-driven / opportunistic (low, non-blocking)

Both items confirmed correctly deferred; no action this round.

- **`valve.Modulating` profile-registry wiring — at parity, blocked.**
  The type and constructor exist and are largely complete
  (`internal/model/custom/valve/`, Info/Config/HA-discovery), but no
  device profile maps onto it. Crucially, the reference stack has **no
  modulating-valve device either** — it ships only an irrigation valve
  (`IP_IRRIGATION_VALVE`). So leaving `valve.Modulating` unregistered is
  parity, not a gap; wiring it would mean inventing a device mapping the
  reference does not have. Wire it in `init.go` only if such a device
  appears upstream. *Effort: S. Blocked on a real device.*
- **`GetProgramDataPointByStatePath` O(1) index.** Pure performance, not
  a parity item. Current O(n) scan is fine at the typical ≤300 programs.
  *Effort: S. Deferred to a performance milestone.*

## HomegearBackend depth-parity

**Status**: backend abstraction + basic backend in place; depth-parity
deferred to a future release.

The `internal/client/backends/homegear.go` backend speaks the same
XML-RPC surface as `CcuBackend` and works end-to-end for devices,
data points, and value writes. The hub surfaces, measured against
aiohomematic's *Homegear* support (the correct parity target — see
Phase 3 above):

- **Sysvars — DONE.** Loaded + refreshed over XML-RPC
  `getAllSystemVariables` via
  `internal/central/adapter/homegear_hub_wiring.go`; value writes via
  `setSystemVariable`. `/api/v1/sysvars` now populates for Homegear.
  Sysvar create / update-metadata / delete stays nil (ReGa-only
  metadata Homegear does not model).
- **Programs — at parity (no work).** aiohomematic sets
  `programs=False` for Homegear and ships no program methods (no ReGa
  engine). openccu-loom returns `ErrUnsupported`; `/api/v1/programs`
  is empty on both stacks by design.
- **Rooms / Functions — at parity (no work).** aiohomematic sets
  `rooms=False` / `functions=False` for Homegear. openccu-loom derives
  rooms/functions from device fields, not a top-level catalogue RPC
  Homegear lacks; behaviour matches.

A Homegear-backed installation now runs with sensors + actors + sysvars
working; programs/rooms/functions are empty by design, matching the
reference stack.

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
