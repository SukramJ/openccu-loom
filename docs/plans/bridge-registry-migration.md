# Implementation plan — North-bound bridges onto `bridge.Registry` (architecturally complete)

**Status**: prioritised, not started. **Effort: M.**
**Decision of record**: [ADR 0047 — North-bound bridges as `Service`s owned
by a `Registry`](../adr/0047-northbound-bridge-registry.md). Read it first;
this plan executes it.
**Sequencing**: scheduled directly **after A4** (run once the webhook epic,
incl. PR4 inbound, is fully landed; before the A1 Matter work). Depends only
on the already-shipped `bridge` contract, not on PR4 — ordered after A4 by
choice so the contract settles with its first real consumer before the
established surfaces are migrated.
**Audience**: a fresh Claude environment with no access to the review
conversation. Verify each cited path against the tree before editing
(paths were correct at the time of writing but code moves).

> **Scope note.** This is the *architecturally complete* migration, not a
> cosmetic wrap. The goal is that the `bridge.Service`/`Registry` contract
> becomes the **mandated** wiring path for every north-bound surface, with
> services owning their own lifecycle, `cmd/openccu-loom` reduced to a
> composition root, the boot-dependency order made explicit and test-pinned,
> and a structural guard that prevents any future surface from bypassing the
> contract. A thin shim-in-`cmd` approach was explicitly rejected (ADR 0047
> §Alternatives).

---

## 1. Summary

A4 introduced the `bridge.Service` + `Registry` contract
(`internal/north/bridge/`) and put the outbound webhook on it. The REST +
WebSocket/SPA/MCP/diagnostics HTTP surface, the MQTT bridge, and the Matter
bridge are still hand-wired in `cmd/openccu-loom/daemon.go` and its
`daemon_*.go` helpers with bespoke `Start`/`Stop`/`defer` calls, teardown
order implicit in `defer` placement.

Deliver the end state ADR 0047 mandates:

- Each north-bound surface is a `bridge.Service` that **owns its lifecycle
  and lives with its package** (as `webhook.Outbound` already does).
- `cmd/openccu-loom` becomes a **composition root**: build deps → construct
  services → register in boot-dependency order → `StartAll`/`StopAll`. No
  bespoke inline `Start`/`defer Stop`.
- The MQTT supervisor is encapsulated **inside** the MQTT service (registry
  owns process lifecycle; the config-watcher owns reconfiguration).
- MCP is owned by the REST service as a **sub-mount**, not a peer service.
- Health aggregates through `Registry.Health()` into `/health`.
- A **registration-completeness guard** locks the pattern so no future
  surface can be hand-wired and bypass the registry.

Behaviour is preserved throughout; the work lands **one surface per PR** in
increasing-risk order.

---

## 2. The contract (already shipped)

`internal/north/bridge/`:

```go
type Service interface {
    Name() string
    Start(ctx context.Context) error // non-blocking
    Stop(ctx context.Context) error  // idempotent, unblocks goroutines
}
type HealthReporter interface { Healthy() (ok bool, detail string) }

type Registry struct { /* … */ }
func NewRegistry(logger *slog.Logger) *Registry
func (r *Registry) Register(s Service)
func (r *Registry) Services() []Service        // snapshot, registration order
func (r *Registry) StartAll(ctx) error         // start in order; rollback-on-error
func (r *Registry) StopAll(ctx)                // reverse order, best-effort
func (r *Registry) Health() (ok bool, detail string)
```

Already used for the webhook in `cmd/openccu-loom/daemon.go`
(`northBridges := northbridge.NewRegistry(...)`,
`northBridges.Register(webhook.NewOutbound(...))`, `StartAll`,
`defer northBridges.StopAll(context.Background())`). `webhook.Outbound` is
the reference for a package-owned `Service` — every surface below follows
its shape (a `Service` type in its own package, not a `cmd` shim).

The contract itself is **not** changed by this work (no reload/hot-swap
method — ADR 0047 §3).

---

