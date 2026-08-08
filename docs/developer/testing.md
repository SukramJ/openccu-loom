# Testing

The test pillars OpenCCU-Loom is held to, the make target behind each, what the contract tests guard, the cross-stack snapshot pipeline, and the pre-release checklist.

!!! info "Who this page is for"
    Contributors and developers writing or running tests. For the development setup itself, see [Development environment](setup.md).

## Test pillars

| Pillar | Location | Run with | What it covers |
| --- | --- | --- | --- |
| Unit | per package | `make test` | Regular Go tests; target ≥ 80 % in core packages (`client`, `central`, `model/custom`, `store`). |
| Contract | `tests/contract/` | `make contract` | Protocol / capability invariants — each test states a hard rule and fails if violated. |
| Golden / replay | `tests/golden/` | `make test` | Recorded CCU sessions replayed against the daemon; assertions compare emitted events / JSON to golden files. |
| Integration | `tests/integration/` | `make integration` | Daemon run against the in-process `godevccu` simulator plus Mosquitto (Docker); end-to-end behavior. |
| End-to-end | `tests/e2e/` | `make e2e` | Full black-box end-to-end flows against the built binary (REST, WS, MQTT, SPA, Prometheus, CLI). |
| Chip-tool commissioner | `tests/chiptool/` (`//go:build chiptool`) | `make chiptool-test`, `make matter-smoke` | Real `chip-tool` commissioner runs against the Matter bridge — the only guard that exercises a real Matter controller, not just parity fixtures. CI-only; cannot run on macOS. |
| Load test | `tests/loadtest/` | `go test -tags=loadtest ./tests/loadtest/...` | Production-shaped load harness (documented in [`notes/testplans/testplan.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/testplans/testplan.md)). |
| Benchmarks | `tests/bench/` | `make bench` | Performance; regressions > 20 % block release. |
| Fuzz | per package | `make fuzz` | XML-RPC / BIN-RPC / JSON-RPC parsers and paramset normalization. |
| SPA unit (vitest) | `assets/ui/src/**/*.test.ts` | `cd assets/ui && npm run test` | Component-level Svelte tests. Not covered by `make test`. |
| SPA browser-e2e | `assets/ui/tests/e2e/` | `cd assets/ui && npm run e2e` | Playwright browser-e2e + light/dark visual-regression baselines, mocked API. Not covered by `make test`. |
| SPA typecheck | `assets/ui/` | `cd assets/ui && npm run typecheck` | `tsc` type-checking; not covered by `make test` and must be green before a release merge. |

`make test` runs the unit and contract pillars together; `make race` adds `-race`. Integration, e2e, bench, fuzz, chip-tool, load-test, and the SPA suites are heavier and run on their own targets.

!!! note "Delegate test boilerplate"
    Production code (architecture, public API, wire decisions) belongs in the main work; the mechanical per-test work — fakes, table cases, assertions, race scaffolding — is well suited to a sub-agent. Name the file and surface, list the cases, and point at an existing test for style.

## What contract tests guard

Contract tests in `tests/contract/` lock invariants that must never silently drift. Representative examples:

- **Protocol / capability invariants** — that every MVP interface has ping/pong, that CUxD uses the BIN-RPC backend, that the device-profile registry stays in parity, and a cross-stack reliability-defaults drift detector.
- **Documentation purity** (`doc_purity_test.go`) — code comments carry no audit / wave / drift tracking tags and no German function-words; markdown references in `//` comments must point at files that exist.
- **Markdown link integrity** — every relative Markdown link across the repo's `.md` files must resolve to a real file on disk.

!!! warning "Touching a boundary means touching a test"
    If you change a protocol, capability, or state-machine boundary, you must add or update a contract test in the same change.

## Cross-stack snapshot pipeline

The release-gate parity check compares OpenCCU-Loom's domain model against aiohomematic's model when both load the same wire data. The whole pipeline runs from one target:

```sh
make snapshot
```

This runs the datasource diff, dumps both stack snapshots, and diffs them per field (`make snapshot-go`, `make snapshot-py`, `make snapshot-diff`). Exit 0 means full intersection parity. The snapshot JSON files are large and gitignored — produced on demand, kept locally. The common-schema definition lives in [`notes/parity/model_snapshot_schema.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/parity/model_snapshot_schema.md).

When you change model code (data-point creation, visibility marks, custom-DP composition, channel methods), rerun the snapshot and verify the drift score has not regressed in your area.

## Matter parity

Matter-side changes carry their own parity tests under `internal/north/matter/.../parity_matterjs_test.go`, plus the chip-tool smoke target:

```sh
make matter-smoke
```

The full set of standing Matter guards is catalogued in the [Matter parity contract](../matter-parity-contract.md).

## Pre-release checklist

Run before tagging a release. All of these must pass:

- [ ] `make lint` clean (`golangci-lint`, including `gosec`).
- [ ] `make vet` clean (`go vet`).
- [ ] `make test` and `make contract` green.
- [ ] `make race` green.
- [ ] `make integration` and `make e2e` green.
- [ ] `make bench` shows no regression > 20 %.
- [ ] `make snapshot` exits 0 (cross-stack parity).
- [ ] `make chiptool-test` / `make matter-smoke` green when the change touches the Matter bridge (CI-only, needs-chiptool label).
- [ ] `cd assets/ui && npm run typecheck` clean (not covered by `make test`).
- [ ] `cd assets/ui && npm run test` (vitest) green.
- [ ] `cd assets/ui && npm run e2e` (Playwright visual regression) green.
- [ ] `make licenses` clean — no GPL / LGPL / MPL / AGPL dependencies.
- [ ] `make vuln` and `make secrets` clean.
- [ ] No CGo introduced (`CGO_ENABLED=0` default build).
- [ ] OpenAPI validates (`make openapi-lint`, advisory) and the spec matches the handlers.
- [ ] `make release` (goreleaser snapshot) and `make docker` build cleanly for all target architectures.

See the [Security guide](../SECURITY.md) for the wider security review expectations that accompany a release.

## Related, repo-only testing docs

Not part of the published site nav, but worth reading in the checkout:

- [`notes/contributor/spa-e2e-against-godevccu.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/contributor/spa-e2e-against-godevccu.md) — SPA end-to-end validation against the embedded `godevccu` simulator, without a physical CCU.
- [`notes/testplans/testplan.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/testplans/testplan.md) and [`notes/testplans/e2e-testplan.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/testplans/e2e-testplan.md) — the fuller test-debt dashboard and the E2E suite's design plan / as-built log.
