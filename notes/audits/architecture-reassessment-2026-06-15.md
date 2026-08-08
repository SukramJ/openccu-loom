# OpenCCU-Loom — Architecture Re-assessment (10-point scale)

> **HISTORICAL — a point-in-time scorecard, not current state.** Retained
> because ADR 0039 cites it as decision context. The scores describe the tree
> as of 2026-06-15 and have not been re-measured since. For what is currently
> open, read [`deep-audit-backlog.md`](./deep-audit-backlog.md).

- **Date**: 2026-06-15
- **Baseline**: [`architecture-analysis-2026-06-15.md`](./architecture-analysis-2026-06-15.md)
  (the original 9-area, code-grounded analysis)
- **What changed since the baseline**: ADRs 0029–0038 and two `-race`
  flake fixes all merged — tier-model `Unit.Stop` (0029), EventCoordinator
  decouple + `serviceBundle`, Matter `imDispatch` extraction (0031),
  HubCoordinator refresh-component (0035), the lean OTLP exporter (0037),
  the nightly cross-stack parity gate (0038), plus event-bus striping
  (0030), sigma-resume (0032), Groups (0033), adapter-split (0034) and
  Bridge-decompose (0036) closed as reasoned decisions.
- **Method**: nine parallel code-grounded sub-audits, one per area, each
  re-verifying the baseline's findings against the *current* tree and
  proposing a score; scores then calibrated for cross-area consistency.
- **Rubric**: 10 = exemplary, no significant issues · 8–9 = strong, only
  minor improvements · 6–7 = solid with real gaps · 4–5 = functional but
  notable weaknesses · 1–3 = significant problems.

## Scorecard

| # | Area | Score |
|---|------|:-----:|
| 1 | Domain Core, Hexagonal Architecture & Multi-CCU | **7 / 10** |
| 2 | Southbound Clients, Backends & Transports | **7 / 10** |
| 3 | Reliability, Recovery & Concurrency | **8 / 10** |
| 4 | Persistence & Caching | **7 / 10** |
| 5 | Northbound REST + WebSocket API | **8 / 10** |
| 6 | MQTT Bridge & Payload Assembly | **8 / 10** |
| 7 | Matter Bridge (native-Go, matter.js parity) | **8 / 10** |
| 8 | SPA Frontend | **7 / 10** |
| 9 | Cross-cutting: Security, Config, Obs, Build, Test & Parity | **8 / 10** |
| — | **Overall (mean)** | **7.6 / 10** |

The project moved decisively in the right direction: of the baseline's
P1 improvement items, the large majority landed and are test-covered, and
**every** security-critical Area 9 finding (W1–W8) is closed. No area
regressed. The residual gap to 8–9 across the board is consistent: a
handful of *new* edge-case findings surfaced by deeper reading (below),
plus a few genuinely-open structural items that were consciously deferred.

## Cross-cutting findings (highest value first)

These span more than one area or carry outsized impact — they are the
natural next round.

1. **`IsUpstreamUnavailable` omits `ErrNoConnection` → CCU-offline reads
   as HTTP 500, not 502.** Found independently by the Area 2 and Area 5
   audits. `problem/problem.go:129-134` does not mirror
   `hmerr.isDomainError` (`boundary.go:112`), so when the transport
   cannot reach the CCU (`hmerr.ErrNoConnection`, `xmlrpc/client.go:157`)
   the REST boundary classifies it `TypeInternal` (500) on the ~25–49
   handler sites that still use `TypeInternal`. The SPA cannot tell
   "CCU offline" from "daemon bug". **Low-effort, high-value fix.**
2. **`Groups.MatterInvoke` returns `StatusFailure` (0x01) instead of
   `StatusUnsupportedCommand` (0x81)** (`cluster/wire/groups.go:76`;
   dispatcher string-classifier `endpoint/dispatcher.go:549-564`).
   ScenesManagement deliberately includes the "no commands" marker to hit
   0x81; Groups does not, and `groups_test.go:82` only asserts `err != nil`.
   Apple/Google expect 0x81 for stub clusters — a latent interop defect,
   untested.
3. **`eventbridge.go` spawns 8 `context.Background()` goroutines with no
   WaitGroup** (`adapter/eventbridge.go:1740,1747,1885,1899,1941,2102,2288`);
   one (`publishCustomDPState`, :1740) is not even `SafeGo`-wrapped.
   Clean-shutdown is not guaranteed on the event-bridge path. (Area 1 + 3.)
