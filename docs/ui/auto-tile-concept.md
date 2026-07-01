# AutoTile Concept — Generalised UI for Unknown Devices

## Status

**Concept approved 2026-05-24 via wizard. Phases 1–4 shipped**: the
Go-side quantity catalogue (`pkg/hmui/quantity.go`), the
`DataPointSummary` `min`/`max`/`default`/`ui_hint` extension,
`composer.ts`, `AutoTile.svelte` + the readout primitives, the
dispatcher integration in `$lib/cdp/dispatch.ts` /
`$lib/cdp/CdpTilesPanel.svelte`, and the `SensorActorTile` retirement
are all in place — do not re-implement the engine described below.
The one remaining piece from the original rollout, a fleet-wide
**Overview** route that lays these same tiles out across every device
(roadmap B8), has since shipped too
(`assets/ui/src/routes/Overview.svelte` +
`assets/ui/src/lib/cdp/ChannelTiles.svelte`,
`assets/ui/src/lib/overview/overview-grouping.ts`). See
[`docs/roadmap.md`](../roadmap.md) for what is still open elsewhere in
the SPA.

Goal: every device the CCU registry adds
in the future renders as a coherent tile in the Übersicht (and a
sensible widget in the Kanäle view) without per-device Svelte code.
Today's three concept docs ([CDP](./cdp-widget-concept.md),
[Control](./control-widget-concept.md),
[Sensor+Actor](./sensor-actor-tile-concept.md)) describe per-kind /
per-family / per-channel-type code paths. This document proposes the
generalisation that sits *under* all three and catches whatever the
hand-coded layers don't claim.

## Audience

Operators who plug in a brand-new HmIP-XXX device the day after it
ships, contributors adding model coverage upstream, and AI agents
asked "why does my device render as a wall of empty boxes?"

---

## 1. The mental model

Today the SPA renders a tile by walking a *registry of hand-coded
widgets* keyed by CDP-kind or CONTROL-family. A device whose kind +
family is unknown falls all the way through to either the orphan
section (`SensorActorTile`) or the generic `ParameterField` list.

The proposal flips the polarity. Instead of "lookup → custom widget",
the pipeline becomes:

```
DP descriptors → role classification → quantity inference → composer
            ↓                                                  ↓
            └────────── hand-coded widget overrides ───────────┘
                       (CDP kind / CONTROL family)
```

Every channel always lands in the composer. Hand-coded widgets are
*overrides*, not the default. Unknown devices get the composer's
output unchanged — and the composer is rich enough that "unchanged"
looks good.

---

## 2. What the descriptor already tells us

The wire descriptor (`DataPointSummary` in the REST DTO) carries
more semantic structure than today's renderers use. Eleven fields
form a decision matrix:

| Field | Used today | Used by composer |
|---|---|---|
| `parameter` (e.g. `MOTION`) | yes (channel-type table) | yes (quantity hint fallback) |
| `parameter_label` | yes (display) | yes (display) |
| `type` (BOOL / INT / FLOAT / ENUM / ACTION / STRING) | partial | **headline rule** |
| `operations` (read / write / event) | yes (role classification) | yes (role classification) |
| `unit` (°C, %, lx, W, dBm, …) | partial (display) | **icon + colour + formatter key** |
| `value_list` (ENUM labels) | yes (SensorActorTile) | yes |
| `control` (`FAMILY.SLOT`) | yes (Kanäle widget tree) | yes (semantic hint) |
| `min` / `max` | **NOT exposed in REST today** | **required for slider vs stepper vs free input** |
| `value_age_seconds` | yes (badge) | yes (freshness) |
| `source` (live / cache / stale / unobserved) | yes (lifecycle stamp) | yes (visual dim) |
| `category` (state / measurement / button / event) | partial | yes (grouping) |

Three of those are not yet on the wire and need a one-line addition
to the REST handler: `min`, `max`, `default`. Without `min`/`max` we
can't choose between "slider" and "free numeric input" for a
writable number. That's the only backend prerequisite.

