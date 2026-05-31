# Sensor + Actor Tile Concept — Übersicht for CDP-less devices

## Status

**Superseded by [`auto-tile-concept.md`](./auto-tile-concept.md).**
All three phases of this concept shipped between 2026-05-23 and
2026-05-24. The successor concept (AutoTile) generalised the
composition logic and replaced `SensorActorTile.svelte`; the
`primary.ts` and `classify.ts` helpers documented here remain in
place as internals of the AutoTile composer. This doc is kept as
historical context for the design decisions; see
`auto-tile-concept.md` for the active surface.

## Context

The Übersicht tab of the device-detail surface today routes devices
through one of two paths:

- **Devices with a Custom-DP (CDP)** — light, cover, climate, lock,
  switch, valve, … — render one CDP tile per CDP via the dispatcher
  in `assets/ui/src/lib/cdp/dispatch.ts`. See
  [`cdp-widget-concept.md`](./cdp-widget-concept.md) and
  [ADR 0016](../adr/0016-custom-dp-aware-ui-rendering.md).
- **Devices without a CDP** — pure sensors (HmIP-SWDO, HmIP-SMI,
  HmIP-SLO, HmIP-STHO …) and some niche actors — fall through to the
  "orphan" path in `CdpTilesPanel.svelte`. Each channel becomes
  either an `actorOrphans` → `ChannelControl` tile (writable actor,
  routed via the control-widget tree
  ([`control-widget-concept.md`](./control-widget-concept.md))) or a
  `statusOrphans` → `ChannelStatusBadge` (read-only).

The orphan path works channel-by-channel and treats every read-only
sensor channel as one one-line status badge. That collapses too
much information for devices whose primary channel carries multiple
related DPs.

## Problem — concrete examples

### HmIP-SMI (motion sensor)

Channel 1 carries six data points; today the Übersicht renders one
status badge showing only the primary motion bool.

| DP | Operations | Role today | Lost in current UI |
|---|---|---|---|
| `MOTION` | read + event | Surfaced as the badge value | — |
| `CURRENT_ILLUMINATION` | read + event | Hidden | secondary readout |
| `ILLUMINATION` | read + event | Hidden | secondary readout |
| `MOTION_DETECTION_ACTIVE` | read + write + event | Hidden | useful setting toggle |
| `ON_TIME` | write only | Hidden | command-with-value (how long the motion alert stays) |
| `RESET_MOTION` | write only | Hidden | useful action button |

### HmIP-SWDO (window/door contact)

Channel 1 has a single read-only `STATE` — fits the existing
badge model; no aggregation needed.

### HmIP-SLO (lux sensor)

Channel 1 is `BRIGHTNESS_TRANSMITTER` with `CURRENT_ILLUMINATION` +
`ILLUMINATION` (raw + averaged). Today: one badge with one value.
Both readouts deserve surfacing.

### HmIP-STHO (climate sensor)

Channel 1 is `CLIMATE_TRANSCEIVER` with `ACTUAL_TEMPERATURE` +
`HUMIDITY` + `VAPOR_CONCENTRATION` + maintenance flags. Today: one
badge. Two valuable readouts (temperature + humidity) get
collapsed.

### HmIP-SMSO (motion outdoor) — similar to SMI

Same pattern: brightness + motion + activation flag + reset.

The pattern is broad — it is not an HmIP-SMI-specific shortcoming.
Every multi-DP sensor or actor channel without a CDP suffers.

## Goals

1. **One device-level tile** for every CDP-less device that
   aggregates the related DPs of its primary channel into one
   visual unit.
2. **Surface secondary readouts** as inline sub-values.
3. **Surface settings** (read+write toggles + numerics) as
   compact controls within the tile.
4. **Surface actions** (write-only triggers + commands-with-value)
   as buttons or short-tap controls.
5. **Stay data-driven** — no per-device-type hand-coded widget
   files. The classification reads off the existing DP descriptor
   (Operations, Category, Parameter name) and works for any future
   device the upstream registry adds.
6. **Coexist with the CDP layer** — devices with a CDP keep their
   tile, unchanged. The new tile only fires for the orphan path.

## Non-goals

- A general-purpose form builder for the Konfigurieren tab (MASTER
  paramset). That remains the `UISchemaAdapter` job.
- Rendering every DP. The new tile is deliberately a summary; deep
  diagnostics stay on the Kanäle / Diagnose tab.
- Per-device wrappers. Going down that road yields hundreds of
  custom layouts that drift with every aiohomematic upstream bump.

## Design

### DP classification roles

