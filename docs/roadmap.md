# OpenCCU-Loom — open roadmap items

This file tracks deliverables that are scoped but not yet started —
both work prioritised for an upcoming cycle and work we have explicitly
deferred. Items land here when we commit to revisiting them later.
Completed items are moved to `CHANGELOG.md`.

## Planned development items (2026-06-30 review)

**Status**: prioritised, not started.

These items came out of a code-verified development review. Each was
checked against the current tree before being accepted — several
originally-considered items shrank or were dropped once verified (for
example: Matter ACL enforcement turned out to be already wired; the MCP
bridge and the incident source are far more complete than assumed; the
SPA user/token CRUD backend already exists; hot-reload is the inverse of
the assumed default). The deferred / dropped candidates are listed at
the end of this section so the decision stays traceable.

Each accepted item below links to a detailed, transferable
implementation plan under [`docs/plans/`](./plans/). A second
verification pass while writing those plans shrank three items further
(see the per-item notes): the Matter subscribe quotas/cadence/teardown
and the Timed-interaction gate are already wired; the auto-tile engine
is already shipped; and combined-parameter auto-routing is already
implemented. The stale source statuses those passes found
(`by_design.md` A4-P01, the subscribe/timed deferral notes,
`docs/ui/auto-tile-concept.md` "Not yet implemented") are corrected as
part of the respective plans.

### Matter

- **Matter IM protocol depth.** The cluster/schema layer is broad, and
  the deeper verification pass corrected the original framing: the
  *subscribe state machine* (cadence enforcement, max-subscription caps,
  teardown) **and** the *Timed-interaction gate* are **already
  implemented and wired** (`cmd/openccu-loom/daemon_matter.go`,
  `internal/north/matter/im/receive_dispatch.go`,
  `StatusNeedsTimedInteraction` in `internal/north/matter/im/receive.go`).
  The genuine remaining gaps, in tractability order:
  - *OTA Provider* — only `OtaSoftwareUpdateRequestor` exists; no
    provider cluster (`internal/north/matter/cluster/core/`). **Decided
    scope: a schema-correct "NotAvailable" responder** (QueryImage
    replies schema-conformant `NotAvailable`), **not** BDX firmware
    hosting — mirrors matter.js `OtaSoftwareUpdateProviderServer`.
  - *GroupKeyManagement persistence* — the group table returns empty;
    persistence is unwired
    (`internal/north/matter/cluster/core/group_key_management.go`).
  - *Subscribe refinements* — small follow-ups (e.g. whether event
    replay must survive a restart); see the plan.
  - *Not a gap:* ACL enforcement is complete (`CheckACL` in
    `internal/north/matter/endpoint/dispatcher.go`, attached in
    production at `cmd/openccu-loom/daemon_matter.go`).
  *Reads matter.js first; mirrors behaviour; extends the parity guards
  per [`docs/matter-parity-contract.md`](./matter-parity-contract.md).*
  *Plan: [`docs/plans/A1-matter-im-depth.md`](./plans/A1-matter-im-depth.md).*

### Northbound bridges & integrations

- **Bidirectional webhook bridge + northbound plugin contract.** Build a
  common northbound `Bridge`/`Service` interface + registry (today each
  bridge is hand-wired in `cmd/openccu-loom/daemon.go`; no shared
  interface), with a bidirectional webhook as the first consumer:
  - *Outbound* — an event-bus consumer (the established
    `events.Subscribe` pattern, cf.
    `internal/north/mqtt/system_status_publisher.go`; fan-out at
    `internal/central/adapter/eventbridge.go`) that POSTs on
    datapoint/system events to configured URLs, with filters, HMAC
    signing and retry. New `north.webhook` config section.
    *Includes* emitting a new `hmevent.IncidentRecorded` bus event from
    `RecordIncident` (`internal/client/reliability/incident.go`) so
    incidents flow to the outbound webhook — the rest of the incident
    surface (SQLite store, REST handler, UI in `Diagnostics.svelte`)
    already exists.
  - *Inbound* — a REST/router surface where external systems POST to set
    values / trigger programs; shares the write/trigger handlers the
    CLI (below) also targets.
  *Plan: [`docs/plans/A4-webhook-plugin-contract.md`](./plans/A4-webhook-plugin-contract.md).*
