# openccu-lite — the ReGa-less CCU, its metadata API, and what it costs us

Research record for a third-party project that changes an assumption
OpenCCU-Loom has held since day one: that a Homematic central runs ReGaHSS.

**Date of record:** 2026-09-06.
**Status of the subject:** announced, not released. No public repository at
the time of writing; a first beta was announced for September 2026.

Everything below is split into what was **measured** in this repository and
what is **reported** by the project's author. The two are never mixed, because
only the first half is checkable today.

---

## 1. What openccu-lite is

*Reported — source: the announcement thread, see [Sources](#7-sources).*

A fork of OpenCCU (the successor to RaspberryMatic) with **ReGaHSS and the
eQ-3 WebUI removed**. What remains is the interface layer: `rfd` (BidCos-RF),
`hs485d` (BidCos-Wired), the HmIP server, `multimacd`, `eq3configd`, device
firmware updates, radio modules, LAN gateways, backup and restore — all
unchanged from the OpenCCU build, same recovery system, same partitioning.

Added on top: `occulited`, a single Go binary (~11 MB, ~9 MB RSS idle) with a
small web UI for what one actually configures on a central — network,
firewall, time, radio modules, security keys, LAN gateways, services, log,
addons, backup, system update. Service management moves to systemd + journald;
every addon gets a generated unit, so stop is reliable, crashes restart, and
logs survive a reboot. `logind`, `resolved`, `networkd` and `timesyncd` are
not included.

Names, rooms and functions move out of ReGa into a metadata store with a
documented API. Device management (pairing, paramsets, direct links) moves out
of the firmware into an addon — Homematic Manager v3.

**Explicit non-goals:** ReGa programs, HM-Script, system variables and alarms
do not exist and no migration is planned. Addons that depend on ReGa — CUxD
above all — are listed as incompatible and are not started.

**Migration path:** the ordinary firmware-update dialog in both directions.
Recovery flashes `boot` and `root` only, `/usr/local` survives, so pairings,
radio configuration, keys and addons carry over. On first boot the metadata
store is seeded by reading `homematic.regadom` (plain XML) directly. Nothing is
ever written back into the ReGa database.

### Why this matters here

The target audience is stated as "users whose logic lives in Node-RED, Home
Assistant, ioBroker, openHAB or their own code, and who want only the gateway
and device management from the central". That is precisely OpenCCU-Loom's
audience. openccu-lite is **not** a competitor to this daemon — it is a
firmware, we are a client — but it removes the interfaces this daemon reads
from, and it removes the WebUI that used to be the operator's fallback. A
central with no ReGa and no WebUI needs a northbound surface more than a stock
one does, not less.

---

## 2. The metadata API

*Reported — the author's own description, quoted structurally rather than
verbatim. Treat every shape here as unverified until a repository exists.*

### 2.1 Data model

Three entities and nothing else.

**Object** — a device or a channel, keyed by `<interface>.<address>`
(`BidCos-RF.ABC1234567:1`). Carries `name`, `enums` (node paths) and a
namespaced `meta` object, one namespace per consumer so that consumers cannot
collide. The store never invents objects: it holds metadata *about* addresses
the interface processes report. An entry whose address disappears is kept and
flagged `orphaned`, so replacing a defective device does not discard its room
assignment.

**Enum** — a named taxonomy. `room`, `function` and `floor` exist by default;
users may create more (`zone`, `tenant`, anything). Deliberately not
hard-coded — ReGa's fixed pair of "rooms and functions" is described as the
limitation worth removing.

**Enum node** — a node in a taxonomy tree: `id`, `name`, optional `icon`,
`children`, and members by object reference. Trees are the point:
`room/eg/wohnzimmer`, `room/og/bad`. Membership attaches at any depth, and a
query for `room/eg` returns the members of the whole subtree.

### 2.2 Storage

One `meta.json`, atomic write, in-memory index, a monotonically increasing
`revision` on every change. Every write returns the new revision; every read
may be conditional on one. A corrupt file falls back to the previous atomic
write and says so loudly rather than starting empty.

### 2.3 API surface

REST + JSON under `/api/meta/v1/`, plus:

| Endpoint | Purpose |
|---|---|
| `GET /api/meta/v1/version` | `{"api":"meta","version":1,…}`. **No auth.** This is the feature probe — a CCU answers 404 or HTML. |
| `GET /api/meta/v1/snapshot` | objects + enums + revision in one document; the startup read |
| `GET /api/meta/v1/events/sse` | Server-Sent Events, one JSON event per message; `?since=<revision>` replays, `{"kind":"resync"}` means re-fetch the snapshot |
| `GET /api/meta/v1/export?format=yaml` + `PUT` | whole-configuration import/export, for git-tracked setups |
| optional MQTT publication | mqtt-smarthome style, off by default, pointed at the user's broker |

Event kinds: `object.updated`, `object.deleted`, `node.created`,
`node.updated`, `node.deleted`, `node.moved`, `enum.created`, `enum.updated`,
`enum.deleted`, `import`.

A WebSocket change stream is named in the prose alongside SSE, but only the SSE
path is given in the porting reference. **Open question — the WS path is
undocumented.**

### 2.4 Authentication

`Authorization: Bearer <credential>` on everything except `/version`. The
credential is either the user's session id (a settings page receives it as
`?sid=@xxxxxxxxxx@`) or an API token `olt_<32 hex>`.

