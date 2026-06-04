# Caching Architecture

Operator + contributor reference for every cache layer in
OpenCCU-Loom: what each one holds, how it is populated, whether it
survives restart, and how it relates to the CCU's own caches. The
last two sections translate the architecture into "when does the
daemon hit the radio" and "what does a boot look like under
different conditions."

Audience: operators tuning a restart, contributors changing a
boot path, anyone asking "why did the DutyCycle spike when I
restarted the daemon" or "where does this value come from."

Related design notes:
- [ADR 0019 — Persistent VALUES Cache](adr/0019-persistent-values-cache.md)
- [ADR 0018 — Health Parity](adr/0018-health-parity-with-aiohomematic.md)
- `CLAUDE.md` → §Critical Rules (live-CCU write authorisation)

---

## 1. Layer overview

```
┌─────────────────────────────────────────────────────────────────┐
│ Embedded static data (compile-time, read-only)                  │
│   device-profile registry, master/link profile templates,       │
│   translations, easymode, openccu-data extracts, Matter schema  │
├─────────────────────────────────────────────────────────────────┤
│ In-memory caches (volatile — rebuilt on restart)                │
│   Device.valueCache, coordinators data cache, reliability       │
│   command + ping/pong trackers, hub catalogues, devicedetails,  │
│   visibility, patches, masterprofile, linkprofile               │
├─────────────────────────────────────────────────────────────────┤
│ Persistent stores (SQLite, survive restart)                     │
│   ValuesCacheStore, MasterValuesStore, paramset patches,        │
│   device descriptors, incidents, audit log, sessions,           │
│   visibility-unignore, Matter persistence (fabric/ACL/etc.)     │
├─────────────────────────────────────────────────────────────────┤
│ Filesystem caches                                               │
│   var/backups/, var/ccu_data/*.json.gz                          │
└─────────────────────────────────────────────────────────────────┘
            ▲
            │  XML-RPC / BIN-RPC / JSON-RPC
            ▼
┌─────────────────────────────────────────────────────────────────┐
│ CCU-side caches we leverage (not ours, but load-bearing)        │
│   persistence.hmip (MASTER + LINK file cache, HmIP)             │
│   CCU's in-memory value DB (populated by device push events)    │
└─────────────────────────────────────────────────────────────────┘
            ▲
            │  RF (radio — DutyCycle-relevant)
            ▼
                         Device
```

Two rules govern the whole design:

1. **No cache writes through the radio.** Everything the daemon
   caches comes from a path that does not cost DutyCycle:
   push events, JSON-RPC ReGa script reads, file-cache hits, or
   SQLite reads. The exceptions are explicit user actions
   (`setValue`, `putParamset`) and operator-triggered `?fresh=true`
   refreshes — both well-bounded.
2. **Lazy where possible, push everywhere else.** The boot path
   walks no DP just to "populate" a cache. If a DP has no observed
   value yet, it stays unobserved until the next CCU push.

---

## 2. Embedded static data (compile-time)

Read-only, baked into the binary via `go:embed`. Zero runtime
cost beyond the lazy decode on first access.

| Package | Contents | Source |
|---|---|---|
| `internal/ccudata` | openccu-data extracts: translations, easymodes, profiles, receivers | embedded from `openccu-data` |
| `internal/i18n` | translation catalogues (de, en) | embedded JSON |
| `internal/model/custom` | device-profile registry (139 generated profiles + hand-written wrappers) | generated from upstream reference |
| `internal/store/masterprofile` | MASTER paramset templates (e.g. "Toggle", "Door-lock") | embedded gzip JSON, lazy decode |
| `internal/store/linkprofile` | LINK paramset templates | embedded gzip JSON, lazy decode |
| `internal/north/matter/schema` | Matter cluster + device-type definitions | generated from matter.js HEAD |

These never go stale at runtime. They change with `make generate`
+ a rebuild.

---

## 3. In-memory caches (volatile)

Live only for the lifetime of the process. Rebuilt from the
authoritative sources (push events + persistent stores +
embedded data) on every restart.

### `Device.valueCache` — per-device VALUES + MASTER cache

`internal/model/device/value_cache.go`. The hot-path cache for
all `Device.LoadValue` calls.

- Singleflight-coalesces concurrent loads for the same key (one
  channel for MASTER, one parameter for VALUES).
- Caches sentinels for "CCU has no value for this parameter" so
  callers don't retry forever (TTL `sentinelCacheTTL`).
- Populated by:
  - CCU push events (`OnObservedValue`)
  - Persistent VALUES cache restore on boot
  - Explicit `LoadValue` calls (rare; see §7)

### coordinators data cache — last-observed VALUES per channel

`internal/central/coordinators/cache.go` (`DataCacheEntry` +
`CachePersister`). The cache sits between the wire and the domain
layer; every channel carries its last observed VALUES paramset here.

