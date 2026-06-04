# CLAUDE.md — AI Assistant Guide for OpenCCU-Loom

This document is the entry point for AI assistants (like Claude)
working on **OpenCCU-Loom**. It is intentionally concise. The design
intent (goals, non-goals, constraints, resolved decisions, risk
register) lives in [`SPECIFICATION.md`](./SPECIFICATION.md);
everything else lives in the authoritative sources listed in that
spec's preamble (code, ADRs, `assets/openapi.yaml`,
`config.example.yaml`, etc.). When in doubt about *intent*, read
the spec; when in doubt about *implementation*, read the code.

---

## Table of Contents

1. [Orientation](#orientation)
2. [Project Overview](#project-overview)
3. [Critical Rules (do not violate)](#critical-rules)
4. [Repository Structure](#repository-structure)
5. [Development Environment](#development-environment)
6. [Code Quality & Standards](#code-quality--standards)
7. [Architecture Quick Reference](#architecture-quick-reference)
8. [Testing Guidelines](#testing-guidelines)
9. [Common Tasks](#common-tasks)
10. [Git Workflow](#git-workflow)
11. [aiohomematic as a Reference](#aiohomematic-as-a-reference)
12. [matter.js as the Matter Gold Standard](#matterjs-as-the-matter-gold-standard)
13. [Implementation Policy](#implementation-policy)
14. [Interaction Protocol](#interaction-protocol)
15. [Tips for AI Assistants](#tips-for-ai-assistants)

---

## Orientation

**If you are a fresh agent starting work on this repo:**

1. Read [`SPECIFICATION.md`](./SPECIFICATION.md) end-to-end. It is
   short by design (~600 lines): goals, constraints, resolved
   decisions, risk register. The preamble points at the
   authoritative sources for everything else (code, ADRs,
   openapi.yaml, etc.) — follow those pointers when you need
   implementation detail.
2. Read [`docs/adr/0001-license-mit.md`](./docs/adr/0001-license-mit.md)
   and [`docs/adr/0002-multi-ccu-first-class.md`](./docs/adr/0002-multi-ccu-first-class.md)
   for the two most consequential design decisions.
3. When the user mentions "aiohomematic", treat it as a shorthand
   for the whole Python reference family
   (`aiohomematic`, `aiohomematic-config`, `homematicip_local`,
   `homematicip-local-frontend`, `openccu-data`). They live side by
   side under `../` on the developer's
   machine. See §[aiohomematic as a Reference](#aiohomematic-as-a-reference)
   for the mapping and when to consult which.
4. The project is **substantially implemented** (phases 0–10
   structurally complete, status 2026-05-26): ~547 k Go LOC across
   2 099 files (817 production / 194 k LOC + 1 282 tests / 353 k LOC),
   all 8 coordinators, all
   three transports (XML-RPC, BIN-RPC, JSON-RPC), 25 REST handler
   files (~80 endpoints in `assets/openapi.yaml`), 85 WebSocket
   commands, 20 ReGa scripts, 139 generated device profiles. The
   primary config UI is a Svelte 5 SPA under `assets/ui/` (embedded
   via `go:embed all:spa_dist`); a minimal HTMX bootstrap surface
   (login, first-run setup, OIDC callback, server-rendered /health
   and /about) covers what the SPA cannot reach — pre-auth flows
   and SPA-down diagnosis. Open work focuses on depth parity against
   aiohomematic, not the skeleton build-out — architectural
   divergences are documented as by-design items in
   `docs/parity/by_design.md`. Ongoing correctness is held by standing
   build- and test-time parity guards rather than regenerated audit
   reports; for the Matter side the binding contract is
   [`docs/matter-parity-contract.md`](./docs/matter-parity-contract.md).

---

## Project Overview

**OpenCCU-Loom** is a standalone Go daemon that talks to Homematic CCUs
and bridges their devices to MQTT, a REST + WebSocket API, a web
Config UI, and a Matter bridge. It is a Go port of
[`aiohomematic`](https://github.com/SukramJ/aiohomematic) that adds
the standalone-daemon surface on top; the two projects coexist —
`aiohomematic` powers the Home Assistant integration, OpenCCU-Loom
serves users who want MQTT / REST / UI / Matter access without HA.

### Key Characteristics

- **Language**: Go 1.26+
- **License**: MIT (source); binary aggregates openccu-data extracts
  under the eQ-3 HomeMatic Software License (non-commercial). See
  ADR 0003.
- **Module path**: `github.com/SukramJ/openccu-loom`
- **Deployment**: single static binary (`CGO_ENABLED=0`) + Docker
  (linux/amd64, arm64, armv7). No CGo dependencies.
- **Persistence**: SQLite (`modernc.org/sqlite`, pure Go) + filesystem.
- **Architecture**: hexagonal / ports & adapters, plus an internal
  typed generic event bus for cross-domain communication.
- **Multi-CCU**: first-class feature from 0.1.0 — one daemon, many CCUs.
- **Matter**: ships in 0.1.0.

### Primary bridges (0.1.0)

- MQTT — Home Assistant Discovery **and** raw topic planes in parallel.
- REST + WebSocket API.
- Config UI — Svelte 5 SPA (Tailwind 4 + Vite, embedded via `go:embed`)
  as primary surface. A tiny HTMX bootstrap surface (login, first-run
  setup, OIDC callback, server-rendered /health, /about) covers
  pre-auth flows and the SPA-down diagnosis case.
- Matter — native-Go bridge, default off; operator opt-in via
  `cfg.North.Matter.Enabled`. See SPECIFICATION.md §6 and ADR 0012.

### South-bound protocols (0.1.0)

| Transport | Interfaces | Callback |
|---|---|---|
| XML-RPC + JSON-RPC | HmIP-RF, BidCos-RF, BidCos-Wired, HmIP-Wired, VirtualDevices | HTTP (`:8120`) |
| BIN-RPC | CUxD | raw TCP (`:8129`) |

Every interface in 0.1.0 supports push callbacks. **There is no
polling / JSON-RPC-only code path in the MVP** — this is a deliberate
divergence from aiohomematic.

---

## Critical Rules

These rules are non-negotiable. Violating them breaks design intent.

### License headers (MIT)

Every Go source file must start with:

```go
// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.
```

No stray GPL / Apache / BSD headers in `pkg/` or `internal/`. Any
vendored or forked code keeps its upstream notice and adds a short
modification line.

### No CGo in the default build

```
// go:build !cgo
```

`CGO_ENABLED=0` at all times. If you ever feel the need for CGo
(crypto, SQLite acceleration, Matter SDK), raise it in an ADR — do not
add it silently.

### Dependency licensing

The source is MIT. MIT / Apache-2.0 / BSD dependencies are fine.
**GPL / LGPL / MPL / AGPL pull in copyleft obligations — stop and
discuss before adding one.** The embedded openccu-data archives are
non-commercial eQ-3 content and are handled by a separate aggregation
path (see ADR 0003); they do not constrain the dependency tree.

### CUxD uses BIN-RPC, not JSON-RPC

Unlike aiohomematic, we speak BIN-RPC directly to CUxD and run our
own BIN-RPC callback server. Never treat CUxD as "JSON-RPC only".
Contract tests enforce this.

### `CommandPriority.Critical = 0`

Mirrors aiohomematic. Never use `if priority != 0` as "set".

```go
// ✅ Correct
if priority == hmenum.CommandPriorityCritical { ... }

// ❌ Wrong (zero-value bug waiting to happen)
if priority != 0 { ... }
```

### Multi-CCU from day one

Every coordinator, every adapter, every store must be multi-CCU-safe.
Do not hard-code "the single CentralUnit". `CentralRegistry` holds them
all; `central_name` is the scoping dimension. See ADR 0002.

### Pure-Go SQLite

Use `modernc.org/sqlite` (no CGo). Do not switch to `mattn/go-sqlite3`
without an ADR documenting the reason.

### Interfaces in the consumer package, except for cross-cutting protocols

Standard Go convention. The one exception: protocol interfaces used
across `central`, `client`, and north-bound adapters live in
`pkg/interfaces` because multiple packages need to declare dependencies
on the same type.

### Callback ports must be re-advertised on every reconnect

If `rpc_callback.port` is `0` (dynamic), the OS assigns an ephemeral
port at bind time. Every `init()` call to the CCU must carry the
**effective** port, not the configured value. A reconnect after restart
may change the port; the CCU learns the new one at reconnect time.

### matter.js HEAD is the Matter gold standard

Anything under `internal/north/matter/` (and the corresponding wire
paths in `bridge/`, `endpoint/`, `im/`, `tlv/`, `secure/`) is a
semantic 1:1 port of [matter.js](https://github.com/project-chip/matter.js)
HEAD (Apache-2.0). Cluster IDs / revisions / attribute IDs /
constraints / defaults / wire shape are taken verbatim. Hand-coding
any of these from the Matter PDF is forbidden — drift creeps in
within days. Every Matter-side fix or feature reads matter.js
first, cites the matter.js path + function in the Go comment, and
adds a parity test. Deliberate divergences land in
`docs/parity/by_design.md`. See the
[matter.js as the Matter Gold Standard](#matterjs-as-the-matter-gold-standard)
section for the full workflow.

### Live-CCU writes need explicit user approval — including device selection

The developer's CCU at `172.18.4.29` runs real, in-use devices.
**Reads are free** (every parameter / paramset / event read, plus all
OpenCCU-Loom REST `GET` and chip-tool reads / subscribes / event-reads).
**Writes need explicit user approval AND the user must name the
specific target device.** Self-chosen test devices are unsafe — what
looks like "just another HMIP-PSM" can be a `Weinkühlschrank` that
shouldn't be cycled on/off six times per chip-tool run.

The brief (`docs/matter/chip-tool-test-brief.md` §T6) directs a real
on/off/toggle cycle through the bridge → CCU → device chain — that
authorization covers the test type, NOT the device. The device choice
is a separate decision the user owns.

**How to apply:**

- Before any chip-tool sweep or other live-CCU integration test that
  involves writes (`onoff on/off/toggle`, REST `PUT
  .../STATE/value`, paramset writes, anything that triggers
  `Switch.Set` / xml-rpc `setValue` on a real device), confirm the
  target address + channel with the user.
- The current sanctioned write-target slot is
  `00021BE9957782:4 Bücherregal` (HMIP-PS, bookshelf lamp). Use it as
  default; propose an alternative + reasoning if it doesn't fit.
- After the sweep, leave the switch in a deterministic OFF state via
  one final explicit `chip-tool onoff off` before unpair — don't
  trust toggle parity to land you OFF.
- For hermetic test paths that don't require real wire-CCU
  validation, use `godevccu` (in-process simulator,
  `tests/integration/` with `-tags=integration`) — that's a parallel
  path, not a substitute for the chip-tool brief's
  Apple-independence T6.

---

## Repository Structure

```
openccu-loom/
├── SPECIFICATION.md         — source of truth for 0.1.0 design
├── README.md
├── LICENSE                  — MIT
├── CLAUDE.md                — this file
├── CHANGELOG.md             — release history
├── CONTRIBUTING.md
├── Makefile
├── go.mod / go.sum
├── .goreleaser.yaml
├── Dockerfile
├── .golangci.yaml
├── config.example.yaml      — annotated reference config
├── .github/workflows/       — CI/CD
├── cmd/
│   ├── openccu-loom/         — main daemon (daemon.go, ws_adapters.go,
│   │                          audit_wiring.go)
│   └── hmcli/               — admin CLI
├── pkg/                     — public library surface (thin)
│   ├── hmtypes/             — primitive types, DataPointKey
│   ├── hmenum/              — all enums (Interface, Parameter, ...)
│   ├── hmerr/               — error types + sentinels
│   ├── hmevent/             — domain event types
│   ├── hmlog/               — contextual slog helpers + request filters
│   ├── hmapi/               — REST/WS DTOs shared with external clients
│   ├── interfaces/          — DI contracts (Protocol interfaces)
│   └── hmproto/             — Homematic wire shapes + normalization
├── internal/                — daemon-internal, non-reusable
│   ├── audit/               — change-log append, persistence
│   ├── auth/                — Basic / Session / OIDC / API Token
│   ├── build/               — version metadata
│   ├── ccudata/             — embedded openccu-data extracts
│   │                          (translations, easymodes, profiles)
│   ├── central/             — CentralUnit, coordinators, registries,
│   │                          callback servers (XML-RPC + BIN-RPC)
│   ├── client/              — InterfaceClient, backends, transports,
│   │                          ReGa runner, reliability (CB, retry, throttle)
│   ├── clock/               — wall-clock abstraction for test seams
│   ├── config/              — config loading (koanf wiring)
│   ├── configui/            — form schema, grouping, labels, sessions
│   │                          (port of aiohomematic-config)
│   ├── health/              — unified health tracker
│   ├── i18n/                — translation catalogues (de, en)
│   ├── metrics/             — Prometheus collectors
│   ├── model/               — domain model (devices, data points,
│   │                          custom profiles, calculated, combined,
│   │                          optimistic, schedule, week_profile, hub)
│   ├── north/               — REST, WS, UI (Svelte SPA + HTMX bootstrap), MQTT adapters
│   ├── observability/       — instrumentation + tracing helpers
│   ├── parameter/           — validation, coercion, diff
│   ├── payload/             — north-bound payload assembly + topology
│   ├── reqctx/              — request-scoped context (locale, user, …)
│   ├── scheduler/           — periodic jobs
│   └── store/               — SQLite persistence (migrations, sessions,
│                              paramsets, devices, incidents, audit)
│                              + in-memory caches (visibility, patches,
│                              master/link profile, devicedetails)
├── assets/
│   ├── ui/                  — Svelte 5 SPA source (Tailwind 4, Vite)
│   ├── openapi.yaml         — REST spec
│   └── wsapi.json           — WebSocket command schema
├── docs/
│   ├── adr/                 — architecture decisions
│   ├── audit/
│   ├── contributor/
│   ├── parity/              — model_snapshot_schema, by_design, …
│   ├── caching.md           — every cache layer + boot-time radio cost
│   ├── mqtt-topic-schema.md — MQTT topic reference
│   ├── roadmap.md
│   ├── testplan.md
│   ├── user-guide.md
│   └── SECURITY.md
├── script/
│   ├── generate_profiles.py — auto-generates profile registry from aiohomematic
│   ├── aiohomematic_snapshot.py
│   ├── homematicip_local_snapshot.py
│   ├── model_snapshot_diff.py
│   ├── datasource_diff.py
│   └── ...
└── tests/
    ├── contract/            — protocol / capability invariants
    ├── golden/              — session replay
    ├── integration/         — godevccu-based (in-process)
    └── bench/
```

The tree reflects the substantially implemented 0.1.0-rc state; the
`internal/north/ui/spa_dist/` directory is populated by `vite build`
out of `assets/ui/` and embedded into the binary at compile time.

---

## Development Environment

### Prerequisites

- Go 1.26 or newer
- Python 3.14+ (only for the profile generator script; integration
  tests run a pure-Go simulator and need no Python toolchain)
- `golangci-lint` v1.60+
- `gofumpt`
- `goreleaser`
- Docker + buildx (for multi-arch images)
- `goose` (SQLite migrations; `go install github.com/pressly/goose/v3/cmd/goose@latest`)

### Everyday commands

Once Phase 0 has produced the `Makefile`:

```sh
make build           # build ./bin/openccu-loom
make test            # unit + contract
make integration     # tests against godevccu (in-process; Mosquitto needs Docker)
make contract        # contract tests only
make bench           # run benchmarks
make lint            # golangci-lint
make fmt             # gofumpt + goimports
make generate        # go generate ./... + profile generator
make docker          # multi-arch Docker images
make release         # goreleaser snapshot (for testing)
```

No `prek` / `pre-commit` in this project — we use `golangci-lint`
+ `gofumpt` via a simple pre-commit hook installed by `make setup`.

---

## Code Quality & Standards

### Linting

`.golangci.yaml` enables: `errcheck`, `govet`, `staticcheck`, `revive`,
`gocritic`, `gosec`, `bodyclose`, `errorlint`, `exhaustive`, `nilerr`,
`goimports`, `unconvert`, `unparam`, `wastedassign`, `prealloc`.

### Formatting

`gofumpt` (stricter than `gofmt`). `goimports` for import grouping
(stdlib → third-party → internal → pkg).

### Context & cancellation

Every I/O method takes `ctx context.Context` as the first argument.
Never ignore cancellation (`ctx.Done()`). Pass `ctx` down; do not
stash it in struct fields except in a few well-defined places
(scheduler workers).

### Errors

- Sentinel errors: `var ErrAuthFailure = errors.New("auth failure")` in
  `pkg/hmerr`.
- Wrapping: `fmt.Errorf("init proxy: %w", err)`.
- Type assertions: `errors.Is` / `errors.As`. No type-asserting
  switches across the error graph.
- Each transport error carries `hmerr.Context{Protocol, Method, Host,
  Interface}` for log enrichment.
- No bare `panic` from library code.

### Concurrency

- `sync.Mutex` / `sync.RWMutex` for shared mutable state.
- Channels for pipelines and fan-out cancellation.
- `golang.org/x/sync/errgroup` for bounded parallel fan-out.
- Every goroutine has a documented lifecycle and a way to stop.
- Shared state at package-level scope is forbidden except for
  compile-time constants and `log/slog` default handler.

### Generics

Fine and expected. Example: `events.Subscribe[T Event](bus, handler)`.

### No `interface{}` / `any` without justification

A comment must justify any use of `any` (usually: "wire decoded
JSON before type-dispatch").

### Naming

- Package names: short, lowercase, no underscores.
- Exported identifiers: start with a capital letter; document them.
- Interfaces that carry a single method: `MethodNamer` pattern (e.g.
  `Dialer`, `Stringer`).
- Protocol interfaces (DI contracts) in `pkg/interfaces`: no `I` prefix,
  no `Iface` suffix — just the noun.

### Comments in code

Comments must offer **durable value to a future reader** — explain the
*why* of the code, not the audit-row or wave that requested the change.
Internal tracking codes are illegible to anyone who joins the project
later, and the documents they point at decay fast. `make test` blocks
on `TestDocPurity` (under `tests/contract/`) which enforces these
rules mechanically.

**Forbidden in `//` comments (TestDocPurity):**

- Wave / Welle / phase tags: `Wave-3`, `W6-A`, `Welle 4`, `Phase-3`,
  `Phase 4`, `migration step N`.
- Audit item IDs: `A3-L05`, `L7.4`, `M1234`, `G-24`, `V8-N29`, `Q-23`,
  `QW-23`.
- Drift IDs in every observed shape:
  - `Drift L0-D01`, `drift L1-NEW-2` (with the literal `Drift` prefix)
  - `L9-D8`, `L2-D06`, `L10-D02` (bare layer-drift IDs)
  - `L9-NEW-5`, `L5-NEW-D03` (NEW-suffix forms)
  - `L3-D6-FUTURE`, `L0x-D_FUTURE_OBSERVER` (skip-placeholder suffixes)
  - `L0-OC-01` (sub-system-specific IDs)
- Audit-run references: `audit run #02`, `parity audit`,
  `parity sweep`, `parity_audit.md`, `parity_request.md`.
- Audit date stamps: `\b2026-0[456]-\d{2}\b` and any peer pattern.
- German/English audit hybrids: `MANDATORY-FEHLT`.
- Legacy-project provenance tokens in code comments: `aiohomematic`,
  `homematicip_local`, `pydevccu`, `openccu-data`, etc. — these belong
  in the markdown documentation, not in production code.
- Short German function-words: `darf`, `soll`, `muss`, `nicht`, `über`,
  `dürfen`, `müssen`, `während`, `damit`, `dafür`, `daher`, `liefert`,
  `enthält`, `erlaubt`, `ergänzt`, `bzw.`, `z.B.` — code comments stay
  in English.

**Markdown references must point at durable documents.**
`TestDocPurity_MarkdownRefsExist` walks every `.md` reference in a
`//` comment and fails when the cited file is missing on disk. Cite:

- ✅ Permanent docs: `CLAUDE.md`, `SPECIFICATION.md`,
  `docs/adr/*.md` (ADRs are immutable once landed),
  `docs/parity/by_design.md`, `docs/matter-conformance.md`,
  `docs/matter-ui-concept.md`, and the matter.js / chip source-file
  references (`packages/.../X.ts:line`, `src/.../Y.cpp:line`).

Do NOT cite transient audit-trail files in code comments: audit-run
reports, hand-off memories, todo files, ad-hoc parity sweeps. The
audit-trail lives in Git history + `docs/parity/by_design.md` (the
living catalogue of intentional divergences); code comments should
reference neither.

**Rewrite pattern** when removing audit-tracking from an existing
comment — preserve the rationale, drop the tracking tag:

```go
// Before:
// Drift L8-D01 (parity audit 2026-05-12): FeatureMap (0xFFFC) and
// ClusterRevision (0xFFFD) must be enumerated so the initial
// Subscribe pre-populates Apple's HAP-mapper cache.

// After:
// FeatureMap (0xFFFC) and ClusterRevision (0xFFFD) must be enumerated
// so the initial Subscribe pre-populates Apple's HAP-mapper cache.
// Mirrors chip AdministratorCommissioningCluster.cpp:53-56.
```

What *stays* in the comment: the invariant, the matter.js / chip
provenance with `path:line`, the spec section (`Matter §11.18.6.4`),
the observable symptom when broken. What *goes*: the audit row, the
date, the wave number, the FUTURE-skip placeholder ID.

**Exceptions:**

- `ha_`-prefixed files (legacy HA-compat zone) — out of scope.
- `tests/integration/testdata/` — golden wire data, untouched.
- `tests/contract/doc_purity_test.go` itself — it enumerates the
  forbidden patterns in its own doc-comment.

If you need to discuss audit provenance, write it into the commit
message body or a Markdown doc — both survive code churn far better
than a comment that names a row in a deleted spreadsheet.

### Documents in markdown

Markdown docs (`*.md` under the repo) are deliberately held to a
**looser** standard than production code comments — they are the
home of audit metadata, drift catalogues, and timestamped tracking.
The one rule that *does* transfer cleanly is **link integrity**.

**`TestMarkdownLinksValid`** (in `tests/contract/`) walks every
`.md` file and fails when a Markdown-style link (square brackets
followed by a parenthesised target) points at a file that does
not exist on disk. Anchor fragments (`#section`) are tolerated
against the file but anchor existence is NOT verified (would
require a Markdown parser).

What is checked:
- Relative-link targets (e.g. `./sibling.md`, `../parent.md`)
  resolved against the linking file's directory.
- Absolute targets (leading `/`) resolved against the repo root.
- Directory targets (trailing `/`) count as satisfied if the
  directory exists.

What is NOT checked:
- Bare path tokens in prose (`see by_design.md`) — would
  false-positive on every mention.
- External URLs, `mailto:`, `tel:`, `ftp:` — ignored.
- Reference-style Markdown links (`[text][ref]` + `[ref]: url`) — rare in
  this repo; ignored.

Exclusions:
- `node_modules/`, `spa_dist/`, `.git/` — vendored or out of scope.

What is *not* a markdown-purity rule (and why):
- **Drift-IDs, audit dates, "parity sweep" mentions** — `by_design.md`
  is the audit-trail itself; banning these tokens would break the
  document it exists to populate.
- **Legacy-project names (`aiohomematic`, `pydevccu`, …)** —
  `CLAUDE.md`, `SPECIFICATION.md`, ADRs need to name these projects.
- **German words** — beispielhafte deutschsprachige Zitate sind in
  Doku ok, even though they would trip `TestDocPurity` in code.
- **Audit date stamps** — "Last update: 2026-05-12" headers are
  normal markdown metadata.

The asymmetry is deliberate: code is the durable artefact, markdown
is the conversation about that artefact.

---

## Architecture Quick Reference

### Layers

```
Outside world
   ↓
Northbound adapters     (north/mqtt, north/rest, north/ui)
   ↓
Domain core             (central + model + client + store + health)
   ↓
Southbound adapter      (client/transport/{xmlrpc,binrpc,jsonrpc})
   ↓
CCU
```

### Key packages

- `internal/central`: `CentralUnit`, coordinators, callback servers,
  scheduler, registry. Multi-CCU: one `CentralUnit` per configured CCU;
  all held by a `CentralRegistry` shared with north-bound adapters.
- `internal/client`: `InterfaceClient`, circuit breaker, retry,
  throttle, coalescer, ping/pong. One `InterfaceClient` per
  `(central, interface)` pair.
- `internal/client/backends`: `CcuBackend` (XML-RPC + JSON-RPC, with
  `JsonCcuBackend` for CCU-Jack JSON-only mode), `CuxdBackend`
  (BIN-RPC), `HomegearBackend` (XML-RPC; depth-parity with CCU is a
  post-0.1.0 milestone — see `SPECIFICATION.md` §2.2 Non-Goals).
- `internal/model/custom`: device profile registry + custom data point
  types. Profiles are generated from aiohomematic via the Python
  helper; hand-written Go wrappers per device type.
- `internal/store/sqlite`: persistent stores. Schema in
  `internal/store/sqlite/migrations/` via goose.
- `internal/central/events`: the internal `EventBus` — generic,
  typed, priority-aware, no re-entrancy.

### Callback servers

Two listeners, one each protocol, both shared across all centrals:

- XML-RPC over HTTP on `rpc_callback.port` (default `:8120`). Routes
  by URL path `/RPC2/<central_name>`.
- BIN-RPC over raw TCP on `rpc_callback.bin_port` (default `:8129`).
  Routes by `interface_id` inside the envelope.

Both listeners accept fixed / dynamic (`0`, OS-assigned) / range
(`"<lo>-<hi>"`) port modes. The *effective* port is re-advertised to
the CCU in every `init()` call and every reconnect.

### Event bus usage

```go
unsubscribe := events.Subscribe(bus, func(e hmevent.DataPointValueChanged) {
    // handle
}, events.WithPriority(events.PriorityHigh))
defer unsubscribe()

events.Publish(bus, hmevent.DataPointValueChanged{ /* ... */ })
```

---

## Testing Guidelines

### Test file & test naming (do not create tracking-named tests)

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
catch-all label. One generated file is exempt: the profile-parity table is
emitted by `script/generate_profiles.py` to a fixed path and must keep its
name.

Three mandatory test pillars:

### Contract tests (`tests/contract/`)

Protocol / capability invariants. Every test states a hard rule and
blows up if violated. The catalogue lives in `tests/contract/` —
representative: `TestAllMVPInterfacesHavePingPong`,
`TestCuxdUsesBINRPCBackend`, `TestDeviceProfileRegistryParity`,
`TestRecordedReliabilityDefaults` (cross-stack drift detector).

**If you touch a protocol / capability boundary, you must add or
update a contract test.**

### Golden-file / session replay (`tests/golden/`)

Recorded CCU sessions played back against the daemon. Assertions
compare emitted events or output JSON against golden files. Run with
`-update` to refresh.

### Integration tests (`tests/integration/`)

Run the daemon against an in-process `godevccu` simulator (a pure-Go
port of pydevccu — no Python toolchain required) and assert
end-to-end behavior. Slow; gated behind `-tags=integration`.

### Unit tests

Regular Go tests per package. Target ≥ 80 % coverage in core packages
(`client`, `central`, `model/custom`, `store`). Lower is OK for
adapter shims.

### Benchmarks

Live in `tests/bench/`. Run weekly in CI. Regressions > 20 % block release.

### Fuzz tests

XML-RPC parser, BIN-RPC codec, JSON-RPC parser, paramset normalization.
Short runs on every PR; longer nightly.

### Cross-stack model-snapshot verification

End-to-end parity check: OpenCCU-Loom's domain model (Devices →
Channels → DataPoints) is compared against aiohomematic's model
when both stacks load the same wire data. The snapshot infrastructure
is the canonical test that the OpenCCU-Loom→aiohomematic port produces
equivalent output. **Mandatory before any release** — the four-script
cross-stack snapshot pipeline below is the release gate.

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

Common-schema definition: `docs/parity/model_snapshot_schema.md`.

The two snapshot JSON files (`tests/integration/testdata/model_snapshot_*.json`,
total ~70 MB) are gitignored — they are produced on demand and live
only locally. Set `OPENCCU_LOOM_SNAPSHOT_DEVICES=A,B,C` to scope both
sides to a smoke subset for fast iteration; default loads the entire
embedded fleet.

When you change model code (DataPoint creation, visibility marks,
custom-DP composition, channel methods), rerun (2) and (4) and verify
the drift score has not regressed in your area. The current baseline
sits at ~270 architecturally-accepted drifts; growth beyond that
without a corresponding entry in `docs/parity/by_design.md` is a regression.

---

## Common Tasks

### Regenerate Matter schema from matter.js HEAD

When matter.js HEAD ships cluster-revision or device-type-revision bumps,
update the codegen pipeline in one shot:

```sh
make generate-matter-schema
```

This runs three steps:
1. `python3 script/extract_matterjs_head.py` — re-extracts
   `docs/parity/matter/matter-schema-snapshot.json` from
   `../matter.js/packages/model/src/standard/elements/*.element.ts`.
2. `go run ./script/generate_matter_schema.go` — reads the snapshot and
   regenerates `internal/north/matter/schema/clusters.go` and
   `internal/north/matter/schema/devicetypes.go`.
3. `gofumpt -w internal/north/matter/schema/` — formats the output.

After regeneration, run `go test ./internal/north/matter/schema/...` — the
`TestParityCodeMatchesGeneratedSchema` test will flag any cluster where the
hand-coded revision constant in the cluster source files has drifted from the
new schema. Update those constants to match, then re-run `make test`.

Callers that need a device-type revision at runtime should use
`schema.DeviceTypeRevision(id)` (from `internal/north/matter/schema`) rather
than hard-coding a switch — that way the next `make generate-matter-schema`
automatically propagates the update without requiring a second manual edit.

### Regenerate device profiles from aiohomematic

```sh
./script/generate_profiles.py --aiohomematic-version=2026.4.19 \
    --out-go=internal/model/custom \
    --out-test=tests/contract/profile_parity_generated_test.go
```

### Add a new device type (new profile in aiohomematic → follow here)

1. Bump the aiohomematic pin in the generator.
2. Regenerate profiles.
3. Review the diff; any new `DeviceProfile` enum values require a
   hand-written Go wrapper type under `internal/model/custom/<cat>/`.
4. Add or update contract tests.

### Add a REST endpoint

1. Update `assets/openapi.yaml` first (spec-driven).
2. Implement the handler in `internal/north/rest/handlers/`.
3. Route it in `internal/north/rest/router.go`.
4. Add request/response DTOs in `internal/north/rest/dto/`.
5. Unit tests + integration test.
6. Regenerate OpenAPI client if we publish one.

### Add a translation key

1. Add the key + English value to
   `internal/i18n/catalogs/en.json`.
2. Add the German value to `internal/i18n/catalogs/de.json`.
3. Reference from templates via `{{ t "key" }}` or from Go code via
   `i18n.T(locale, "key")`.

### Add a new database table

1. Create the migration file under
   `internal/store/sqlite/migrations/` (goose naming convention
   `<nnnn>_<name>.sql` with `-- +goose Up` / `-- +goose Down`).
2. Add the access struct under `internal/store/sqlite/`.

---

## Git Workflow

- Main branch: `main` (protected).
- Feature branches: `feature/<desc>`, fixes: `fix/<desc>`, AI sessions:
  `claude/<desc>`.
- Conventional-commit style: `<type>(<scope>): <subject>`. Scopes
  align with the package boundaries (`client`, `central`, `model`,
  `north`, `store`, `rest`, `mqtt`, `ui`, `docs`, `ci`, ...).
- Every PR must pass `make test`, `make contract`, `make lint`.
- Integration tests (`make integration`) must pass on main at least
  every 24 h; feature branches trigger them on `needs-integration`
  label.
- Commits are signed off (`git commit -s`) — DCO applies so the
  authorship chain stays traceable.
- Releases tagged `vX.Y.Z`. Pre-releases `vX.Y.Z-rc.N`.
- Never push `--force` to `main`.

---

## aiohomematic as a Reference

> **Shorthand convention:** when the user says "aiohomematic", they
> mean the whole Python reference family, not just the single repo.
> Expand the term to include **all** of the sibling projects that
> live on the developer's machine and treat them as a single pool of
> prior art when cross-referencing:
>
> | Repo                                                            | Local path                                                     | Purpose                                                            |
> | --------------------------------------------------------------- | -------------------------------------------------------------- | ------------------------------------------------------------------ |
> | [`aiohomematic`](https://github.com/SukramJ/aiohomematic)       | `../aiohomematic/`                 | Core async HomeMatic library — transports, devices, paramsets.    |
> | [`aiohomematic-config`](https://github.com/SukramJ/aiohomematic-config) | `../aiohomematic-config/`  | Configuration-panel logic: form schemas, grouping, labels, profiles. |
> | [`homematicip_local`](https://github.com/SukramJ/homematicip_local)     | `../homematicip_local/`    | Home-Assistant integration — config flow, WebSocket API, tests.   |
> | [`homematicip-local-frontend`](https://github.com/SukramJ/homematicip-local-frontend) | `../homematicip-local-frontend/` | Lit web-components for cards + HA config panel; the SPA reference for UI patterns. |
> | [`openccu-data`](https://github.com/SukramJ/openccu-data)       | `../openccu-data/`                 | Single source of truth for OCCU metadata extracts (translations, easymodes, profiles). |
>
> When any of these projects contains the answer to a semantic
> question ("how does aiohomematic do X?"), quote the specific file
> + function so the provenance stays traceable.

They are the **reference implementations** — not dependencies.

When in doubt about the semantics of a coordinator, a backend, a
parameter, a device profile, or a UI pattern, look there first.
Examples:

```
# Schedule cache policy
Read ../aiohomematic/aiohomematic/model/week_profile.py

# Paramset patches
Read ../aiohomematic/aiohomematic/store/patches/

# Form schema + parameter grouping for MASTER paramsets
Read ../aiohomematic-config/aiohomematic_config/form_schema.py
Read ../aiohomematic-config/aiohomematic_config/grouping.py

# Channel-config UX: session handling, undo/redo, dirty tracking
Read ../homematicip-local-frontend/packages/config-panel/src/views/channel-config.ts

# WebSocket API shapes, session lifecycle
Read ../homematicip_local/custom_components/homematicip_local/websocket_api.py

# OCCU metadata archives + extractor scripts
ls ../openccu-data/openccu_data/data/
Read ../openccu-data/openccu_data/translations/extractor.py
```

Things that are directly ported (1:1):
- Enumerations and their string values.
- Interface classification sets (with the divergences documented
  in `SPECIFICATION.md` §5.1).
- Paramset normalization rules.
- Paramset patches.
- Device profile registration shape.
- Visibility filters, grouping rules, label-resolution chains from
  `aiohomematic-config`.
- UI interaction patterns (session-based MASTER editing, undo/redo,
  dirty tracking, preset selection) from `homematicip-local-frontend`.
- Contract test invariants that make sense cross-language.

Things that are **not** ported (different world):
- Python / asyncio idioms → Go idioms.
- Pydantic validation → Go `validator`-style checks on deserialization
  boundaries.
- HA-specific shims.
- MQTT workaround for CUxD (we have BIN-RPC).
- Lit web components → Svelte 5 runes.

---

## matter.js as the Matter Gold Standard

> **Hard rule for everything under `internal/north/matter/`,
> `internal/north/matter/cluster/`, `internal/north/matter/bridge/`,
> `internal/north/matter/endpoint/`, `internal/north/matter/im/`,
> `internal/north/matter/tlv/`, `internal/north/matter/secure/` and
> any other Matter-side code:** the gold standard is
> [`matter.js`](https://github.com/project-chip/matter.js) HEAD.
> Apache-2.0 — MIT-compatible — and the certified, production-tested
> reference Matter stack. The platform-specific exceptions Apple
> Home / Google Home / Alexa apply have already been encoded into
> matter.js's behavior + protocol layers through real interop
> testing; we do not re-derive them, we mirror them.

| Repo | Local path | Role |
| --- | --- | --- |
| matter.js | `../matter.js/` | Matter Core implementation: schema (`packages/model`), wire codec (`packages/types`), behavior layer (`packages/node/src/behaviors`), device types (`packages/node/src/devices`), protocol engine (`packages/protocol`). The single Matter-side gold standard. |

[`home-assistant-matter-bridge`](https://github.com/Nabu-Casa/home-assistant-matter-bridge)
(Apache-2.0, local at `../home-assistant-matter-bridge/`) is one
specific consumer of matter.js. Useful as an occasional helper
reference for "how does a real bridge wire its Aggregator + bridged
devices end-to-end?", but **not** a gold standard — it carries
Home-Assistant-specific shims (Entity-Domain → Cluster mapping, HA
Device Registry as data source) that do not translate to
OpenCCU-Loom. When in doubt, pull the pattern from matter.js itself,
not from ha-bridge.

**Goal:** OpenCCU-Loom's Matter side is a 100 % port of matter.js —
**semantically**, not syntactically. TypeScript idioms
(decorators, `Behavior.with(...)` mixins, `Promise<T>`) translate to
Go idioms (struct-with-methods, `context.Context`, goroutines). The
same defaults, the same constraints, the same wire shape, the same
order of attributes / commands / events. Where the Go translation
forces a different surface, the Go code calls out the matter.js
function it mirrors in a comment + the contract it enforces.

### Workflow

1. **Before writing any Matter-side fix or feature, read the
   corresponding matter.js source.** Likely paths:
   - schema constant / cluster revision / attribute id →
     `../matter.js/packages/model/src/standard/elements/<name>.element.ts`
   - cluster behavior (defaults, mandatory attributes, conformance
     checks) → `../matter.js/packages/node/src/behaviors/<name>/`
   - device type (DeviceTypeList revision, mandatory cluster set) →
     `../matter.js/packages/node/src/devices/<name>.ts`
   - bridge composition pattern → `../matter.js/packages/node/src/devices/aggregator.ts`
     and `../matter.js/packages/node/src/devices/bridged-device.ts`
     (ha-bridge's `packages/backend/src/matter/` is a useful
     supplementary read but is not the gold standard)
   - wire codec, IM messages, sigma → `../matter.js/packages/types/src/tlv/`
     and `../matter.js/packages/protocol/src/`
2. **Cite the matter.js path + function in the Go code**
   (`// Mirrors matter.js packages/node/src/behaviors/.../FooBehavior.ts:bar`)
   so the provenance survives drift. PR descriptions quote it too.
3. **Every Matter-side change updates the parity tests** under
   `internal/north/matter/.../parity_matterjs_test.go`. The
   schema snapshot at
   `docs/parity/matter/matter-schema-snapshot.json` is the
   matter.js HEAD pin (regen via
   `docs/parity/matter/extract-from-matter-js.ts`); the wire-byte
   fixtures at `docs/parity/matter/tlv-wire-fixtures.json` lock the
   TlvCodec wire shape. New cluster-server tests add a parity case;
   PRs without parity coverage are rejected.
4. **Deliberate divergences are documented in
   `docs/parity/by_design.md` (matter.js section)** — and the same
   divergence on a non-trivial scale gets an ADR. Examples of valid
   divergences: a TypeScript-only optimisation that would fight Go's
   GC, a Decorator pattern that has no Go equivalent. Examples of
   invalid divergences: hand-coding cluster revisions, attribute IDs,
   constraint defaults, Apple-Home-required tag patterns — those go
   verbatim from matter.js.
5. **Behavioral-parity contract + standing guards.** Ongoing Matter
   parity is held by the build- and test-time guards catalogued in
   [`docs/matter-parity-contract.md`](./docs/matter-parity-contract.md)
   — schema parity tests, the behavioural negative-write parity table,
   wire-codec fixtures, wiring-capability pins, and the `by_design.md`
   divergence catalogue — not by periodically regenerated audit reports.
   Every Matter change reads matter.js / chip first, mirrors behaviour
   (not just schema), cites the source, and extends the relevant guard.

### Lockstep with aiohomematic

aiohomematic remains the gold standard for the **CCU side** —
transports, devices, paramsets, custom-DP composition. matter.js is
the gold standard for the **Matter side**. The two reference layers
do not overlap; CCU wire knowledge stays in aiohomematic, Matter wire
knowledge stays in matter.js. When a single bridge feature spans
both (e.g. a HmIP DataPoint surface that has to map onto a Matter
cluster) the boundary is the `internal/model/custom/<dp>/matter.go`
file — left side mirrors aiohomematic, right side mirrors matter.js.

---

## Implementation Policy

The project is at 0.1.0-rc; release history is in `CHANGELOG.md`.

### Completion checklist (per change)

- [ ] Fully typed, passes `golangci-lint`.
- [ ] Tests updated (unit + contract where applicable).
- [ ] `CHANGELOG.md` entry for user-visible changes.
- [ ] `SPECIFICATION.md` updated if the change touches a goal,
      non-goal, hard constraint, or resolved decision; ADR written
      for any architectural shift that future readers will need to
      understand.
- [ ] No CGo added.
- [ ] No GPL/AGPL/LGPL/MPL-licensed code inadvertently introduced.
- [ ] License header present on every new `.go` file.

---

## Interaction Protocol

Non-negotiable rules for how the assistant works with the user:

1. **Describe approach before coding** — explain files, approach and
   trade-offs; wait for approval. Trivial edits (typo fixes,
   single-line fixes) may proceed directly.
2. **Clarify ambiguous requirements** — ask before guessing.
3. **Suggest edge cases and tests after implementation** — list what
   the change covers and what is worth testing next.
4. **Bug fixing is test-first** — write a failing reproducer, then
   fix, then verify nothing else broke.
5. **Learn from corrections** — identify the root cause and update
   memory when a pattern recurs.

---

## Tips for AI Assistants

### Do's

- ✅ Read `SPECIFICATION.md` for design intent (~10 min read).
  Implementation detail lives in code, ADRs, and the artefacts
  listed in the spec preamble.
- ✅ Full type annotations. Go is strict; don't fight it.
- ✅ `context.Context` as first param on every I/O method.
- ✅ Run `make lint && make test` before committing.
- ✅ Add or update a contract test when touching protocols,
  capabilities, or state machines.
- ✅ Update `docs/` when public APIs change; open an ADR for major
  decisions.
- ✅ Use `sync.RWMutex` for read-heavy caches; benchmark before
  assuming `Mutex` is enough.
- ✅ For multi-CCU correctness, name the `central` explicitly in every
  cross-cutting call.
- ✅ For Matter-side code: read the matter.js source for the
  cluster / behavior / device-type **first**; cite the matter.js
  path + function in your Go comment; add the parity-test case.
  The pattern is enforced under `internal/north/matter/` and the
  related `bridge/` / `endpoint/` / `im/` / `tlv/` / `secure/`
  trees; see [matter.js as the Matter Gold
  Standard](#matterjs-as-the-matter-gold-standard).
- ✅ **Delegate test implementation to Sonnet sub-agents.** Production
  code (architecture, public API, wire decisions) belongs in the main
  conversation; the per-test mechanical work — fakes, table cases,
  assertions, race / parallel scaffolding — should be handed to a
  Sonnet sub-agent via the `Agent` tool with `model: "sonnet"`. The
  test code never has to land in the main context, which keeps the
  budget on architectural decisions instead of repetitive test
  boilerplate. Briefing pattern: name the file(s) and the public
  surface, list the cases to cover, point at an existing test as the
  style reference, ask for a < 250-word report. Multiple independent
  test clusters can run in parallel by launching several sub-agents
  with `run_in_background: true` in one message.

### Don'ts

- ❌ No `interface{}` / `any` without a justifying comment.
- ❌ No `panic()` outside `main()` or test helpers.
- ❌ No MIT / Apache / BSD headers.
- ❌ No CGo dependencies.
- ❌ No direct commits to `main`.
- ❌ No assuming a single `CentralUnit` — always multi-CCU safe.
- ❌ No treating CUxD as JSON-RPC only (that is aiohomematic's
  workaround, not ours).
- ❌ No hard-coding callback ports — honor the dynamic-port mode.
- ❌ No backwards-compatibility shims for aiohomematic data / caches;
  this is greenfield.
- ❌ No hand-coding Matter cluster IDs / revisions / attribute IDs /
  constraints / defaults from the spec PDF or memory. Mirror
  matter.js HEAD verbatim. Drift produces silent Apple Home /
  Google Home pair-aborts that take days to attribute back.
- ❌ No audit-tracking codes (`Drift L0-D01`, `Wave 4`, `Phase-3`,
  audit dates `2026-05-12`, `parity audit`, …) in code comments.
  No markdown references to transient audit files (audit-run reports,
  hand-off memories, todo lists). Comments must offer durable value —
  TestDocPurity blocks the build otherwise. See
  §[Code Quality & Standards → Comments in code](#comments-in-code).

### When in doubt

1. **Intent**: read `SPECIFICATION.md`.
2. **Architecture**: check the relevant ADR under `docs/adr/`.
3. **Contract**: read `assets/openapi.yaml` (REST),
   `assets/wsapi.json` (WS), or the relevant contract test under
   `tests/contract/`.
4. **Implementation**: read the code; structure is hexagonal
   (north → domain core → south).
5. **Caching, boot-time data flow, CCU radio cost**: read
   [`docs/caching.md`](./docs/caching.md) — covers every cache
   layer (embedded / in-memory / SQLite / filesystem / CCU-side),
   the four boot scenarios (warm/cold CCU × with/without cache),
   and the steady-state radio paths.
6. **Semantic question on the CCU side** (e.g. "how does
   aiohomematic do X?"): cross-reference the Python sibling repo.
7. **Semantic question on the Matter side** (e.g. "what does
   matter.js advertise for `BasicInformation.ProductAppearance`?",
   "how does matter.js wire its Aggregator?"):
   cross-reference `../matter.js/packages/{node,protocol,types,model}/`.
   home-assistant-matter-bridge under `../home-assistant-matter-bridge/`
   is a supplementary read for end-to-end bridge composition, but
   matter.js itself is the gold standard.
8. Run the contract / parity tests; they lock invariants.
9. Ask the user.

---

*This file points at `SPECIFICATION.md` and `docs/` for anything that
would go stale. When in doubt, prefer the linked document over this
summary.*