- **On the box:** read from `/usr/local/etc/occulite/local-token`, one line,
  role `user` = read-only.
- **Off the box:** the user pastes a token created on the box's Users page into
  the consumer's configuration.

There are no numeric ReGa ids anywhere. Refs (`<interface>.<address>`) are the
only identity.

### 2.5 What it is not

Quoted, because the boundary is the useful part: *"not a value store, not a
time series, not a device registry, not a home for paramsets. Names and
taxonomy."*

This is the single most consequential sentence in the whole description for
this project — see §4.4.

### 2.6 Porting guidance the author supplies

A porting prompt accompanies the API, addressed at addon maintainers. Its
invariants map onto this codebase almost unchanged:

1. **The existing ReGa path stays.** Add a provider, delete nothing. Behaviour
   on a stock CCU must be identical before and after.
2. **Detection is at runtime**, via `GET /api/meta/v1/version`, once at start
   and again on reconnect. No new mandatory configuration.
3. **The data shape consumers emit does not change.** Rooms and functions that
   were arrays of names stay arrays of names; build them from the tree.
4. **No user configuration may break.** ReGa-only options stay accepted, log
   one line, and are otherwise silent.
5. **Missing credential degrades, it does not fail.** `401` → run on addresses
   only, log once, retry on reconnect.
6. Keep the addon settings-page session check as it is — a `tclrega.so` shim
   answers exactly that call.
7. Do not add an HTTP or SSE dependency when the runtime already has one
   (`net/http` for Go).

Conformance fixtures are promised as `store/*.json` in the openccu-lite
repository, described as the documents the API actually serves.

---

## 3. How OpenCCU-Loom depends on ReGa today

*Measured in this repository on 2026-09-06, at `main`. Every claim below is a
file the reader can open.*

### 3.1 The ReGa surface

| Surface | Where | Size |
|---|---|---|
| ReGa scripts | `internal/client/rega/scripts/` | **36 scripts** |
| ReGa script call sites | `internal/central/adapter/` | **19** — 18 in `hub_wiring.go`, 1 in `device_pipeline.go` |
| JSON-RPC methods in use | `internal/client/` | **48** |
| Readiness marker | `internal/central/adapter/ccu_readiness.go:24` (`checkRegaPath`) | 1 constant, 6 call sites in `ccu_wiring.go` / `cuxd_wiring.go` |

The three calls that carry names, rooms and functions:

- `Device.listAllDetail` — device and channel names
- `Room.getAll` + `Room.getChannelIDs` — rooms
- `Subsection.getAll` — functions

The Python reference family reads the same three. `aiohomematic` declares them
at `aiohomematic/client/json_rpc.py:326,347,351` — two independent integrations
converged on the identical surface, which is what makes the question to the
author worth asking on behalf of both.

### 3.2 What survives on a ReGa-less box

XML-RPC against `rfd`, `hs485d` and the HmIP server is untouched. That is the
southbound core, and it is the larger half.

### 3.3 What the architecture already provides

The backend layer anticipated this case:

- `backends.Kind` — three values today (`KindCCU`, `KindCUxD`, `KindHomegear`),
  `capabilities.go:13`
- `backends.Capabilities` — 32 flags, `capabilities.go:35`
- `CapabilityFor(kind)` — one literal per kind, `capabilities.go:176`
- `FactoryWithKind` — one case per kind, `factory.go:33`
- `HomegearBackend` — **574 LOC, already a ReGa-less backend.** XML-RPC only;
  programs, rooms, functions, backup, install mode and firmware update return
  `ErrUnsupported`. This is the precedent and the size anchor.

Gate distribution, measured:

- **28** capability gates inside `internal/client`
- **2** outside it (`central/adapter/device_pipeline.go` → `RPCCallback`,
  `central/central.go` → `PingPong`)
- **21** `ErrUnsupported` handlers across 13 files in `internal/north/`

The asymmetry is the finding: the client layer degrades on capabilities, the
northbound layer largely does not.

### 3.4 The model already has the right shape

`Channel.Rooms() []string` (`internal/model/device/channel_set.go:186`) is a
flat list of names, per channel. That is exactly the shape the author's
invariant 3 prescribes — flatten the tree on read, and the domain model needs
no change at all.

`IseIDLookup` is already a capability flag (`capabilities.go:173`) with the
lookup gated at `interface_client_orchestration.go:639`. "No numeric ids" costs
one `false` in a literal, not a refactor.

`WithValuesCacheStore` (`device_pipeline.go:345`, restore pass at `:2224`)
already replays the last known wire values per channel at boot, before the seed
runs.

### 3.5 The CCU addon does not touch ReGa

`packaging/ccu-addon/` ships `rc.d`, `update_script`, two CGIs and a monit
config. `update_script:118` states it directly: *"Nothing here needs a
boot-time hook (no ReGa patch, …)"*. No `tclrega`, no system-variable use, no
ReGa DOM.

By the author's description of the addon mechanism — `rc.d`, `update_script`,
`hm_addons.cfg` all preserved — this addon is a candidate for the openccu-lite
catalogue essentially as-is, once a lite backend exists. The monit config
becomes inert (systemd generates the unit) but harmless.

---

## 4. What a lite backend would cost

*Inferred. An estimate, not a measurement — the size of §4.3 rests on an API
that has no public implementation yet.*

### 4.1 Cheap, because Homegear paved it

`KindLite` + `String()` + capability literal + factory case. The 28 in-client
gates then apply automatically.

### 4.2 Small but not delegable

The readiness marker. `checkRegaPath` is a constant consumed at six sites; it
has to become kind-dependent. Composition-root adjacent.

Detection improves rather than costs: `detection.go:68` says of the existing
prober *"nothing outside this package's tests calls DetectBackend"*, and the
comment goes on to show both discriminators are unsound. `GET
/api/meta/v1/version` against a CCU's 404/HTML is the first sound discriminator
available — cheaper *and* better than what is there.

### 4.3 The real implementation block

The metadata provider: HTTP client against `/snapshot`, SSE follower with
revision tracking, `resync` handling, flattening of enum trees into the flat
name arrays the model already speaks. Size comparable to the JSON-RPC half of
`ccu_extended.go` (1152 LOC), likely less, because bulk-snapshot + event stream
is a friendlier contract than 48 individual JSON-RPC methods.

One concrete side cost: the token is a `cfg:"secret"` field, so the masked
secret rules apply — `restoreMaskedSecrets` before validation, SPA
serialisation skipping secret-class fields, plus `config.field.*` and
`config.help.*` in both catalogues.

### 4.4 The one thing with no replacement

`seedValues` (`device_pipeline.go:1296`) fetches every current value in a
**single** ReGa call. The metadata API explicitly is not a value store, so
there is no counterpart. The fallback is `getParamset(VALUES)` per channel —
the radio-cost question from `docs/caching.md`.

Mitigated, not solved, by the values cache (§3.4): boot shows the last known
state immediately, and the refresh cost is spread rather than paid in one
burst. This is a design decision, not typing, and it is the only item here that
deserves a plan of its own.

### 4.5 The block that dominates