- **Migrate MQTT / Matter / MCP / REST onto `bridge.Registry`.** Follow-up
  to A4: the north-bound `bridge.Service` + `Registry` contract
  (`internal/north/bridge/`) shipped with the outbound webhook as its only
  registered consumer. The established bridges (MQTT, Matter, MCP, REST) are
  still hand-wired with bespoke `Start`/`Stop` calls in
  `cmd/openccu-loom/daemon.go`. Wrap each in a thin `Service` adapter and
  register it on the shared `Registry`, replacing the inline lifecycle calls
  one at a time — behaviour-preserving, independently testable (the A4 plan
  §3a describes this incremental path). **Order MQTT last:** it carries a
  runtime supervisor (`cmd/openccu-loom/mqtt_supervisor.go`, `SwapBridge`)
  for hot-reload, so its `Service.Stop` must wrap that lifecycle cleanly
  rather than fight it; Matter/MCP/REST are mechanical wraps. Pure refactor
  — guard with the existing per-bridge behaviour/contract tests; do **not**
  generalise hot-swap into the `Service` interface (out of scope, per A4
  §3a). *Effort: S–M.*
  *Plan: [`docs/plans/bridge-registry-migration.md`](./plans/bridge-registry-migration.md).*
- **`hmcli` power-user CLI.** Grow `cmd/hmcli/` (today only
  `version`/`config validate`/`cache clear`/`export-def`) into a full
  REST client against a running daemon — `devices list/get/set`,
  `sysvar`, `program run`, `paramset`, `events tail`. Reuses the
  existing daemon-client pattern (`cmd/hmcli/export_def.go`,
  `cache.go`) and `pkg/hmapi` DTOs. **Decided:** adopt Cobra (MIT) now —
  root + `devices`/`sysvar`/`program`/`paramset`/`events` subcommand
  groups with shared persistent `--host`/`--token` flags, replacing the
  hand-rolled `switch`. Does *not* consume openccu-data, so it does not
  unblock the `openccu-data-go` module below. *Effort: M.*
  *Plan: [`docs/plans/B1-hmcli.md`](./plans/B1-hmcli.md).*
- **MCP `list_incidents` tool.** Add the one missing tool to the
  otherwise-complete MCP bridge (`internal/north/mcp/tools.go` already
  ships read + gated-write + hub tools); incident data is real
  (`internal/store/sqlite/incidents.go`,
  `internal/north/rest/handlers/incidents.go`). *Effort: S.*
  *Plan: [`docs/plans/B2-mcp-list-incidents.md`](./plans/B2-mcp-list-incidents.md).*
- **Expose `AdditionalInformation` north-bound.** Merge the enriched
  metadata maps (`OperatingVoltageLevelSensor.AdditionalInformation`,
  `internal/model/calculated/voltage.go:129`; `ServiceMessages` /
  alarm messages, `internal/model/hub/messages.go`) additively into the
  MQTT state payload (`internal/payload/`, `internal/north/mqtt/`) and
  REST. Recorded as by-design A1-BD01 in `docs/parity/by_design.md`;
  done as an additive schema extension. *Effort: S.*
  *Plan: [`docs/plans/C3-additional-information.md`](./plans/C3-additional-information.md).*
- **Combined-parameter auto-routing.** Verification found this is
  **already implemented**: `InterfaceClient.WriteUnconfirmedValue`
  (`internal/client/interface_client_orchestration.go`) already routes
  convertable parameters via `AddCombinedParameter`. So by-design
  **A4-P01 is stale**. Remaining work is small: add a regression test,
  mark A4-P01 RESOLVED, and optionally reconcile the duplicate predicates
  (`paramconvert.IsConvertable` vs `value.IsConvertableParameter`).
  *Effort: XS.*
  *Plan: [`docs/plans/C4-combined-parameter-routing.md`](./plans/C4-combined-parameter-routing.md).*

### Time series

