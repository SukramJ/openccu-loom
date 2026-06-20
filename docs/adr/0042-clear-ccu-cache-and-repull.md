# ADR 0042 — Clear CCU-derivable caches and re-pull as a first-class operation

- **Status**: accepted
- **Date**: 2026-06-20
- **Related**:
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  [ADR 0019 — persistent VALUES cache](./0019-persistent-values-cache.md),
  [docs/caching.md](../caching.md)

## Context

In the Python reference family, operators repeatedly hit situations where
the only fix was to **clear the cache** — after reconfiguring a device on
the CCU, after a firmware change, after a paramset description drifted from
what the daemon had cached. OpenCCU-Loom inherits the same caches (device
descriptions, paramset descriptions, MASTER values, the persistent VALUES
cache, the in-memory model + registries) and therefore the same need.

Today there is **no robust user-facing way to clear them**:

- The WS command `ccu.cache_clear` exists only as a parity-shape stub; its
  `CacheClearer` dependency is wired to `nil` in production, so the command
  is never registered, and there is no concrete `ClearAllCaches`
  implementation.
- `POST /devices/refresh` and `ccu.reload_device_config` **refresh**
  (re-pull over the existing rows) rather than **clear** — they cannot
  recover from a cache that is structurally wrong, only from one that is
  out of date in place.
- A true purge requires stopping the daemon and editing SQLite or deleting
  files by hand.

This has to become a first-class, **robust** capability: it must always
work, never leave a partial-stale state, and — above all — never destroy
operator-authored data while clearing CCU-derivable data.

## Decision

Add a scoped **"clear CCU-derivable caches + readiness-gated re-pull"**
operation, exposed on every operator surface, built on the existing boot
path rather than a bespoke teardown.

### 1. Preserve/clear policy (the load-bearing decision)

Every persisted and in-memory store is classified once. Only
**CCU-derivable** state is ever cleared; **operator-authored** and
**system** state is never touched by this operation.

**Cleared (re-fetchable from the CCU):**

- SQLite: `devices` (device descriptions), `paramsets` (paramset
  descriptions), `values_cache`, `master_values`.
- In-memory: the device-description registry, the paramset registry (incl.
  its address→parameter index), the value cache coordinator, the model
  registry, and the devicedetails cache (names/rooms/functions, sourced
  from the CCU).

**Never touched (operator-authored or system):**

- `visibility_unignore` (operator visibility rules), `centrals` and
  `config_sections` (operator configuration), `users` / `tokens` /
  `auth_sessions` (auth), **all `matter_*` tables** (clearing them would
  un-commission the bridge from Apple/Google Home), and `audit_log` /
  `incidents` / `session_recorder` (history and diagnostics — the clear
  action is itself recorded into `audit_log`).

A standing contract test enumerates the never-touched tables and fails if
the clear path ever references one, so the guarantee cannot silently rot.

`master_values` is deliberately included: MASTER values are CCU-acked and
re-derivable, and leaving them while clearing descriptions would risk a
partial-stale mismatch. The cost is a lazy per-channel `getParamset(MASTER)`
re-read on next access (a modest DutyCycle tick), accepted for
consistency.

### 2. Scopes

The operation accepts a scope: `global`, `central:<name>`,
`interface:<central>/<iface>`, or `device:<central>/<iface>/<addr>`. The
scope selects **which rows are deleted**. Re-pull granularity is always the
**owning central** — even for an interface- or device-scoped clear the
whole owning central is re-initialized — because re-pulling through the
proven boot path is more robust than a surgical partial re-pull with its
own quiesce requirements.

### 3. Mechanism — reuse the boot path

There is no safe atomic point to clear-and-rebuild a central's model while
inbound callbacks stream into it (no event-bus pause; ingest and event
handlers run on independent goroutines). Rather than invent a fragile
partial quiesce, the re-pull reuses the daemon's existing, tested startup
choreography:

1. **Tear the central down** via `Unit.Stop()` (ordered stop tiers: detach
   north-bound adapters, stop scheduler/recovery/clients, hub logout,
   unsubscribe + clear the in-memory caches) and detach its south-bound
   callbacks.
2. **Clear the scoped persisted rows** in a single transaction.
3. **Re-initialize the central** through the readiness-gated bring-up
   (`gatedCentralBringUp` → `WaitForCCUReady` → `bringUpCentral` →
   device list → descriptions → paramsets → values), then `Unit.Start()`.

To make this re-entrant per central, the per-central bring-up is factored
out of the one-shot daemon wiring into a restartable handle that owns its
own context and closers, so one central can be re-initialized without
disturbing the others (ADR 0002 multi-CCU neutrality).

### 4. Surfaces

A single domain service (`ClearAndReinit(ctx, scope)`) is the one
implementation, fronted by:

- **REST** — `POST /api/v1/admin/cache/clear` (scope in the body).
- **WS** — the dormant `ccu.cache_clear` is wired to the service, taking a
  scope argument.
- **hmcli** — `hmcli cache clear --scope …` with an **online** mode (calls
  REST, including the re-pull) and an **offline** mode that clears the
  persisted rows directly against the database when the daemon is down or
  wedged (the "always works" escape hatch; it cannot re-pull, so it reports
  that a start is required).
- **SPA** — a destructive action in the diagnostics surface using the
  shared confirm dialog + toast, localized de/en.

### 5. Robustness guarantees

- **Transactional**: the multi-store delete is one unit of work; a partial
  failure is reported, not half-applied.
- **Idempotent**: clearing already-empty caches is a no-op; re-init is
  safe to repeat.
- **Readiness-gated**: if the CCU is down, the re-pull waits (as at boot)
  and surfaces the "waiting for CCU" health state — it never fails hard or
  trips `/health` to 503.
- **Audited**: every clear is appended to `audit_log` (operator, scope,
  timestamp), which is itself in the never-touched set.

## Implementation note — descriptor persistence

In the current build the `devices` / `paramsets` SQLite tables are not written
on the pipeline persistence path (only the boot-time schema wipe touches them);
device and paramset **descriptions** live in the in-memory registries and are
refreshed when the re-pull re-ingests from the CCU (a fresh `ListDevices` +
`hydrateDataPoints` overwrites the registry entries). The persisted caches that
the re-pull would otherwise reload stale — the VALUES cache and the MASTER
values — are the ones the clear deletes. The `DeviceClearer` / `ParamsetClearer`
ports are still part of the service (nil-safe, so a disabled store is a no-op)
so that the SQLite descriptor clear is wired automatically if that persistence
path is added later, without changing the surfaces.

## Consequences

- The clear is honest about cost: an interface- or device-scoped clear
  still briefly takes the **whole owning central** offline during re-init
  (all its entities go unavailable until the re-pull completes), the price
  of reusing the proven path over a surgical one.
- The per-central bring-up must be refactored from a one-shot into a
  restartable handle at the composition root. This is the main engineering
  risk and is covered by an integration test that clears and re-pulls a
  central against the `godevccu` simulator.
- The offline hmcli mode clears persistence but cannot re-pull; the next
  daemon start performs the readiness-gated bring-up. This is the only mode
  that works when the daemon itself is unavailable, which is exactly when
  it is most needed.
- **Alternatives rejected**: (a) a partial in-place quiesce + rebuild —
  there is no safe atomic pause for inbound callbacks, so it cannot meet
  the "always works" bar; (b) clearing everything including operator data —
  a recurring support footgun and a data-loss hazard; (c) restarting the
  whole daemon — too coarse for a multi-CCU deployment where one CCU's
  cache is stale.
