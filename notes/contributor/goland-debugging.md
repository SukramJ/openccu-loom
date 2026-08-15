# Debugging OpenCCU-Loom in GoLand

This document explains how to use the **shared GoLand run/debug
configurations** that ship with the repo, and what one-time setup each
developer has to do.

The configurations live under `.idea/runConfigurations/` and are
checked into Git. Per-user state in `.idea/workspace.xml` stays local
(see the carve-out in the root `.gitignore`).

---

## One-time developer setup

### 1. Open the project in GoLand

`File → Open…` and pick the repo root. GoLand picks up the existing
`.idea/` settings, including the run configurations below.

### 2. Create your local `config.yaml`

The `Daemon (config.yaml)` configuration expects a `config.yaml` at
the repo root. That file is **not** checked in (per-developer; see
`/config.yaml` entry in `.gitignore`). Copy the reference and edit
the CCU host/credentials for your local setup:

```sh
cp example.config.yaml config.yaml
$EDITOR config.yaml
```

If you only want to run the daemon with built-in defaults
(`config.Default()`), use the `Daemon (defaults)` configuration
instead — it does not need any local file.

### 3. (Optional) Install Delve

GoLand bundles its own copy of `dlv` and uses it automatically when
you click **Debug**. Nothing to install. If you ever want a CLI
Delve for remote debugging:

```sh
go install github.com/go-delve/delve/cmd/dlv@latest
```

---

## Shipped configurations

| Configuration | What it does | Notes |
|---|---|---|
| **Daemon (defaults)** | `openccu-loom run` (no `--config`) | Uses `config.Default()`. Good for spec exploration; no real CCU traffic unless defaults point at one. |
| **Daemon (config.yaml)** | `openccu-loom run --config $PROJECT_DIR$/config.yaml` | Requires the local `config.yaml` from step 2 above. |
| **All Unit Tests** | `go test ./...` | No build tags. Runs every package's unit + contract tests. |
| **Contract Tests** | `go test ./tests/contract/...` | Just the contract pillar (catalogue lives under `tests/contract/`). |
| **Integration Tests** | `go test -tags=integration ./tests/integration/...` | godevccu-based; slower. Mosquitto-backed cases need Docker. |
| **E2E Tests** | `go test -tags=e2e -timeout=300s ./tests/e2e/...` | Black-box, against the built binaries. Runs **Build Binaries** first — without fresh binaries the suite silently tests yesterday's build. |
| **Build Binaries** | `make build-all` | Every `cmd/*` into `./bin/`. Mostly a before-launch step for **E2E Tests**, but useful on its own. |
| **Lint** | `make lint` | `golangci-lint run ./...` in the IDE terminal. Full-repo, not scoped to changed files. |
| **hmcli** | `hmcli --help` | Admin CLI. Replace the program argument with the subcommand you are debugging. |
| **SPA Unit Tests** | `npm run test` in `assets/ui` | vitest component suite. |
| **SPA Browser E2E** | `npm run e2e` in `assets/ui` | Playwright browser-e2e + visual regression. macOS baselines only — CI compares the Linux ones. |

The Go configurations set `CGO_ENABLED=0` and use `$PROJECT_DIR$` as
the working directory, matching the build policy in
[`CLAUDE.md`](../../CLAUDE.md#critical-rules).

### Run vs. Debug

Top-right toolbar in GoLand:

- **Run** (▶): runs the binary as-is.
- **Debug** (🐞): rebuilds with `-gcflags="all=-N -l"` and attaches
  Delve. Breakpoints, watches, evaluate-expression, goroutine
  inspection — all available.

Set breakpoints by clicking in the gutter next to a line number, or
`Cmd+F8` on the current line.

---

## Hints specific to this codebase

### Callback-port collisions

Running the daemon twice on one machine with the default config
collides on `:8120` (XML-RPC) and `:8129` (BIN-RPC). Either stop the
other instance, or edit `rpc_callback.port` / `rpc_callback.bin_port`
in your local `config.yaml` to `0` (OS-assigned ephemeral) — see
[`SPECIFICATION.md`](../../SPECIFICATION.md) for the full callback
contract.

### Goroutine breakpoints in callback handlers

XML-RPC and BIN-RPC callbacks land on separate goroutines. When a
breakpoint fires inside `internal/central/adapter/`, switch to the
right goroutine in **Frames** to see the originating call stack.

### Conditional breakpoints

Right-click a breakpoint → **Condition…** to scope it, e.g.
`interfaceID == "HmIP-RF"` or `dpKey.Channel == 5`. Cheaper than
ten thousand log lines.

### Test-debugging shortcut

In any `*_test.go` file there's a green ▶ in the gutter next to each
`TestXxx` function — right-click → **Debug 'TestXxx'**. GoLand
generates a one-off configuration (does not pollute the shared list).

---

## Adding a new shared configuration

If you create a new run/debug configuration that's broadly useful
(e.g. a new top-level command, a new test pillar), share it with the
team:

1. Open **Run → Edit Configurations…**.
2. Select the configuration.
3. Tick **Store as project file** (top right).
4. GoLand writes the XML into `.idea/runConfigurations/<Name>.xml`.
5. Commit the new file.

Per-user tweaks (interactive args, attached debuggers, ad-hoc
breakpoints) stay in `workspace.xml` and are not committed.

---

## Local project structure (not shareable via Git)

`.idea/openccu-loom.iml` holds the module structure and is gitignored,
so every developer configures it once. Two settings matter enough to
call out:

**Custom build tags.** Without them, roughly 120 test files carrying
`//go:build integration|e2e|chiptool|loadtest|bench` are greyed out and
unresolved in the editor. Set them under **Settings → Go → Build Tags &
Vendoring → Custom tags**: `integration e2e chiptool loadtest bench`.
Do *not* add `deadlock`, `snapshot_gen` or `integration_live` — each has
a mutually exclusive counterpart (`!deadlock`, `!snapshot_gen`, the
`integration`-tagged files in the same package), so enabling them
produces phantom duplicate-declaration errors.

**Excluded folders.** Mark at least `.claude/worktrees` (agent worktrees
are full copies of the source tree, so search returns every hit three
times), `assets/ui/node_modules`, `bin`, `var`, `dist`,
`internal/north/ui/spa_dist` and `script/__pycache__` as excluded, plus
the exclude-file patterns `*.test`, `coverage.out`,
`model_snapshot_*.json` and `discovery_snapshot_*.json` — the snapshot
dumps alone are ~70 MB of generated JSON. Excluding `spa_dist` does not
break the `//go:embed all:spa_dist` directive in
`internal/north/ui/spa.go`.
