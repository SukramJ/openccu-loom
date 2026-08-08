# Caching & Performance

What OpenCCU-Loom caches, why it matters for boot time and CCU radio
load, and the few levers you have to tune it.

!!! info "Who this page is for"
    Operators who want fast restarts and a low CCU DutyCycle. You do not
    need to know the internals — this page explains the behaviour you can
    observe and the handful of knobs you can turn. Contributors changing
    a boot path should also read the code in `internal/central` and
    `internal/store`.

## Why caching matters here

A Homematic CCU talks to most devices over a shared radio. Every value
read that actually goes to the radio counts against the CCU's
**DutyCycle** budget — read too much, too fast, and the CCU throttles or
warns. OpenCCU-Loom is built around one rule: **never read through the
radio just to fill a cache.** Everything it caches comes from a path
that costs no radio — push events the CCU sends on its own, ReGa script
reads served from the CCU's database, file-cache hits, or the daemon's
own SQLite store.

The only radio traffic the daemon causes is for explicit actions you
take: turning a device on, writing a paramset, or asking for a forced
refresh.

## What gets cached

| Layer | Survives restart? | What it holds |
|---|---|---|
| Embedded static data | n/a (compiled in) | device profiles, paramset templates, translations, Matter schema |
| In-memory caches | no — rebuilt on start | last-observed values, hub catalogues (programs, sysvars, rooms), device details, visibility decisions |
| SQLite store (`<data_dir>/openccu-loom.db`) | yes | last-known values snapshot, MASTER values, paramset/device descriptors, audit log, sessions, Matter fabrics |
| Filesystem | yes | CCU descriptor backups, optional translation override |

The key persistent layer for performance is the **values cache** in
SQLite: at shutdown the daemon writes the last-known state of every data
point, and on the next boot it restores that snapshot so the model is
populated *before* the first CCU push — with zero radio cost.

## Cold start vs. warm start

A "warm" start is the normal case: the CCU has been running, and the
daemon's database carries a recent values snapshot.

| Situation | What boot looks like |
|---|---|
| **Warm CCU + values cache present** (normal) | Device list, descriptors, and MASTER paramsets load from caches; values restore from SQLite. Effectively zero radio reads to reach a ready state. |
| **Warm CCU + no cache** (fresh install or wiped `data_dir`) | The daemon fills the cache from push events and a bounded bootstrap read pass. Most of those reads are served from the CCU's own value database; DutyCycle ticks up but stays modest on a healthy CCU. The next restart is a warm start. |
| **Cold CCU just rebooted** | The CCU re-polls its devices over the radio to rebuild its own state — that DutyCycle activity is CCU work, not daemon-induced. Expect a spike for several minutes after a CCU reboot. |
| **Cold CCU + no daemon cache** (worst case) | Both sides are cold, so the daemon's bootstrap reads hit devices that the CCU also has no value for. **Avoid this** — if you wipe `data_dir`, wait until the CCU has been up at least a minute before starting the daemon. |

!!! note "After a CCU reboot, don't immediately restart the daemon"
    A freshly rebooted CCU is busy re-learning device state over the
    radio. Restarting the daemon during that window can push the
    combined load into the DutyCycle warning band. Let the CCU settle
    first.

### Restored values and north-bound availability

Boot applies caches in a fixed order: the device model is built, the
persistent values cache is **restored** onto each data point, then
`fetch_all_device_data` **seeds** live values, then the initial snapshot is
published north-bound.

The restore step is what makes a warm start look ready instantly — and it also
governs the `available` flag that REST, WebSocket and MQTT expose. Restoring a
cached value marks the data point as **observed** and stamps it with the
persisted timestamp, so it satisfies the same `IsValid()` gate a live value
would (refreshed + acceptable status + value type + range). The north-bound
`available` flag is therefore `true` and the entity carries its **last-known
value** — stale but plausible — until the CCU pushes a fresh one.

Only a data point that has **never** been observed **and** has no cached value
publishes `available:false` (a brand-new device, a freshly added parameter, an
un-cached value type, or a wiped `data_dir`). It flips to `available:true`
automatically on the first real value.

