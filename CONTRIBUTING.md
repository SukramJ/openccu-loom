# Contributing to OpenCCU-Loom

Thank you for your interest — here is what we need from every
pull request.

> **AI policy:** AI-assisted contributions are welcome; autonomous
> agent submissions are not. See [`AI_POLICY.md`](./AI_POLICY.md).

## Before you open a PR

- Skim [`SPECIFICATION.md`](./SPECIFICATION.md). It is the source of
  truth for design questions. ADRs live under [`docs/adr/`](./docs/adr/).
- Open an issue for any non-trivial change so we agree on the shape
  before the code lands.
- Sign off every commit (`git commit -s`) — DCO applies so the
  authorship chain for contributions stays verifiable.

## Local setup

```sh
git clone https://github.com/SukramJ/openccu-loom
cd openccu-loom
make setup    # installs git hooks + linters
make build
make test
```

Prerequisites:

- Go 1.26+
- `golangci-lint` v2 (installed by `make setup`; the repo's `.golangci.yaml`
  is v2-format, a v1 binary rejects the config)
- `gofumpt`
- Docker + Buildx (for Docker image + Mosquitto integration tests)
- Python 3.14+ (only for the profile generator script)

## What a passing PR looks like

- `make lint` clean (zero findings).
- `make test` green.
- `make integration` green when the change touches anything
  south-bound (transports / coordinators / bridges).
- New user-visible behaviour has an entry in `CHANGELOG.md`.
- Protocol / capability boundaries have a contract test (see
  `tests/contract/`).
- Every new `.go` file starts with the MIT SPDX header:

  ```go
  // SPDX-License-Identifier: MIT
  // Copyright (C) 2026 OpenCCU-Loom authors.
  ```

## Style notes

- Use MIT SPDX headers consistently — no stray GPL / Apache / BSD
  headers in `pkg/` or `internal/`. Dependencies keep their own
  licenses (documented in `go.sum` / vendor reports).
- No CGo. `CGO_ENABLED=0` at all times.
- `context.Context` is the first argument on every I/O method.
- Interfaces live in the consumer package (Go convention). The only
  exception is `pkg/interfaces` for cross-cutting protocol contracts.

## Pin tests for new wiring

When you add a new exported method, constructor, or event subscriber that
is wired through an indirect path — factory closure, callback field,
struct-literal assignment — add a pin test in
`tests/contract/wiring_pins/`.

Pin tests are 3-5 lines each.  They use the helpers in
`tests/contract/wiring_helpers.go` to assert at the AST level that a
specific identifier or string literal appears in a specific production
file.  A pin test turns a silent refactoring regression into an
immediate build failure.

**When to add a pin test:**

- A new exported method is the sole caller of a critical side effect and
  is reached only via a constructor argument, closure, or interface
  assignment (not a direct call chain that static analysis can follow).
- A new struct-literal field enables a feature that silently degrades
  when the field is dropped (e.g. `FactoryInput.JSONRPC`).
- A new RPC method name string controls routing between incompatible
  server implementations (e.g. `Interface.setInstallModeHMIP` vs.
  `Interface.setInstallMode`).

**What a pin test must not do:**

- Test behaviour — that belongs in unit or integration tests.
- Hard-code line numbers — use identifier-based searches so the pin
  survives normal code movement.
- Reference audit IDs, wave numbers, or date stamps in comments.

**Example:**

```go
func TestPin_MyNewMethod_WiredInFoo(t *testing.T) {
    contract.MustFindCallerInFile(t,
        "internal/some/package/foo.go",
        "internal/some/package", "MyNewMethod",
    )
}
```

The four helper signatures available in `tests/contract/wiring_helpers.go`:

| Helper | Use case |
|---|---|
| `MustFindCallerInFile` | identifier referenced anywhere in the file |
| `MustFindStringLiteralInFile` | exact string literal (e.g. an RPC method name) |
| `MustFindMethodCall` | `receiver.Method(...)` call on a named receiver |
| `MustFindStructLiteralField` | `StructName{FieldName: ...}` composite literal |

## Reachability

Before any PR that adds a new exported identifier:

```bash
make reachability
go run ./script/reachability/crosscheck.go
```

If your new exported symbol shows up as "genuine dead code":
1. **If it is used in production:** wire in a production caller.
2. **If it is reached via reflection/factory:** annotate the declaration
   with `// loom:reachable:reason="..."`.
3. **If it is a genuine library export:** annotate it as well.
4. **If it is superfluous:** propose it for deletion (review marker).

## Wire snapshots

Whenever you change a custom-DP setter:

```bash
make wire-snapshots                                 # regen
git diff tests/contract/wire_snapshots/snapshots/   # review
```

The diff shows every wire-byte change. Expected changes → commit them.
Unintended changes → fix the code.

## E2E smoke

Before a release or after an architectural change:

```bash
make e2e
```

The black-box E2E suite (`tests/e2e/`) drives the built binary against
an embedded godevccu + mochi-mqtt broker, exercising REST, WebSocket,
MQTT discovery, the SPA, Prometheus, and the CLI end to end.

## SPA tests

The Svelte SPA (`assets/ui/`) has its own test pillars — none of them
run as part of `make test`:

```sh
cd assets/ui
npm run test       # vitest — component-level unit tests
npm run e2e        # Playwright browser-e2e + visual regression (mocked API)
npm run typecheck  # tsc — must be green before a release merge
npm run check      # svelte-check
```

## Commit style

Conventional commits scoped to the package boundary:

```
feat(rest): add /backups/download endpoint
fix(mqtt): retry SUBACK on broker restart
docs(adr): clarify CUxD wire switch
```

## Running only one piece of the test matrix

```sh
go test ./internal/central/...                    # unit
go test -tags=bench -bench=. ./tests/bench/...    # benchmarks
go test -tags=integration ./tests/integration/... # godevccu + Mosquitto
go test -tags=loadtest ./tests/loadtest/...       # production-load harness
go test -cover ./...                              # coverage report
```

The Matter bridge also has a real chip-tool commissioner suite
(`tests/chiptool/`, `//go:build chiptool`, run via `make chiptool-test`
or `make matter-smoke`) — it requires a real `chip-tool` binary and only
runs in CI (macOS runners cannot execute it; see
[`docs/developer/testing.md`](./docs/developer/testing.md)).

## Reviewing a PR

- Prefer blocking comments over nits. Mention explicitly when a
  comment is optional.
- Confirm CI is green before approving — contract and integration
  tests cannot be skipped.
- Verify `CHANGELOG.md` reflects the user-visible delta.
- Ask for a rebase on `main` if the branch is more than a day
  behind — avoids noise in the final merge.

## Releasing

Automated via GitHub Actions on a `vX.Y.Z` tag. Maintainers:

```sh
# Update CHANGELOG.md, move [Unreleased] to [X.Y.Z].
git tag -s vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

The release workflow runs `goreleaser release`, builds multi-arch
Docker images, and publishes to GitHub Releases + ghcr.io.