Each DP on the primary channel falls into one of six roles. The
classification is a pure function of `(Operations, Category,
Parameter)`:

| Role | How to detect | Example DPs |
|---|---|---|
| **Primary state** | First DP matching channel's category-implied "headline" parameter. Heuristic list below. | `MOTION`, `STATE`, `ACTUAL_TEMPERATURE`, `CURRENT_ILLUMINATION`, `PRESENCE` |
| **Secondary readout** | `read` + `event`, not write, not the primary. | `ILLUMINATION`, `HUMIDITY`, `VAPOR_CONCENTRATION`, `ENERGY_COUNTER` (when not Custom) |
| **Setting (toggle)** | `read` + `write`, type `bool`. | `MOTION_DETECTION_ACTIVE`, `INHIBIT`, `OPERATING_MODE` (when bool) |
| **Setting (numeric)** | `read` + `write`, type number, has min/max. | `SENSITIVITY`, `MIN_HEATING_TIME`, `EVENT_DELAY` |
| **Action** | `write` only, no value (TYPE=ACTION). | `RESET_MOTION`, `PRESS_SHORT`, `STOP` |
| **Action with value** | `write` only, value-typed (number / float / string). | `ON_TIME`, `BOOST_TIME_PERIOD`, `INSTALL_TEST` |

### Primary-DP heuristic

Pick the primary state DP in this order:

1. **Channel-type → parameter map** — explicit table for known
   sensor categories. Initial scope:
   ```
   MOTIONDETECTOR_TRANSCEIVER → MOTION
   PRESENCEDETECTOR           → PRESENCE_DETECTION_STATE
   SHUTTER_CONTACT            → STATE
   TILT_SENSOR                → STATE
   SMOKE_DETECTOR             → SMOKE_DETECTOR_ALARM_STATUS
   WATER_DETECTOR             → ALARMSTATE
   BRIGHTNESS_TRANSMITTER     → CURRENT_ILLUMINATION
   CLIMATE_TRANSCEIVER        → ACTUAL_TEMPERATURE
   WEATHER                    → ACTUAL_TEMPERATURE
   ```
2. **First read+event DP** of any non-MAINTENANCE category — used
   as fallback when the channel type is unknown.
3. **First read+event DP overall** — last-resort safety net.

The table extends with new channel types when the upstream registry
adds them; small enough to maintain in `quickcontrol/primary.ts`.

### Tile composition

```
┌────────────────────────────────────────────────────┐
│ <icon>  <device name>                  <reach·age> │
│         <channel name / sub-device>                │
├────────────────────────────────────────────────────┤
│  ╳  Bewegung erkannt                               │ ← primary state
│      vor 3 min                                     │
│                                                    │
│  ☀ 124 lx    📊 84 lx (Ø)                          │ ← secondary readouts
├────────────────────────────────────────────────────┤
│  [⏻ Aktiv]   [⟳ Zurücksetzen]   [⏱ 60 s an…]      │ ← settings + actions
└────────────────────────────────────────────────────┘
```

Section rules:

- **Header** — device + sub-device label + last-seen freshness
  (already in `ChannelStatusBadge` via the cache lifecycle stamp).
- **Primary row** — icon + value + age. Bool maps to icon + label
  ("Bewegung erkannt" / "Ruhig"); number maps to value + unit.
- **Secondary row** — at most three readouts inline; everything
  beyond goes to the disclosure ("mehr…"). Order: numeric
  measurements first, booleans after.
- **Setting/action row** — toggles are pill switches; actions are
  buttons; commands-with-value open a small inline dialog (the
  same `NumericInputFeature` primitive the control-widget tree
  uses).

### Multi-channel devices

HmIP-SMI exposes three meaningful channels (1: motion, 2:
state-reset receiver, 3: alarm-condition transmitter). The
secondary channels are either virtual receivers driven by linked
actors (CCU-internal use, no operator UX value) or alarm-output
channels mirrored elsewhere.

**Decision (B):** Single device tile with a collapsed disclosure
footer for secondary channels.

The tile renders the primary channel as the main surface and
shows a "weitere Kanäle (N)" footer when secondary channels
exist. Tap expands inline; the expanded section reuses the
existing `ChannelStatusBadge` / `ChannelControl` widgets so the
look stays consistent with the rest of the SPA. Default state
is collapsed so the grid layout stays tight; users who need the
secondary surface (e.g. wiring an alarm channel) don't have to
context-switch to the Kanäle tab.

### Where it slots into the SPA

