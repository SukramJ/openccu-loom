# Implementation plan — Migrate MQTT / Matter / MCP / REST onto `bridge.Registry`

**Status**: prioritised, not started. **Effort: S–M.**
**Audience**: a fresh Claude environment with no access to the review
conversation. Verify each cited path against the tree before editing
(paths were correct at the time of writing but code moves).

---

## 1. Summary

The north-bound `bridge.Service` + `Registry` lifecycle contract
(`internal/north/bridge/`) shipped in A4 (PR #225) with the outbound
webhook bridge (PR #227) as its **only** registered consumer. Every other
north-bound adapter is still hand-wired in `cmd/openccu-loom/daemon.go`
(and its `daemon_*.go` helpers) with bespoke `Start`/`Stop`/`defer` calls.

Goal: wrap each established bridge in a thin `Service` adapter and register
it on the shared `Registry`, replacing the inline lifecycle calls **one at
a time**. This is a **pure refactor** — no behaviour change. The payoff is
a single ordered start/stop path (and a single place to roll
`HealthReporter` into `/health`) instead of N bespoke `defer` chains.

This is the incremental migration the A4 plan already foresaw
([`A4-webhook-plugin-contract.md`](./A4-webhook-plugin-contract.md) §3a):
"do **not** rewrite every bridge at once … wrap each existing bridge in a
thin `Service` adapter and register it, replacing the inline `.Start(ctx)`
lines one at a time. Each migration is behaviour-preserving and
independently testable."

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
func (r *Registry) StartAll(ctx) error // start in order; rollback-on-error
func (r *Registry) StopAll(ctx)        // reverse order, best-effort
func (r *Registry) Health() (ok bool, detail string)
```

The registry is already constructed and used for the webhook:
`cmd/openccu-loom/daemon.go` (`northBridges := northbridge.NewRegistry(...)`,
`northBridges.Register(webhook.NewOutbound(...))`, `StartAll`, and
`defer northBridges.StopAll(context.Background())`).

---

## 3. Current state (verified) — what each bridge looks like today

The lifecycle calls are scattered across `daemon.go` and its helpers.
Confirm each before wrapping; line numbers drift.

- **MQTT** — the hardest, do **last**. Several pieces:
  - `EventBridge`: `bridge.Start(ctx)` / `defer bridge.Stop()`
    (`daemon.go`, ~215). Built via `adapter.NewEventBridge(...)`.
  - `HubMQTTPublisher`: `hubMQTT.Start(ctx)` / `defer hubMQTT.Stop()`
    (~244), plus a re-`Start` on every broker reconnect via
    `mqttSup.OnConnect(...)`.
  - **Runtime supervisor** `mqttSup` (`cmd/openccu-loom/mqtt_supervisor.go`,
    `SwapBridge`, `OnConnect`) drives hot-reload of the whole MQTT stack
    and re-publishes snapshots on reconnect. A `Service.Stop` for MQTT
    must wrap this lifecycle cleanly, not fight it. **Do not** try to
    model hot-swap in the `Service` interface (explicitly out of scope per
    A4 §3a) — the supervisor stays as-is; the `Service` wrapper only owns
    the start/stop seam.
- **Matter** — a large subsystem with its own ordered teardown.
  `daemon_matter.go`: `bridge.Start(ctx)` (~213) and a multi-step stop
  (~843: `bridge.Stop(stopCtx)`, `subMgr.Stop()`, advertiser `adv.Stop()`,
  `db.Close()`), plus `subMgr.Start(ctx)` (~483) and the mDNS advertiser
  `adv.Start(ctx)` (~2300). The wrap must preserve the **stop ordering**
  (bridge → sub-manager → advertiser → db) and the default-off gate
  (`cfg.North.Matter.Enabled`).
- **REST** — the HTTP server: `s.Start()` (`daemon_north.go`, ~55, tolerates
  `http.ErrServerClosed`) and graceful `Shutdown`. This is the listener the
  SPA + API ride on; its `Service.Stop` is a context-bounded
  `http.Server.Shutdown`. Note REST is also where **MCP** mounts (below),
  so REST must start after the router (incl. MCP route) is assembled.
- **MCP** — **not a server**: it is a route mounted into the REST router at
  boot (`daemon_rest_mount.go`; gated by `cfg.North.MCP.Enabled`,
  restart-required). There is no goroutine to start/stop, so MCP does not
  obviously need a `Service` at all. Options: (a) skip MCP — it has no
  independent lifecycle; or (b) give it a no-op `Service` purely so the
  registry enumerates it for `/health`. Recommend **(a) skip** unless a
  health surface is wanted; document the decision.

---

## 4. Design decisions

1. **Adapters, not rewrites.** Each bridge keeps its current constructor
   and internals. Add a small `xxxService` struct whose `Start`/`Stop`
   delegate to the existing calls. Example:
   ```go
   type mqttService struct{ sup *mqttSupervisor; bridge *adapter.EventBridge; hub *adapter.HubMQTTPublisher }
   func (s mqttService) Name() string { return "mqtt" }
   func (s mqttService) Start(ctx context.Context) error { s.bridge.Start(ctx); s.hub.Start(ctx); return nil }
   func (s mqttService) Stop(ctx context.Context) error  { s.hub.Stop(); s.bridge.Stop(); return nil }
   ```
   These adapters live in `cmd/openccu-loom/` (daemon-internal wiring), not
   in the bridge packages — they are composition glue, and several need
   types from `cmd` scope (the supervisor, closers).
2. **Preserve order exactly.** `StartAll` starts in registration order and
   `StopAll` stops in reverse. Register in the SAME order the current
   `defer` chain tears down (defers run LIFO), so the reverse-order
   `StopAll` reproduces today's teardown sequence. Map the current order
   before moving anything; a reordering is a behaviour change.
3. **One bridge per PR.** Migrate in increasing-risk order:
   **Matter → REST → (MCP decision) → MQTT**. Each PR wraps exactly one
   bridge, leaves the others untouched, and is independently revertible.
   MQTT last because the supervisor + reconnect hooks are the subtlest.
4. **No interface growth.** Do not add reload/hot-swap to `Service`
   (A4 §3a scope guard). The supervisor keeps owning hot-reload.
5. **Health is opportunistic.** Where a bridge already exposes a liveness
   signal, implement `HealthReporter` on its adapter and (optionally) fold
   `Registry.Health()` into the `/health` aggregator. Not required for the
   refactor; can be a follow-up.

---

## 5. Implementation steps (per bridge, repeat)

1. Identify the bridge's current `Start`/`Stop`/`defer` lines and the exact
   teardown ordering relative to its neighbours.
2. Write the `xxxService` adapter in `cmd/openccu-loom/`.
3. Replace the inline `.Start(ctx)` + `defer .Stop()` with
   `northBridges.Register(xxxService{…})` at the position that preserves
   teardown order (remember `StopAll` is reverse-registration-order vs.
   today's LIFO `defer`).
4. Keep `StartAll` where it is, or move its call to the point after the
   last bridge is registered (currently it runs right after the webhook
   register — as more bridges register, ensure `StartAll` runs once, after
   all `Register` calls, OR call `Register` before the existing `StartAll`).
   **Watch the boot data-dependency**: some bridges must start only after
   southbound hydration / router assembly. If a bridge cannot move its
   start to the single `StartAll` point without reordering boot, leave it
   hand-wired and note why (the registry is a convenience, not a mandate).
5. Build, run that bridge's existing test suite + a focused boot/shutdown
   test; confirm no behaviour change.

---

## 6. Config / API / doc changes

- **None.** Pure refactor: no `cfg:` field, no `openapi.yaml` / `wsapi.json`
  change, no `APIVersion` bump, no i18n. `TestConfigFieldsHaveLabelsAndHelp`
  and the api-contract guard are unaffected.
- If MCP gets a no-op `Service`, that is code-only.

---

## 7. Tests

- **Reuse existing per-bridge suites** — they assert the behaviour that must
  not change (MQTT publish/discovery, Matter wire/parity, REST routing).
  Keep them green; that is the primary guard for a behaviour-preserving
  refactor.
- **Add a boot/shutdown ordering test** (if a daemon-level harness exists)
  asserting that with all bridges registered, `StopAll` tears down in the
  reverse of `StartAll` and the daemon exits cleanly. At minimum, a unit
  test on each `xxxService` adapter that `Start` then `Stop` delegates in
  the right order (a fake of the wrapped bridge recording call order —
  mirror `internal/north/bridge/registry_test.go`'s `fakeService`).
- No new contract test unless a capability boundary changes (it does not).

---

## 8. Project-rule checklist

- [ ] SPDX header on any new `.go` file.
- [ ] No CGo; pure-Go; no new copyleft deps.
- [ ] Multi-CCU safe — the adapters wrap registry-wide bridges already
      built multi-CCU-aware; no per-central assumptions added.
- [ ] `ctx context.Context` first param on `Start`/`Stop`; `Stop` must be
      idempotent and must not hang on the cancelled daemon ctx (the
      registry's `StopAll` is already called with a fresh background ctx).
- [ ] No `panic` outside `main`/tests.
- [ ] Adapters live in the consumer/composition layer (`cmd/openccu-loom`),
      not in `pkg/interfaces`.
- [ ] `make lint && make test` green; run `golangci-lint run ./...`
      (cross-package linters flag callers in untouched files).

---

## 9. Acceptance criteria

- Each migrated bridge is started/stopped via `northBridges` instead of an
  inline `defer`, with identical observable boot + shutdown behaviour
  (its existing test suite stays green).
- Teardown order is unchanged (reverse-registration == today's LIFO defer).
- MQTT's supervisor + reconnect hooks still function (hot-reload + snapshot
  republish on broker reconnect) after its wrap.
- No config / API / schema change; no `APIVersion` bump.
- The `Service` interface is unchanged (no reload/hot-swap added).

---

## 10. References

- [`docs/plans/A4-webhook-plugin-contract.md`](./A4-webhook-plugin-contract.md)
  §3a (the incremental-migration design this item completes).
- Contract: `internal/north/bridge/service.go`, `registry.go`,
  `registry_test.go`.
- Current wiring: `cmd/openccu-loom/daemon.go` (webhook register + MQTT
  EventBridge/HubMQTT), `mqtt_supervisor.go`, `daemon_matter.go`,
  `daemon_north.go` (REST server), `daemon_rest_mount.go` (MCP mount).
- `CLAUDE.md` → "Interfaces in the consumer package"; "Multi-CCU from day
  one".