- Populated by callback events as they arrive.
- Consumed by REST snapshot, MQTT publish, Matter Subscribe report.
- Not persistent. The persistent VALUES cache (next section) is
  what survives restart; on boot it seeds `DataCache` via
  `OnWireValue` so steady-state code paths see a hydrated cache
  even before the first CCU push.

### reliability command tracker — echo suppression

`internal/client/reliability/command_tracker.go`. Tracks the last
command the daemon sent per `(channel,
parameter)`. When the CCU echoes the change back (the device
acknowledged + state-change event fired) we suppress the
re-publish because the daemon already announced the optimistic
state to its consumers.

### reliability ping/pong tracker — health window

`internal/client/reliability/pingpong.go` (`PingPongTracker`).
Rolling window of ping/pong events used by the health tracker.
Drives the per-interface health score (see [ADR 0018](adr/0018-health-parity-with-aiohomematic.md)).

### Hub catalogues — programs, sysvars, device names, rooms, functions

Loaded once at boot via JSON-RPC ReGa scripts (`Program.getAll`,
`SysVar.getAll`, `Subsection.getAll`, `Room.getAll`, the
`fetch_device_names.fn` / `fetch_device_details.fn` scripts). None of
these reads cost radio — the CCU serves them from its own
SQLite-backed device DB.

Cached on `CentralUnit`. Refreshed by the scheduler on a long
cadence (and explicitly on the CCU-reboot recovery path).

### `devicedetails.Cache` — JSON-RPC device-details

Per-device extracts pulled from `Device.listAllDetail` (firmware
version, type metadata, sub-device info). Boot-load + scheduler
refresh; no radio.

### `visibility.Decider` — north-bound visibility decisions

Built-in deny list + device-profile rules + (future) user
overrides from the `visibility_unignore` SQLite table.
Read-mostly; rebuilt on profile-registry change.

### `patches` registry — CCU-bug paramset overrides

Static, registered at process start. Applied during paramset
normalisation. Examples: `HM-ES-PMSw1-Pl` energy-counter unit
fixes, `HmIP-RGBW` saturation bounds, missing WRITE operations
bits.

### `masterprofile.Store` / `linkprofile.Store`

Lazy decoders for the embedded profile templates. Decoded on
first access; cached for the process lifetime.

---

## 4. Persistent stores (SQLite, survive restart)

All persistent state lives in `var/openccu-loom.db` (pure-Go
`modernc.org/sqlite`, WAL mode). Migrations under
`internal/store/sqlite/migrations/` via goose.

| Store | Schema migration | Purpose |
|---|---|---|
| `ValuesCacheStore` | `016_values_cache.sql` | Per-DP last-observed VALUES wire snapshot (ADR 0019). Survives restart so boot can hydrate the model without radio. |
| `MasterValuesStore` | `015_master_values.sql` | Per-channel MASTER paramset values cache. Boot reads this before falling back to `getParamset(MASTER)`. |
| `paramsets` cache | `paramsets.go` | Paramset descriptor cache to avoid re-fetching `getParamsetDescription` every boot. |
| `devices` | `devices.go` | Device descriptors from `listDevices`. |
| `incidents` | `incidents.go` | Incident log (recovery failures, transport errors, etc.). |
| `audit` | `audit.go` | User-action audit trail. |
| `sessions` | `sessions.go` | REST session cookies. |
| `visibility_unignore` | `visibility_unignore.go` | User-configured un-ignore overrides. |
| Matter persistence | `006_..017_matter_*` | Fabrics, ACL, endpoints, resumption, exposures, root cert, metadata. |

### Persistent VALUES cache lifecycle (ADR 0019)

The flow is:

```
boot:
  ValuesCacheStore.Load() → restore each DP via RestoreCachedValue()
                            DP source = "cache"
                            DP marked observed (downstream sees value)
runtime:
  CCU push event → DP source = "live"  → onSourceChanged → republish
  long idle      → DP source = "stale" → state-eviction
  flush tick     → dirty centrals write current VALUES to SQLite
shutdown:
  final flush → every central written (bypasses dirty filter)
```

The per-central dirty tracker means quiet centrals are skipped on
each tick — the SQLite write rate scales with activity, not fleet
size.

---

## 5. Filesystem caches (outside SQLite)

| Path | Contents | Purpose |
|---|---|---|
| `var/backups/` | Automatic CCU descriptor backups (XML) | Recovery from a CCU rebuild |
| `var/ccu_data/translation_extract.json.gz` | Optional disk override for translations | Lets the operator swap in an updated translation pack without rebuilding the binary; falls back silently to the embedded copy when absent |

Neither is performance-critical; both are operator-facing
escape hatches.