4. **User-enumeration timing oracle**: `auth.go:247-249` returns early on
   unknown username, skipping bcrypt — response-time distinguishes
   "no such user" from "wrong password". Add a dummy compare on the miss
   path. (Area 9.)
5. **Per-class southbound throttles exist but are never wired**
   (`ccu_wiring.go:527-550` sets only `Throttle`; `ReadThrottle`/
   `WriteThrottle`/`ControlThrottle` stay nil). Reads can starve behind
   writes; the three-pool code is reachable only in tests. (Area 2.)

## Area 1 — Domain Core, Hexagonal Architecture & Multi-CCU — 7/10

**Strengths.** Eight single-purpose coordinators communicate only via the
typed `events.Bus` or `Unit` (no coordinator-to-coordinator imports); the
deferred-dispatch path with a 4096 high-water alert is well-tested.
Hexagonal boundary is real at the south seam — `EventCoordinator` now
takes `hmtypes.ParamValue` and the `xmlrpc` conversion lives in
`adapter/wire_value.go`. Multi-CCU scoping is structural (`Registry`
`map[string]*Unit` under `RWMutex`; every event carries `CentralName`; no
package-level `*Unit`). `Unit.Stop()` is now tier-structured
(`stop_tiers.go`); `serviceBundle` groups the 10 service closures.

**Weaknesses.** `Unit` is still ~1226 lines / 51 methods — `serviceBundle`
moved the closures but the 10 `Set*Fn` setters and the manual
`ServiceWiringStatus` map remain on `Unit`, so adding a service is still a
three-site change. `internal/model/custom/*` imports
`internal/north/matter/cluster` for `DataVersionTracker`
(`light/light.go:21`, `climate/climate.go:26`, +4) — undocumented
downward coupling not in `by_design.md`. 15 adapter files import
`internal/north/rest/handlers` types directly (`InterfaceState` defined in
`handlers/hub.go:853`) — a reverse coupling ADR 0034 didn't address.
`device.Channel` is 1414 lines / 69 methods with a two-lock design whose
acquisition order is undocumented (`channel.go:121,128,153`).

**Delta.** W2 (EventCoordinator imports xmlrpc) **fixed**; W6 (unordered
Stop hooks) **fixed**; W1 (Unit god-object) **partial**; W4 (adapter flat
namespace) **partial** (taxonomy doc); W5 (HubCoordinator) **partial**
(refresh-set, 942→798). W3 (`model/custom`→`matter/cluster`) **still
open**. New: `eventbridge.go` detached goroutines; `Channel` lock-order.

## Area 2 — Southbound Clients, Backends & Transports — 7/10

**Strengths.** Three transports cleanly separated, each enriching errors
with `hmerr.Context{Protocol,Method,Host,Interface}` uniformly
(`xmlrpc/client.go:207`, `binrpc/client.go:184`, `jsonrpc/client.go:502`).
The CUxD-uses-BIN-RPC invariant is pinned at four levels (factory +
three tests). `scheduleSelfReload` is now fully lifecycle-safe — a bounded
semaphore (cap 16, `callback_handlers.go:62-76`) + WaitGroup + cancellable
ctx — closing the baseline's W3. Per-class throttle routing exists;
`goleak.VerifyTestMain` guards the package.