- **Energy view + history rollup.** History infra exists
  (`internal/history/`, `internal/store/sqlite/measurements.go`, REST
  `GetHistory` with query-time bucketing in
  `internal/north/rest/handlers/history.go`) but has no rollup table
  (single raw table + retention purge) and no dedicated UI route.
  - *Energy/consumption view* — a new SPA route aggregating power/energy
    over time (per-device breakdown, daily/monthly totals) on the
    existing `HistoryChart.svelte` + `control/widgets/Powermeter.svelte`.
  - *Rollup/downsampling* — low-res aggregate tables in
    `internal/store/sqlite/migrations_history/` + shifted retention so
    long-term history stays cheap.

  *Plan: [`docs/plans/A2-timeseries-energy.md`](./plans/A2-timeseries-energy.md).*

### Config UI (SPA)

- **User & token management view.** Frontend-only: a `Users`/`Tokens`
  admin view driving the already-existing SQLite-backed CRUD
  (`GET/POST /users` via `admin_users.go`, `/auth/tokens` via
  `admin_tokens.go`, self-password `auth_me_password.go`; routed at
  `internal/north/rest/router.go:861`). Uses the shared `DataTable` +
  confirm dialog. Also fixes the now-stale "planned future feature"
  comment at `internal/north/rest/handlers/auth.go:56`. *Effort: M (UI).*
  *Plan: [`docs/plans/B6-user-token-ui.md`](./plans/B6-user-token-ui.md).*
- **Fleet overview route (auto-tile follow-up).** Verification found the
  auto-tile *engine* of [`docs/ui/auto-tile-concept.md`](./ui/auto-tile-concept.md)
  is **already shipped** (`AutoTile` + `composer.ts` wired in
  `CdpTilesPanel`, `SensorActorTile` retired) — the concept doc's "Not
  yet implemented" status is stale and is corrected by the plan. The
  genuine remaining gap is only a **fleet-wide overview route** that
  composes the existing tiles across all devices/centrals. *Frontend;
  low risk.*
  *Plan: [`docs/plans/B8-auto-tile-dashboard.md`](./plans/B8-auto-tile-dashboard.md).*

### Operations & multi-CCU

- **Scheduled / automatic CCU backups.** Wire a periodic backup job onto
  the existing scheduler (`internal/scheduler/scheduler.go`) using the
  existing CCU-backup Create/Restore surface
  (`internal/north/rest/handlers/backup.go`). New `backup:` config
  section (`Schedule`, `KeepLast`); the job runs per-central via a new
  `TriggerBackupForCentral` plus a `Prune` rotation primitive. Off-box
  targets (S3/WebDAV) and restore-to-new-instance are deferred (below).
  *Effort: S.*
  *Plan: [`docs/plans/B9-scheduled-backups.md`](./plans/B9-scheduled-backups.md).*
- **Live CCU adopt without daemon restart.** Today a change to
  `centrals` is restart-required (`internal/config/restart.go`); make
  adding/removing a CCU a runtime coordinator-lifecycle operation
  (`internal/central/`), so a freshly-discovered CCU
  (`internal/north/rest/handlers/discovery.go`) can be adopted live.
  Split out of the (dropped) "extend hot-reload" idea — this is the one
  structural-reload case with real user value. *Effort: L.*
- **Read-only cross-CCU overview.** A new SPA route showing all CCUs,
  their interfaces, health and device counts at a glance — additive on
  the existing per-central data (`CentralsAdmin.svelte`, the device
  central-filter in `DeviceList.svelte`). Rooms/functions stay per-CCU
  (unifying them across CCUs is a separate, semantically-tricky idea and
  is *not* in scope). *Frontend; low risk.*

### Housekeeping

- **Doc-drift fixes (bundle with the user/token view).** Correct the
  stale `// reserved for future session auth` comment at
  `internal/auth/auth.go:25` (session auth is wired:
  `oidc/client.go`, `csrf.go`, `session.go`), and the `JsonCcuBackend` /
  CCU-Jack mention in `CLAUDE.md` (CCU-Jack was removed —
  `internal/client/backends/doc.go`, and it is a `SPECIFICATION.md` §2.2
  non-goal). *Effort: XS.*
  *Plan: [`docs/plans/C1-doc-drift-fixes.md`](./plans/C1-doc-drift-fixes.md).*

