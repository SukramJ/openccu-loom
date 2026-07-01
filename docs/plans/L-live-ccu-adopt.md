# Implementation plan — Live CCU adopt without daemon restart

**Status:** prioritised, foundation not yet landed. **Effort: L, structural Go.**
**Audience:** a fresh environment. Everything needed is inline; verify every
`file:line` anchor against the tree before editing (they drift).

## Summary

Today adding or removing a CCU is **restart-required**: `CreateCentral` /
`DeleteCentral` (`internal/north/rest/handlers/admin_centrals.go`) only persist a
`centrals` row; the DB→cfg overlay folds it into `cfg.Centrals` on the **next
boot only** (`cmd/openccu-loom/daemon_boot.go:53-55`), and `RestartRequiredDiff`
flags `centrals` (`internal/config/restart.go:53`). The goal is to make
add/remove a **live coordinator-lifecycle operation** so a freshly-discovered or
operator-added CCU is adopted (and a removed one torn down) without a restart.

**The runtime primitives already exist** — this is a wiring + refactor job, not a
green field:

- `central.Registry` (`internal/central/central_registry.go:18`) — `sync.RWMutex`
  + `map[string]*Unit`; `Register`/`Unregister`/`Get`/`List` are all
  runtime-safe. North-bound adapters that iterate `reg.List()`/`reg.Get()` pick
  up changes immediately.
- Both callback servers expose **Register AND Deregister**: XML-RPC
  (`internal/central/rpcserver/xmlrpc_server.go:117/124`, routes by
  `/RPC2/<central_name>`) and BIN-RPC
  (`internal/central/rpcserver/binrpc_server.go:113/120`, routes by
  `interface_id`). They are shared singletons that mutate their route maps live.
  `registerCentralCallbacks` (`internal/central/adapter/ccu_wiring.go:467`)
  already returns a deregister closure stored as a permanent closer.
- `centralBringUp` (`internal/central/adapter/central_bringup.go:24`) is
  explicitly "a restartable unit … torn down and brought up again mid-life
  without disturbing its peers": `start()`/`teardown()`/`shutdown()`/`reinit()`,
  with **generation** closers (per bring-up) vs **permanent** closers (callback
  route). `BringUpManager` (`:196`) already maps `byCentral` under a mutex and
  ships `ReinitCentral(ctx,name)` — proving live single-central teardown+re-pull
  works.
- `central.Unit.New` (`internal/central/central.go:205`) is pure in-memory (no
  I/O, no goroutines); `Start` (`:451`) only starts the scheduler; `Stop`
  (`:504`) is a full idempotent 12-step teardown with external hook tiers.
- Per-central SQLite state is keyed by `central_name` with delete methods
  (`centrals_store.go:167`, `devices.go`, `paramsets.go`, `values_cache.go`,
  `sessions.go`). `clearModel()` (`central_bringup.go:171`) does the in-memory
  eviction with `DeviceRemovedEvent` fan-out.

## The hard parts (what actually needs building)

1. **Per-central north-bound hooks are inlined in boot, not in the handle.**
   `wireSouthbound` (`cmd/openccu-loom/daemon_southbound.go:233-336`) runs, in
   `reg.List()` loops *after* `WireCentrals`: `WireHealth`,
   `AddOnStopHook`→`reg.Unregister`, `WireDeviceAvailability`,
   `WireClimateLinkPeerRefresh`, and MQTT `SetHubInfoFor`/`hubMQTT.Start`. An
   added central must re-run exactly these; a removed one must run their closers.
   **This is the biggest refactor: extract a `wireCentralNorthbound(central)`
   used by both boot and the runtime path.**
2. **Adapters closed over the static `cfg.Centrals` slice won't see a runtime
   add.** `ccu_auth_wiring.go:45`, `system_ccu_adapter.go:37`,
   `visibility_wiring.go:64`, `cachereset_wiring.go:37/45`, `daemon_jobs.go:32`,
   `daemon_rest_mount.go:165` (device-icon proxy), `daemon_wiring.go:38` iterate
   the boot-time array. Each is a place a new CCU is invisible (auth, scopes,
   primary-interface, icon proxy). Migrate each to the dynamic registry, or push
   an update on add.
3. **Standard scheduler jobs are registered once, pre-`StartAll`.**
   `registerStandardJobs` (`daemon.go:150`, `daemon_jobs.go`). A runtime-added
   central starts its own scheduler (via `Unit.Start`) but nothing registers its
   recurring hub jobs — job registration must be made per-central and invoked on
   add.
