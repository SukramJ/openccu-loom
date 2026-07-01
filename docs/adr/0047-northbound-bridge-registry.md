# ADR 0047 — North-bound bridges as `Service`s owned by a `Registry`

- **Status**: Accepted
- **Date**: 2026-06-30
- **Related**:
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  [ADR 0025 — MCP north-bound adapter](./0025-mcp-northbound-adapter.md),
  [ADR 0044 — single-port onboarding and HA Ingress auth](./0044-single-port-onboarding-and-ha-ingress-auth.md),
  Plan: [`docs/plans/bridge-registry-migration.md`](../plans/bridge-registry-migration.md)

## Context

OpenCCU-Loom exposes several north-bound surfaces: the REST + WebSocket
API (with the SPA, the MCP mount and the diagnostic pages folded onto the
same listener, ADR 0044), the MQTT bridge, the Matter bridge, and — since
the A4 work — an outbound webhook.

Historically each surface was **hand-wired** in `cmd/openccu-loom/daemon.go`
and its `daemon_*.go` helpers: a bespoke constructor, an inline
`Start(ctx)`, and a `defer Stop()`. Teardown order was implicit in the LIFO
ordering of those `defer`s, scattered across several hundred lines. There
was no shared lifecycle abstraction, no single place that knew "what are the
north-bound surfaces", no ordered/rolled-back startup, and no aggregated
health.

A4 introduced a minimal lifecycle contract — `bridge.Service`
(`Name`/`Start`/`Stop`) plus an optional `HealthReporter` — and a `Registry`
(`internal/north/bridge/`), with the outbound webhook as its first and (so
far) only registered consumer. The other surfaces remained hand-wired. That
left the contract half-adopted: it cannot enforce ordering, health, or
"no surface bypasses the lifecycle" while three of four surfaces ignore it.

The hand-wired model has concrete failure modes we have already hit or risk
hitting:

- A bridge whose `Stop` does not fully unblock its goroutines (the webhook
  shipped with exactly such a worker/`Stop` race, caught only by a test).
  Nothing structurally forces `Stop` to be complete.
- Startup with no rollback: if surface *N* fails to start, surfaces *1..N-1*
  are already running and there is no single owner to tear them back down.
- Teardown order encoded only in `defer` placement — invisible, easy to
  break when code moves.
- No way to ask "is every north-bound surface healthy?" in one place.

## Decision

**Every north-bound surface is modelled as a `bridge.Service` and owned by
the `bridge.Registry`. `cmd/openccu-loom` becomes a composition root: it
builds dependencies, constructs each Service, registers them in boot-
dependency order, and delegates lifecycle to `Registry.StartAll` /
`StopAll`. No north-bound surface is started or stopped by a bespoke
inline call.**

The specifics that make this architecturally sound rather than a cosmetic
wrap:

1. **Services own their lifecycle and live with their package.** Each
   adapter exposes a Service implementation in (or beside) its own package
   — `webhook.Outbound` already does this natively; `mqtt`, `matter` and
   the REST/HTTP surface follow. A Service's constructor takes its
   dependencies and its config slice; `Start` brings up everything it owns
   (listeners, subscriptions, goroutines); `Stop` tears them down
   deterministically and idempotently. There are **no thin shim adapters in
   `cmd/`** that merely forward to loose calls — that would leave ownership
   scattered and defeat the point. `cmd/openccu-loom` only constructs and
   registers.

2. **Registration order is the boot-dependency order, made explicit and
   testable — via *phased* start, not one `StartAll`.** A boot-order scout
   (see the plan §3.1) established that the surfaces do **not** all start at
   one point: the **MQTT EventBridge must start *before* southbound
   hydration** (that is how retained CCU state is published to the broker
   during boot), whereas **Matter** and the **REST HTTP server** must start
   *after* hydration (Matter builds its endpoint topology from hydrated
   devices; REST binds last, after the whole router incl. the MCP mount is
   assembled). A single end-of-boot `StartAll` would therefore silently
   move MQTT's start past hydration and drop the retained-state publish — a
   behaviour change. **Decision: the composition root starts the registry
   in phases** — an *early* phase (before southbound hydration: MQTT) and a
   *late* phase (after: Matter, REST, and the webhook). Each service
   declares its phase; the registry starts phase-by-phase in registration
   order and stops **all** services in one reverse pass at shutdown. So the
   ordering is still explicit and test-pinned (a characterization test fixes
   the phase + within-phase order), but it honours the real, non-uniform
   dependency graph instead of being implicit in `defer` placement. The
   webhook currently starts pre-hydration; moving it to the late phase drops
   boot-hydration events from its stream — treat that as an explicit,
   documented behaviour decision in the webhook-migration PR (it is
   arguably desirable — no initial-state POST flood on restart — but it is a
   change, not a silent move).

