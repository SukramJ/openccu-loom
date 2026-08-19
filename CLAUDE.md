# CLAUDE.md — AI Assistant Guide for OpenCCU-Loom

The entry point for AI assistants working on **OpenCCU-Loom**. It is
deliberately short: it states each rule once and names the guard or the
document that carries the rest. Design intent lives in
[`SPECIFICATION.md`](./SPECIFICATION.md); implementation detail lives in the
code. When in doubt about *intent*, read the spec; about *implementation*,
read the code.

**Rules that apply only inside one subtree live in that subtree**, so you
pay for them only when you work there:

| File | Covers |
|---|---|
| [`internal/north/matter/CLAUDE.md`](./internal/north/matter/CLAUDE.md) | the matter.js gold-standard workflow, schema regeneration |
| [`assets/ui/CLAUDE.md`](./assets/ui/CLAUDE.md) | SPA operating concept, i18n + theme duty, Playwright suite |
| [`tests/CLAUDE.md`](./tests/CLAUDE.md) | the test pillars, test naming, the snapshot-parity pipeline |
| [`internal/model/custom/CLAUDE.md`](./internal/model/custom/CLAUDE.md) | adding a device profile |

Two long-form companions carry the reasoning behind the compressed rules
below — read them when a guard fires and you want to know what it protects:
[`notes/contributor/engineering-rules.md`](./notes/contributor/engineering-rules.md)
and
[`notes/contributor/subagent-delegation.md`](./notes/contributor/subagent-delegation.md).

---

## Orientation

If you are a fresh agent starting work on this repo:

1. Read [`SPECIFICATION.md`](./SPECIFICATION.md) end-to-end (~900 lines):
   goals, constraints, resolved decisions, risk register. Its preamble points
   at the authoritative source for everything else.
2. Read [`docs/adr/0001-license-mit.md`](./docs/adr/0001-license-mit.md) and
   [`docs/adr/0002-multi-ccu-first-class.md`](./docs/adr/0002-multi-ccu-first-class.md)
   — the two most consequential decisions.