---

## 6. CCU-side caches we leverage

Not OpenCCU-Loom's caches, but the design depends on them
existing. Knowing what's there lets us avoid duplicating work
the CCU already does.

### `persistence.hmip` (HmIP MASTER + LINK)

The CCU holds the entire MASTER paramset for every paired HmIP
device in a file-backed map (`persistence.hmip`). The file is
reloaded on CCU boot and updated on every `putParamset(MASTER)`.
`getParamset(MASTER)` and `getParamset(LINK, peer)` read from this
map without consulting the device.

**Implication:** boot can fetch MASTER for every channel without
costing the radio anything. We do exactly that.

### CCU's in-memory value DB

Populated by push events the devices send when state changes. The
CCU surfaces it via the ReGa script `fetch_all_device_data.fn` and
indirectly via `Device.listAllDetail`. **It is NOT consulted by
`getValue`** — `getValue` always queries the device through the
RF module.

**Implication:** we get the CCU's value cache "for free" via the
`fetch_all_device_data` boot path; we never call `getValue` to
read it.

### BidCos-RF differences

BidCos-RF stores configuration on the device itself, not in a CCU
file. `getParamset(MASTER)` on a BidCos channel typically goes
through the RF module; same for `getParamset(LINK)`. This is one
reason OpenCCU-Loom targets HmIP first and treats BidCos-RF as a
secondary path.

---

## 7. RPC method radio costs

Summarised across §6 and the daemon's call sites:

| RPC method | Backend path | Radio? |
|---|---|---|
| `listDevices` | JSON-RPC over CCU device DB | **No** |
| `getParamsetDescription` (any paramset) | metadata table lookup | **No** |
| `getParamset(MASTER)` | `persistence.hmip` file cache | **No** (HmIP); may radio on BidCos |
| `getParamset(VALUES)` | KCommunicator → RF | **Yes** |
| `getParamset(LINK, peer)` | `persistence.hmip` file cache | **No** (HmIP); **Yes** (BidCos-RF — link data lives in the device) |
| `getValue(VALUES, dp)` | KCommunicator → RF | **Yes** |
| `setValue` / `putParamset` | RF → device | **Yes** (unavoidable — the daemon is commanding) |
| `fetch_all_device_data.fn` (ReGa script) | reads CCU's value DB | **No** (returns only what the CCU already learned via push) |
| Push callback CCU → daemon (XML-RPC reverse) | RF → CCU → daemon | **No** (CCU emitted the radio itself; the daemon is a passive consumer) |
| `ping` health probe | XML-RPC handshake | **No** (service health only) |

The "yes" rows are the ones an operator pays for. The bridge
deliberately makes "no" the default for every path that runs more
than a few times per restart.

### What "VALUES is radio" actually means

VALUES is the live state: switch position, current setpoint, last
RSSI sample, button last-press. The CCU holds an in-memory cache
populated by push events, but `getValue` / `getParamset(VALUES)`
from XML-RPC do NOT consult that cache — they go through
KCommunicator → RF → device → RF → KCommunicator → response.
Every call is one round-trip on the radio. A non-trivial fleet
(~120 devices, ~5 readable DPs each) is 600 calls and pushes the
CCU into the DutyCycle warning band within seconds.

---

## 8. Boot scenarios

Four corners; the operator-relevant one is A. The matrix axes are
"CCU warm vs cold" × "daemon has persistent cache vs not."

### A. Warm CCU + daemon with persistent VALUES cache (the normal case)

The CCU has been running long enough to have observed every
device at least once; the daemon's `var/openccu-loom.db` carries a
snapshot of the last-known VALUES state.

```
boot:
  listDevices                                   no radio
  getParamsetDescription × all channels         no radio (metadata)
  getParamset(MASTER) × all channels            no radio (file cache)
  persistent VALUES cache restore               no radio (SQLite read)
  seedRelevantInitParameters                    no radio (cache covers
                                                          UNREACH /
                                                          STICKY_UNREACH /
                                                          CONFIG_PENDING)
  seedReadableEvents                            no radio (cache covers
                                                          last button press)
  fetch_all_device_data.fn                      no radio (CCU value DB)
daemon.ready                                    0 getValue calls
```

Verified on a 124-device HmIP-RF fleet: zero `getValue` radio
calls between `wire.ingest.ok` and `daemon.ready`. The 462
`getParamset(MASTER)` calls during the same window are all
file-cache hits.

### B. Warm CCU + daemon without cache (fresh install or `var/` wiped)

Persistent VALUES cache is empty; the daemon must populate it
from push events + the bootstrap whitelist.