3. **The MQTT supervisor is an implementation detail of the MQTT Service.**
   MQTT carries a runtime supervisor (`mqtt_supervisor.go`, `SwapBridge`,
   `OnConnect`) for hot-reload and reconnect-time re-publish. It stays
   *inside* the MQTT Service, hidden behind `Start`/`Stop`. The
   `bridge.Service` interface is **not** grown with a reload/hot-swap
   method. The boundary is: **the registry owns process lifecycle
   (start/stop); the config-watcher owns runtime reconfiguration** and keeps
   driving the MQTT Service's own reload path directly. A capability
   interface for supervised reload is explicitly deferred — only MQTT needs
   it and the config-watcher already provides the seam.

4. **MCP is a sub-mount of the REST Service, not a peer bridge.** MCP has no
   independent process lifecycle: it is an HTTP handler mounted into the
   REST router (ADR 0025/0044). It is therefore owned by the REST Service
   (which owns the whole HTTP surface: REST API, WebSocket, SPA, MCP mount,
   diagnostics), not registered as its own Service. This is a decision, not
   an omission — a no-op MCP Service would be dishonest about the topology.

5. **Health aggregates through the registry.** Services that have a liveness
   signal implement `HealthReporter`; `Registry.Health()` rolls them up and
   feeds the `/health` surface, so "are the north-bound surfaces healthy?"
   has one answer.

6. **A registration-completeness guard prevents regression.** A contract /
   wiring test asserts that every enabled north-bound surface is present in
   `Registry.Services()` after boot. This is the structural lock that stops
   a future surface from being quietly hand-wired and bypassing the
   contract — the failure mode that made A4's half-adoption fragile.

The migration is executed **incrementally, one surface per PR, behaviour-
preserving**, in increasing-risk order (Matter → REST → MQTT; MCP folds
into REST), each guarded by the tests above plus the surface's existing
suite. Sequenced directly after the A4 webhook epic. See the plan for the
step-by-step.

## Alternatives considered

- **Thin `cmd/`-level shim adapters that forward to the existing calls.**
  Rejected: it yields ordered start/stop but leaves construction, deps and
  health scattered in `daemon.go`. The registry would enumerate shims, not
  owned services — cosmetic, not architectural.
- **Grow `Service` with a `Reload`/`SupervisedService` capability so the
  registry drives hot-reload too.** Deferred: over-generalises for a single
  consumer (MQTT); the config-watcher already owns reconfiguration. Revisit
  only if a second surface needs supervised reload.
- **Give MCP its own no-op `Service` for "consistency".** Rejected: it has
  no lifecycle; a no-op Service misrepresents the topology. MCP is a REST
  sub-mount.
- **Leave it as A4 left it (webhook-only on the registry).** Rejected: a
  half-adopted contract cannot enforce ordering, rollback, health, or
  no-bypass — the value of the contract is only realised once every surface
  is on it.

## Consequences

- `cmd/openccu-loom` shrinks to a composition root; the per-surface
  `Start`/`defer Stop` chains disappear. Boot-dependency ordering becomes
  explicit (registration order) and test-pinned.
- Startup gains rollback (a failed surface tears down the already-started
  ones); shutdown gains a single ordered owner; `/health` gains north-bound
  surface health.
- A new structural guard (registration completeness) blocks regression to
  hand-wiring.
- MQTT's hot-reload path is unchanged in behaviour but now clearly scoped as
  an internal concern of the MQTT Service, with the registry/config-watcher
  boundary documented.
- One-time refactor cost across `daemon.go` and the surface packages,
  amortised over per-PR increments; each increment is behaviour-preserving
  and independently revertible.
- Discovery (SSDP/mDNS) advertisers are *not* in scope here; they may be
  brought onto the same contract later as a follow-up.