The seed pass overwrites a restored value only where the CCU returns a fresh,
non-empty value. It deliberately does **not** coerce an empty (not-yet-measured)
value into `0`: doing so used to clobber the restored last-known reading with an
implausible placeholder (e.g. `0 °C` right after a CCU restart). See
`notes/parity/by_design.md` (BD-CCU-StatusUncertainViaTracker).

`available` means "we have a value", not "it is fresh". How old the value is
travels separately — `refreshed_at` / `modified_at` on the REST/WS payload, and
the MQTT `expire_after` retention — so a consumer can distinguish a freshly
pushed reading from a restored one.

## Steady-state radio load

Once the daemon is ready, the only radio traffic it generates is from
explicit actions:

| Trigger | Radio cost |
|---|---|
| Turn a device on/off (SPA, MQTT, REST) | one write per action |
| Edit a MASTER or LINK paramset | one write per action |
| Force a fresh value read | one read per request |
| Push events from the CCU | none (the CCU emits them on its own) |
| Connectivity ping | none (service-level handshake only) |

Some data is never pre-loaded and is fetched only on demand — for
example LINK paramsets and MASTER values for hidden parameters are read
only when the configuration UI asks for them.

## Operator levers

There are deliberately few knobs.

- **Values-cache flush interval** —
  `persistence.values_cache.flush_interval` (default `60s`). Trades the
  crash-loss window against the SQLite write rate. A shorter interval
  means more frequent writes but less data lost on an ungraceful kill.
- **Values-cache disable list** —
  `persistence.values_cache.disabled_centrals`. Turns the persistent
  values cache off for a named central. Use only to work around a
  corrupted snapshot.
- **Clear all in-memory caches at runtime** — the WebSocket command
  `ccu.cache_clear` drops the central's in-memory caches so the next
  read fetches fresh data from the CCU. Useful after you change device
  configuration directly on the CCU and want the daemon to re-read.

There is intentionally **no** "preload everything on boot" option — that
would defeat the radio-cost guarantees above.

## Clearing the CCU caches (and re-pulling)

When a device is reconfigured on the CCU and the daemon is holding stale
descriptions or values, clear the CCU caches and re-pull fresh. The operation
(ADR 0042) clears only **CCU-derivable** state — the persistent VALUES cache,
persisted MASTER values, and the in-memory model + value cache — then
re-initializes the affected central(s) through the normal readiness-gated boot
path, so device/paramset descriptions and values are fetched again from the
CCU. It never touches operator-authored or system state: configuration,
visibility rules, auth, Matter pairing, and the audit / incident history all
survive (the clear is itself recorded in the audit log).

Scopes: **global**, a single **central**, a single **interface**, or a single
**device**. The scope decides which rows are cleared; the re-pull always
re-initializes the whole owning central — so the affected central's entities
briefly read `unavailable` while it re-pulls, exactly as during a normal boot.

Surfaces (all drive the same operation):

- **Config UI** — a "Clear CCU cache" action (with a confirmation dialog).
- **REST** — `POST /api/v1/admin/cache/clear` with `{"kind":"global", …}`.
- **WebSocket** — the `ccu.cache_clear` command (same scope arguments).
- **CLI** — `hmcli cache clear --scope …`. Add `--offline` to clear the
  persisted rows directly against the database when the daemon is down or
  wedged; an offline clear cannot re-pull, so the next daemon start performs
  the readiness-gated bring-up.

## Where this data lives

- Persistent caches and all daemon state: `<data_dir>/openccu-loom.db`
  (default `<data_dir>` is `./var`). Back this up — see
  [Backup & restore](admin/backup.md).
- Optional on-disk translation override and CCU descriptor backups live
  under the data directory as well.

## See also

- [Configuration reference](admin/configuration.md) — the
  `persistence.values_cache.*` keys.
- [Troubleshooting](admin/troubleshooting.md) — DutyCycle spikes and
  missing updates.
- [Backup & restore](admin/backup.md) — what the database holds.
