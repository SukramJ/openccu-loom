# ADR 0026 — MCP dev-mode: build-tag-gated introspection surface

- **Status**: deferred
- **Date**: 2026-06-04
- **Deferred**: 2026-06-06
- **Related**:
  [ADR 0025 — MCP north-bound adapter](./0025-mcp-northbound-adapter.md),
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  [ADR 0017 — logging and diagnostics](./0017-logging-and-diagnostics.md),
  `internal/north/rest/handlers/diagnostics*.go`,
  `internal/observability/`,
  `tests/integration/` (godevccu),
  `CLAUDE.md` §Critical Rules (live-CCU writes)

## Status note

**Deferred (2026-06-06) — full adapter not built.** Most of the proposed
tool surface overlaps tooling an agent can already drive: the diagnostics
REST endpoints (`diagnostics_*`, `metrics`), `hmcli`, `go test` /
`tests/integration` (godevccu + golden replay), stdlib
`net/http/pprof`, and direct read-only SQLite. Only `eventbus_tap` and
`reliability_state` exposed internals that had no surface at all.

Building the full tag-gated MCP adapter + a second build target + a CI
lane up front is perpetual maintenance against churning internals for an
audience of one (the developer's agent), so it is deferred. The two
pieces that genuinely lacked any surface — a reliability snapshot and an
event-bus tap — were instead shipped as **production** diagnostics REST
endpoints behind the existing admin auth chain
(`GET /diagnostics/reliability`, `GET /diagnostics/eventbus/tap`),
consistent with the `diagnostics_*` family, rather than as a dev-only
adapter: they are read-only, useful to operators, and need no separate
build target. The full MCP dev-mode adapter — specifically its
write / fault-injection tooling (`godevccu_control`, `golden_replay`,
forced reconnects) — remains deferred; the build-tag isolation design
below is the record for *how* to isolate that if it is ever built.

## Context

ADR 0025 establishes a production MCP adapter: a *dumb* north-bound
adapter that projects the domain over MCP by calling the same service
layer the REST handlers call. It is deliberately minimal,
auditable, and bounded by the existing auth chain — an MCP client can
do exactly what the same token can do over REST, no more.

A separate need surfaced during AI-assisted development: an agent
(Claude Code) helping build and debug this daemon would benefit from
**reading the daemon's internal state live** — EventBus traffic,
circuit-breaker state, in-memory cache contents, in-flight commands —
and from **driving the test harness** (godevccu, golden replay,
forced reconnects, fault injection). None of that is exposed by any
production surface, and rightly so: it is debug machinery, not a
product feature.

The daemon already carries a deep diagnostics layer aimed at *humans*:
`diagnostics_capture`, `diagnostics_rpc_recorder`,
`diagnostics_loglevels`, `diagnostics_logs`, `metrics`, plus
`internal/observability` (tracing / instrumentation), the in-process
godevccu simulator, and golden-session replay. What is missing is a
*machine-readable* projection of that machinery — and of the internals
that have no diagnostic surface at all — that an agent can drive
during development.

The question this ADR settles: **should there be a deeper "dev-mode"
MCP surface, and how is it isolated from production so its power can
never leak into a shipped binary?**

## Decision

**Adopt a dev-mode MCP server as a separate, build-tag-gated adapter
(`internal/north/mcpdev/`), compiled only under `//go:build
dev_mcp`, loopback-only, godevccu-default.** It is *not* a privilege
tier of the ADR-0025 production adapter; it is a distinct surface that
deliberately breaches the hexagonal boundary to reach internals, and
is kept safe by never being compiled into release builds.

The two adapters are siblings, not a hierarchy:

| | Production MCP (ADR 0025) | Dev-mode MCP (this ADR) |
|---|---|---|
| Package | `internal/north/mcp/` | `internal/north/mcpdev/` |
| Compiled into release | yes (behind `cfg.North.MCP.Enabled`) | **never** (`//go:build dev_mcp`) |
| Boundary | dumb adapter → service layer only | reaches internals on purpose |
| Transport | Streamable HTTP (remote agents) | loopback HTTP / stdio only |
| Default write target | real CCU (gated by `AllowWrites`) | godevccu simulator |
| Audience | operators' agents | the developer's agent during dev |

### Why a build tag, not a config flag

A runtime flag (`cfg.North.MCPDev.Enabled`) would mean the
introspection and fault-injection code ships in every release binary,
one mis-set YAML line away from being reachable in production. A build
tag removes the code from the artefact entirely: `make build` (release)
does not compile `mcpdev`; only `make build-dev` (or `go build -tags
dev_mcp`) does. This is the single robust guarantee that
fault-injection / internals-access can never be toggled on in a
shipped daemon. The cost — a second build target — is trivial against
that guarantee.

### Loopback-only, never remote

The dev adapter binds `127.0.0.1` (or stdio) exclusively, with no
configurable remote bind. It is the mirror image of the production
adapter's remote-by-design Streamable-HTTP transport. An agent driving
dev-mode runs on the same host as the daemon under development.

### The hexagonal-boundary breach is intentional and isolated

ADR 0025's production adapter calls only the service layer — it owns no
knowledge of EventBus internals, cache structures, or reliability
state. The dev adapter does the opposite on purpose: it taps the
typed EventBus (`internal/central/events`), reads circuit-breaker /
retry / throttle / coalescer state out of `internal/client`, and dumps
in-memory caches from `internal/store`. This violates the "dumb
adapter" rule by design. The violation is acceptable **only because**
the build tag guarantees it cannot leak into the production data path.
Mixing these tools into the production adapter would destroy that
adapter's auditability; keeping them in a tag-gated sibling preserves
it.

## Tool surface (dev-mode only)

Scoped to what has **no machine-readable production surface today** —
not a re-wrapper of `hmcli`, the diagnostics REST endpoints, or
`make integration`, which already cover their ground for humans.

### Introspection (read)

- **`eventbus_tap`** — subscribe to the typed EventBus, filtered by
  event type and `central_name`, for a bounded window. "Show every
  `DataPointValueChanged` for device X for the next 30 s."
- **`reliability_state`** — per-`InterfaceClient` circuit-breaker
  state, retry / throttle counters, coalescer queue depth, ping/pong
  status. No production read-API exposes this.
- **`cache_dump`** — contents of the in-memory caches (visibility,
  patches, master / link profile, devicedetails) and ad-hoc read-only
  SQLite queries against the persistent stores.
- **`runtime_introspect`** — `pprof` profiles, goroutine dumps,
  `runtime` stats — aimed at the leak / deadlock work the `goleak` +
  `go-deadlock` CI gates already target.

### Harness control (write, godevccu-default)

- **`godevccu_control`** — instantiate / mutate simulator devices,
  inject faults, force a reconnect or callback re-advertise. Default
  and only safe target is the in-process simulator.
- **`golden_replay`** — play a recorded session against the daemon and
  return the emitted events for assertion.

## Binding constraints

### Multi-CCU scoping still applies (ADR 0002)

Even in dev-mode there is no implicit "the single CentralUnit". Every
tool that touches a central takes `central_name` explicitly; tooling
that enumerates state lists it per-central. Dev convenience does not
license the single-central antipattern.

### The live-CCU-write rule is unchanged

`CLAUDE.md`'s live-write rule overrides dev-mode convenience. Tools
that can reach the real CCU at `172.18.4.29` — forced reconnect,
fault injection that triggers a wire write, anything that lands a
`setValue` — default their target to godevccu and require explicit
user approval *plus* a named target device before touching the real
CCU. Reads against the real CCU stay free. The default and intended
target for every harness-control tool is the simulator.

### No production-config coupling

`internal/config/config.go` gains **no** `NorthMCPDev` field — there
is nothing to configure in a release binary. Any dev-mode tuning
(loopback port, default central) lives behind the same `dev_mcp` tag,
read from a dev-only source (env var or a `.dev.yaml` not loaded by
the release config path), never from the shipped config schema.

### Not in the capability handshake

Unlike the production adapter's `mcp.v1` token, dev-mode advertises
**nothing** through `GET /info.capabilities`. It is not a product
capability; a release binary has no awareness it exists. (A release
binary literally cannot — the code is not compiled in.)

### Pure Go, no CGo

Same dependency rules as everywhere: the dev adapter and any
introspection helper are pure-Go, MIT/Apache-2.0/BSD. `pprof` /
`runtime` are stdlib. No CGo creeps in via a debug dependency.

## Consequences

### Positive

- An AI agent gets live, structured visibility into the running daemon
  during development — EventBus, reliability state, caches, harness
  control — none of which any human-facing surface exposes as data.
- Zero production risk: the code is absent from release artefacts, so
  there is no flag to mis-set, no remote bind, no privilege path.
- The production MCP adapter (ADR 0025) stays minimal and auditable;
  the deep, boundary-breaking tooling lives clearly apart.
- Complements the existing `goleak` / `go-deadlock` / diagnostics
  investments by making them agent-drivable.

### Negative

- A second build target (`-tags dev_mcp`) and a CI job that at least
  *compiles* it, so the tag-gated code does not bit-rot.
- The tooling reaches into internals, so it couples to package
  structure that the production adapter is insulated from; internal
  refactors can break dev-mode tools. Accepted: dev tooling tracking
  internals is the point, and its breakage never reaches a user.
- Risk of scope creep into a re-wrapper of `hmcli` / diagnostics REST.
  Mitigated by the explicit "no machine-readable surface today" filter
  on the tool list.

### Migration

- `internal/north/mcpdev/` is created under `//go:build dev_mcp`.
- `Makefile` gains `build-dev` (and the dev adapter is wired into a
  `dev_mcp`-tagged daemon entrypoint, e.g.
  `cmd/openccu-loom/daemon_devmcp.go` under the same tag).
- A CI lane compiles `-tags dev_mcp` (build-only, not shipped) so the
  tag-gated tree keeps compiling against internal refactors.
- No change to `example.config.yaml`, `GET /info`, or any release
  artefact.

## Follow-ups

- Sequence after the ADR-0025 production adapter exists, so the
  service-layer shapes the production tools settle on can inform which
  internals the dev tools genuinely need to reach (and which are
  already covered by a production read-tool).
- Revisit whether `godevccu_control` and `golden_replay` belong in the
  dev-MCP surface or stay in the `tests/integration` harness — if an
  agent only ever drives them from test code, the MCP projection may
  be redundant.