**Weaknesses.** `Operations` is a 58-method god-interface
(`backends/backend.go`); CuxdBackend stubs 86 `ErrUnsupported`, Homegear
41 — no capability sub-interfaces. `IsUpstreamUnavailable` omits
`ErrNoConnection` (cross-cutting #1). Per-class throttles are nil in
production wiring (`ccu_wiring.go:527-550`). The BIN-RPC callback server
accepts any LAN peer — no IP allowlist (XML-RPC validates the URL path,
BIN-RPC has no equivalent). `MasterPoller` goroutines are ctx-bound but
not WaitGroup-tracked (`backends/master_poll.go:102`).

**Delta.** W3 (self-reload cap) **fixed**. W1 (god-interface, now 58),
W2 (BIN-RPC allowlist) **still open**. New: `ErrNoConnection`
mis-mapping; throttles inert in prod; MasterPoller drain.

## Area 3 — Reliability, Recovery & Concurrency — 8/10

**Strengths.** Recovery goroutine lifecycle is now fully disciplined —
`recoveryWG` + `runCtx`, `Stop` cancels then waits
(`connection_recovery.go:446,570`), `goleak` enforced. `ensureHook` →
`AddOnStateChange` (`retry.go:404`) fixed (sibling waiters no longer
stranded). `CancelledRetries` double-count gone (`retry.go:525,580`,
single count per cancel path). Event-bus re-entrancy/deferral is sound and
ADR 0030 correctly rejects striping (it would break
`TestReentrantPublishDeferredCrossType`).

**Weaknesses.** `safeFire` (`circuit.go:23`) spawns bare goroutines with
no WaitGroup — an OPEN→HALF_OPEN flip during shutdown can deliver a stale
state to a removed handler. `hub_retry.go:59` bare `go func()` (ctx-bound
but unwaited); `:109` uses `time.After` in the retry `select` — a 60 s
backoff timer can't be cancelled, so a restart mid-backoff hangs the
goroutine. `eventbridge.go:1740` calls `publishCustomDPState` on
`context.Background()` synchronously, not `SafeGo`-wrapped. The bus
`deferred` slice is unbounded (soft alert only).

**Delta.** W1, W2, W3 all **fixed**; W5 (striping) **closed by ADR 0030**;
W6 **partial** (eventbridge loads now `SafeGo`; `hub_retry` still bare);
W4, W7 **still open** (W7 now debug-logged). New: `time.After` leak;
unguarded `publishCustomDPState`.

## Area 4 — Persistence & Caching — 7/10

**Strengths.** Correct WAL + busy_timeout (`store.go:112`), `central_name`
as first PK column everywhere (no cross-central leakage expressible),
versioned caches with atomic boot-time stale wipe (incl. the new
`config_sections` schema_version, migration 020). `GCDeadRows` is now
wired (`values_cache_evict.go` subscribes `DeviceRemovedEvent`), audit
retention `Purge` lands (`audit.go:46,112`), and `EnforcePerTypeCap` is a
single CTE DELETE (`incidents.go:213`).

**Weaknesses.** The periodic WAL checkpoint covers only `auditDB`, **not
the values-cache DB** (`daemon.go:76` vs `values_cache_wiring.go:33`) —
that WAL can grow unbounded on busy ARM. The opportunistic purge zeros its
counter *before* calling `Purge` (`audit.go:112-115`), so a persistent
purge failure silently never deletes. `List` with `limit<=0` is still a
full unbounded scan (`audit.go:136`). `migrateMu` package-level mutex
still serialises test migrations.

**Delta.** W1 (retention), W2 (GCDeadRows), W3 (N+1), W4 (`LIKE` ESCAPE),
W6 (config schema_version) all **fixed**; W5 (migration Down), W7
(`migrateMu`) **still open**. New: values-cache WAL un-checkpointed;
Purge counter ordering; unbounded `List`.

## Area 5 — Northbound REST + WebSocket API — 8/10

**Strengths.** Spec-driven runtime enforcement intact (`OpenAPIValidator`
+ `TestSchemaDigestFresh` + version pin). WS command rate-limiting fully
wired (`ws/ws_ratelimit.go`, burst 60 / 20 rps, auth-subject key, typed
`CommandErrorRateLimited`). `wsapi.json` ↔ registration parity now guarded
(`TestWSCommandsMatchPinnedSchema`). All four hub list endpoints paginated
(`applyHubPagination[T]`). `GetSysvar` multi-CCU read regression fixed
(`resolveHubForRead`).

**Weaknesses.** `TypeInternal` still on ~49 sites including genuine
upstream paths (`links.go:99,188`, `matter.go:65,72,232`,
`matter_exposures.go:99…`) — the cross-cutting #1 fix was applied to
paramsets/hub but not these. `Deps` struct grew 58 → 82 fields with
nil-semantics as prose only. Two decode sites bypass the canonical
`DecodeJSON` (`admin_config.go:409`, `values_batch.go:68`). No
max-body-size guard before JSON decode at any site.

**Delta.** W4 (WS rate), W5 (pagination), W7 (wsapi parity), W2 (GetSysvar)
**fixed**; W1 (`TypeInternal`), W3 (rogue decoders) **substantially
fixed** with residual sites; W6 (batch op-gate), W8 (`Deps` nil-semantics)
**still open**. New: `Deps` +41 % field growth; no body-size cap.

## Area 6 — MQTT Bridge & Payload Assembly — 8/10

**Strengths.** Clean six-sink `CommandSink` decomposition; bucket-aware
8-segment dispatch natively routes `master/<param>/set` → `SetMasterValue`,
`values/…` → `SetValue`, drops read-only `calculated`
(`command_subscriber.go:535`). `PruneDeclaredForDevice` on device-removed
fixes monotonic `declared` growth; orphan-cleanup is serialised after the
synchronous initial snapshot; retain-cleanup window is operator-configurable
and clamped; per-filter QoS is replayed on reconnect.

**Weaknesses.** `birth_sync.go:59` still uses `context.Background()` for
`RepublishDiscovery` (detached from `lifecycleCtx`) — a shutdown
mid-republish leaks the goroutine until broker timeout. `SetMasterValue`
discards `interfaceID` (`mqtt_sink.go:70`) — a same-address collision
across two centrals would resolve to the first silently. `CombinedDPSink`
hard-errors on any non-`duration` kind (`mqtt_sink.go:233`) rather than
logging-and-dropping → observable noise.

**Delta.** W2 (silent MASTER drop / doc gap) **fixed** (now `SetMasterValue`,
mirroring the CCU `getMasterValue` vocabulary); W3 (fixed retain window),
W4 (orphan race), W5 (declared growth), W6 (QoS replay) **fixed**; W1
(`context.Background`) **fixed for command handlers, still open in
birth_sync**. New: `interfaceID` discarded; CombinedDP hard-error.

## Area 7 — Matter Bridge (native-Go, matter.js parity) — 8/10

**Strengths.** The dual-`schema.json` staleness gap is fully closed by two
standing guards (`TestMatterSchemaSnapshotInSync` +
`schema_provenance_gen.go` hash). `handleIMOpcode` decomposed — pure
`classifyIMOpcode` (`im_gate.go:38`) + per-opcode `receive_dispatch.go`,
the router down to 59 lines, `TestClassifyIMOpcode` needs no `Bridge`.
Sigma-resume confirmed already-extracted (`tryResume`, 82 lines, with
`resume_test.go`). Parity discipline is pervasive (8 `parity_matterjs_test.go`,
217 test files). Groups/Scenes stubs and the Bridge god-object carry ADR
cover (0033, 0036).

**Weaknesses.** `Groups.MatterInvoke` → `StatusFailure` instead of
`StatusUnsupportedCommand` (cross-cutting #2). Groups/ScenesManagement
cluster revisions are hand-coded and **not** in the wire-package parity
test (`cluster/wire/parity_matterjs_test.go:56`) — a matter.js revision
bump would be invisible. `Bridge` still 46 fields / ~1305 lines (ADR 0036
deferred). `handleSubscribeRequest` keeps its `gocognit,gocyclo,funlen`
suppression (`subscribe.go:521`, 200+ lines) — the W4 extraction didn't
reach the subscribe path.

**Delta.** W1 (schema staleness), W4 (`handleIMOpcode`), W5 (one-way parity
guard) **fixed**; W2 (Bridge), W3 (Groups/Scenes) **by-design / deferred**
(ADR 0036/0033); W6 (sigma 941) **finding corrected** (ADR 0032). New:
Groups status-code mis-map; missing Groups/Scenes revision parity.

## Area 8 — SPA Frontend — 7/10

**Strengths.** Hardcoded German fully resolved in control widgets
(`Switch.svelte:30`, `Siren.svelte:74`, `ToggleFeature.svelte:32` all via
`t()` + correct aria-labels). `ConnectionBadge` polling eliminated —
`events.svelte.ts` `onStateChange` reactive store, `ConnectionBadge.test.ts`
covers all three states. Device-list hard cap replaced with real
`total`-driven pagination (`devices.svelte.ts:31`, tested). Generated DTOs
adopted (`types.generated.ts`, 8023 lines); the 84-cast smell consolidated
to one documented `asWidget` helper.

**Weaknesses.** 9 unreconciled `TODO(openapi-typescript)` blocks remain in
`types.ts` (hand-written shapes that diverge silently from the spec). A
**new** hardcoded German string slipped in while fixing W1 —
`devices.svelte.ts:59` `"Sitzung abgelaufen"` (the `api.error.unauthorized`
key exists). The cast smell migrated: `cdp/dispatch.ts:36-67` still carries
20 bare `as unknown as` casts. Component test coverage is still thin (no
route/`ChannelPanel`/CDP-tile tests).

**Delta.** W1 (widget German), W3 (badge poll), W6 (device cap), W8
(a11y) **fixed**; W2 (DTOs), W5 (casts), W7 (component tests) **partial**;
W4 (locale dual-path) **still open**. New: German in `devices.svelte.ts:59`;
casts in `cdp/dispatch.ts`.

## Area 9 — Cross-cutting: Security, Config, Observability, Build, Test & Parity — 8/10

**Strengths.** **Every** baseline security finding closed: bcrypt-hashed
seed passwords + dual-mode `AuthenticateBasic` (`auth.go:231,251`); secret
plaintext-fallback surfaced on `/health` + Prometheus gauge
(`secret_health.go`); per-IP login token bucket (`login_ratelimit.go`);
`MemoryTokenStore` O(1) sha256-keyed (`auth.go:94`); OTLP/HTTP exporter
(ADR 0037); the cross-stack parity gate now nightly (ADR 0038); the
`instrument.go` German comment rewritten. Strong CI (lint/gofumpt/tidy,
govulncheck, go-licenses copyleft gate, fuzz, codeql, contract, the new
cross-stack gate).

**Weaknesses.** User-enumeration timing oracle (cross-cutting #4,
`auth.go:247`). Raw bearer tokens held in heap for fingerprinting
(`auth.go:69,86,149`) — a memory profile exposes active secrets. The
nightly cross-stack gate's first validation run surfaced a real baseline-vs-
CI-environment drift (generic-DP 760 vs baseline 70) that is *not*
aiohomematic-version skew (5.7 and 6.2 identical) — baseline recalibration
in `model_snapshot_drift_check.py` against the CI environment is the open
follow-up. e2e port-allocation TOCTOU (`tests/e2e/harness/ports.go`) still
open (documented). `integration.yml:58` cites a deleted
`parity_request.md`.

**Delta.** W1–W4, W6, W7, W8 all **fixed**; W5 (e2e TOCTOU) **still open**
(documented). New: timing oracle; raw-token residency; stale CI comment;
the drift-baseline calibration item.

## Recommended next round (prioritised)

- **P1, S** — Mirror `ErrNoConnection` into `problem.IsUpstreamUnavailable`
  and sweep the remaining `TypeInternal` upstream sites (links, matter,
  matter_exposures) → correct 502s. *(cross-cutting #1; Areas 2, 5)*
- **P1, S** — `Groups.MatterInvoke` → `StatusUnsupportedCommand` (0x81);
  assert the IM status code in `groups_test.go`. *(Area 7)*
- **P1, S** — Dummy-bcrypt on the unknown-username path to close the
  enumeration oracle. *(Area 9)*
- **P1, M** — Wire the periodic WAL checkpoint (and a shutdown checkpoint)
  to the values-cache DB. *(Area 4)*
- **P2, S** — Wrap `eventbridge.go`'s detached goroutines in `SafeGo` +
  lifecycle ctx/WaitGroup; replace `hub_retry.go`'s `time.After` with a
  cancellable timer. *(Areas 1, 3)*
- **P2, S** — Replace the new hardcoded `devices.svelte.ts:59` German with
  the existing `api.error.unauthorized` key; add `asWidget` to
  `cdp/dispatch.ts`. *(Area 8)*
- **P2, S** — Move `audit.go`'s counter reset to *after* a successful
  `Purge`; add a safety cap to `List(limit<=0)`. *(Area 4)*
- **P2, M** — Recalibrate the cross-stack drift baselines against the CI
  environment (or document the environment delta), so the nightly gate
  goes green and starts catching real regressions. *(Area 9 / ADR 0038)*
- **P2, M** — Wire the per-class southbound throttles in production, or
  remove the dead three-pool path. *(Area 2)*
- **P3, M** — Decompose the remaining god-objects opportunistically:
  `Unit` setter/wiring-status, `device.Channel` lock-order docs,
  `Operations` capability sub-interfaces, the Matter `Bridge` per ADR 0036's
  step-plan. *(Areas 1, 2, 7)*

## Verdict

**Overall 7.6 / 10** — a solid, well-tested 0.2.0 codebase that has
measurably hardened since the baseline analysis: all security-critical
items closed, the highest-risk concurrency lifecycle issues fixed and
`goleak`-guarded, the Matter parity surface tightened, and the documented
release gate finally enforced in CI. What separates it from an 8.5+ is a
consistent pattern of *partial* god-object decomposition (the structural
debt is contained and documented, not eliminated) and a thin tail of
newly-surfaced edge cases — most of them small, several genuinely
high-value (the 502 mis-classification and the Groups status code in
particular). None block deployment on a trusted home LAN.