### Reviewed and deferred (2026-06-30)

Considered in the same review and explicitly **not** scheduled now, with
the reason — so they are not re-proposed without new information:

- **Observability ecosystem (OTel metrics/logs, shipped dashboards).**
  Deferred. Metrics already render Prometheus exposition format
  (scrapeable); a full OTel SDK fights the dependency-lean stance. If
  revisited, ship Grafana dashboards + alert rules for existing gauges
  (`events.DeferredHighWaterAlert`) first, hand-rolled OTLP later — no
  SDK.
- **Discovery hardening.** Deferred — no field symptom; the
  serial-match path is wired, backfilled (`ccu_wiring.go`) and tested
  (`routingkey/canonical_test.go`, ~660 LoC discovery tests).
- **MQTT discovery state → SQLite.** Dropped — the in-memory state
  (`wiring.go`, `lastDiscovered` hash cache) is an optimisation whose
  loss is harmless (`RepublishDiscovery` re-emits everything on
  reconnect regardless).
- **Generic hot-reload extension.** Dropped — most sections are already
  hot-applied; the restart-required set (`internal/config/restart.go`)
  is genuinely structural and deliberately excluded
  (`internal/config/watcher.go`). The one valuable case (live CCU adopt)
  is split out above as its own item.
- **O(1) program/sysvar lookup index.** Deferred — premature; gated on
  profiling per `by_design.md` §A5; linear scan
  (`coordinators/hub.go::HubStatePaths`) is fine at fleet ≤ 300.
- **Configurable optimistic burst window.** Deferred — the timeout is
  already per-DP configurable (`DataPoint.OptimisticTimeout`); the
  burst window (`model/optimistic/tracker.go`) is a placeholder for an
  unimplemented time-bounding feature with no user pain.
- **RPC-handler semaphore cap.** Deferred — premature; gated on
  profiling per `by_design.md` (net/http goroutine-per-connection
  already isolates slow handlers; CCU throttle bounds input rate).
- **Dead-code harvest.** Deferred — the curated genuine dead set is only
  ~9 functions (`docs/parity/dead-code-genuine.json`); the large
  "unreachable" count is by-design API mirrors. Trivial cleanup at best,
  not a project.
- **Cross-stack diff in CI.** Deferred — the Go side already runs
  (`.github/workflows/cross-stack-parity.yml`); the full bidirectional
  diff is intentionally local/release (no aiohomematic venv in CI), and
  tightening a parity guard runs counter to the de-prioritisation of
  parity.
- **`openccu-data-go` module / release artifact.** Deferred — still
  blocked on a genuine second Go consumer (see the dedicated section
  below); the accepted REST-client `hmcli` does *not* consume the
  archives directly.

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
  `hub_metric_sensors_test.go`. *`MetricLastEventAgeSecs` now has a
  production observer: the `LastEventAgeRefresh` scheduler job
  (`internal/central/jobs.go`, wired in `cmd/openccu-loom/daemon_jobs.go`)
  samples the metric and the publisher mirrors it to MQTT
  (`hub_mqtt_publisher.go`). No open gap.*
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

One item remains, correctly deferred; no action this round. (A former
second item — a `GetProgramDataPointByStatePath` O(1) index — was
dropped: no such symbol exists in the code, so the entry was stale.)

- **`valve.Modulating` profile-registry wiring — at parity, blocked.**
  The type and constructor exist and are largely complete
  (`internal/model/custom/valve/`, Info/Config/HA-discovery), but no
  device profile maps onto it — `valve/init.go` registers only
  `DeviceProfileIPIrrigationValve`. Crucially, the reference stack has
  **no modulating-valve device either** — it ships only an irrigation
  valve (`IP_IRRIGATION_VALVE`). So leaving `valve.Modulating`
  unregistered is parity, not a gap; wiring it would mean inventing a
  device mapping the reference does not have. The divergence is recorded
  in `docs/parity/by_design.md` (entry `A2-BD03`).
  Wire it in `init.go` only if such a device appears upstream.
  *Effort: S. Blocked on a real device.*

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
