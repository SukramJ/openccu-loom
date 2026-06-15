# OpenCCU-Loom — Architectural Analysis (2026-06-15)

> **Basis: the code, not the documentation.** Every claim below is anchored to a
> source location (`path:line`). Where a documented intent (SPECIFICATION, ADRs,
> `by_design.md`, CLAUDE.md) diverges from what the code actually does, the
> divergence is itself recorded as a finding (see §C, Doc-vs-Code Drift Register).
> Several agent-reported findings did not survive code verification and were
> corrected or dropped — the verification log in §E is part of the result, not an
> aside.

- **Scope:** branch `docs/architecture-analysis` @ `adfbe62`, which includes the
  merged PRs #76–#79 (mobile/i18n pass, best-effort hub refresh, CCU
  system-update UI + progress monitor).
- **Method:** nine parallel area analyses, each reading production code directly
  and citing `path:line`; followed by an independent code-verification pass on
  the highest-stakes claims (§E). Structural headline numbers were re-measured
  from the tree rather than copied from the spec (§A).
- **What this is not:** a re-statement of `SPECIFICATION.md`. Where the spec and
  the code disagree, the code wins the description and the spec earns a drift
  entry.

---

## Table of Contents

- [§A. Code-Derived Structural Facts](#a-code-derived-structural-facts)
- [§B. Executive Summary](#b-executive-summary)
- [Area 1 — Domain Core, Hexagonal Architecture & Multi-CCU](#area-1--domain-core-hexagonal-architecture--multi-ccu)
- [Area 2 — Southbound Clients, Backends & Transports](#area-2--southbound-clients-backends--transports)
- [Area 3 — Reliability, Recovery & Concurrency](#area-3--reliability-recovery--concurrency)
- [Area 4 — Persistence & Caching](#area-4--persistence--caching)
- [Area 5 — Northbound REST + WebSocket API](#area-5--northbound-rest--websocket-api)
- [Area 6 — MQTT Bridge & Payload Assembly](#area-6--mqtt-bridge--payload-assembly)
- [Area 7 — Matter Bridge (native-Go, matter.js parity)](#area-7--matter-bridge-native-go-matterjs-parity)
- [Area 8 — SPA Frontend](#area-8--spa-frontend)
- [Area 9 — Cross-cutting: Security, Config, Observability, Build, Test & Parity](#area-9--cross-cutting-security-config-observability-buildrelease-testing--parity)
- [§C. Doc-vs-Code Drift Register](#c-doc-vs-code-drift-register)
- [§D. Cross-Cutting Themes](#d-cross-cutting-themes)
- [§E. Prioritized Improvement Roadmap](#e-prioritized-improvement-roadmap)
- [§F. Verification Log (corrected / dropped claims)](#f-verification-log-corrected--dropped-claims)

---

## §A. Code-Derived Structural Facts

Measured from the tracked tree (`git ls-files`), not from the spec.

| Fact | Code (measured) | Doc says | Status |
|---|---|---|---|
| Production Go LOC (non-test) | **217 460** | 194 k (0.1.0 snapshot, CLAUDE.md) | grown; doc stale-by-design |
| Test Go LOC | **391 625** | 353 k (0.1.0 snapshot) | grown; ~1.8:1 test:prod |
| Coordinators (types) | **8** (`cache, client, configuration, connection_recovery, device, event, hub, link`) + `reconciler.go`, `recovery_stages.go` helpers | "all 8 coordinators" | ✅ accurate |
| South transports | **3** (`xmlrpc`, `binrpc`, `jsonrpc`) | 3 | ✅ accurate |
| Backends | `ccu`, `ccu_extended`, `cuxd`, `homegear`, `combined` (+ JSON CCU mode) | CCU / CUxD / Homegear | ✅ accurate |
| REST handler files (non-test) | **47** | "25 REST handler files" (CLAUDE.md) | ⚠️ **drift** (≈88 % more) |
| WS commands registered | **85** (86 `Register` calls; `wsapi.json` = 101 = 85 cmd + 16 push) | 85 | ✅ accurate (1 off worth a guard) |
| Matter LOC | 41 276 (`internal/north/matter`) | ~41 k | ✅ accurate |
| `context.Background()` sites (prod) | **53 files** — hot: `command_subscriber.go` (9), `eventbridge.go` (8), `bridge.go` (5), `ccu_wiring.go` (5), `hub_wiring.go` (4) | — | theme T1 |
| `//nolint` (prod files) | **205** — `gosec` 168, `contextcheck` 79, `exhaustive` 49, `funlen` 26, `gocognit,gocyclo,funlen` 15 | — | theme T2 |
| `TODO/FIXME/HACK/XXX` (prod) | **1** (`matter/cluster/core/access_restriction.go:22`) | — | ✅ exceptionally clean |
| `docs/audit/` directory | **absent on disk** | listed in CLAUDE.md repo tree | ⚠️ **drift** |

**Per-area production LOC** (non-test): `internal/model` 49 120 · `internal/north/matter`
41 276 · `internal/central` 33 525 · `internal/north/rest` 16 678 (incl.
`rest/ws`) · `internal/client` 15 781 · `internal/north/mqtt` 11 468 ·
`cmd` 9 697 · `pkg` 8 343 · `internal/store` 8 662 · `internal/config` 2 192 ·
`internal/auth` 1 248 · `internal/north/ui` 1 154.

---

## §B. Executive Summary

OpenCCU-Loom is, by the evidence of its own source, a **mature and unusually
disciplined** codebase for a 0.2.0 release. The hexagonal split is real and
mostly enforced by the import graph; multi-CCU is structural (no global
`CentralUnit`, `central_name` threaded through every event and store key); the
reliability stack (circuit breaker, retrier, throttle, coalescer, ping/pong) is
cleanly composed and clock-seamed for deterministic tests; the contract-test
culture (`TestDocPurity`, capability-pin tests, schema-parity tests, golden
replay, godevccu integration) prevents whole classes of rot. One `TODO` in
217 k lines of production code is a remarkable signal of hygiene.

The weaknesses are the weaknesses of a fast-grown system, and they cluster into a
small number of **recurring shapes** rather than scattered one-offs:

1. **Inconsistent goroutine lifecycle.** Some background work is exemplary
   (`scheduleSelfReload` is `WaitGroup` + ctx-bound; the scheduler drains
   cleanly), but several hot paths spawn detached goroutines on
   `context.Background()` with no cancellation or wait — recovery runs, MQTT
   command handlers, eventbridge loads. 53 production files touch
   `context.Background()`; clean shutdown is not guaranteed.
2. **God-objects accreting wiring.** `Unit` (1 253 LOC / 53 methods),
   `HubCoordinator` (942 / 61, nine semaphores), `device.Channel` (1 414 / 69),
   the `Operations` backend interface (57 methods), the Matter `Bridge`
   (46 fields), the REST `Deps` (58 fields). Each grows by a fixed multi-site
   ritual when a feature is added.
3. **Error taxonomy is not applied uniformly.** ~37 upstream-failure 502s use
   `TypeInternal` instead of `TypeUpstreamUnavailable`; southbound errors are not
   consistently enriched with `hmerr.Context`.
4. **Unbounded accumulation / missing backpressure.** No `audit_log` retention;
   `GCDeadRows` implemented but never wired; the MQTT `declared` map never
   pruned; hub list endpoints unpaginated; the SPA device list capped silently
   at 200; the WS command channel ungated.
5. **A LAN-trust security posture with thin defense-in-depth.** YAML-seeded
   passwords are stored verbatim (`MVP: plaintext`); the secret cipher
   silently degrades to pass-through when its key is unavailable; the HTMX login
   has no brute-force friction; the BIN-RPC callback server has no allowlist.
6. **Doc-vs-code drift in load-bearing places** (the reason for this analysis's
   code-first mandate): a documented MQTT MASTER-write topic the code silently
   drops; CLAUDE.md counts (25 handler files, 194 k LOC) overtaken by the tree;
   an i18n pass advertised "complete" while control widgets still hardcode
   German `"An"`/`"Aus"`.

None of these are architecture-invalidating. The hexagonal skeleton, the
multi-CCU model, and the parity discipline are sound foundations. The roadmap in
§E is overwhelmingly **small, surgical fixes** (most P1s are effort-S) that pay
down the recurring shapes above without structural surgery.

---

## Area 1 — Domain Core, Hexagonal Architecture & Multi-CCU

**Overview.** Four tiers: the public surface (`pkg/hmenum`, `hmtypes`,
`hmevent`, `hmerr`, `hmproto`, `interfaces`); the orchestration layer
(`internal/central/central.go` — `Unit` — with `central_registry.go` —
`Registry`); the eight coordinators (`internal/central/coordinators/`); and the
domain model (`internal/model/`, including eleven custom-DP packages). The
`internal/central/adapter/` package (64 files) is the explicit wiring layer that
composes coordinators into the handler interfaces the north consumes. Multi-CCU
is genuinely first-class: `Registry` is a `map[string]*Unit` under `RWMutex`,
every coordinator carries its own `centralName`, every event payload carries
`CentralName`, and the generic typed event bus dispatches on concrete struct
type (no string discriminators).

**Strengths.**
- **Clean coordinator decomposition.** Each coordinator has one stated purpose
  and a narrow constructor; none depends on another directly — they communicate
  through the shared `events.Bus` or via `Unit`.
- **Hexagonal discipline at the boundary.** Southbound hooks (`DeviceLister`,
  `ChannelWriter`, `ChannelRefresher`, `ProgramExecutor`, `SysvarValueWriter`, …)
  are narrow interfaces in the consumer package, injected at boot; north wiring
  goes through `adapter/` files implementing handler interfaces — the core stays
  HTTP-unaware.
- **Multi-CCU scoping is mechanical.** `hmtypes.DataPointKey` carries
  `InterfaceID + ChannelAddress + ParamsetKey + Parameter`; every event struct
  carries `CentralName`; the only cross-central utilities (`Registry.HubFor`,
  `SerialSuffix`) guard with `RLock`. No global state.
- **Event-bus deferred dispatch is sound.** Re-entrant publishes land in a
  deferred queue rather than recursing, with a high-water alert at 4 096.
- **`pkg/interfaces/matter.go` is a real DI seam.** Matter projection interfaces
  live in `pkg`, so model packages implement them without importing the bridge
  and the bridge consumes them without a reverse import — "rich model, dumb
  bridge" enforced by import direction.

**Weaknesses, risks & smells.**
- **`Unit` is drifting toward a god-object.** `central.go` is 1 253 lines / 53
  methods, combining orchestration with a runtime service-dispatch table of eight
  `fn`-closure pairs guarded by one `serviceMu` (`acceptInboxFn`,
  `createBackupFn`, `setInstallModeFn`, `renameDeviceFn`, `loadAndRefreshFn`, …),
  each with a `Set*` setter and a `ServiceWiringStatus` map entry. Adding a
  southbound service is a three-site change.
- **`internal/central/adapter/` is 64 files with no sub-structure.** It holds the
  2 293-line `EventBridge`, CCU-wiring, the device pipeline, hub MQTT publishers,
  schedule I/O, diagnostics — all in `package adapter`. 20+ files import
  `internal/north/rest/handlers` directly; a rename in `handlers/` propagates
  invisibly.
- **`EventCoordinator` imports a concrete transport.** `coordinators/event.go:13`
  imports `internal/client/transport/xmlrpc` and uses `xmlrpc.Value` in
  `HandleRawEvent` — a domain coordinator carrying a compile-time dependency on
  one wire encoding. A second event-producing transport would need a parallel
  path.
- **`internal/model/custom/*` imports `internal/north/matter/cluster`.**
  `climate/climate.go:26`, `cover/cover.go:20`, `light/light.go:21`,
  `lock/lock.go:27`, `siren/siren.go:23`, `switch/switch.go:18` — a **downward
  coupling violation** (domain → north impl) not listed in `by_design.md`. It
  works today only because `cluster` does not import `model` (no cycle).
- **`HubCoordinator` is the second god-object.** 942 lines / 61 methods across
  nine sub-domains, each behind its own semaphore (`semaPrograms`, `semaSysvars`,
  `semaInbox`, `semaServiceMessages`, `semaAlarmMessages`, `semaSystemUpdate`,
  `semaInstallMode`, `semaMetrics`, `semaConnectivity`). The semaphore
  proliferation is the tell: this is nine concerns.
- **`Unit.Stop()` is a hand-ordered 13-step sequence** with a dynamically-grown
  `onStopHooks` slice. Correct today, but a late hook that assumes a still-running
  coordinator is not structurally prevented.
- **`device.Channel` is 1 414 lines / 69 methods / 14-field struct** under one
  `sync.RWMutex` plus a separate `linkPeersMu` — the second lock already signals
  lock-splitting pressure and creates a lock-ordering hazard.

**Improvements.**
- **[P1, M]** Extract `Unit`'s eight `fn`-closure pairs into a `ServiceBundle`
  built at boot and held as one field; replace `ServiceWiringStatus()` with
  `bundle.Complete()`. Removes the three-site ritual; ~150 lines off `central.go`.
- **[P1, S]** Replace the `xmlrpc.Value` dependency in
  `EventCoordinator.HandleRawEvent` with a transport-neutral `WireValue`
  interface (or convert to `hmtypes.ParamValue` in the transport layer before the
  coordinator boundary).
- **[P2, M]** Move `internal/north/matter/cluster` imports out of
  `model/custom/*` — define the needed constants/interface in `pkg/interfaces` or
  the model package, restoring the layering the spec describes.
- **[P2, L]** Sub-divide `internal/central/adapter/` into `wiring/`, `hub/`,
  `rest/`, keeping `EventBridge` distinct — a navigable structure that surfaces
  cycle risk at the package level.
- **[P2, L]** Split `HubCoordinator` into `SysvarCoordinator`,
  `ProgramCoordinator`, `AlertCoordinator`, leaving `HubCoordinator` a thin
  facade.
- **[P3, S]** Formalize `Unit.Stop()` teardown with a tier model rather than one
  unordered hook slice.

---

## Area 2 — Southbound Clients, Backends & Transports

> **This section was rebuilt from code after verification corrected three of its
> six originally-reported findings (see §F).**

**Overview.** `internal/client/` (`InterfaceClient`, reliability primitives),
`internal/client/backends/` (`ccu`, `ccu_extended`, `cuxd`, `homegear`,
`combined`, plus JSON-only CCU mode), `internal/client/transport/{xmlrpc,binrpc,jsonrpc}`,
and the callback servers in `internal/central/rpcserver/` (XML-RPC `:8120`,
BIN-RPC `:8129`). One `InterfaceClient` per `(central, interface)` pair.

**Strengths.**
- **Three transports cleanly separated**; CUxD speaks BIN-RPC directly (not the
  JSON-RPC workaround the Python reference uses), with its own callback server.
- **Per-backend capability detection** (`backends/capabilities.go`) drives the
  init payload (`client/payload.go:40` copies `RPCCallback` into the advertised
  capabilities).
- **Self-reload is lifecycle-safe.** `scheduleSelfReload`
  (`adapter/callback_handlers.go:354`) runs `LoadValue(direct=true)` on
  coerce-failure, and the spawning goroutines are tracked by a `sync.WaitGroup`
  and bound to the handler context `h.ctx` so `Stop()` drains them
  (`callback_handlers.go:37-47`). This is a model the other detached-goroutine
  sites (Area 3) should follow.
- **Effective-port re-advertisement** is honored on reconnect (dynamic/`0` and
  range port modes).

**Weaknesses, risks & smells.**
- **[W1, confirmed] `Operations` is a 57-method god-interface**
  (`backends/backend.go:31`). Every backend must satisfy all 57; `CuxdBackend`
  and the JSON CCU mode stub the unsupported majority. A new capability is a
  change across every backend.
- **[W2, confirmed] The BIN-RPC callback server has no IP-allowlist and no
  auth.** `rpcserver/binrpc_server.go` logs `conn.RemoteAddr()` (lines 177, 212)
  but accepts any peer that frames a valid envelope. Same trust model as the
  XML-RPC callback; tolerable on a trusted LAN, but no defense-in-depth, and the
  BIN-RPC envelope carries no shared secret.
- **[W3, corrected] No concurrency cap on self-reload.** The goroutines are
  lifecycle-safe (above) but unbounded in *number*: a device flooding
  uncoercible wire values can spawn many simultaneous direct `LoadValue` reads
  against the CCU, competing with normal traffic on the throttle.
- **[W4, medium confidence] Southbound errors are not uniformly enriched.** The
  spec's `hmerr.Context{Protocol, Method, Host, Interface}` enrichment contract
  is not consistently applied at the backend boundary; some transport errors
  reach the coordinator without host/interface attribution.

**Improvements.**
- **[P2, S]** Add a bounded semaphore (e.g. 16) around `scheduleSelfReload` so a
  value-flood cannot saturate the CCU read path.
- **[P3, S]** Add an optional IP-allowlist to the BIN-RPC and XML-RPC callback
  servers, defaulting to the configured CCU host(s); reject + log others.
- **[P3, L]** Split the 57-method `Operations` interface into capability-scoped
  sub-interfaces (`Reader`, `Writer`, `ParamsetOps`, `LinkOps`, …) so backends
  declare only what they implement and the stub surface collapses.

---

## Area 3 — Reliability, Recovery & Concurrency

**Overview.** The reliability layer (`internal/client/reliability/`:
`circuit.go`, `retry.go`, `throttle.go`, `coalesce.go`, `pingpong.go`), the
recovery state machine (`internal/central/coordinators/connection_recovery.go`,
`recovery_stages.go`), the typed generic event bus
(`internal/central/events/bus.go`), the scheduler (`internal/scheduler/`), the
`-tags deadlock` lock-order shim (`internal/syncx/`), and daemon-lifetime
goroutines in `internal/central/adapter/`.

**Strengths.**
- **Well-composed primitives.** Circuit breaker, retrier, throttle, coalescer,
  ping/pong — separated, tested, composable, each with an injected `clock.Clock`.
- **Nuanced circuit-breaker semantics.** `Do()` distinguishes semantic XML-RPC
  faults from transport failures, uses a single-probe HALF_OPEN with an
  `atomic.Int32` in-flight guard, fires callbacks outside the mutex.
- **Clean supersede-by-key retrier.** `DoForKey` registers a per-`DataPointKey`
  cancel channel; a newer write closes it, aborting the older chain without
  polling. `CancelDevice`/`CancelInterface` sweep in one pass.
  `RecoveryWaiter` short-circuits backoff when the breaker reopens.
- **Event-driven recovery coordinator.** Wires onto `ConnectionLostEvent`,
  `CircuitBreakerTrippedEvent`, `CircuitBreakerStateChangedEvent`,
  `HeartbeatTimerFiredEvent`; the per-stage `Pipeline` with injected
  `RecoveryStep`s is testable without hardware.
- **Re-entrancy-safe bus** (TryLock sentinel + deferred drain + high-water
  alert), `goleak.VerifyTestMain` in `client` and `coordinators`, scheduler with
  `WaitGroup` drain, `StartUnobservedSweepLoop` returning an explicit `stop()`.

**Weaknesses, risks & smells.**
- **[W1] `triggerRecovery` spawns `go c.Run(context.Background(), …)` with no
  cancellation** (`connection_recovery.go:434`). `Stop()` after a trigger returns
  while recovery goroutines (which can sleep through `COOLDOWN`/`WARMING_UP` for
  minutes) are still alive on a background context nobody can cancel. No
  `WaitGroup` covers them. (`heartbeatLoop` *is* guarded via `stopCh` — the gap
  is specific to the recovery runs.)
- **[W2] `circuitRecoveryWaiter.ensureHook` calls `OnStateChange` (replace) not
  `AddOnStateChange` (append)** (`retry.go:403`). A second waiter on the same
  breaker silently evicts the first; goroutines already in `WaitForRecovery`
  never wake.
- **[W3] `CancelledRetries` double-counts a supersede** (`retry.go:502` by the
  superseder, then `:524` by the evicted goroutine) — one event, two increments.
- **[W4] `refreshLocked` fires detached `go cb(...)`** (`circuit.go:351-352`)
  with no context, no `WaitGroup`, no panic recovery; can race `Reset()` and
  deliver state callbacks out of order.
- **[W5] The bus `dispatch` mutex serializes all event types** (`bus.go:101`).
  Concurrent `Publish` of unrelated types contend; under multi-CCU callback
  fan-in this is a single lane. The deferred buffer is unbounded (soft alert
  only).
- **[W6] More `context.Background()` fire-and-forget**: `hub_retry.go:59`,
  `eventbridge.go:1722,1859` — no `SafeGo` wrapper, so a panic in the
  week-profile loader dies silently.
- **[W7] Only two recovery attempts can queue per interface**; a third trigger
  before the second starts is swallowed by the `alreadyActive` check with no
  drop log (`connection_recovery.go:817-824`).

**Improvements.**
- **[P1, S]** `ensureHook` → `AddOnStateChange`; test two concurrent waiters both
  unblock.
- **[P1, M]** Track recovery goroutines in a `WaitGroup` and pass a
  `stopCh`-derived cancellable context through `triggerRecovery`; `Stop()` waits
  — the pattern `StartUnobservedSweepLoop` already uses.
- **[P1, S]** Remove the duplicate `CancelledRetries` increment at `retry.go:524`.
- **[P2, M]** Wrap `refreshLocked`'s `go cb(...)` in a panic-recovering `SafeGo`.
- **[P2, M]** Stripe the bus dispatch by `EventType` hash bucket so unrelated
  types proceed in parallel while preserving within-type order.
- **[P3, S]** `SafeGo`-wrap the `eventbridge.go` background loads.

---

## Area 4 — Persistence & Caching

**Overview.** One `modernc.org/sqlite` file (`<data_dir>/openccu-loom.db`),
opened and migrated in `internal/store/sqlite/store.go` (goose, 19 embedded
migrations). Thin domain stores over a shared `*sql.DB` (`DeviceStore`,
`ParamsetStore`, `MasterValuesStore`, `ValuesCacheStore`, `IncidentStore`,
`AuditStore`, `SessionRecorderStore`, `ConfigSectionStore`, `CentralsStore`, plus
Matter stores). Three pure in-memory caches above SQL (devicedetails,
visibility/decision memoizer, paramset address index). Static data
(`profiles`, translations, Matter schema) is `go:embed`-compiled, never
persisted.

**Strengths.**
- **Correct WAL + busy_timeout** (`store.go:112-126`): WAL for file DBs,
  `busy_timeout = 5000 ms`, `journal_mode = MEMORY` fallback for in-memory DSNs.
- **Rigorous multi-CCU partitioning** — `central_name` is the first PK column of
  every table; no cross-central leakage is expressible.
- **Versioned paramset cache** — `ParamsetCacheSchemaVersion` stamped per row;
  `WipeOutdated` runs in `Open` before any caller, evicting stale rows atomically.
- **Values-cache GC nil-guard** (`values_cache.go:478`) refuses to delete on a
  `nil` alive-set; `SaveBatch` uses one prepared statement per transaction.
- **`PersistentCache.SaveDelayed`** SHA-256 dirty-tracks to skip no-op writes;
  `Flush` drains the timer synchronously for clean shutdown.
- **Incident dedup** (`BumpIfRecent`, 5-min window) before TTL/cap enforcement.
- **`VACUUM INTO` backup** (`backup.go:272`) — consistent defragmented snapshot
  under live WAL.

**Weaknesses, risks & smells.**
- **[W1] `audit_log` has no retention.** `AuditStore` exposes only `Append`/`List`
  — no `Purge`/`DeleteBefore`, no scheduler job. `List` with `limit <= 0`
  (`audit.go:82`) full-scans. On a busy install the table grows without bound.
- **[W2] `GCDeadRows` is production-dead code.** `values_cache.go:478` + `AliveKey`
  (`:550`) are correct but have zero non-test callers; `HandleDeleteDevices`
  (`coordinators/device.go:451`) deletes paramsets and descs but **not**
  `valuesCacheStore.DeleteDevice`. Unpair/re-pair on the same address orphans
  rows the GC was built to remove.
- **[W3] `EnforcePerTypeCap` is N+1** (`incidents.go:212-248`): one query for
  distinct types, then one `DELETE` per type (6 round-trips for 5 types) on every
  `RecordWithLimits`.
- **[W4] `LIKE ? || '%'` without `ESCAPE`** (`values_cache.go:428`,
  `master_values.go:190`) — benign for today's addresses (no `%`/`_`) but
  inconsistent with the safe `ESCAPE` pattern at `paramsets.go:384`.
- **[W5] Migration 004 Down is a documented no-op** (`SELECT 1`) — rolling back
  past it leaves `journal_excerpt` in place; schema audits read misleadingly.
- **[W6] `config_sections` has no `schema_version`** (migration 017) unlike
  paramsets/values_cache — no stale-row detection if a section's JSON shape
  changes.
- **[W7] `migrateMu` is a package-level mutex** (`store.go:90`) serializing
  `Migrate` across parallel test packages — a goose-race workaround that costs
  test wall time.

**Improvements.**
- **[P1, S]** Add `AuditStore.PurgeBefore(ctx, cutoff)` + a daily scheduler job
  (default 90 days / 10 000 rows), mirroring incidents' retention.
- **[P1, S]** Wire `GCDeadRows`: either subscribe `ValuesCacheStore.DeleteDevice`
  to `DeviceRemovedEvent` in `HandleDeleteDevices`, or a periodic job building the
  alive-set from the live registry (more event-loss-robust).
- **[P2, S]** Collapse `EnforcePerTypeCap` into one CTE `DELETE` with
  `ROW_NUMBER() OVER (PARTITION BY type ORDER BY last_seen DESC)`.
- **[P2, S]** Add `schema_version` to `config_sections` with boot-time stale
  detection.
- **[P3, XS]** Add `ESCAPE '\'` to the two `LIKE` device-delete patterns.
- **[P3, S]** Schedule periodic `PRAGMA wal_checkpoint(PASSIVE)` to bound WAL
  growth on ARM targets.

---

## Area 5 — Northbound REST + WebSocket API

**Overview.** `internal/north/rest/` (router, 47 handler files, middleware,
`problem` package), `internal/north/rest/ws/` (hub, command router, **85**
commands), `internal/reqctx/`, `pkg/hmapi/`. Spec: `assets/openapi.yaml`
(~5 470 lines, 80+ paths); `assets/wsapi.json` (101 entries = 85 commands +
16 push events).

**Strengths.**
- **Spec-driven with runtime enforcement.** The `kin-openapi`
  `OpenAPIValidator` middleware validates every `/api/v1` request against
  `openapi.yaml` at runtime — drift is rejected at the boundary.
- **Consistent RFC 9457 errors** (`problem/problem.go`) with a
  `TypeUpstreamUnavailable` sentinel and `IsUpstreamUnavailable` discriminator.
- **Solid middleware**: RequestID, security headers, structured logging, panic
  recovery, timeouts, per-identity token-bucket rate limiting, CSRF double-submit,
  CORS.
- **Multi-CCU correct at the boundary.** List endpoints fan out over
  `HubIndex.Hubs()` and tag `central`; mutations use `resolveHubForMutation`
  (`handlers/hub.go:50`) → single-CCU fallback, 400 when ambiguous.
- **Typed, auditable WS commands** with `reqctx` enrichment, per-outcome slog,
  and policy-error mapping (`ErrParameterHidden` → `Forbidden`). Unimplemented
  commands are honestly registered as `NotImplemented` stubs and visible via
  `system.commands`.

**Weaknesses, risks & smells.**
- **[W1] `TypeInternal` on ~37 upstream-failure 502s** (e.g. `handlers/hub.go:265`,
  10 sites; `paramsets.go`, 3 sites; 37 total). A 502 from a CCU write path is
  transient-upstream, not a daemon bug. `WriteFromError`/`IsUpstreamUnavailable`
  exist and are used correctly in `devices.go:676`/`schedules.go:346` but most
  handlers never call them — SPA clients can't tell "daemon crashed" from "CCU
  unreachable".
- **[W2] `resolveHubForMutation` is called by the read-only `GetSysvar`**
  (`hub.go:473`). In multi-CCU, `GET /sysvars/{name}` without `?central=` returns
  400, while `ListSysvars` correctly fans out.
- **[W3] Four decode sites bypass `DecodeJSON`/`DisallowUnknownFields`**
  (`diagnostics_capture.go:47`, `system_admin.go:75`,
  `diagnostics_loglevels.go:100`, `visibility.go:144`) — client typos silently
  dropped with 200 OK.
- **[W4] WS command channel is unrate-limited.** `RateLimit` middleware gates
  only the HTTP upgrade; once connected, `Router.Dispatch` (`ws/commands.go:150`)
  has no per-connection gate — one session can fan out paramset writes / ReGa
  executions indefinitely.
- **[W5] Hub list endpoints unpaginated** (`ListSysvars` `hub.go:192`,
  `ListPrograms`, `ListAlarmMessages`, `ListServiceMessages`) while
  `GET /devices` does pagination + `X-Total-Count` correctly. Large catalogues =
  one unbounded allocation per request.
- **[W6] `POST /devices/values:batch` is not `op`-gated** (`router.go:430`).
  Intentional (it's a batch *read*), but `POST`-with-body makes it look like a
  write to tooling and the OpenAPI validator.
- **[W7] No `wsapi.json` ↔ registration parity test** — the 101/86 delta is
  explained (16 push + 5 dual-listed stubs) but unguarded; future additions drift
  silently.
- **[W8] `Deps` has 58 fields with three undocumented nil-semantics** (nil → 404 /
  nil → 503 `service_unready` / nil → silent no-op).

**Improvements.**
- **[P1, S]** Replace `TypeInternal` with `WriteFromError`/`IsUpstreamUnavailable`
  on the ~37 502 paths — mechanical, semantically correct.
- **[P1, M]** Add a per-connection WS `rate.Limiter` (e.g. 20/s, burst 60) keyed
  on auth identity in `Router.Dispatch`; new `CommandErrorRateLimited` code.
- **[P2, S]** Add `resolveHubForRead` (accept the unambiguous single match when
  `?central=` is absent) for `GetSysvar` and peers.
- **[P2, S]** Funnel the four rogue decode sites through `DecodeJSON`.
- **[P2, M]** Paginate the hub list endpoints (mirror `ListDevices`); update the
  spec.
- **[P3, S]** Add `TestWSCommandCatalogParity` (`wsapi.json` command entries ↔
  `Router.Has`).
- **[P3, S/M]** Document the three `Deps` nil-semantics blocks, or wrap in typed
  optionals.

---

## Area 6 — MQTT Bridge & Payload Assembly

**Overview.** `internal/north/mqtt/` (broker lifecycle, dual-plane publish, HA
Discovery, command subscribe, retain cleanup), `internal/payload/` (the
`Source`/`Slotted`/`TopicSlot`/`Bucket` interfaces decoupling model from
transport), and `cmd/openccu-loom/` wiring. ADR 0011 drove the topology;
`docs/mqtt-topic-schema.md` is the operator reference.

**Strengths.**
- **Genuinely dumb bridge.** `bridge.go` carries zero per-device knowledge; domain
  objects declare their own topology and discovery shape via the `payload`
  interfaces — the bridge cannot accrete device special-cases.
- **Per-DP push topology** (`values/<param>/state` + curated
  `custom/<kind>/state`) eliminated the boot-time partial-JSON race; each DP is
  independently live.
- **Reactive HA Discovery** (`DiscoveryDynamic`, `topology.go:117`) re-renders on
  mode/profile changes, diff-gated to zero broker traffic when unchanged.
- **Diff-gated discovery + config** (`b.declared`, `b.configCache` byte-compared).
- **Atomic bridge swap** (`Wiring.bridge atomic.Pointer[Bridge]`) — runtime
  credential/topic/discovery changes without tearing down EventBridge.
- **Multi-CCU namespacing** (`<base>/<central>/…`), orphan-cleanup scoped to
  `<central>_` prefix.
- **Birth-sync on HA restart**, thorough subscription replay on broker reconnect
  (`adapter_tcp.go:197-215`), server-side ENUM label resolution (`bridge.go:1300`).

**Weaknesses, risks & smells.**
- **[W1] `context.Background()` throughout the command path** — every handler in
  `command_subscriber.go` (9 sites: `:333,376,422,470,540,563,583,624,665`) and
  `birth_sync.go:59` builds a fresh context. The MQTT handler signature
  `(topic, body, retained)` carries no context, so a stopping daemon's in-flight
  CCU writes run until the 20 s `AckTimeout`.
- **[W2] MASTER paramset writes documented but silently dropped — CODE-VERIFIED.**
  `docs/mqtt-topic-schema.md:54` advertises
  `<base>/<central>/<iface>/<addr>/<ch>/master/<param>/set` as writable;
  `command_subscriber.go:517` drops every `bucket != "values"` at Debug level. The
  code's own comment (`:500-504`) says MASTER edits route via REST and discovery
  never emits the master set topic — so the **topic-schema doc overpromises** a
  write path the code intentionally refuses. Doc/impl contract gap.
- **[W3] Retain-cleanup window is a fixed 2 s sleep** (`retain_cleanup.go:259,324`)
  — too short on a slow broker (phantom entities survive), a forced delay on a
  fast one.
- **[W4] Orphan cleanup races `PublishInitialSnapshot`** (`daemon_southbound.go:322`
  → `:339`): cleanup reads `b.declared` which may still be filling for a large
  fleet, mis-classifying live entities as orphans.
- **[W5] `b.declared` never pruned** for removed devices (only
  `RunDiscoveryOrphanCleanupOnce` deletes) — monotonic growth across fleet
  changes.
- **[W6] Reconnect resubscribes all filters at QoS 1 unconditionally**
  (`adapter_tcp.go:209`) regardless of original QoS.

**Improvements.**
- **[P1, S]** Store the daemon lifecycle context in `CommandSubscriber` at
  construction (`WithLifecycleContext`) and use it (+ short per-command timeout)
  instead of `context.Background()`; same for `birth_sync.go`.
- **[P1, M]** Either route `bucket == "master"` through a `SetMasterParam` sink,
  or remove `master/<param>/set` from `mqtt-topic-schema.md` and ADR 0011 until
  implemented. The current "documented-writable, silently-dropped" state is a
  correctness gap.
- **[P2, S]** Make the retain-cleanup window operator-configurable
  (`mqtt.retain_cleanup_window_ms`, clamped 500 ms–30 s) with a rationale comment.
- **[P2, S]** Serialize the snapshot → orphan-cleanup ordering, or freeze a
  `declared` snapshot argument passed into cleanup.
- **[P3, M]** Prune `b.declared` for removed addresses before the cleanup pass.
- **[P3, S]** Store the subscribed QoS per filter and replay at original QoS.

---

## Area 7 — Matter Bridge (native-Go, matter.js parity)

**Overview.** `internal/north/matter/` and sub-packages (`bridge/`, `endpoint/`,
`im/`, `tlv/`, `secure/{sigma,spake2,aesccm,attestation,…}`, `cluster/`,
`schema/`, `parity/`, `transport/`, `mdns/`, `store/`). Codegen:
`script/generate_matter_schema.go`, `extract-from-matter-js.ts`. ~41 k LOC,
215 test files.

**Strengths.**
- **Disciplined codegen.** `make generate-matter-schema` runs the TS extractor
  against matter.js HEAD → snapshot → embedded copy → regenerated
  `clusters.go`/`devicetypes.go`. `TestParityCodeMatchesGeneratedSchema`
  (`schema/parity_test.go:24`) cross-checks every hand-written revision constant
  against the generated map.
- **Behavioral parity, not just schema.** `matter_negative_write_parity_test.go`
  enforces that writes matter.js rejects return the matching IM status from Loom;
  the dormant-capability wiring-pin test catches "unit-tested but never attached"
  bugs; SPAKE2+/Sigma parity uses matter.js test vectors, not synthetic values.
- **Careful protocol-engine concurrency.** `Bridge` uses `RWMutex` for topology
  swaps, `sync.Map` for the four concurrent maps, `atomic` for the start-claim and
  counters; the `sigma1Replied` dedup map is mutex-guarded at every access,
  fixing the Apple multicast Sigma1 replay bug.
- **Parity coverage is pervasive** — every cluster sub-package and the six
  `model/custom/*/matter.go` boundary files carry `parity_matterjs_test.go`; the
  `chiptool` suite gives end-to-end wire validation against the CSA reference
  commissioner with in-process godevccu (no live-CCU writes).

**Weaknesses, risks & smells.**
- **[W1] Dual `schema.json` copies with no staleness guard.** The Makefile copies
  the master snapshot to `parity/schema.json`; `parity/parity_test.go:12` only
  checks non-empty + starts-with-`{`. A partial pipeline run (regenerate master,
  skip the copy) runs all parity tests against stale embedded data — false-green.
- **[W2] `Bridge` is a 46-field god-object** (`bridge/bridge.go:151-379`) —
  UDP listener, topology, dispatcher, session/PASE/CASE handlers, subscription
  manager, event log, commissioning window, mDNS, five `sync.Map`s, counters,
  hooks. `//nolint:gocognit,gocyclo,funlen` appears 14× across `bridge/`,
  `cluster/core/`, `im/`.
- **[W3] Groups & ScenesManagement are permanent stubs** returning
  `UnsupportedCommand` (`cluster/wire/groups.go:34`,
  `scenes_management.go:37`) for mandatory commands. Catalogued in `by_design.md`
  (BD-Matter-P2-D18/19) but a future, stricter controller could reject on the
  mandatory-cluster command surface.
- **[W4] `handleIMOpcode` is one 350+-line dispatch** (`bridge/receive.go`,
  `nolint:gocognit,gocyclo,funlen`): opcode routing, session gate, timed gate,
  TLV decode, invocation all inline — no unit-testable "decode and route" seam.
- **[W5] The parity guard works one-way.** `TestParityCodeMatchesGeneratedSchema`
  reads the generated Go map, so a hand-edited constant in the *generated* file
  passes while diverging from matter.js HEAD (no content hash of the snapshot in
  the generated file).
- **[W6] `secure/sigma/protocol.go:processSigma1Locked` is 941 lines**
  (`sigma.go` 863) carrying full SIGMA-1/2/3 incl. resumption + multi-fabric +
  cert-chain in one mutex-held function; resumption error-path tests are thinner
  than the happy path.

**Improvements.**
- **[P1, S]** Add `TestMatterSchemaSnapshotInSync` (`tests/contract/`) asserting
  SHA-256 equality of the two `schema.json` copies — closes the W1 false-green.
- **[P1, M]** Extract an `imDispatch` helper (gate-check + TLV decode + session
  validation) from `handleIMOpcode`, making those paths unit-testable without a
  full `Bridge`.
- **[P2, S]** Embed a content hash of `matter-schema-snapshot.json` into
  `clusters.go` at generation and verify it in the parity test (closes W5).
- **[P2, L]** Add a minimal in-memory Groups implementation
  (`AddGroup`/`RemoveGroup`/`ViewGroup`/`GetGroupMembership`) in `matter/store/`
  to satisfy conformance without persistence.
- **[P2, L]** Decompose `Bridge` into a `CommissioningSession` (PASE/CASE/OpCreds/
  window/`sigma1Replied`) and an `IMEngine` (subscriptions/event log/timed gates),
  leaving `Bridge` a composing coordinator; external `Attach*` API unchanged.
- **[P3, M]** Extract `processSigma1ResumeLocked` for targeted resumption tests.

---

## Area 8 — SPA Frontend

**Overview.** Svelte 5 runes + Tailwind 4 + Vite under `assets/ui/src/`.
`App.svelte` (hash router + auth shell); `routes/`; rune-store factories in
`lib/stores/*.svelte.ts`; `lib/api/{client.ts,ws.ts,types.ts}`; device control
split across `lib/control/` (CONTROL-tagged widgets) and `lib/cdp/` (Custom-DP
tiles), plus generic `lib/sensor-actor/`; a 2 800-line `lib/i18n.ts`.

**Strengths.**
- **Idiomatic runes.** Every store is a `$state`-backed factory exposing getters
  — reactivity without leaking the raw state; external mutation impossible.
- **One WS multiplexer with clean lifecycle** (`events.svelte.ts`): single socket
  per subscriber set, capped-exponential reconnect, normalized `EventEnvelope`.
- **Clean control-widget resolver** (`resolver.ts:33-82`) — slot-count dominance
  heuristic + `siblings` upgrade path, fully tested.
- **Structured API errors** (`ApiError` with `.problemCode`/`.problemDetail`,
  RFC 9457-aware, locale-aware `friendlyError()`), CSRF locked by tests.
- **Deliberate code-splitting** (Matter/Diagnostics/Logs dynamic; per-chunk
  budget enforced at build).
- **Mechanically-enforced widget contract** (`registry.test.ts` walks every
  `.svelte`, asserts the `onSetSlot` contract).

**Weaknesses, risks & smells.**
- **[W1] Hardcoded German in control widgets — CODE-VERIFIED.** `Switch.svelte:30`
  → `(isOn ? "An" : "Aus")`; `Siren.svelte:73` → `label="Aus"`. The keys
  `quick.on`/`quick.off` already exist in the catalogue but the widgets don't use
  them. **This contradicts the "complete de/en localisation" claim of PR #76** —
  the `control/widgets/` layer was missed. English-locale users see German labels
  on the most visible controls.
- **[W2] Manual DTO mirror drifts from the Go spec.** `types.ts` (723 lines)
  self-documents "will be replaced by openapi-typescript once the spec
  stabilises"; every new REST endpoint needs a parallel hand-edit; mismatches are
  silent. Inline `client.ts` comments still carry banned "Wave" labels
  (`:564,677,919-1047`) that would trip `TestDocPurity` if it covered TS.
- **[W3] WS connection-state polling.** `ConnectionBadge.svelte:21-24` runs a
  2 000 ms `setInterval` to re-derive status because the WS abstraction emits no
  state-change event — stale badge for up to 2 s, a live timer in every idle tab.
- **[W4] Locale is both a prop and a store** — `t()` reads `prefs.locale`
  reactively, yet `locale` is also threaded through ≥6 prop hops; new components
  can follow either path. (The prop is only semantically needed where it reaches
  REST calls doing server-side label resolution.)
- **[W5] 84 `as unknown as WidgetEntry` double-casts** in `index.ts` — a
  props-shape change is swallowed rather than surfaced.
- **[W6] Device list hard-capped at 200** (`devices.svelte.ts:27`,
  `listDevices(1, 200)`) with no pagination, no "load more", no `total > 200`
  warning.
- **[W7] No component-level tests** — the six Vitest files cover only pure TS;
  zero Svelte component / route / store-interaction tests.
- **[W8] A11y gaps** — the Switch/`ToggleFeature` toggle lacks an `aria-label`
  conveying device name + state, while sliders and `ControlButton` carry them
  (inconsistent rather than total).

**Improvements.**
- **[P1, S]** Route control-widget state strings through `t("quick.on/off")` in
  `Switch.svelte`, `ToggleFeature.svelte`, `Siren.svelte` — keys already exist;
  closes the PR #76 i18n gap.
- **[P1, M]** Adopt `openapi-typescript` (`make ui-types` from `openapi.yaml`),
  replacing the 723-line manual `types.ts`; replace the "Wave" comments with
  durable text.
- **[P1, S]** Emit `onStateChange` from `EventStream` (`ws.ts`), propagate as
  reactive `$state`, drop the `ConnectionBadge` `setInterval`.
- **[P2, S]** Keep the `locale` prop only where it reaches locale-bearing REST
  calls; use `t()` everywhere else; document the two valid patterns.
- **[P2, M]** Fix the 200-device ceiling — paginate `deviceStore.refresh()` or at
  minimum raise the limit + warn on `total > per_page`.
- **[P2, M]** Replace the 84 casts with one typed `asWidget<P>(c)` helper.
- **[P3, L]** Add `@testing-library/svelte` tests for the three stores and the two
  critical routes (`DeviceList`, `DeviceDetail`).
- **[P3, S]** Add `aria-label` (device name + state) to the toggle controls.

---

## Area 9 — Cross-cutting: Security, Config, Observability, Build/Release, Testing & Parity

**Overview.** `internal/auth/` (Basic/Bearer/Session/OIDC), `internal/config/`
(YAML + env-file + koanf), `internal/secret/` (AES-256-GCM at-rest),
`internal/observability/` + `internal/metrics/` + `internal/health/`,
`internal/audit/`, `.github/workflows/`, and the four test pillars under
`tests/`.

**Strengths.**
- **Clean auth separation, correct primitives.** Constant-time compares
  (`auth.go:87,196`); full OIDC RS256/JWKS with issuer/audience/`exp` pinning
  (`oidc/client.go:190-212`); CSRF double-submit on by default, bearer-exempt,
  64 KiB form cap; bcrypt cost 12 for the SQLite user store; salted-SHA-256 token
  storage; `ChainedUserStore` short-circuits on non-auth errors; server-side
  sessions (12 h TTL, `HttpOnly`, `SameSite=Lax`).
- **Production-sound config split.** Bootstrap/full two-tier (`bootstrap.go`) —
  small read-only Docker mounts, mutable runtime config in SQLite; `Validate()`
  enforces ports, duplicate central names, MQTT URL semantics; strict env-file
  loader keeps secrets out of YAML.
- **Strong CI.** lint+gofumpt+tidy, `govulncheck`, `go-licenses` copyleft gate,
  `gitleaks` full-history, CodeQL, 3-OS unit tests, contract, e2e, integration,
  nightly 2 M-iteration fuzz, weekly mutation testing; all actions pinned by SHA;
  oasdiff API-contract guard.
- **Disciplined tests.** `TestDocPurity`, `TestFilenamePurity`,
  `TestMarkdownLinksValid`, `TestDocPurity_MarkdownRefsExist`, capability pins,
  multi-CCU scope invariants, schema-digest guards — collectively prevent
  audit-tag and stale-reference rot.
- **Parity discipline.** `by_design.md` living catalogue;
  `matter-parity-contract.md` locks guards as build/test invariants; the four-step
  cross-stack snapshot pipeline is a real gate.

**Weaknesses, risks & smells.**
- **[W1] YAML `auth.users` passwords are plaintext — CODE-VERIFIED.**
  `config.go:689`: `Users map[string]string … // username → bcrypt hash (MVP:
  plaintext)`; `daemon_north.go:98`: `users.Put(name, pass, …)` puts the raw
  string. The YAML-seeded path (documented bootstrap before the first-run wizard)
  never invokes bcrypt; `cfg:"secret"` only encrypts at-rest in SQLite, not the
  value itself. Live in 0.2.0.
- **[W2] Secret-key unavailability is a silent plaintext downgrade.**
  `secret.go:73-76` logs one `Warn` and returns a pass-through `Cipher`. A
  misconfigured read-only `data_dir` silently stores CCU/MQTT/OIDC secrets in
  plaintext in SQLite; the condition surfaces in neither `/health` nor metrics.
- **[W3] No brute-force protection on the HTMX login.**
  `ui/auth_handlers.go:40-47` logs on failure, no delay/lockout; the REST rate
  limiter defaults **off** (`config.go:606`) and gates the REST listener, not the
  UI `/login` POST.
- **[W4] Stale German comment in `observability/instrument.go:93-95`**
  (`// Anwendungsstellen werden in P0-1 nachgezogen (siehe` — German verb + phase
  tag + broken citation) — would trip `TestDocPurity` intent.
- **[W5] e2e port allocation TOCTOU** (`tests/e2e/harness/ports.go:26-36`,
  `pickFreePort` × 4) — a flake source on shared CI runners.
- **[W6] The cross-stack release gate is only half-enforced in CI.**
  `integration.yml` (lines 52-58) notes the Python `aiohomematic` snapshot is not
  provisioned; CI runs steps 1-2, the `model_snapshot_diff.py` comparison
  (steps 3-4) stays local-only. The ~270-drift baseline can regress undetected.
- **[W7] Tracing is process-local only** (`observability/tracing.go`) — full
  span model, but stored in memory + slog; no OTLP/Jaeger exporter.
- **[W8] `MemoryTokenStore.AuthenticateToken` is O(n)** (`auth.go:86-92`) — a
  constant-time scan over every token per request; the scan time leaks the token
  count.

**Improvements.**
- **[P1, S]** Bcrypt-hash YAML-seeded passwords in `daemon_north.go:97-99`
  (detect a `$2a$`/`$2b$` prefix; else `GenerateFromPassword`).
- **[P1, M]** Surface secret-key plaintext-fallback as a `/health` degraded state
  + Prometheus gauge (`openccu_loom_config_secrets_plaintext`).
- **[P1, S]** Per-IP token bucket on the `/login` POST (UI listener), burst 5 /
  1 RPS — blunts brute force without inconveniencing users.
- **[P2, S]** Fix the `instrument.go` German/phase-tag comment.
- **[P2, M]** Provision `aiohomematic` + `pydevccu` in a nightly CI job and run
  all four snapshot-diff steps as a blocking gate — promote "mandatory before
  release" from doc to enforcement.
- **[P2, S]** Replace `pickFreePort` with `:0` daemon configs + post-start port
  extraction (the daemon already supports dynamic ports) — eliminates the TOCTOU
  window.
- **[P3, M]** Add an OTLP-gRPC exporter behind `north.rest.tracing.otlp_endpoint`
  — the span model already exists; only export is missing.
- **[P3, S]** Key `MemoryTokenStore` on `sha256hex(token)` for O(1), timing-safe
  lookup.

---

## §C. Doc-vs-Code Drift Register

The reason this analysis was mandated code-first. Each row is a place the
documentation asserts something the code does not deliver (or vice-versa).

| # | Doc claims | Code does | Where | Severity |
|---|---|---|---|---|
| C1 | `mqtt-topic-schema.md:54`: `master/<param>/set` is a **writable** command topic | `command_subscriber.go:517` silently drops every non-`values` bucket at Debug; MASTER routes via REST only | confirmed | **High** — silent automation failures |
| C2 | PR #76: "complete de/en localisation of the SPA" | `Switch.svelte:30` / `Siren.svelte:73` hardcode `"An"`/`"Aus"`; `control/widgets/` layer un-i18n'd | confirmed | Med — wrong-language UI |
| C3 | CLAUDE.md: "25 REST handler files (~80 endpoints)" | 47 handler files on disk | confirmed | Low — stale count |
| C4 | CLAUDE.md 0.1.0 snapshot: 194 k prod / 353 k test LOC | 217 k / 392 k | measured | Low — stale-by-design |
| C5 | CLAUDE.md repo tree lists `docs/audit/` | directory absent on disk (hence the SPEC-referenced `architecture-review-2026-05-05.md` is missing) | confirmed | Low — broken provenance |
| C6 | CLAUDE.md: four-step cross-stack snapshot is the "release gate" | CI runs only steps 1-2; steps 3-4 local-only (`integration.yml:52-58`) | confirmed | Med — gate not enforced |
| C7 | Matter parity tests imply schema is matter.js HEAD | dual `schema.json` copies, no byte-equality guard → false-green on a partial regen | `parity/parity_test.go:12` | Med — silent parity rot |
| C8 | `instrument.go` comment cites a follow-up location | citation is blank; comment is German + phase-tag | `:93-95` | Low — doc hygiene |

**General observation:** drift concentrates at *contract surfaces the code
deliberately narrowed* (MASTER writes, schema staleness) and at *headline
counts in the human-facing guides* (handler files, LOC, directory tree). The
code-internal `path:line` provenance comments (matter.js citations, ReGa script
references) held up well — the rot is in the prose layer, exactly where CLAUDE.md
predicts it ("code is the durable artefact, markdown is the conversation").

---

## §D. Cross-Cutting Themes

**T1 — Inconsistent goroutine lifecycle.** 53 production files use
`context.Background()`; hotspots `command_subscriber.go` (9), `eventbridge.go`
(8), `bridge.go` (5), `ccu_wiring.go` (5), `hub_wiring.go` (4); 79
`nolint:contextcheck`. The discipline is *uneven*, not absent: `scheduleSelfReload`
(WaitGroup + ctx) and the scheduler/unobserved-sweep are exemplary, while
recovery runs (A3-W1), MQTT handlers (A6-W1), and eventbridge loads (A3-W6) are
fire-and-forget. **Net effect: clean shutdown is best-effort; in-flight CCU
writes can outlive `Stop()`.** Fixes are individually small.

**T2 — God-objects accreting wiring.** `Unit` (1 253/53), `HubCoordinator`
(942/61, 9 semaphores), `device.Channel` (1 414/69), `Operations` (57 methods),
Matter `Bridge` (46 fields), REST `Deps` (58 fields), `handleIMOpcode` (350+),
`processSigma1Locked` (941). 26 `nolint:funlen`. Each grows by a fixed multi-site
ritual; refactors are hazardous because tests must compose the whole aggregate.

**T3 — Layering inversions.** Domain → transport (`event.go:13` →
`xmlrpc.Value`) and domain → north impl (`model/custom/*` →
`north/matter/cluster`). Both compile today (no cycle) but invert the hexagonal
direction the spec describes and are not in `by_design.md`.

**T4 — Error taxonomy not applied uniformly.** ~37 upstream 502s mislabeled
`TypeInternal` (A5-W1); southbound errors under-enriched with `hmerr.Context`
(A2-W4); 24 `nolint:nilerr`. The *machinery* is correct and present — it's just
not used everywhere.

**T5 — Doc-vs-code drift.** See §C. The user's instruction made this visible; it
is a real, recurring theme, not a one-off.

**T6 — Unbounded accumulation / missing backpressure.** No `audit_log` retention
(A4-W1); `GCDeadRows` unwired (A4-W2); `b.declared` unpruned (A6-W5); bus
deferred queue unbounded (A3-W5); hub lists unpaginated (A5-W5); SPA list capped
at 200 (A8-W6); WS channel ungated (A5-W4); self-reload uncapped (A2-W3). A
fleet- or time-scaled deployment hits these before a small one does.

**T7 — LAN-trust security with thin defense-in-depth.** Plaintext YAML passwords
(A9-W1), silent secret-key downgrade (A9-W2), no login brute-force friction
(A9-W3), BIN-RPC callback without allowlist (A2-W2), ungated WS (A5-W4). The
implicit threat model is "trusted home LAN"; that's defensible for the product,
but each surface deserves one layer of friction.

**T8 — Strong contract-test culture with specific holes.** No `wsapi.json`↔router
parity test (A5-W7), no Matter `schema.json` byte-equality test (A7-W1), no SPA
component tests (A8-W7), cross-stack gate half-enforced (A9-W6). The culture
exists to close exactly these gaps; they're additions, not new infrastructure.

---

## §E. Prioritized Improvement Roadmap

P1 = correctness / security / clean-shutdown, do first. P2 = robustness /
maintainability. P3 = polish / future-proofing. Effort: XS/S = hours, M = a
day-ish, L = multi-day.

### P1 — Correctness, security, lifecycle (do first)

| Theme | Action | Where | Effort | Area |
|---|---|---|---|---|
| Security | Bcrypt-hash YAML-seeded passwords | `daemon_north.go:97-99` | S | 9 |
| Security | Per-IP rate limit on `/login` POST | `ui/auth_handlers.go` | S | 9 |
| Security | Surface secret-key plaintext-fallback to `/health` + metric | `secret.go:73` | M | 9 |
| Security | Per-connection WS command rate gate | `ws/commands.go:150` | M | 5 |
| Correctness | Implement **or** remove the MQTT MASTER-write route (C1) | `command_subscriber.go:517` / `mqtt-topic-schema.md:54` | M | 6 |
| Correctness | `TypeInternal` → `WriteFromError` on ~37 upstream 502s | REST handlers | S | 5 |
| Correctness | Fix `CancelledRetries` double-count | `retry.go:524` | S | 3 |
| Correctness | `ensureHook` → `AddOnStateChange` | `retry.go:403` | S | 3 |
| Correctness | i18n the control-widget labels (C2) | `Switch/Siren/ToggleFeature.svelte` | S | 8 |
| Lifecycle | WaitGroup + cancellable ctx for recovery goroutines | `connection_recovery.go:434` | M | 3 |
| Lifecycle | Lifecycle ctx in `CommandSubscriber` (drop `context.Background()`) | `command_subscriber.go` ×9 | S | 6 |
| Persistence | `audit_log` retention (`PurgeBefore` + job) | `audit.go` | S | 4 |
| Persistence | Wire `GCDeadRows` to device-removal / scheduler | `values_cache.go:478` | S | 4 |
| Structure | Extract `Unit` services into `ServiceBundle` | `central.go` | M | 1 |
| Structure | Decouple `EventCoordinator` from `xmlrpc.Value` | `event.go:13` | S | 1 |
| Test guard | Matter `schema.json` byte-equality contract test (C7) | `tests/contract/` | S | 7 |
| Frontend | `openapi-typescript` DTO generation | `types.ts` | M | 8 |
| Frontend | `EventStream` state-change events; drop badge polling | `ws.ts` | S | 8 |

### P2 — Robustness & maintainability

- Paginate hub list endpoints (mirror `ListDevices`) — `hub.go`, M, [5]
- `resolveHubForRead` for single-named GETs — `hub.go`, S, [5]
- Funnel four rogue decode sites through `DecodeJSON` — S, [5]
- Stripe the event-bus dispatch by event-type bucket — `bus.go`, M, [3]
- `SafeGo`-wrap `refreshLocked` callback goroutines — `circuit.go:351`, M, [3]
- Operator-configurable MQTT retain-cleanup window — S, [6]
- Serialize snapshot → orphan-cleanup ordering — S, [6]
- Collapse `EnforcePerTypeCap` N+1 into one CTE delete — S, [4]
- `schema_version` on `config_sections` — S, [4]
- Move `north/matter/cluster` imports out of `model/custom/*` — M, [1]
- Sub-divide `internal/central/adapter/` — L, [1]
- Split `HubCoordinator` into 3 sub-coordinators — L, [1]
- Concurrency cap on `scheduleSelfReload` — S, [2]
- Extract `imDispatch` from `handleIMOpcode` — M, [7]
- Minimal in-memory Matter Groups — L, [7]
- Decompose Matter `Bridge` into `CommissioningSession` + `IMEngine` — L, [7]
- Provision Python stack + enforce 4-step snapshot gate in CI (C6) — M, [9]
- Fix the German/phase-tag `instrument.go` comment (C8) — S, [9]
- `:0` daemon configs in e2e (kill the TOCTOU) — S, [9]
- SPA: fix 200-device ceiling; replace 84 casts with `asWidget` helper — M, [8]
- Consolidate SPA locale prop/store access — S, [8]

### P3 — Polish & future-proofing

- `wsapi.json`↔router parity contract test — S, [5]
- Document `Deps` nil-semantics (or typed optionals) — S/M, [5]
- Prune `b.declared` for removed devices; per-filter QoS on resubscribe — [6]
- `ESCAPE '\'` on LIKE deletes; periodic `wal_checkpoint(PASSIVE)` — [4]
- Tier-model `Unit.Stop()` teardown — S, [1]
- `SafeGo` on eventbridge background loads — S, [3]
- Embed snapshot content-hash in generated Matter schema — S, [7]
- Extract `processSigma1ResumeLocked` — M, [7]
- OTLP exporter for the existing span model — M, [9]
- O(1) keyed `MemoryTokenStore` — S, [9]
- SPA component tests (`@testing-library/svelte`); a11y labels on toggles — L/S, [8]

**Suggested sequencing.** A first hardening sweep of the P1 effort-S items
(passwords, login limiter, `TypeInternal`, retry double-count, `ensureHook`,
control-widget i18n, audit retention, `GCDeadRows` wiring, schema-equality test,
`EventStream` events) is roughly a day of mechanical work and closes the sharpest
security/correctness edges plus the two confirmed doc-drifts (C1 decision, C2).
The P1 effort-M items (WS rate gate, recovery WaitGroup, command-subscriber ctx,
`ServiceBundle`, openapi-typescript, secret-key health surfacing) form a natural
second wave. The structural P2/P3 decompositions (adapter split, HubCoordinator
split, Matter `Bridge` split) are worth doing but should each carry an ADR, since
they reshape load-bearing types.

---

## §F. Verification Log (corrected / dropped claims)

Code verification materially changed the result — recorded here so the analysis
is auditable.

| Claim (as first reported) | Code check | Outcome |
|---|---|---|
| Southbound: `reportValueUsage` misclassified read at `classifier.go:63` | symbol exists only in `ccu.go:447,452`, `homegear.go:351`, `backend.go:126`; no `classifier.go` contains it | **DROPPED** — citation invalid |
| Southbound: self-reload goroutines unbounded/leaking at `callback_handlers.go:366` | `callback_handlers.go:37-47` — WaitGroup-tracked, `h.ctx`-bound, drained by `Stop()` | **CORRECTED** → reframed as "no *concurrency* cap" (real, smaller) |
| Southbound: `Capabilities.RPCCallback` declared but never gates Init | consumed at `client/payload.go:40` to build the init payload | **DROPPED** — contradicted |
| Southbound: `Operations` ~50-method monolith | `backend.go:31` — **57** methods | **CONFIRMED** (larger) |
| Southbound: BIN-RPC callback no allowlist/auth | `binrpc_server.go` logs `RemoteAddr()` only; no allow/auth logic | **CONFIRMED** |
| MQTT: MASTER write documented but dropped | `command_subscriber.go:517` drops non-`values`; `mqtt-topic-schema.md:54` advertises it | **CONFIRMED** (C1) |
| SPA: hardcoded `"An"`/`"Aus"` in control widgets | `Switch.svelte:30`, `Siren.svelte:73` | **CONFIRMED** (C2) — contradicts PR #76 |
| Cross-cutting: YAML passwords plaintext | `config.go:689` (`MVP: plaintext`), `daemon_north.go:98` raw `pass` | **CONFIRMED** (C1/W1) |
| Doc: "25 REST handler files" | 47 on disk | **CONFIRMED drift** (C3) |
| Doc: `docs/audit/` exists | absent on disk | **CONFIRMED drift** (C5) |

*Method note:* nine area agents read production code and cited `path:line`; a
separate pass re-ran the structural measurements (§A) and spot-checked the
highest-stakes findings against the tree. One agent section (Southbound) was
reconstructed from a pre-compaction summary and therefore received the heaviest
verification — three of its six findings needed correction, which is precisely
why "analyse the code, not the doc" was the right mandate.
