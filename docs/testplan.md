# OpenCCU-Loom — Test Plan

This document captures the test-debt landscape and the migration
strategy the project is following to close it. The reference list
of which Python tests have Go counterparts is regenerated on demand
via `script/test_migration_inventory.py`; this file is the operational
dashboard contributors check before touching tests.

## Volume snapshot (2026-04-27)

| Source | Files | LOC | Notes |
|---|---|---|---|
| aiohomematic core (`tests/`) | 131 | ~81 800 | 94 % async |
| aiohomematic-config (`tests/`) | 11 | ~3 815 | high async share |
| aiohomematic2mqtt (`tests/`) | 34 | ~4 690 | high async share |
| homematicip_local (`tests/`) | 33 | ~17 300 | mid async share |
| **Python family total** | **209** | **~107 600** | — |
| **OpenCCU-Loom** | **152** | **~24 000** | goroutine + channel idiom |

Coverage ratio against the Python family: ~22 % by LOC. The test gap
is the project's single largest deficit.

## Cluster mapping

Each Python cluster falls into one of four migration tiers. Use this
table to pick the right approach when porting tests.

| Python cluster | Python LOC | Go status | Migration tier |
|---|---|---|---|
| Model unit tests | ~8 400 | 🟢 covered (`internal/model/**/*_test.go`) | **1:1 portable** |
| Coordinators | ~37 200 | 🟡 skeleton (`internal/central/coordinators/*_test.go`, ~3 200 LOC) | **adapt (async→goroutine)** |
| Reliability | ~17 400 | 🟡 present (`reliability_test.go` + 4 sub-tests, ~1 200 LOC) | **adapt (timing mocks)** |
| RPC / Client | ~29 500 | 🟡 present (~3 900 LOC) | **1:1 with codec adaptation** |
| Event Bus | ~33 900 | 🟠 bench-only (~85 LOC) + scattered unit tests | **rewrite** |
| Store / persistence | ~29 500 | 🟠 minimal (~1 200 LOC) | **rewrite** |
| Calculated DPs | ~3 300 | 🟢 covered (`internal/model/calculated/*_test.go`) | **1:1 portable** |
| Custom DPs | (subset of model) | 🟢 covered | **1:1 portable** |
| Optimistic | ~410 | 🟢 actually deeper in Go (`generic/optimistic_test.go`, 680 LOC) | **already ahead** |
| Integration (pydevccu) | ~7 550 | 🟡 scaffold (`tests/integration/`, ~630 LOC) | **adapt to godevccu** |
| MQTT | ~990 | 🟡 minimal (`mqtt_test.go`, scattered discovery tests) | **adapt** |
| Contract | ~12 000 | 🟢 equivalent (`tests/contract/`, ~2 200 LOC incl. sub-tests) | **consolidate** |
| Linting (`test_kwonly_lint.py`, `test_lint_rega_scripts.py`, `test_scan_aiohomematic_calls.py`) | ~230 | 🔴 do **not** port | **skip** (golangci-lint covers the surface) |

## Migration tiers explained

The expectation **"the Python tests carry over and the assertions
still pass"** is true for *one* tier only. The other three demand
real architectural work.

### 1:1 portable (Model, Calculated, Custom, Optimistic, RPC/Client)

Translate the Python test almost verbatim. Replace `pytest`-style
parametrisation with Go table-tests; assertions stay identical. The
implementation is the spec.

### Adapt (Coordinators, Reliability, MQTT, Integration)

Setup machinery changes:

- `pytest-asyncio` fixtures become goroutines + channels.
- `freezegun` / `asyncio.sleep` mocks become an explicit `internal/clock`
  abstraction (introduced as part of P1-2).
- `pydevccu`-driven integration suites move to `godevccu`; expected
  event sequences must be recalibrated because the simulator's
  timing differs.

The **invariants** stay the same; the *test code* does not.

### Rewrite (Event Bus, Store / persistence)

Python tests check `asyncio.Lock` reentrancy and pytest-async race
helpers; Go uses `go test -race` plus channel-order assertions.
Different tools, different tests. Treat the Python test as a *spec
extracted from behaviour* and write a fresh Go test from scratch.