---

## 3. Two new SPA layers

### 3.1 `quantity.ts` — what a value *means*

A lookup that, given `(parameter, unit, type, value_list)`, returns:

```ts
type QuantityHint = {
  icon: IconName;            // 🌡 / ☀ / ⚡ / 💧 / 📶 / 🚶 / 🚪 / 🔋 / 🔔 …
  semantic: SemanticKind;    // "temperature" | "illuminance" | "power" | "motion" | "contact" | "battery" | ...
  stateColor?: (v: unknown) => string | undefined;
  format: (v: unknown) => string;       // "21.3 °C" / "124 lx" / "1.4 kW"
  rangeHint?: { min?: number; max?: number };
};
```

Three resolution layers:

1. **By unit** — `°C` → temperature, `lx` → illuminance, `dBm` → signal, `W` / `Wh` → energy. Covers ~70 % of numeric DPs.
2. **By parameter substring** — `MOTION` / `PRESENCE` / `STATE` (when channel is contact) / `RAINING` / `SMOKE` / `SABOTAGE`. Covers most booleans.
3. **By `value_list` shape** — ENUM with `["IDLE_OFF", "PRIMARY_ALARM", ...]` → alarm-state semantic (state-colour red on non-idle).

Anything that doesn't match keeps a neutral 📊 icon and the value
formatter that the type implies.

### 3.2 `composer.ts` — what to *show*

Given a channel's DP list, the composer produces a *layout
description* (no Svelte, pure data):

```ts
type ComposedTile = {
  headline: ReadoutSpec;            // primary state, big
  readouts: ReadoutSpec[];          // secondary measurements, inline
  settings: ControlSpec[];          // read+write DPs
  actions: ActionSpec[];            // write-only DPs
  meta: { freshness: string; reachable: boolean; channel: ChannelSummary };
};
```

The composer walks the channel:

- Picks the **headline** DP via (a) the existing `primary.ts` table,
  then (b) "first DP whose role is a measurement", then (c) "first
  read+event DP".
- Buckets remaining DPs by **quantity semantic**. Two `lx`-typed
  readouts (CURRENT_ILLUMINATION + ILLUMINATION) cluster as one
  "illuminance" pair. Three particulate-matter readings (PM1 / PM2.5 / PM10)
  cluster as a "particles" trio. This produces visual grouping in
  the readouts row.
- Sorts within each bucket by "interest" — recent change > steady
  value > zero / unset.
- Picks the **right control primitive** for each writable DP:
  - bool → TogglePill
  - number with min/max → ControlSlider
  - number without min/max → NumericInputFeature
  - ENUM with ≤ 4 entries → ControlButtonGroup
  - ENUM with > 4 entries → ControlEnumSelect (dropdown)
- Picks the **right action primitive**:
  - ACTION-type → ActionButton (one-tap)
  - write-only number → NumericActionFeature (inline expand + Send)

The output is fully described by data. No Svelte component runs
yet — everything is decided in TypeScript.

### 3.3 `AutoTile.svelte` — what to *render*

A single Svelte component takes a `ComposedTile` and stamps it out
from the existing primitives (`TogglePill`, `ActionButton`,
`NumericActionFeature`, `ControlSlider`, `ControlButton`, `EnumSelect`,
plus four new tiny readouts: `BooleanReadout`, `NumericReadout`,
`EnumReadout`, `StringReadout`).

The hand-coded CDP / CONTROL widgets stay — they're polished cases
where the *semantic operation* (set_color {hue, sat}, set_temperature
{°C}) is curated. They sit in front of `AutoTile` in the dispatcher
pipeline; AutoTile catches everything else.

---

## 4. Dispatcher pipeline (after the change)

```
channel + dps
    │
    ▼
1. Custom-DP kind has a registered tile?      → LightTile / ClimateTile / …
    │ no
    ▼
2. CONTROL family + slots map to a widget?    → Dimmer / Blind / Climate / …
    │ no
    ▼
3. Composer produces a ComposedTile           → AutoTile
    │ fallback
    ▼
4. ParameterField list (diagnostic only)      → never reached in normal use
```