```
boot:
  listDevices                                   no radio
  getParamset(MASTER)                           no radio (file cache)
  seedRelevantInitParameters                    getValue × 3 × N devices
                                                (~hundreds), most hit
                                                CCU's internal value DB,
                                                so radio is bounded to
                                                the fraction CCU has no
                                                current value for
  seedReadableEvents                            getValue × every readable
                                                event DP, similar mix
daemon.ready                                    ~100–500 getValue calls
                                                most cache-served by CCU
```

DutyCycle ticks up but stays below the warning band on a healthy
CCU. The persistent VALUES cache fills during steady-state from
push events, and the second restart drops into scenario A.

### C. Cold CCU (just rebooted) + daemon with cache

The CCU has just rebooted — its in-memory value DB is empty, but
`persistence.hmip` survives the reboot. The daemon's persistent
VALUES cache is also intact (from the last graceful shutdown).

```
boot:
  getParamset(MASTER)                           no radio (file cache,
                                                          survives reboot)
  persistent VALUES cache restore               no radio
daemon.ready                                    0 getValue calls
```

The CCU is independently re-polling devices over the radio to
rebuild its own value DB, so the operator sees DutyCycle activity
in the CCU UI — but that is CCU work, not daemon-induced. The
push-event firehose to the daemon resumes within seconds to
minutes as devices re-announce.

**Operator note:** if you intentionally rebooted the CCU, expect
a DutyCycle spike for ~5–15 minutes that is independent of the
daemon. Don't restart the daemon during that window unless you
want to drop into scenario D.

### D. Cold CCU + daemon without cache (worst case)

Both sides are cold. The bootstrap whitelist fires while the CCU
itself has no value for the queried parameter — every `getValue`
the daemon issues triggers a CCU-side radio call to fetch fresh.

```
boot:
  getParamset(MASTER)                           no radio
  seedRelevantInitParameters                    getValue × 3N, mostly
                                                radio (CCU has no value)
  seedReadableEvents                            getValue × event DPs,
                                                same
daemon.ready                                    DutyCycle warning likely
```

**Avoid this.** If you must wipe `var/`, wait until the CCU has
been up for at least a minute before starting the daemon so its
own value DB is warm.

---

## 9. Lazy paths — never pre-loaded

Two whole data classes are loaded only when something actually
asks:

- **LINK paramset** — fetched on demand for the configuration UI
  (one channel, one peer). Boot, ingest, Matter wiring, and MQTT
  discovery never touch it. Matches the reference design's lazy
  fetch.
- **MASTER paramset for hidden parameters** — the boot pass only
  loads MASTER for channels that surface to a north-bound
  consumer. Hidden parameters stay un-fetched until a UI
  inspector requests them.

---

## 10. Steady-state radio paths

Once `daemon.ready` is signalled, the only daemon-issued radio
traffic is:

| Trigger | RPC | Frequency |
|---|---|---|
| Operator clicks "turn on" in SPA / sends MQTT command / REST PUT | `setValue` | Per user action |
| Operator edits a MASTER paramset | `putParamset(MASTER)` | Per user action |
| Operator edits a LINK paramset | `putParamset(LINK)` | Per user action |
| REST / MQTT consumer requests `?fresh=true` on a VALUES DP | `getValue` | Per request |
| Type-mismatch self-heal on a push event | `getValue` (one DP) | Rare |
| Connectivity probe | `ping` | Every 15 s — but `ping` is no radio, only XML-RPC service health |

Push events flow into the daemon for free (no daemon-induced
radio). The CCU emits them at the device's own discretion.

---

## 11. How the boot was actually verified

Boot getValue counts on a 124-device HmIP-RF fleet, scenarios A
and C (warm CCU + persistent cache, daemon restart only):

| Build | getValue (radio) | getParamset(MASTER) (file cache) |
|---|---|---|
| pre-fix (per-DP LoadValue in EventBridge + LoadAllValues in Matter init) | 2894 | 462 |
| EventBridge fix only | 334 | 462 |
| EventBridge + Matter `LoadAllValues` removal | **0** | 462 |

The 462 file-cache reads are non-negotiable — they populate
MASTER for every channel that has a UI-visible parameter, and
they cost the CCU nothing.

---

## 12. Tuning levers

There are very few; this is intentional.

- **Persistent VALUES cache flush interval**
  (`persistence.values_cache.flush_interval`, default 60 s) — trades
  crash-window data loss against SQLite write rate. Smaller window
  = more writes; larger window = more potential loss on `kill -9`.
- **Persistent VALUES cache disable list**
  (`persistence.values_cache.disabled_centrals`) — turns the cache off
  for a named central. Should only be used to debug a corrupted
  snapshot.
- **`?fresh=true` REST / MQTT modifier** — explicit operator opt-in
  to a single radio call. Use sparingly.

There is no "preload everything on boot" knob and there will not
be one. The whole design is built around "lazy where possible,
push everywhere else."