4. **Removal teardown ordering across three subsystems.** A live remove fires
   `Unit.Stop()` (whose OnStop tier hooks include `reg.Unregister`),
   `centralBringUp.shutdown()` (drains bring-up + InterfaceClient goroutines +
   deregisters callback routes), and the extracted north-bound closers — in an
   order that does not deadlock against an in-flight callback arriving on
   `/RPC2/<name>`. Both `Unit.Stop` and the handle touch `reg.Unregister`/closers
   → **one owner must sequence it** to avoid double-teardown. Deregister the
   callback route FIRST (stop new inbound), then drain, then Unregister + evict.

## Increment decomposition (one PR each, mergeable independently)

- **PR 1 — Foundation refactor (behaviour-preserving).** Extract the inline
  per-central north-bound hooks from `wireSouthbound` into a reusable
  `wireCentralNorthbound(deps, central)` (returns its closers). Boot calls it in
  the existing `reg.List()` loop; behaviour is identical. No runtime add/remove
  yet. Verified by: daemon still boots (integration/`godevccu`), existing tests
  green. This de-risks everything downstream and is the prerequisite.
- **PR 2 — `BringUpManager.AddCentral`/`RemoveCentral`.** Thin wrappers:
  `AddCentral(ctx, cc)` = `Unit.New` → `reg.Register` → build handle
  (register callbacks) → `start()` → `Unit.Start` → `wireCentralNorthbound` →
  register per-central jobs. `RemoveCentral(ctx, name)` = deregister callback
  route → `handle.shutdown()` → run north-bound closers → `Unit.Stop` →
  `reg.Unregister` → `clearModel`. Single sequence owner (§hard-part 4). Unit
  tests with a fake bring-up + `godevccu` integration test (add a central live,
  assert its devices appear; remove it, assert teardown).
- **PR 3 — Dynamic-registry migration of the static-`cfg.Centrals` adapters**
  (§hard-part 2), one commit per adapter with a test that a
  runtime-registered central is visible to it.
- **PR 4 — REST wiring + restart-required removal.** Hang `AddCentral`/
  `RemoveCentral` off `CreateCentral`/`DeleteCentral`
  (`admin_centrals.go`); drop `centrals` from `RestartRequiredDiff`
  (`restart.go:53`) + `restartRequiredPaths` (`admin_config.go:156`) + the
  `reload.go:181` restart bump; update the hot-reload watcher to call the live
  path. Discovery adopt (`discovery.go`) then adopts live. e2e/integration proof.

## Design decisions

- **Reuse, don't rebuild.** Every runtime primitive (registry, callback
  Register/Deregister, `centralBringUp`, per-central SQLite deletes) already
  exists — the work is wiring + the hook extraction, never new lifecycle
  machinery.
- **`context.Context` first arg on the new add/remove methods.** Multi-CCU: never
  assume a single central; the whole point is N live centrals.
- **Removal keeps audit history** (`central_name`-referenced) and per retention
  policy incidents/measurements; deletes the `centrals` row + derived
  device/paramset/values/session rows. `clearModel` handles in-memory eviction;
  SQLite purge is the remove path's explicit job.
- **Concurrency:** add/remove serialise through `BringUpManager`'s existing
  mutex; the callback servers + registry are already mutex-guarded.

## Tests

- **Integration (`godevccu`, `-tags=integration`):** add a central at runtime →
  its devices/hub appear via REST without restart; remove it → callback route
  gone, devices evicted, no goroutine leak. This is the headline proof.
- **Contract:** a guard that `centrals` is NO LONGER in `RestartRequiredDiff`
  once PR 4 lands (and IS until then).
- Unit tests per extracted function + per migrated adapter.

## Acceptance criteria

- Operator (or discovery adopt) adds a CCU via `POST /centrals`; its devices,
  hub, health, MQTT discovery and callbacks come up **without a restart**, and
  the config UI stops showing "restart required" for `centrals`.
- `DELETE /centrals/{name}` tears the CCU down live (callbacks deregistered,
  goroutines drained, model evicted, rows purged) without disturbing peers.
- `make test` + `make integration` green; no goroutine leak on add/remove cycles.

## References

- `docs/adr/0002-multi-ccu-first-class.md` (the multi-CCU contract).
- Roadmap "Operations & multi-CCU → Live CCU adopt without daemon restart".
- Architecture anchors: this document's §"The hard parts".