3. "aiohomematic" is shorthand for the whole Python reference family; see
   [References](#references).
4. `CHANGELOG.md` is the authoritative release history. The build version is
   `internal/build/version.go`; the REST `APIVersion` in
   `internal/north/rest/handlers/info.go` is bumped independently. Counts of
   handlers / commands / profiles drift every release — read
   `assets/openapi.yaml`, `assets/wsapi.json` and the registry instead of
   trusting a number in prose.

The project is a large Go codebase spanning all 8 coordinators and all three
transports (XML-RPC, BIN-RPC, JSON-RPC). The primary config UI is a Svelte 5
SPA under `assets/ui/`, embedded via `go:embed all:spa_dist`; login, OIDC and
the first-run wizard all live there (ADR 0045). A minimal server-rendered
surface (`/health`, `/about`) remains only as a no-JS SPA-down diagnostic
anchor. Open work is driven by OpenCCU-Loom's own product needs; aiohomematic
is a reference implementation, and the standing parity guards are regression
detectors, not the roadmap.

---

## Project Overview

**OpenCCU-Loom** is a standalone Go daemon that talks to Homematic CCUs and
bridges their devices to MQTT, a REST + WebSocket API, a web Config UI, and a
Matter bridge. It originated as a Go port of
[`aiohomematic`](https://github.com/SukramJ/aiohomematic) and now develops
independently. The two projects coexist — aiohomematic powers the Home
Assistant integration, OpenCCU-Loom serves users who want MQTT / REST / UI /
Matter access without HA.

- **Language**: Go 1.26+ · module `github.com/SukramJ/openccu-loom`
- **License**: MIT (source); the binary aggregates openccu-data extracts under
  the eQ-3 HomeMatic Software License (non-commercial) — ADR 0003.
- **Deployment**: one static binary (`CGO_ENABLED=0`) + Docker
  (linux/amd64, arm64, armv7). No CGo.
- **Persistence**: SQLite (`modernc.org/sqlite`, pure Go) + filesystem.
- **Architecture**: hexagonal / ports & adapters, plus an internal typed
  generic event bus.
- **Multi-CCU**: first-class since 0.1.0 — one daemon, many CCUs.

North-bound: MQTT (HA Discovery **and** raw topic planes in parallel), REST +
WebSocket, the Svelte SPA, and a native-Go Matter bridge (default off, opt-in
via `cfg.North.Matter.Enabled` — SPECIFICATION.md §6, ADR 0012).

South-bound:

| Transport | Interfaces | Callback |
|---|---|---|
| XML-RPC + JSON-RPC | HmIP-RF, BidCos-RF, BidCos-Wired, HmIP-Wired, VirtualDevices | HTTP (`:8120`) |
| BIN-RPC | CUxD | raw TCP (`:8129`) |

Every interface supports push callbacks. **There is no polling / JSON-RPC-only
code path** — a deliberate divergence from aiohomematic.

---

## Critical Rules

Non-negotiable. Each one is followed by the guard that enforces it, or by
"reviewed" where none exists.

### Licensing and build

- **License header** on every Go file:
  `// SPDX-License-Identifier: MIT` + `// Copyright (C) 2026 OpenCCU-Loom authors.`
  No stray GPL / Apache / BSD headers in `pkg/` or `internal/`; vendored code
  keeps its upstream notice plus a modification line.
- **No CGo.** `CGO_ENABLED=0` at all times. If you think you need it (crypto,
  SQLite acceleration, Matter SDK), raise an ADR — do not add it silently.
- **Pure-Go SQLite** (`modernc.org/sqlite`). Switching to `mattn/go-sqlite3`
  needs an ADR.
- **Dependency licences**: MIT / Apache-2.0 / BSD are fine. **GPL / LGPL /
  MPL / AGPL pull in copyleft obligations — stop and discuss.** The embedded
  openccu-data archives are handled by a separate aggregation path (ADR 0003).

### Domain invariants

- **CUxD speaks BIN-RPC, not JSON-RPC.** We run our own BIN-RPC callback
  server. Never treat CUxD as "JSON-RPC only". Guard: `TestCuxdUsesBINRPCBackend`.
- **`CommandPriority.Critical = 0`.** Never use `if priority != 0` as "set" —
  compare against `hmenum.CommandPriorityCritical`.
- **Multi-CCU from day one.** Every coordinator, adapter and store is
  multi-CCU-safe; `CentralRegistry` holds them all and `central_name` is the
  scoping dimension. Never hard-code "the single CentralUnit". ADR 0002.
- **Interfaces live in the consumer package** — except protocol interfaces
  used across `central`, `client` and north-bound adapters, which live in
  `pkg/interfaces` because several packages declare the same dependency.
- **Callback ports are re-advertised on every reconnect.** When the listener
  does not bind a fixed port (only `rpc_callback.port_range`, XML-RPC only),
  the effective port is known at bind time; every `init()` carries the
  **effective** port. Neither `rpc_callback.port: 0` nor `bin_port: 0` is a
  dynamic mode — `applyDefaults` rewrites them to 8120 / 8129. BIN-RPC has no
  range setting, so two daemons on one host each need an explicit `bin_port`.

### Config surface

- **Never round-trip the `***` secret mask.** `GET /api/v1/config` masks every
  `cfg:"secret"` value (`maskSecrets` in
  `internal/north/rest/handlers/admin_config.go`). The SPA section editor
  skips secret-class fields when serialising a save, and the PUT handler calls
  `restoreMaskedSecrets` **before** validation + persistence. Breaking either
  side regresses real bugs: a complex secret (`north.rest.auth.users`, a
  `map[string]string`) fails strict type-validation as the string `***`, and a
  string secret saved as `***` overwrites the real credential. When you add a
  `cfg:"secret"` field, extend the masked-secret tests under
  `internal/north/rest/handlers/`.
- **Every `cfg:` field needs a label AND a help text in en + de** —
  `config.field.<path>` and `config.help.<path>`, in both catalogues of
  `assets/ui/src/lib/i18n.ts`. A missing label renders untranslated; a missing
  help silently drops the hint row. Guard:
  `TestConfigFieldsHaveLabelsAndHelp`.

### Wiring — the four rules that cost this project the most

A test that constructs the collaboration itself proves only that the
collaboration *can* happen, never that a running daemon *makes* it happen.
Call it a **bracketing test** and treat it as a defect. The full case history
is in
[`notes/contributor/engineering-rules.md`](./notes/contributor/engineering-rules.md);
the rules:

1. **Wiring is pinned through the composition root, never at the setter.** A
   new `Set*` / `Attach*` / `Register*` obliges a pin under
   `tests/contract/wiring_pins/` that constructs through the real constructor
   and asserts the *effect*. Reference: `internal/central/hub_notifier_wiring_test.go`.
   Guard: `TestEveryWiringSetterHasAProductionCaller` (ratchets:
   `wiringSettersWithoutCaller`, `wiringSeamsUnderInvestigation` — kept
   separate on purpose).
2. **Walking the central registry once is walking it at boot.** Do not write
   the walk; register a `central.Registry.OnRegister` observer, which replays
   over existing centrals and runs for every later one. The exception is a
   subsystem whose order relative to south-bound bring-up is load-bearing —
   those keep a named seam on `centralOrchestrator`. Guard:
   `TestEveryRegistryWalkerHasAnAdoptSeam`.
3. **A lifecycle test uses the production order.** Boot the simulated CCU
   **not ready** (`harness.Options{StartCCUNotReady: true}`), then flip it —
   otherwise the daemon finishes bring-up first and every subsystem reads a
   populated model however broken the wiring is. Guard:
   `tests/e2e/boot_order_test.go`.
4. **Declared and published must be the same set.** Any plane that declares
   entities (MQTT discovery above all) needs a round-trip test comparing
   declared topics against published ones. Guard: one
   `Test*PlaneTopicsRoundTrip` per plane.

### A verification without a negative control measures nothing

The same defect one level up. A check meant to *confirm* something is only
sound once it produces a **different** result when the cause is absent. Same
result in both directions means the check is untethered from what it claims to
measure — the claim is unverified, not confirmed.

It is the bite proof, generalised from guards to every act of verification:
running a script, reading a log, asking a sub-agent, probing a config switch.
The failure is seductive because the untethered check usually returns the
answer you expected.

So: before reporting something as verified, state what result the check would
have produced had the claim been false. If you cannot name that result, or the
check cannot produce it, the finding is **unverified** — say so in those words.
It is a legitimate outcome; a false "verified" is not.

And the twin rule: **an event nobody consumes is a dead feature that looks
identical to a live one** — both the type-switch that silently `default:`s and
the event with no subscriber at all. A comment naming a consumer is a
hypothesis; write the check instead. Guards: one
`Test*SinkFansOutEveryEventType` per fan-out, plus
`TestEveryEventTypeHasASubscriber` (declared silence goes in
`eventsWithoutSubscriber`).

### Live-CCU writes need explicit user approval — including device selection

The developer's CCU at `172.18.4.29` runs real, in-use devices. **Reads are
free** (every parameter / paramset / event read, all REST `GET`, all chip-tool
reads / subscribes). **Writes need explicit approval AND the user must name
the target device** — a self-chosen "just another HMIP-PSM" can be a
`Weinkühlschrank` that must not be cycled six times per chip-tool run. The
brief (`notes/contributor/chip-tool-test-brief.md` §T6) authorizes the *test
type*, never the *device*.

- Confirm address + channel before any write (`onoff on/off/toggle`, REST
  `PUT .../STATE/value`, paramset writes, anything reaching `Switch.Set`).
- Current sanctioned slot: `00021BE9957782:4 Bücherregal` (HMIP-PS, bookshelf
  lamp). Propose an alternative with reasoning if it does not fit.
- Leave the switch OFF via one final explicit `chip-tool onoff off` before
  unpair — do not trust toggle parity.
- For hermetic paths use `godevccu` (`tests/integration/`,
  `-tags=integration`) — a parallel path, not a substitute for the brief's
  Apple-independence test.

### matter.js HEAD is the Matter gold standard

Everything under `internal/north/matter/` (and the wire paths in `bridge/`,
`endpoint/`, `im/`, `tlv/`, `secure/`) is a semantic 1:1 port of
[matter.js](https://github.com/project-chip/matter.js) HEAD (Apache-2.0).
Cluster IDs, revisions, attribute IDs, constraints, defaults and wire shape
are taken verbatim — hand-coding any of them from the Matter PDF is
forbidden, because drift produces silent Apple/Google pair-aborts. The full
workflow (which file to read, how to cite it, which parity test to extend)
is in [`internal/north/matter/CLAUDE.md`](./internal/north/matter/CLAUDE.md);
deliberate divergences land in `notes/parity/by_design.md`.

---

## Repository Structure

```
openccu-loom/
├── SPECIFICATION.md  CHANGELOG.md  CONTRIBUTING.md  Makefile
├── example.config.yaml      — annotated reference config
├── cmd/openccu-loom/        — main daemon (composition root)
├── cmd/hmcli/               — admin CLI
├── pkg/                     — public surface: hmtypes, hmenum, hmerr,
│                              hmevent, hmlog, hmapi, hmreliability, hmui,
│                              interfaces, hmproto
├── internal/
│   ├── central/             — CentralUnit, coordinators, registries,
│   │                          rpcserver (XML-RPC + BIN-RPC callbacks)
│   ├── client/              — InterfaceClient, backends, transports, ReGa
│   ├── model/               — devices, data points, custom profiles,
│   │                          calculated, combined, schedule, hub
│   ├── store/               — SQLite (goose migrations) + in-memory caches
│   ├── north/               — rest (+ws), ui (SPA + no-JS /health,/about),
│   │                          mqtt, matter, mcp, webhook, bridge, discovery
│   ├── config/ configstore/ configui/ auth/ secret/ audit/ i18n/
│   └── health/ history/ metrics/ scheduler/ diagnostics/ …
├── assets/  ui/ (Svelte 5 SPA)  openapi.yaml  wsapi.json
├── docs/                    — PUBLISHED site only (mkdocs docs_dir)
├── notes/                   — engineering working docs, NEVER published
├── script/                  — snapshot + diff tooling
└── tests/                   — contract, golden, integration, e2e, chiptool,
                               harness, loadtest, bench
```

`internal/north/ui/spa_dist/` is produced by `vite build` and embedded at
compile time (gitignored — regenerated in CI, never committed).

**`docs/` vs `notes/` is a hard boundary.** Everything under `docs/` is
published to <https://sukramj.github.io/openccu-loom/> and needs a nav entry
in `mkdocs.yml`; nothing under `notes/` ever is. Read
[`notes/README.md`](./notes/README.md) before adding a document — it names the
four guards and how a published page cites a working document (an absolute
repo URL, never a relative link out of `docs_dir`). Published documents are
English-only.

---

## Development Environment

Go 1.26+, `golangci-lint` **v2** (a v1 binary rejects this repo's config),
`gofumpt`, `goreleaser`, Docker + buildx, `goose`. Python 3.14+ is needed only
for the cross-stack snapshot scripts under `script/` — build, tests and the
integration simulator are pure Go.

```sh
make build           # ./bin/openccu-loom
make test            # unit + contract
make integration     # against godevccu (in-process; Mosquitto needs Docker)
make contract lint fmt generate bench docker release
```

No `prek` / `pre-commit`; `make setup` installs a `golangci-lint` + `gofumpt`
hook.

---

## Code Quality & Standards

- **Linting**: `.golangci.yaml` enables `errcheck`, `govet`, `staticcheck`,
  `revive`, `gocritic`, `gosec`, `bodyclose`, `errorlint`, `exhaustive`,
  `nilerr`, `goimports`, `unconvert`, `unparam`, `wastedassign`, `prealloc`.
- **Formatting**: `gofumpt`; `goimports` grouping stdlib → third-party →
  internal → pkg.
- **Context**: `ctx context.Context` first on every I/O method; never ignore
  `ctx.Done()`; do not stash it in structs except in scheduler workers.
- **Errors**: sentinels in `pkg/hmerr`; wrap with `fmt.Errorf("…: %w", err)`;
  match with `errors.Is` / `errors.As`; every transport error carries
  `hmerr.Context{Protocol, Method, Host, Interface}`. No bare `panic` from
  library code.
- **Concurrency**: `sync.RWMutex` for read-heavy caches, channels for
  pipelines, `errgroup` for bounded fan-out. Every goroutine has a documented
  lifecycle and a way to stop. No package-level mutable state.
- **Generics** are expected (`events.Subscribe[T Event]`). **`any` needs a
  justifying comment** (usually "wire-decoded JSON before type-dispatch").
- **Naming**: short lowercase packages; `MethodNamer` for single-method
  interfaces; protocol interfaces in `pkg/interfaces` carry no `I` prefix.

### Comments and markdown

Comments must offer **durable value to a future reader** — the *why* of the
code, not the audit row that requested the change. `make test` blocks on
`TestDocPurity`, which bans wave/phase tags, audit item and drift IDs,
audit-run references and date stamps, German function-words, and
legacy-project provenance tokens (`aiohomematic`, `pydevccu`, …) in `//`
comments. Markdown references in comments must point at durable documents
(`TestDocPurity_MarkdownRefsExist`): permanent docs, ADRs,
`notes/parity/by_design.md`, matter.js / chip `path:line` — never a
transient audit report. Preserve the rationale, drop the tracking tag.

Markdown itself is held to a **looser** standard — it is where audit metadata
belongs. The one rule that transfers is link integrity
(`TestMarkdownLinksValid`).

The full ban list, the rewrite pattern and the exceptions are in
[`notes/contributor/engineering-rules.md`](./notes/contributor/engineering-rules.md).

---

## Architecture Quick Reference

```
Outside world → northbound adapters (north/mqtt, north/rest, north/ui)
              → domain core (central + model + client + store + health)
              → southbound adapter (client/transport/{xmlrpc,binrpc,jsonrpc})
              → CCU
```

- `internal/central` — `CentralUnit`, coordinators, callback servers,
  scheduler, registry. One `CentralUnit` per configured CCU, all held by a
  `CentralRegistry` shared with north-bound adapters.
- `internal/central/adapter` — per-central bring-up is **readiness-gated**:
  `ccu_readiness.go` polls the CCU's boot marker (`GET /ise/checkrega.cgi`
  → `OK`) before loading names then devices, so a co-booting CCU never yields
  devices-without-names. The northbound surface comes up immediately with a
  per-central "waiting for CCU" state that never trips `/health` to 503. The
  same gate guards mid-life reconnects.
- `internal/client` — `InterfaceClient` (one per `(central, interface)`),
  circuit breaker, retry, throttle, coalescer, ping/pong. Backends:
  `CcuBackend` (XML-RPC + JSON-RPC), `CuxdBackend` (BIN-RPC),
  `HomegearBackend` (XML-RPC; depth-parity is post-0.1.0).
- `internal/model/custom` — device profile registry, hand-maintained
  (ADR 0063). See [`internal/model/custom/CLAUDE.md`](./internal/model/custom/CLAUDE.md).
- `internal/central/events` — the typed, priority-aware `EventBus`, no
  re-entrancy:

```go
unsubscribe := events.Subscribe(bus, func(e hmevent.DataPointValueChanged) {
    // handle
}, events.WithPriority(events.PriorityHigh))
defer unsubscribe()
events.Publish(bus, hmevent.DataPointValueChanged{ /* ... */ })
```

**Callback servers** — two listeners, both shared across all centrals:
XML-RPC over HTTP on `rpc_callback.port` (default `:8120`, routed by URL path
`/RPC2/<central_name>`, accepts a `port_range`), and BIN-RPC over raw TCP on
`rpc_callback.bin_port` (default `:8129`, routed by `interface_id` in the
envelope, no range equivalent).

For caching, boot-time data flow and CCU radio cost, read
[`docs/caching.md`](./docs/caching.md).

---

## Testing

Details, the pillar-by-pillar catalogue and the snapshot-parity pipeline live
in [`tests/CLAUDE.md`](./tests/CLAUDE.md). The rules that apply everywhere:

- **Test names describe what is tested**, never a coverage push, audit row or
  wave (`backup_adapter_test.go`, not `coverage_boost37_test.go`).
- **Touching a protocol or capability boundary means adding or updating a
  contract test** in `tests/contract/`.
- **Behaviour-governing config needs one end-to-end test per value**, driven
  through the real path the value governs. `alarm.duress_visibility` shipped
  with three levels, a validator, a localized help text and a documented
  threat model — and a sink that dropped the event.
- **Never cite your own unverified wiring.** When a slice depends on an
  earlier slice's seam, its first test crosses that seam. A feature area
  spanning several PRs is audited **before** it is called done: seven green
  PRs let 72 defects through, two of them critical.
- Unit-test coverage target ≥ 80 % in `client`, `central`, `model/custom`,
  `store`; lower is fine for adapter shims.

---

## Common Tasks

### Add a REST endpoint

1. Update `assets/openapi.yaml` first (spec-driven).
2. Handler in `internal/north/rest/handlers/`, routed in
   `internal/north/rest/router.go`.
3. DTOs in `pkg/hmapi` (shared) or alongside the handler.
4. Unit + integration tests; regenerate the OpenAPI client if published.
5. Walk the two surfaces below.

### A new capability has more surfaces than the one you are editing

Both are reviewed, not guard-enforced — which is exactly why they get
forgotten: nothing fails when they are skipped.

- **MCP (`internal/north/mcp/`)** is how an assistant drives the daemon. A new
  or renamed verb, resource or payload field either extends the MCP surface in
  the same change, or the change records why not.
- **Navigation & views (`internal/north/ui/surface/`)** — every SPA view is a
  registered surface, switchable per operating mode. An unregistered view
  cannot be hidden, cannot be profiled, and is missing from the operator's own
  navigation editor. A moved or folded view updates its entry.

### Add a translation key

English value in `internal/i18n/catalogs/en.json`, German in `de.json`;
reference via `{{ t "key" }}` or `i18n.T(locale, "key")`. SPA strings go
through `assets/ui/src/lib/i18n.ts` instead — see
[`assets/ui/CLAUDE.md`](./assets/ui/CLAUDE.md).

### Add a database table

Migration under `internal/store/sqlite/migrations/` (goose
`<nnnn>_<name>.sql` with `-- +goose Up` / `-- +goose Down`), access struct
under `internal/store/sqlite/`.

---

## Git Workflow

- `main` is protected. Branches: `feature/<desc>`, `fix/<desc>`,
  `claude/<desc>`.
- Conventional commits, scopes along package boundaries (`client`, `central`,
  `model`, `north`, `store`, `rest`, `mqtt`, `ui`, `docs`, `ci`, …).
- Every PR passes `make test`, `make contract`, `make lint`; integration is a
  required check.
- Commits are signed off (`git commit -s`) — DCO applies.
- Releases tagged `vX.Y.Z`, pre-releases `vX.Y.Z-rc.N`.
- Never push `--force` to `main`.

---

## References

**aiohomematic** is shorthand for the whole Python reference family. They are
reference implementations, not dependencies — the gold standard for the **CCU
side**:

| Repo | Local path | Purpose |
|---|---|---|
| [`aiohomematic`](https://github.com/SukramJ/aiohomematic) | `../aiohomematic/` | transports, devices, paramsets |
| [`aiohomematic-config`](https://github.com/SukramJ/aiohomematic-config) | `../aiohomematic-config/` | form schemas, grouping, labels, profiles |
| [`homematicip_local`](https://github.com/SukramJ/homematicip_local) | `../homematicip_local/` | HA integration: config flow, WebSocket API |
| [`homematicip-local-frontend`](https://github.com/SukramJ/homematicip-local-frontend) | `../homematicip-local-frontend/` | Lit components; the SPA's UI-pattern reference |
| [`openccu-data`](https://github.com/SukramJ/openccu-data) | `../openccu-data/` | OCCU metadata extracts |

Quote the specific file + function when you use one, so provenance stays
traceable. Ported 1:1: enumerations and their string values, interface
classification sets (divergences in SPECIFICATION.md §5.1), paramset
normalization and patches, profile registration shape, visibility filters and
label-resolution chains, UI interaction patterns. **Not** ported: asyncio
idioms, Pydantic validation, HA-specific shims, the MQTT workaround for CUxD
(we have BIN-RPC), Lit components.

**matter.js** (`../matter.js/`) is the gold standard for the **Matter side**.
The two reference layers do not overlap; where a feature spans both, the
boundary is `internal/model/custom/<dp>/matter.go`.
[`home-assistant-matter-bridge`](https://github.com/Nabu-Casa/home-assistant-matter-bridge)
(`../home-assistant-matter-bridge/`) is a supplementary read for end-to-end
bridge composition, never a gold standard.

---

## Implementation Policy

Completion checklist per change:

- [ ] Fully typed, passes `golangci-lint`.
- [ ] Tests updated (unit + contract where applicable).
- [ ] `CHANGELOG.md` entry for user-visible changes.
- [ ] On a version bump: root `CHANGELOG.md` **and** both add-on changelogs
      (`packaging/ha-addon/openccu-loom/`,
      `packaging/ha-addon/openccu-loom-remote/`) **and** both `config.yaml`
      versions, alongside `internal/build/version.go`. Home Assistant shows
      these in the add-on store; release.yml guards both against the tag.
- [ ] Before a release: run the **comment-claims sweep** — fan out read-only
      agents that verify comment claims against the code (comments naming
      consumers, "stub"/"not wired yet" notes, file-header inventories,
      ratchet justifications). The mechanical guards cover only declared-silent
      events and ratchet texts; prose elsewhere is caught by this sweep alone.
      The 0.54.4 sweep found one live delivery bug and a dozen refuted claims
      that seven green PRs had let through.
- [ ] `SPECIFICATION.md` updated if a goal, non-goal, hard constraint or
      resolved decision moved; ADR written for any architectural shift.
- [ ] No CGo, no copyleft dependency, license header on every new `.go` file.

---

## Sub-Agent Delegation

The main conversation owns planning, contract and wire decisions, guard
specifications, sub-agent steering, and final verification. It does not own
typing.

**Delegate what a gate can prove; keep what only a reader can accept.**
Delegate freely: table cases, fakes, scaffolding, vitest cases,
`config.field.*` catalogue work, doc-purity and link cleanups, version bumps
across the changelog set, inventories and grep sweeps. Never delegate: the
composition root and any new wiring seam, `assets/openapi.yaml` /
`assets/wsapi.json` semantics and `pkg/hmapi` DTOs, Matter constants, auth /
session / secret handling, and *which* guard gets built. Locating is
delegable; reading is not.

| Agent | Model | Use for |
|---|---|---|
| `impl` | Sonnet | scoped implementation with a stated acceptance command |
| `guard` | Sonnet | tests from a caller-written guard spec, plus the bite proof |
| `sweep` | Haiku | read-only inventories and grep sweeps, high fan-out |
| `hunt` | Fable | adversarial read-only defect hunt, ranked candidates |

**Size CPU-bound fan-out from the host, never from a constant** — this project
is worked on from a 4-core box and a 14-core box:

```sh
cores=$(nproc); agents=$(( (cores - 1) / share ))   # share = cores per agent
```

Check the 1-minute load first; if it is at or above the core count, run one
CPU-bound agent and queue the rest. Eight concurrent agents is a practical
ceiling whatever the core count. Every CPU-bound agent must **pin** its share
in the command it runs (`GOMAXPROCS=<share> go test -p <share> …`,
`golangci-lint run --concurrency=<share>`, `vitest --maxWorkers=<share>`) —
handing out a number without pinning it changes nothing. Read-only agents cost
no core worth counting; 6–8 is a practical ceiling there.

Two hard rules: **no sub-agent runs `make test` or a repo-wide lint** (the
full gate runs once, in the main conversation, at the end of the slice), and
**no two writing agents in the same package** (disjoint file sets, or
`isolation: "worktree"`).

Every brief carries five things: the files and public surface plus one style
reference; the acceptance command the agent runs itself; the invariants it
touches; **stop conditions** (new dependency, DTO or API change, Matter
constant, composition-root wiring, ADR-shaped decision — a stop is a
successful outcome); and a report format ≤ 250 words, large artefacts to the
scratchpad as a path.

**A report is a claim.** Read the diff, run the gate yourself. For `guard`,
the acceptance artefact is not a green test but the **bite proof**: the named
production line removed, the test observed red with its message, the line
restored, green again.

The reasoning behind all of this is in
[`notes/contributor/subagent-delegation.md`](./notes/contributor/subagent-delegation.md).

---

## Interaction Protocol

1. **Describe approach before coding** — files, approach, trade-offs; wait for
   approval. Trivial edits may proceed directly.
2. **Clarify ambiguous requirements** — ask before guessing.
3. **Suggest edge cases and tests after implementation.**
4. **Bug fixing is test-first** — failing reproducer, then fix, then verify.
5. **Learn from corrections** — find the root cause, update memory when a
   pattern recurs.

---

## Tips for AI Assistants

**Do**: read `SPECIFICATION.md` for intent · full type annotations ·
`context.Context` first · `make lint && make test` before committing · add a
contract test when touching protocols, capabilities or state machines · pin
every new wiring through the composition root and assert the *effect* · update
`docs/` when public APIs change, open an ADR for major decisions · name the
`central` explicitly in every cross-cutting call · read matter.js first for
Matter-side code and cite it.

**Don't**: `any` without justification · `panic()` outside `main()` or test
helpers (exception: `Must*` constructors and documented `// invariant:`
panics) · non-MIT headers · CGo · direct commits to `main` · assume a single
`CentralUnit` · treat CUxD as JSON-RPC · hard-code callback ports ·
backwards-compatibility shims for aiohomematic data (this is greenfield) ·
hand-code Matter constants · audit-tracking codes in code comments.

**When in doubt**: intent → `SPECIFICATION.md` · architecture → `docs/adr/` ·
contract → `assets/openapi.yaml`, `assets/wsapi.json`, `tests/contract/` ·
implementation → the code (north → domain core → south) · caching and boot
data flow → [`docs/caching.md`](./docs/caching.md) · CCU semantics → the
Python siblings · Matter semantics → `../matter.js/packages/{node,protocol,types,model}/`
· then run the contract / parity tests · then ask the user.