## 3. Current state (verified)

Lifecycle calls are scattered; confirm each before moving (line numbers
drift).

- **REST / HTTP surface** — the listener the SPA + REST API + WS + **MCP
  mount** + diagnostics all ride on (ADR 0044, single port). Server start
  `s.Start()` (`daemon_north.go`, ~55; tolerates `http.ErrServerClosed`) and
  a graceful `Shutdown`. MCP is mounted into this router
  (`daemon_rest_mount.go`, gated by `cfg.North.MCP.Enabled`,
  restart-required). **MCP is part of this surface, not a peer** (ADR 0047
  §4). The router must be fully assembled (incl. the MCP mount) before the
  server starts.
- **MQTT** — three pieces under one logical surface:
  - `EventBridge`: `bridge.Start(ctx)` / `defer bridge.Stop()`
    (`daemon.go`, ~215), built via `adapter.NewEventBridge(...)`.
  - `HubMQTTPublisher`: `hubMQTT.Start(ctx)` / `defer hubMQTT.Stop()`
    (~244), re-`Start` on every broker reconnect via `mqttSup.OnConnect`.
  - **Runtime supervisor** `mqttSup` (`cmd/openccu-loom/mqtt_supervisor.go`,
    `SwapBridge`, `OnConnect`) — hot-reload of the MQTT stack +
    reconnect-time snapshot re-publish. Driven by the config-watcher
    (`cmd/openccu-loom/reload.go`). This is the **internal concern** the
    MQTT service must encapsulate (ADR 0047 §3), keeping the
    registry/config-watcher boundary clean.
- **Matter** — a large default-off subsystem with its own ordered teardown.
  `daemon_matter.go`: `bridge.Start(ctx)` (~213); multi-step stop (~843:
  `bridge.Stop(stopCtx)` → `subMgr.Stop()` → advertiser `adv.Stop()` →
  `db.Close()`); plus `subMgr.Start(ctx)` (~483) and the mDNS advertiser
  `adv.Start(ctx)` (~2300). Gate `cfg.North.Matter.Enabled`. The internal
  stop ordering (bridge → sub-manager → advertiser → db) is part of the
  service's `Stop`, not the registry's concern.
- **Webhook** — already a package-owned `bridge.Service`
  (`internal/north/webhook/outbound.go`), already registered. The template.

### 3.1 Boot-dependency graph (the crux — analyse before moving anything)

The single hardest part is that surfaces have **start preconditions**, and
registration order must encode them (ADR 0047 §2). Map the real graph from
`daemon.go` before the first PR:

- The **event-bus consumers** (MQTT EventBridge/Hub, webhook) need the
  centrals registered (their event buses exist) — today they start mid-boot,
  after `reg` is populated.
- The **HTTP/REST service** needs the fully-assembled router (every handler,
  incl. the MCP mount and the southbound-backed handlers) before it binds.
- **Matter** needs its store open and the bridge composed.

Produce a written ordering (start order top-to-bottom; stop is its reverse)
and justify each edge. That ordering becomes the registration order **and**
the golden the ordering test pins (§7). If a surface genuinely cannot start
at the unified `StartAll` point, that is a real boot-sequencing fact to
encode in the order — **not** a license to leave it hand-wired (the rejected
escape hatch from the earlier draft).

---

## 4. Design decisions (executing ADR 0047)

1. **Services own their lifecycle, in their own package.** Each surface
   exposes a `Service` implementation beside its adapter
   (`mqtt.Service`/`mqtt.NewService(...)`, `matter.Service`,
   `resthttp.Service` or similar), constructed from its dependencies + its
   config slice. `Start` brings up everything it owns; `Stop` tears it down
   deterministically and idempotently. **No `xxxService` shim structs in
   `cmd/`.** (Go structural typing means the package need not import
   `bridge` to satisfy `bridge.Service` — `webhook.Outbound` already
   demonstrates this; keep that decoupling.)
