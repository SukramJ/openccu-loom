# CLAUDE.md — Test suites

Loaded when you touch `tests/`. Repo-wide rules: root [`CLAUDE.md`](../CLAUDE.md).

## Test file & test naming (do not create tracking-named tests)

Test file names and test-function names must describe **what is tested**,
not how or when the test was produced. Do **not** name a test file (or a
`TestXxx` function) after a coverage push, an audit row, a migration wave,
or a sequence number. The same tracking tokens banned from code comments by
`TestDocPurity` are banned from test names:

- ❌ `coverage_boost37_test.go`, `central_batch10_test.go`,
  `daemon_coverage4_test.go`, `gap_g_test.go`, `a3_hub_update_test.go`,
  `coverage_sweep_test.go`, `misc_nil_guards_test.go`, and any
  `_p1_/_g34_/_m3_/_v12_/_w6_/_a5_` style suffix or prefix.
- ✅ `backup_adapter_test.go`, `hub_wiring_refresh_test.go`,
  `lock_command_test.go` — named after the production unit or behaviour
  under test.

Write each test into the file named for the production unit it exercises
(`foo.go` → `foo_test.go`, or a behaviour-themed `foo_<aspect>_test.go`).
When you would otherwise create a `*_coverage`/`*_boostN`/`*_batchN` file,
add the cases to the existing unit's test file instead. `parity` /
`golden` / `contract` remain acceptable **only** when the file genuinely
implements that test kind (e.g. a real matter.js parity check), never as a
catch-all label.

Three mandatory test pillars:


## Contract tests (`tests/contract/`)

Protocol / capability invariants. Every test states a hard rule and
blows up if violated. The catalogue lives in `tests/contract/` —
representative: `TestAllMVPInterfacesHavePingPong`,
`TestCuxdUsesBINRPCBackend`, `TestDeviceProfileRegistryParity`,
`TestRecordedReliabilityDefaults` (cross-stack drift detector).

**If you touch a protocol / capability boundary, you must add or
update a contract test.**

## Golden-file / session replay (`tests/golden/`)

Recorded CCU sessions played back against the daemon. Assertions
compare emitted events or output JSON against golden files. Run with
`-update` to refresh.

## Integration tests (`tests/integration/`)

Run the daemon against an in-process `godevccu` simulator (a pure-Go
port of pydevccu — no Python toolchain required) and assert
end-to-end behavior. Slow; gated behind `-tags=integration`.

## SPA browser-e2e + visual regression (`assets/ui/tests/e2e/`)

Playwright drives the real SPA in a headless Chromium and locks in the
homogeneous operating concept (navigation + document titles + skip-link,
the shared loading/empty/error states, toast feedback, the confirm
dialog) plus visual baselines of representative views in **both light
and dark mode**. The suite is hermetic: it serves the SPA via the Vite
dev server and **mocks every `/api/v1/*` call** (`tests/e2e/helpers/
mock-api.ts`), so no daemon or CCU is needed and screenshots are
deterministic. Run with `cd assets/ui && npm run e2e`; refresh baselines
with `npm run e2e:update`. CI (`.github/workflows/spa-e2e.yml`) runs it
inside the official `mcr.microsoft.com/playwright` container so
rendering matches — screenshot baselines are committed **per platform**
(`*-chromium-linux.png` for CI; macOS `-darwin` baselines coexist for
local runs). The component-level Svelte tests are the separate `vitest`
suite (`*.test.ts` under `assets/ui/src/`); keep both green.

## Unit tests

Regular Go tests per package. Target ≥ 80 % coverage in core packages
(`client`, `central`, `model/custom`, `store`). Lower is OK for
adapter shims.

## Benchmarks

Live in `tests/bench/`. Run weekly in CI. Regressions > 20 % block release.

## Fuzz tests

XML-RPC parser, BIN-RPC codec, JSON-RPC parser, paramset normalization.
Short runs on every PR; longer nightly.

## Cross-stack model-snapshot verification

End-to-end regression check: OpenCCU-Loom's domain model (Devices →
Channels → DataPoints) is compared against aiohomematic's model as a
reference when both stacks load the same wire data. This catches
unintended model regressions — it runs as a scoped parity guard, not
as a measure that output must match aiohomematic (parity is no longer
the project's primary goal). The four-script pipeline below is the
snapshot regression run.

Four scripts, run in this order:

```sh
# 1. Wire-data identity (399 devices × 12 attributes per parameter
#    between pydevccu and godevccu). Must be 0 drift.
python3 script/datasource_diff.py

# 2. Dump OpenCCU-Loom's model against godevccu (~80k DPs, 60+ MB JSON).
go test -tags=integration -timeout=300s \
    -run TestModelSnapshotDumpAgainstGodevccu ./tests/integration/...

# 3. Dump aiohomematic's model against pydevccu (~8k DPs, ~8 MB JSON).
#    The script auto-re-execs in the aiohomematic venv if openccu_data
#    is not on the active sys.path — without that the python snapshot
#    silently emits empty parameter labels and masks real drift.
python3 script/aiohomematic_snapshot.py

# 4. Per-field diff with documented tolerated fields (`profile`,
#    `wrapped_dps`) and a paramsets-channel-field exclusion. Exit 0
#    means full intersection parity.
python3 script/model_snapshot_diff.py
```

Common-schema definition: `notes/parity/model_snapshot_schema.md`.

The two snapshot JSON files (`tests/integration/testdata/model_snapshot_*.json`,
total ~70 MB) are gitignored — they are produced on demand and live
only locally. Set `OPENCCU_LOOM_SNAPSHOT_DEVICES=A,B,C` to scope both
sides to a smoke subset for fast iteration; default loads the entire
embedded fleet.

When you change model code (DataPoint creation, visibility marks,
custom-DP composition, channel methods), rerun (2) and (4) and verify
the drift score has not regressed in your area. The current baseline
sits at ~270 architecturally-accepted drifts; growth beyond that
without a corresponding entry in `notes/parity/by_design.md` is a regression.

---