### Skip (Lint suite)

`golangci-lint` already covers the same scope; a Go-side AST linter
adds maintenance for no gain.

## Risk top 5 (drives schedule)

| # | Cluster | Risk | Mitigation |
|---|---|---|---|
| 1 | Event-Bus race tests | 38+ async race scenarios reduce poorly to Go's RWMutex model | `go test -race` is mandatory in CI; channel-order assertions |
| 2 | ConnectionRecovery | 27+ scenarios depend on time | introduce `internal/clock` abstraction (P1-2 prerequisite) |
| 3 | Coordinator async migration | `asyncio.gather` ↔ `errgroup.Wait` differ on cancel order | re-derive cancel expectations from invariants, not the Python source |
| 4 | Backend integration | `pydevccu` ↔ `godevccu` produce events in different order | re-record golden sessions; do not transplant event sequences blindly |
| 5 | Store cache coherency | Python uses in-memory mock locks, Go must verify SQLite txns | rewrite tests against the SQLite store; benchmark with `-race` |

## Effort breakdown (in person-days)

| Phase | Cluster | Days |
|---|---|---|
| P0 | Model + Calculated + Custom + Contract consolidation | 7–9 |
| P1 | RPC/Client + Reliability | 13–19 |
| P2 | Coordinators + Event Bus | 22–29 |
| P3 | Store + MQTT + Backend integration | 18–24 |
| **Total** | | **60–81 person-days** |

Realistic with two parallel streams: ~6–8 weeks.

## Operating principles

- The **Python test is the spec** when behaviour is in question.
  The Go test is a fresh statement of the same spec in idiomatic Go.
- **Add a contract test** when touching protocols, capability matrix,
  or state machines (`tests/contract/` is the catalogue).
- **`go test -race`** is the default for CI on `main`; feature
  branches run it on `needs-race` label.
- **Clock abstraction** is a hard prerequisite for the Reliability
  and ConnectionRecovery clusters — do not start P1-2 until it lands.
- **Golden sessions** for the integration suite live in
  `tests/golden/` and are regenerated with `go test -update` after
  godevccu fixture changes.

## Concurrency analysis

Three layers guard the goroutine and locking model. The first two run
in CI; the third is an opt-in build for local investigation.

- **`go test -race`** — the data-race detector, default on `main`
  (the CI test job sets `CGO_ENABLED=1` for it).
- **goleak** — goroutine-leak detection. `go.uber.org/goleak`'s
  `VerifyTestMain` is installed in the leak-prone core packages
  (`internal/client`, `internal/central`,
  `internal/central/coordinators`, `internal/store/sqlite`) via a
  per-package `leak_test.go`. The package test run fails if any
  goroutine a test spawned is still alive at the end — which is how a
  missing `Stop()`/`cancel()` path surfaces. Extend coverage by adding
  a `leak_test.go` with the same `TestMain` to another package; if a
  legitimate background goroutine must be tolerated, suppress it with
  the narrowest `goleak.IgnoreTopFunction(...)` and a justifying
  comment rather than disabling the check.
- **go-deadlock** — lock-order / stuck-lock detection, opt-in under
  the `deadlock` build tag (`make deadlock-test`). Packages opt in by
  declaring mutex fields as `syncx.Mutex` / `syncx.RWMutex`
  (`internal/syncx`) instead of `sync.Mutex` / `sync.RWMutex`. In the
  default build `syncx` types are plain aliases for the `sync` types
  (zero cost); under `-tags deadlock` they become go-deadlock's
  checking mutexes, and the run aborts with a goroutine dump on a
  lock-order cycle or a lock held past `DeadlockTimeout` (60s).
  Migration is incremental — only opted-in locks participate in the
  cross-package lock graph; `connection_recovery.go` is migrated as
  the first consumer. Widen coverage by switching more hot-path mutex
  fields to `syncx` and extending the `make deadlock-test` package set.

## Tracking

- **Status by cluster + per-file inventory**: regenerate via
  `python3 script/test_migration_inventory.py`.