2. **`cmd/openccu-loom` is a composition root.** It builds dependencies,
   constructs each service, `Register`s them in the §3.1 order, and calls
   `StartAll` once / `StopAll` on shutdown. The per-surface inline
   `Start`/`defer Stop` chains are deleted.
3. **Registration order == boot-dependency order, pinned by a test.** Not
   implicit in `defer` LIFO. See §3.1 and the ordering test in §7.
4. **MQTT supervisor is internal to the MQTT service.** `Start` starts the
   supervised stack; `Stop` stops it. Hot-reload is **not** on the `Service`
   interface; the config-watcher keeps calling the MQTT service's own reload
   method directly. Document the boundary in the service's doc comment.
5. **MCP is a REST sub-mount, owned by the REST service** — no separate (or
   no-op) Service. The REST service owns router assembly incl. the MCP
   handler.
6. **Health is first-class, not opportunistic.** Every service that has a
   liveness signal implements `HealthReporter`; `Registry.Health()` is
   folded into the `/health` aggregator as part of this work (not deferred).
7. **No interface growth.** `bridge.Service` is unchanged; a supervised-
   reload capability is explicitly deferred (ADR 0047 §Alternatives).

---

## 5. Implementation steps — one surface per PR

Order: **Matter → REST(+MCP) → MQTT** (increasing risk; MQTT last for the
supervisor). Precede PR1 with the §3.1 ordering analysis (can be a tiny
doc-only PR that also adds the *failing* ordering/registration guard tests
as the target to make green).

Per surface:

1. Add a `Service` type to the surface's package (constructor takes deps +
   config slice; `Start` non-blocking; `Stop` idempotent, fully unblocking).
   Implement `HealthReporter` where a signal exists.
2. In `cmd/openccu-loom`, construct it and `Register` it at the §3.1
   position; **delete** the surface's inline `Start`/`defer Stop`.
3. Keep a single `StartAll` after all `Register`s; `StopAll` on shutdown.
4. Run the surface's existing suite + the new guards (§7); confirm identical
   observable boot/shutdown.

Special handling:

- **Matter**: move the bridge→subMgr→advertiser→db **stop ordering** inside
  `matter.Service.Stop`; honour `cfg.North.Matter.Enabled` (a disabled
  Matter registers a service whose `Start` is a no-op, or is not registered
  — pick one and pin it in the registration-guard test).
- **REST**: `matter`/southbound handlers must be mounted before `Start`;
  `Stop` is a context-bounded `http.Server.Shutdown`. MCP mount stays inside
  the REST service's router assembly.
- **MQTT**: encapsulate `mqttSup` + EventBridge + HubMQTT in
  `mqtt.Service`; preserve the `OnConnect` reconnect-republish; leave the
  config-watcher's reload seam pointing at the service's reload method.

---

## 6. Config / API / doc changes

- **No** `cfg:` field, **no** `openapi.yaml`/`wsapi.json` change, **no**
  `APIVersion` bump, **no** i18n. Pure structural refactor.
- `/health` gains north-bound-surface health (additive; document in the
  health section of the user guide if it enumerates components).
- ADR 0047 is the decision record (this PR series references it).

---

## 7. Tests — the safeguards that make it "abgesichert"

These are mandatory, not optional. They are the difference between a real
architectural lock and a cosmetic move.

1. **Registration-completeness guard (the structural lock).** A daemon-level
   wiring test that boots a minimal daemon (test config, in-process, no live
   CCU) and asserts `Registry.Services()` contains exactly the expected
   north-bound surfaces for that config — e.g. webhook+mqtt+rest present;
   matter present only when enabled; MCP **absent** as a service (it is a
   REST sub-mount). This fails the moment someone hand-wires a new surface
   instead of registering it. Lives in `tests/contract/` or a
   `cmd/openccu-loom` wiring test.
2. **Ordering characterization (golden).** Assert `StartAll` start order
   equals the documented §3.1 order and `StopAll` stop order is its exact
   reverse. Implement by having each service record into a shared ordered
   log under test (or by reading the registry order + a per-service start/
   stop hook). Pin the order as a golden so a reorder is a visible diff.