Northbound degradation. With 2 capability gates outside `internal/client` and
18 ReGa script call sites in `hub_wiring.go` alone, the system-variable,
program, alarm and hub surfaces currently assume ReGa exists. On a lite box
they must **degrade, not fail**: views hidden through the surface registry,
MQTT discovery not declaring hub entities at all (declaring and not publishing
breaks the `Test*PlaneTopicsRoundTrip` rule), alarm subsystem off.

Broad and shallow, which is why it is the block most likely to be
under-estimated.

### 4.6 Rough size

| Block | Estimate |
|---|---|
| Kind + profile + factory + readiness + explicit config switch | small — largely delegable |
| Lite backend + metadata provider | medium — one slice once the API is public |
| Northbound degradation + guards | **large — the actual slice** |
| Value-seed replacement | open — a design question |

Two to three slices, weighted toward the third block. Explicitly an estimate.

---

## 5. Open questions and risks

**`floor` is a default enum.** `room`, `function` and `floor` ship out of the
box, plus arbitrary user taxonomies. This daemon models rooms and functions
only. A lite user therefore gets a floor assignment that we would silently
discard. A product decision, and it should be taken deliberately rather than
disappear inside the tree-flattening step.

**`orphaned` objects must not materialise devices.** The store keeps metadata
for addresses that no longer exist. The device model comes from `listDevices`
and must continue to. A named orphan that creates a phantom device would be a
quiet, ugly defect.

**Two naming authorities.** ADR-level decision here (0.45.0) makes the daemon
the sole naming authority; openccu-lite makes its metadata store the sole
naming authority for the box. The per-consumer `meta` namespace is a mechanism
that could carry our side, but `name` itself remains a single field owned by
the store. Unresolved.

**MQTT overlap.** openccu-lite can optionally publish names and taxonomy to a
broker in mqtt-smarthome style. Different topics and different purpose from our
discovery planes, so not a conflict — but on a lite box two things would be
publishing to the same broker, and the operator has to be able to tell them
apart.

**The wider strategic signal is not lite.** In the same thread the OpenCCU
maintainer states an intent to make MQTT support an integral part of mainline
OpenCCU rather than a manually installed addon. That touches this daemon's MQTT
plane more directly than openccu-lite does, because it concerns the firmware
most users actually run. Not actionable yet; worth watching.

---

## 6. Position

openccu-lite is not a replacement for this daemon. It is a firmware; we are a
client. It ships no MQTT HA-Discovery, no REST/WS API, no Matter bridge, no
MCP, no multi-CCU, and no device model — and its author points users at exactly
those capabilities in other projects.

The genuine overlaps are two: device management (Homematic Manager v3 against
the same XML-RPC methods — duplicated but harmless) and naming authority (a
real collision, §5).

The useful framing: a central without ReGa and without the eQ-3 WebUI has lost
both its logic engine and its operator fallback. That makes a northbound daemon
more necessary there, not less. A version of this daemon that runs cleanly on
openccu-lite is the most natural catalogue entry that project can have, and
`packaging/ccu-addon/` is already ReGa-free (§3.5).

---

## 7. Sources

- Announcement thread, HomeMatic-Forum, forum 56, topic 88560:
  <https://homematic-forum.de/forum/viewtopic.php?f=56&t=88560> — opening post
  by the openccu-lite author, replies by the OpenCCU maintainer.
- The data-model and porting-prompt text in §2 was supplied by the author as a
  follow-up to a question posted in that thread. It is a description, not a
  published artefact; no repository, no `docs/meta-api.md` and no
  `fixtures/store/*.json` were available for inspection at the time of record.
- Everything in §3 was measured in this repository at `main`, 2026-09-06.

### Verification status

| Claim class | Status |
|---|---|
| §1, §2 — openccu-lite's design, API, invariants | **unverified.** Author's description. No code has been read. |
| §3 — this repository's ReGa coupling, sizes, gate counts | **measured.** Cited by file and line. |
| §4 — effort | **inferred**, and partly resting on §2. |
| §5 — risks | **inferred.** |

The honest limit: no measurement of openccu-lite is performable until its
repository is public. Nothing in §2 should be treated as a contract, and no
implementation work should be started against it — the first sound step is
reading `fixtures/store/*.json` and `docs/meta-api.md` when they exist.