- **Architecture-divergence items** (intentional shape mismatches,
  not gaps): `docs/parity/by_design.md` §A4 + §A5.
- **Risk register and prior open items**: see the `docs/parity/by_design.md` divergence catalogue and the Git history.

When a cluster moves between tiers — for example `Coordinator` going
from "adapt" to "rewrite" because we discover Python lock semantics
that don't translate — update both this document and the audit.

## Contract test invariants pinned (2026-04-27)

`tests/contract/` enforces these load-bearing rules. Add a contract
test entry whenever a CLAUDE.md / SPECIFICATION.md rule otherwise has
no Go-side guard.

- **CommandPriority.Critical = 0** — `enum_parity_test.go`
- **All MVP interfaces support push** — `enum_parity_test.go`
- **CUxD is BIN-RPC only** — `backend_capabilities_test.go`
- **JSON-RPC-only interfaces stay empty** — `enum_parity_test.go`
- **Coordinator floors per file** — `coordinator_size_test.go`
- **CircuitBreaker needs two HALF_OPEN successes to CLOSE** — `reliability_constants_test.go`
- **CircuitBreaker re-opens on HALF_OPEN failure** — `reliability_constants_test.go`
- **XML-RPC fault codes (-1, -2, -8, -9, -10) are stable + retryable** — `reliability_constants_test.go`
- **Profile parity vs. aiohomematic** — `profile_parity_generated_test.go`
- **Event catalogue completeness** — `event_catalogue_test.go`
- **OpenAPI schema matches handlers** — `openapi_test.go`, `openapi_schema_test.go`
- **i18n catalogue parity** — `i18n_parity_test.go`
- **Normalisation rules** — `normalization_test.go`

## Open migration items (still 1:1 portable)

Items still pending under P0-5 / P1, ordered by impact:

1. **Reliability timing tests** — exponential backoff + jitter envelopes
   need a `internal/clock` abstraction (P1-2 prerequisite). Until then,
   the Go tests cover behaviour without timing precision.
2. **Coordinator integration scenarios** — multi-step recovery + fail-
   over chains. Rely on the `Pipeline.Classify` hook landed in P0-2
   to map richer failure reasons.
3. **Store/cache coherency** — paramset patches + invalidation.
   Existing tests cover the in-memory paths; the SQLite txn paths are
   covered for the audit + incident stores but not for paramsets.
4. **Event-bus race tests** — Python carries 38+ scenarios; the Go
   side has scattered `go test -race`-aware unit tests but no
   consolidated battery.

## Pre-release production-load test

**Status**: not started; pre-release QA gate (target: before 1.0.0).

The daemon ships with contract tests + golden-file replay +
godevccu integration tests, but no measured headroom against a
realistic fleet.

### Target shape

- **Fleet size**: ~1 000 devices simulated across one or two
  godevccu instances (mix HmIP-RF / BidCos-RF / VirtualDevices in
  the same proportions a heavy production CCU carries).
- **Request rate**: ~10 000 req/s sustained for ≥ 15 min split
  across REST `GET /devices/.../data-points`, REST `PUT
  /devices/.../value`, MQTT command-topic writes, WS event
  fan-out, and CCU push callbacks.
- **Soak window**: ≥ 60 min continuous to surface goroutine /
  cache leaks the short integration tests miss.

### Pass criteria

- p99 REST latency < 50 ms for reads, < 200 ms for writes.
- 0 dropped audit rows; 0 dropped MQTT publishes.
- Memory RSS stable across the soak; no goroutine leak
  (compare `runtime.NumGoroutine()` at minute 5 vs. minute 60).
- DutyCycle warning band not entered on the simulator side
  (echoes the live-CCU rule).

### Tooling

- `tests/bench/` already houses Go benchmarks; the load test sits
  one tier up. Likely shape: a `tests/loadtest/` package guarded by
  `-tags=loadtest` so it does not run on every `go test ./...`.
- godevccu spins a programmable fleet via the existing
  `tests/integration/testdata/` fixtures; the load harness drives
  it through the same wire-DP construction as the integration
  tests.

### When

Before tagging the 1.0.0 release. Findings + remediation tracked
in the architecture audit doc.