```
DeviceDetail.svelte
  ├─ CdpTilesPanel.svelte                 ← orchestrator
  │    ├─ renderable CDPs ──────────────► CDP tiles (existing)
  │    ├─ orphan channels grouped by device:
  │    │    ├─ device has primary channel ─► SensorActorTile.svelte  ← NEW
  │    │    └─ device has only status     ─► ChannelStatusBadge (existing)
  │    └─ unresolved fallback             ─► ChannelControl (existing)
```

`SensorActorTile.svelte` is the new component. It loads its data
via the existing `api.fetchDataPoints(channel)` call and uses the
`ChannelControl` primitives (`ToggleFeature`, `NumericInputFeature`,
`ActionFeature`) so the look-and-feel matches the rest of the SPA.

### Backend assumptions — no new endpoints

Everything the tile needs is already on the wire:

- DP list + descriptor (operations, type, min/max, unit) →
  `GET /api/v1/devices/{addr}/channels/{n}/data-points`
- DP value + lifecycle (live/stale/cache) → same payload
- Write path (action / toggle / set-with-value) → existing
  `PUT /api/v1/devices/{addr}/channels/{n}/values/{name}` and
  the value-cache-aware `/values/{name}/action` if added later

No backend changes are part of this concept. If the heuristic
ever needs a hint the descriptor doesn't carry, the next step is
adding an OCCU-side `Control.` annotation propagation, not a
parallel REST endpoint.

## Trade-offs

### Heuristic vs. per-device whitelist

The CDP layer is a whitelist (per-device-type wrappers). For the
sensor + actor tile, a whitelist would be on the order of
~200 entries (every model without a CDP). That doubles the
maintenance burden of the device-profile registry without buying
much: most "primary DP" choices are obvious from the channel type
alone.

The data-driven heuristic (channel-type → parameter table + DP
operations) misclassifies maybe 1–2 % of channels. We catch those
with the small override table and move on.

### Render-all vs. summarise

Showing every DP of every orphan channel is what the Kanäle tab
already does. The Übersicht tab's purpose is "one tile per
controllable thing"; staying summary-first keeps that promise.
Power users move to Kanäle.

### Layout density

Three lines (primary / secondary / actions) is the upper bound
for the Übersicht grid. Tiles taller than that break the CDP grid
visually. If a device legitimately needs four rows we either drop
the secondary readout entirely or hide some actions behind an
overflow disclosure. We do NOT grow the tile.

## Rollout plan (when approved)

1. **Phase 1 — primary + secondary readouts only.** New
   `SensorActorTile.svelte` reads a channel, renders header +
   primary + up to three secondary readouts. No settings, no
   actions. Replaces the status-badge path for any channel that
   has more than one readable DP.
2. **Phase 2 — settings.** Add the read+write toggle / numeric
   handling.
3. **Phase 3 — actions.** Add the write-only action + action-with-
   value handling.

Each phase is independently shippable; phase 1 alone closes the
"HmIP-SMI shows only motion, not brightness" gap.

## Resolved decisions (wizard, 2026-05-23)

1. **Multi-channel default — B (Disclosure-Footer).** Primary
   channel renders as the tile body; a collapsed footer "weitere
   Kanäle (N)" expands inline on tap to surface secondary channels
   via the existing `ChannelStatusBadge` / `ChannelControl`
   widgets.
2. **Settings + actions placement — Inline row.** Read+write
   toggles and write-only actions render as a visible row below
   the readouts. No overflow menu; everything operator-relevant
   stays one tap away. Tile gains one row of vertical space.
3. **Action feedback — Pulse + Toast on failure only.** Successful
   write: button transitions `⏳ → ✔ → idle` (~1.2 s total), no
   toast. Failed write: button returns to idle with a red flash,
   AND a toast surfaces the error (`⚠ 'Reset fehlgeschlagen:
   timeout'`). Silent success, loud failure.
4. **`ON_TIME` and similar duration commands — Number input +
   Send button.** Tap on `[⏱ Timer]` expands an inline editor
   with a free numeric input (min/max from the DP descriptor) and
   an explicit Send. Two interactions, but precise and self-
   documenting. No preset pills.
5. **MASTER-paramset DPs in tile — Strictly VALUES.** The
   Übersicht tile is pure runtime operation; MASTER paramset DPs
   (`CYCLIC_INFO_MSG`, `GLOBAL_BUTTON_LOCK`, `EVENT_DELAY`, …) stay
   in the Konfigurieren tab. Clean separation of "Bedienung" vs
   "Konfiguration."

The implementation plan in §Rollout (Phase 1 → 2 → 3) reflects
these decisions. Phase 1 (primary + secondary readouts) does not
yet touch the action / settings row and is the next code step.