3. **Rollback fault-injection.** Force one registered service's `Start` to
   fail and assert (a) every already-started service was `Stop`ped, (b) no
   listener stays bound / no goroutine leaks, (c) the daemon returns the
   error cleanly. Exercises the registry's real rollback at daemon scope,
   not just the unit `registry_test.go`.
4. **Goroutine-leak guard per service.** Around `Start`→`Stop`, assert the
   goroutine count returns to baseline (a `goleak`-style check or a bounded
   count assertion). This generalises the class of bug the webhook
   worker/`Stop` race fell into — every migrated `Stop` must fully unblock.
5. **Health aggregation.** With a service reporting unhealthy,
   `Registry.Health()` is false and names it; `/health` reflects it.
6. **Per-surface behavioural equivalence.** Each surface's existing suite
   stays green (characterization of unchanged behaviour). Add a focused
   boot+shutdown smoke per surface asserting the observable contract is
   identical (REST: port bound + graceful shutdown; MQTT: topics published +
   reconnect republish; Matter: bring-up + ordered teardown).
7. **No tracking-named test files** (`TestDocPurity`); name each after the
   unit/behaviour.

---

## 8. Project-rule checklist

- [ ] SPDX header on every new `.go` file (the new `Service` types).
- [ ] No CGo; pure-Go; no new copyleft deps.
- [ ] Multi-CCU safe — services wrap registry-wide adapters already built
      multi-CCU-aware; no per-central assumptions added.
- [ ] `ctx context.Context` first param on `Start`/`Stop`; `Stop` idempotent
      and must not hang on a cancelled ctx (`StopAll` is called with a fresh
      background ctx); `Stop` must fully unblock goroutines (guard #4).
- [ ] No `panic` outside `main`/tests.
- [ ] `Service` implementations live in their surface package (consumer
      side), not `pkg/interfaces`; they need not import `bridge` (structural
      satisfaction, as `webhook.Outbound`).
- [ ] `bridge.Service` interface unchanged (no reload method).
- [ ] `make lint && make test` green; `golangci-lint run ./...`.

---

## 9. Acceptance criteria

- Every enabled north-bound surface is started/stopped **only** via
  `northBridges` (no inline `Start`/`defer Stop` remains in `daemon.go` for
  REST/MQTT/Matter); `cmd/openccu-loom` reads as a composition root.
- The registration-completeness guard passes and would fail if a surface
  were hand-wired (verified by a deliberately-broken local run).
- Start/stop order matches the documented boot-dependency order and is
  reverse-symmetric (golden test); a forced mid-start failure rolls back
  with no leaked listeners/goroutines.
- MQTT hot-reload + reconnect republish still work; the config-watcher seam
  is unchanged in behaviour.
- `/health` reflects north-bound-surface health via `Registry.Health()`.
- Each surface's existing suite stays green; no config/API/schema change; no
  `APIVersion` bump; `Service` interface unchanged.

---

## 10. References

- [ADR 0047 — North-bound bridges as `Service`s owned by a `Registry`](../adr/0047-northbound-bridge-registry.md)
  (decision of record).
- [`docs/plans/A4-webhook-plugin-contract.md`](./A4-webhook-plugin-contract.md)
  §3a (the contract this completes; note: its "thin adapter" phrasing is the
  *minimum*; ADR 0047 raises the bar to package-owned services).
- Contract + reference consumer: `internal/north/bridge/service.go`,
  `registry.go`, `registry_test.go`; `internal/north/webhook/outbound.go`.
- Current wiring to refactor: `cmd/openccu-loom/daemon.go`,
  `mqtt_supervisor.go`, `reload.go`, `daemon_matter.go`, `daemon_north.go`
  (REST server), `daemon_rest_mount.go` (MCP mount).
- `CLAUDE.md` → "Interfaces in the consumer package"; "Multi-CCU from day
  one".
