# OpenCCU-Loom — open roadmap items

This file tracks deliverables that are scoped but not yet started —
both work prioritised for an upcoming cycle and work we have explicitly
deferred. Items land here when we commit to revisiting them later.
Completed items are moved to `CHANGELOG.md`; the roadmap keeps only what
is still open, blocked, or a recorded decision *not* to do something.

Accepted items link to a detailed, transferable implementation plan
under [`docs/plans/`](./plans/) where one exists.

## Open development items

### Matter

- **Matter IM protocol depth.** The cluster/schema layer is broad, and
  the *subscribe state machine* (cadence enforcement, max-subscription
  caps, teardown) **and** the *Timed-interaction gate* are already
  implemented and wired (`cmd/openccu-loom/daemon_matter.go`,
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

## Reviewed and deferred

Considered in review and explicitly **not** scheduled now, with the
reason — so they are not re-proposed without new information:

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
  has since shipped.
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
  below); the shipped REST-client `hmcli` does *not* consume the
  archives directly.

## Depth-parity execution plan (aiohomematic)

**Status**: structural port (phases 0–10) complete; the depth-parity
follow-up (guard hardening, cheap model closes, resolver hardening, and
the Homegear track) is **concluded**. Homegear is at parity with the
defined target (aiohomematic's *Homegear* support, not the CCU):
sysvars load + refresh over XML-RPC, and programs/rooms/functions are
empty by design on both stacks. Going beyond that target (full CCU-like
depth, non-HomeMatic Homegear families, a Homegear-native sysvar-create
path) is the explicit non-goal in `SPECIFICATION.md` §2.2 and is not
planned. Matter parity is a separate track against matter.js — out of
scope here (see
[`docs/matter-parity-contract.md`](./matter-parity-contract.md)).

One depth-parity item remains open, blocked upstream:

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