Steps 1 + 2 stay opt-in for polish. Step 3 becomes the new default
ceiling — operators see a coherent tile for any device, including
ones the catalogue doesn't know about yet.

---

## 5. Creative ideas (the "be creative" bucket)

The five items below are concrete enough to scope but require an
explicit UX decision before implementation. Each one is independently
shippable.

### 5.1 Unit-driven icon and colour inference

A 30-row table in `quantity.ts` covers every SI unit that appears in
the HmIP / BidCos descriptor catalogue:

| Unit | Icon | State-colour rule |
|---|---|---|
| `°C` | 🌡 | heat-orange when ≥ 24, cool-blue when ≤ 18 |
| `% rF` | 💧 | yellow when < 30, blue when > 70 |
| `lx` | ☀ | none |
| `W` | ⚡ | red when > 90 % of `max` |
| `Wh` | 🔋 | none (counter) |
| `dBm` | 📶 | red when < −85 |
| `µg/m³` | 🌫 | yellow when > 25 (PM2.5 threshold), red when > 50 |
| `%` | (depends on parameter) | (depends) |
| `s` | ⏱ | none |
| `Hz` | 〰 | none |
| `V` | ⚡ | none |
| `A` | ⚡ | none |

Devices with units we've never seen fall back to 📊. Adding a row is
a one-line change, no Svelte rebuild.

### 5.2 Parameter-substring semantic-hint table

For boolean-typed DPs without a unit, `parameter` is the only signal:

- `*MOTION*` / `PRESENCE*` → 🚶 motion
- `*STATE` on contact-channel-types → 🚪 contact (closed/open)
- `SMOKE*` → 🔥 smoke
- `*ALARM*` → 🔔 alarm
- `RAINING` → 🌧 rain
- `LOWBAT` / `LOW_BAT` → 🪫 battery
- `SABOTAGE` → 🛡 tamper
- `UNREACH` / `STICKY_UNREACH` → 🔌 connectivity

Twenty rows cover every boolean DP the registry has shipped. Same
falsifiability: an unknown parameter gets a neutral on/off icon
without crashing the tile.

### 5.3 Smart bucket grouping for measurements

Three readouts in `µg/m³` (PM1 / PM2.5 / PM10) cluster visually as a
"particulate matter" group. Two in `lx` (CURRENT_ILLUMINATION +
ILLUMINATION) cluster as "illuminance". The composer reads the
quantity hint and groups by `semantic`, then renders each bucket as
a labelled mini-card inside the readouts row instead of three flat
inline entries.

Visual result for HmIP-SFD:

```
┌─ Feinstaubsensor ──────────────────────────────┐
│  🌫 6.9 µg/m³ PM2.5                            │
│      vor 2 min                                 │
│                                                │
│  🌫 Partikelmasse                              │
│      PM1   6.5 µg/m³    PM10  6.9 µg/m³        │
│      Ø 1   4.4 µg/m³    Ø 10  4.7 µg/m³        │
│                                                │
│  🔢 Partikelzahl                               │
│      PM1   52 1/cm³     PM10  52.1 1/cm³       │
│                                                │
│  🌡 27.5 °C   💧 52 % rF   📏 0.48 µm          │
└────────────────────────────────────────────────┘
```

### 5.4 Adaptive density

A 1–2-readout tile renders large (current SensorActorTile look).
A 7+-readout tile (HmIP-SFD) auto-compacts: smaller labels,
two-column grid, no per-DP age stamp (carried only on the headline).
The density rule reads `composed.readouts.length` and emits a
density token (`comfortable` / `compact`) the AutoTile honours via
Tailwind classes.

### 5.5 Live freshness pulse

Every readout shows its `source` lifecycle as a 4-pixel sidebar
stripe:

- `live` (green) — fading every 5 s after the last observed value
- `cache` (amber, slow pulse) — restored from disk, waiting for first push
- `stale` (red, no pulse) — bridge said the value is stale
- `unobserved` (grey, dim) — nothing ever observed

Operators see at a glance which DP went quiet. No extra wire data —
`source` already exists on `DataPointSummary` (ADR 0019).

---

## 6. What stays hand-coded, and why

Three categories keep their bespoke widgets:

1. **CDP-level semantic operations.** `set_color {hue, saturation,
   value}` is *atomic*; the composer can't synthesise a hue wheel +
   saturation slider + value slider that share state correctly
   without knowing the semantic. LightTile owns the wheel.
2. **CONTROL families with slot-pair semantics.** `BLIND.LEVEL +
   LEVEL_SLATS` (tilt) and `HEATING_CONTROL_HMIP.CONTROL_MODE +
   BOOST_MODE + FROST_PROTECTION` need coordinated, single-write
   gestures the composer can't infer.
3. **Schedule / week-profile editors.** Out of scope; live in their
   own routes.

Everything else — sensors, contacts, button events, energy meters,
single-value writable DPs — falls into the composer for free.

---

## 7. Backend ask (one PR, wizard Q1 decision)

The wizard locked in the **hybrid model**: Go is the canonical
classifier; the SPA is a dumb renderer. The descriptor gains four
fields plus a precomputed `ui_hint` envelope:

```diff
   type DataPointSummary struct {
       ...
       Type        string
       ValueList   []string
+      Min         any  `json:"min,omitempty"`
+      Max         any  `json:"max,omitempty"`
+      Default     any  `json:"default,omitempty"`
+      UIHint      *UIHint `json:"ui_hint,omitempty"`
   }

+  // UIHint is the backend's classification of the DP for the
+  // SPA's AutoTile composer. The SPA renders icon + label + format
+  // verbatim; no JS-side inference runs.
+  type UIHint struct {
+      Icon           string  `json:"icon"`            // "mdi:thermometer"
+      Semantic       string  `json:"semantic"`        // "temperature"
+      Format         string  `json:"format"`          // "%.1f °C"
+      StateColorRule string  `json:"state_color_rule,omitempty"` // "temp_heat"
+  }
```

A new Go package `pkg/hmui/quantity.go` owns the catalogue
(unit → hint, parameter-substring → hint, ENUM-shape → hint) and
is unit-tested per row. The REST handler calls
`hmui.HintFor(parameter, unit, type, valueList)` per DP at
serialise-time. A contract test asserts that every wire-known unit
has a row and that the SPA never falls back to a generic icon for
units the daemon claims to handle.

Net code change: ~60 lines in Go, ~10 lines in the REST handler,
~5 lines in the OpenAPI schema. Backwards-compatible (additive
fields).

---

## 8. Rollout

1. **Phase 1 — Go-side quantity catalogue + DataPointSummary
   extension.** `pkg/hmui/quantity.go` + REST handler emit `min`,
   `max`, `default`, `ui_hint`. OpenAPI schema + TS types updated.
   Unit tests per quantity row.
2. **Phase 2 — `composer.ts`.** Pure TS function, fully unit-testable
   without a DOM. Picks headline, buckets readouts by `semantic`,
   chooses control primitives by type + range, computes density
   token (`comfortable` / `compact` per wizard Q2). Returns a
   `ComposedTile` data structure plus a `gridSpan: 1 | 2` derived
   from `readouts.length` (≥9 → 2-cell span per wizard Q4).
3. **Phase 3 — `AutoTile.svelte`.** Stamps ComposedTile via
   existing primitives + four small new readout components
   (`BooleanReadout`, `NumericReadout`, `EnumReadout`,
   `StringReadout`). Honours density token + grid-span hint.
   Renders tile-tint per worst-case lifecycle and per-readout
   age-stamp (wizard Q3 hybrid).
