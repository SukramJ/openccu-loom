# Development environment

How to get a working OpenCCU-Loom build on your machine, run it locally, and keep your changes within the project's code-quality rules.

!!! info "Who this page is for"
    Contributors and developers setting up a local checkout for the first time. For the contribution process and review expectations, see [`CONTRIBUTING.md`](https://github.com/SukramJ/openccu-loom/blob/main/CONTRIBUTING.md).

## Prerequisites

| Tool | Version / note |
| --- | --- |
| Go | 1.26+ (`go.mod` pins `go 1.26`) |
| `golangci-lint` | v2 (installed by `make setup`; `.golangci.yaml` is v2-format, a v1 binary rejects it) |
| `gofumpt` | formatter, stricter than `gofmt` |
| `goose` | SQLite migrations |
| Node + npm | only to build the Svelte SPA |
| Python 3.14+ | only for the profile-generator / snapshot scripts |
| Docker + buildx | integration tests (Mosquitto) and multi-arch images |

The integration tests run a pure-Go CCU simulator, so no Python toolchain is needed to run them — only Mosquitto via Docker.

## Clone, set up, build

```sh
git clone https://github.com/SukramJ/openccu-loom.git
cd openccu-loom
make setup     # install developer tooling + the pre-commit hook
make build     # compile the daemon into ./bin/ (embeds the current spa_dist/)
```

`make build` embeds whatever SPA assets are currently in `internal/north/ui/spa_dist/`. For a full release-shaped build that compiles the SPA first, use `make dist`:

```sh
make dist      # ui-build, then build
```

## Run locally

```sh
cp example.config.yaml config.yaml
./bin/openccu-loom --config config.yaml
```

The REST API, WebSocket stream, the Svelte SPA (login, OIDC, first-run onboarding — ADR 0045), and the minimal no-JS `/health` + `/about` diagnostic anchor all bind `:8119` by default (single listener since 0.14.0). See [Getting started](../getting-started.md) and [Configuration](../admin/configuration.md) for the config surface, and [Authentication](../admin/auth.md) for first-run credentials.

To iterate on the SPA against a running daemon:

```sh
make ui-dev    # Vite dev server, proxies /api to :8119
```

## Make targets

| Target | What it does |
| --- | --- |
| `make setup` | install developer tooling and the pre-commit hook |
| `make build` | build the daemon into `./bin/` (embeds current `spa_dist/`) |
| `make dist` | full build: compile the SPA, then the daemon |
| `make build-all` | build every binary under `./cmd/*` |
| `make ui-build` / `make ui-dev` / `make ui-check` | build / serve / type-check the Svelte SPA |
| `make test` | unit + contract tests |
| `make race` | unit + contract tests with `-race` (CGO=1, test-only) |
| `make contract` | contract tests only |
| `make integration` | godevccu + Mosquitto integration tests (Docker) |
| `make e2e` | end-to-end tests |
| `make bench` | benchmarks (`-tags=bench`) |
| `make fuzz` | fuzz targets, smoke duration |
| `make coverage` | coverage profile across unit + contract + integration |
| `make lint` | `golangci-lint` |
| `make fmt` / `make fmt-check` | run / verify `gofumpt` + `goimports` |
| `make vet` | `go vet` |
| `make vuln` / `make licenses` / `make secrets` | govulncheck / copyleft-license gate / gitleaks |
| `make generate` | run all code generators |
| `make snapshot` | full cross-stack model-snapshot pipeline |
| `make matter-smoke` | chip-tool PASE smoke against the Matter bridge (Linux host) |
| `make docker` | multi-arch Docker images (buildx) |
| `make release` | goreleaser snapshot (local test build) |

Run `make help` for the complete list with descriptions.

## Pre-commit hook

`make setup` installs a pre-commit hook that runs `gofumpt` and `golangci-lint`. There is no `prek` / `pre-commit` framework in this project. To run the hook against the working tree without committing:

```sh
make pre-commit
```

## Code-quality rules

A few rules are non-negotiable and are enforced mechanically:

!!! warning "Hard rules"
    - **License header** on every new `.go` file:
      ```go
      // SPDX-License-Identifier: MIT
      // Copyright (C) 2026 OpenCCU-Loom authors.
      ```
    - **No CGo** in the default build — `CGO_ENABLED=0` at all times. SQLite is `modernc.org/sqlite` (pure Go).
    - **No copyleft dependencies** — MIT / Apache-2.0 / BSD are fine; GPL / LGPL / MPL / AGPL are blocked by `make licenses`.
    - **`gofumpt` formatting** and `goimports` grouping (stdlib → third-party → internal → pkg).
    - Code comments stay in English and carry no audit / wave / drift tracking tags — `make test` blocks on the doc-purity contract test otherwise.

## Next steps

- [Testing](testing.md) — the test pillars and the release checklist.
- [Source interface](../contributor/source-interface.md) — the strong-model read/write contract.
- [Matter parity contract](../matter-parity-contract.md) — the binding rules for Matter-side changes.