4. **Phase 4 — dispatcher integration + SensorActorTile retirement.**
   Per wizard Q5, `SensorActorTile.svelte` is **deleted**; its
   logic (channel-type → primary parameter, DP classification)
   migrates into `composer.ts`. The `primary.ts` + `classify.ts`
   helpers move under `composer/` and become composer internals.
   `CdpTilesPanel` falls through CDP → CONTROL → AutoTile (no
   third tile component). The primitives (`TogglePill`,
   `ActionButton`, `NumericActionFeature`) stay and are consumed
   by AutoTile.

Each phase ships independently. After Phase 4 the SPA renders
every known and unknown device through the same composer; later
polish (quantity-group sub-cards within readouts, finer state-color
rules) lands as one-line additions to the Go-side catalogue.

---

## 9. What this earns

- **Day-1 coverage for new devices.** A device the CCU adds tomorrow
  — even one we've never seen — produces a coherent tile because
  the composer reads the descriptor and infers the rest. No
  daemon-side device profile needed for the UI to render
  acceptably.
- **Smaller widget tree.** Today: 15+ control widgets + 8 CDP tiles
  + SensorActorTile. After Phase 5: those stay but the long tail
  (hundreds of unmaintained devices) collapses into one component.
- **One model, three contexts.** The same composer output drives
  Übersicht (`AutoTile`) and could later drive Kanäle (compact
  rendering) and even a printed-PDF summary. The composer is the
  single source of truth for "what does this channel show?".

---

## 10. What this risks

- **Wrong inference.** A unit-driven icon picks the heating icon for
  a DP that happens to report in °C but isn't a temperature
  setpoint. Mitigation: parameter-substring overrides the unit when
  it matches. False positives are non-fatal — the value is still
  correct, just the icon is wrong.
- **Aesthetic floor.** Hand-coded tiles will always look more
  polished than composed ones. The composer is the *floor*, not
  the ceiling — common devices keep their bespoke widgets.
- **Backend coupling.** Three new descriptor fields on the REST
  surface. OpenAPI schema validation catches accidental shape
  changes; the wire shape change itself is non-breaking (additive).

---

## 11. Resolved decisions (wizard, 2026-05-24)

1. **Quantity catalogue — Hybrid, Go-side.** A new
   `pkg/hmui/quantity.go` package classifies every DP at REST
   serialise-time and ships the result as a `ui_hint` envelope on
   `DataPointSummary`. The SPA renders the hint verbatim — no
   JS-side inference, no parallel TS table to drift from. Adding
   a unit / parameter row is a one-line Go edit plus a unit test.
2. **Density tokens — 2 stages.** `comfortable` for 1–6 readouts
   (standard layout, full-size labels, per-readout age stamp).
   `compact` for 7+ readouts (2-col grid, smaller font, age stamp
   only on the headline). The composer emits the token; AutoTile
   maps it to Tailwind classes.
3. **Freshness signal — hybrid tint + per-readout stamp.** Tile
   background carries the worst-case lifecycle (`live` →
   no tint; `cache` → amber tint; `stale` → red tint;
   `unobserved` → grey tint). Individual readouts keep their
   `vor 3 s` / `vor 2 h ⚠` stamp so the operator can pinpoint
   which DP went quiet inside a mixed-lifecycle tile.
4. **Grid span — auto 2 cells at ≥ 9 readouts.** AutoTile keeps a
   1×1 footprint by default. The composer flips `gridSpan: 2`
   when `readouts.length >= 9`, which the panel honours via
   `class="col-span-2 md:col-span-2"`. HmIP-SFD (24 DPs) lands
   on a 2-cell tile; everything below threshold stays uniform.
5. **SensorActorTile retired.** AutoTile is the single fallback
   component after CDP and CONTROL widgets. `SensorActorTile.svelte`
   is deleted in Phase 4; `primary.ts` + `classify.ts` migrate
   into `composer/` as internals. Primitives
   (`TogglePill` / `ActionButton` / `NumericActionFeature`) are
   preserved and consumed directly by AutoTile.

The implementation plan in §Rollout is now concrete. Next code
step: Phase 1 (Go quantity catalogue + DataPointSummary extension).
