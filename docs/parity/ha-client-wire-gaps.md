# ha-client-wire-gaps.md — Open parity gaps blocking the Home Assistant drop-in

> **RESOLVED on the daemon side (2026-06-21).** All seven gaps below were
> triaged; the four genuine daemon gaps (G2, G4, G5, G6) shipped under
> APIVersion 1.18.0 and the other three (G1, G3, G7) were reclassified as
> client-side follow-ups or withdrawn. See **"Verification & resolution"**
> below for the closing detail — the summary table and per-gap sections that
> follow it describe the *original* (now-closed) daemon-side framing and are
> kept for provenance. Nothing in this file currently describes open daemon
> work; any remaining action items live in the `openccu-loom-client` /
> `openccu-loom-types` repos, not here.

**As of:** 2026-06-21
**Purpose:** Catalogue of the **genuine, still-open** parity gaps that prevent the
`py-openccu-loom-client` (and through it the Home Assistant integration
*Homematic(IP) Local*) from being fully feature-equivalent to direct
[aiohomematic](https://github.com/SukramJ/aiohomematic). Each item is an
**implementation gap to close**, not an intentional divergence.

**Relationship to [`by_design.md`](by_design.md):** that file catalogues
*intentional* Go↔reference divergences that must score ✅ (by design). This file
is its complement: items here must score ❌ (open gap) until closed. Two items
that *look* like gaps from the HA side but are actually deliberate configuration
relocation are listed in §"Not gaps" at the end so they are not re-filed here.

**How these were identified:** a cross-stack review of the Home Assistant
integration (`homematicip_local`, branch `feat/loom-client-2026.6.20`) and the
installed `openccu-loom-client` 2026.6.21 / `openccu-loom-types` 0.1.22. The
client side is read-verified (the Python stubs carry explicit
`"the daemon does not surface … yet"` markers); the daemon-side fix locations are
grep-located starting points and are marked accordingly.

### Confidence legend

- ✅ **client-verified** — confirmed by reading the consuming Python code.
- 🔍 **daemon-located** — fix site located by search; confirm exact
  serialization point before implementing.

### Gap classes

| Class | Meaning | Items |
|---|---|---|
| **A. State not exposed** | The daemon already models the value internally but does not serialize it into the north-bound state payload (`CustomDPSummary.state` / `datapoint.value_changed` / `custom_data_point.state_changed`). | G1, G2 |
| **B. Wire field missing** | A field/marker the client needs is absent from the wire contract. | G3, G4 |
| **C. Model not surfaced north-bound** | The daemon has an internal model, but no north-bound query/event exposes it. | G5 |
| **D. No push channel** | Data is reachable via REST but has no event topic, forcing the client to poll. | G6 |
| **E. To verify** | Symptom observed client-side; root side (daemon vs. client) not yet pinned. | G7 |

---

## G1 — Light colour / effect read-back not in the state payload

**Class:** A (state not exposed) · **Confidence:** ✅ client-verified · 🔍 daemon-located

**Symptom (HA):** colour temperature, hue/saturation and the active effect of a
colour/effect light always read as `unknown` in Home Assistant. **Writes work**
(turn-on, brightness, set colour, set effect all drive the daemon `set_*` ops);
only the read-back of the current value is missing.

**Client evidence (✅):** `compat/aiohomematic/model/custom/__init__.py:349-368`
— "Colour / effect state is not carried in the daemon's light CDP state (only
on/off + brightness); these read as 'unknown' until the daemon surfaces them.
Writes still drive the daemon set_* ops." The properties `color_temp_kelvin`,
`hs_color`, `effect` read from `self._state.get(...)`, which the daemon leaves
empty.

**Daemon location (🔍):** the model already exists and accepts colour/effect
*commands* — `internal/model/custom/light/color.go` (`hue`, `saturation`),
`internal/model/custom/light/effect.go`, `internal/model/custom/light/state_change.go:23-24`
(`StateChangeArgColorTempKelvin`, `StateChangeArgEffect`), and capability
resolution already advertises `has_hs_color` / `has_color_temperature` /
`has_effects` (`internal/model/custom/light/init.go`). The missing piece is the
**state serialization**: the light CDP's north-bound `state` map must include the
current `color_temp_kelvin`, `hue`, `saturation`, and `effect` so they ride the
state payload and `custom_data_point.state_changed` events.

**What the daemon must deliver:** add the dynamic colour/effect read values to the
light custom-DP `state` projection that feeds `CustomDPSummary.state`
(`openccu_loom_types/rest.py:72-99`) and the change events.

---

## G2 — Text-display selectable option lists not surfaced

**Class:** A (state not exposed) · **Confidence:** ✅ client-verified · 🔍 daemon-located

**Symptom (HA):** the text-display / notify entity exposes **empty** sets for
selectable icons, sounds, background colours and text colours, so the per-option
`ActionSelect` controls cannot be built. The entity renders but offers no option
pickers.

**Client evidence (✅):** `compat/aiohomematic/model/custom/__init__.py:970-986`
— "The daemon's text-display CDP does not yet surface the selectable option lists;
expose empty sets so the HA notify entity's state attributes render without the
per-option ActionSelects." `available_icons`, `available_sounds`,
`available_background_colors`, `available_text_colors` all return `()`.

**Daemon location (🔍):** `internal/model/custom/textdisplay/` (`textdisplay.go`,
`topology.go`, state payload) already models the device; the option enumerations
must be added to its north-bound config/state projection.

**What the daemon must deliver:** surface the selectable option lists (icons,
sounds, background colours, text colours) in the text-display CDP's
config/capabilities so the client can build the option selects.

---

## G3 — Sysvar "extended" marker missing → writable sysvars downgraded

**Class:** B (wire field missing) · **Confidence:** ✅ client-verified · 🔍 daemon-located

**Symptom (HA):** writable system variables (switch / select / number / text)
appear in Home Assistant as **read-only** sensors. Only ALARM/LOGIC types map to
binary sensors; everything else collapses to a plain sensor because the writable
flavour is gated behind a marker the daemon never sends.

**Client evidence (✅):** `compat/aiohomematic/model/hub/__init__.py:186-217` —
the writable `_SYSVAR_EXTENDED_BY_TYPE` mapping (switch/select/number/text)
"require the 'extended' sysvar marker from the CCU variable description, which the
daemon does not surface yet." `resolve_sysvar_class(..., extended=False)`
therefore always falls back to the read-only classes.

**Daemon location (🔍):** no sysvar "extended" handling found under
`internal/north/rest/handlers/hub.go` / `system_hub.go` or the sysvar model
(grep for `extended` in the sysvar context returns nothing relevant). The CCU
variable description's extended marker must be read on the south-bound side and
propagated to `SysvarSummary`.

**What the daemon must deliver:** add an `extended` (writable) flag to
`SysvarSummary` (`openccu_loom_types/rest.py`), derived from the CCU variable
description, mirroring aiohomematic's extended-sysvar handling.

---

## G4 — Partial hub-coordinator surface (hub singleton data points)

**Class:** B (wire field missing) · **Confidence:** ✅ client-verified · 🔍 daemon-located

**Symptom (HA):** the integration **skips orphan-entity registry cleanup entirely**
on the loom backend, because the per-singleton accounting it relies on cannot be
built. The skip is a safety measure to avoid deleting still-valid entries.

**Client/integration evidence (✅):** `homematicip_local/control_unit.py:500-507`
— "The openccu-loom adapter exposes only a partial hub-coordinator surface (no
alarm/service messages, inbox, metrics, connectivity or install-mode data points),
so the per-singleton accounting below cannot be built. Skip the sweep until the
loom adapter models the full hub surface…"

**Daemon location (🔍):** the daemon has the underlying data (health/metrics,
install-mode, service messages exist across `internal/health/`,
`internal/north/rest/handlers/hub.go`, `system_hub.go`), but they are not all
exposed as the hub *data points* the client's hub coordinator expects. Confirm
which singletons (alarm/service messages, inbox, metrics, connectivity,
install-mode) are missing from the north-bound hub surface.

**What the daemon must deliver:** surface the full set of hub singleton data
points so the client can present a complete hub coordinator, unblocking the
orphan-cleanup sweep. Pairs with G6 (these singletons also need a push channel).

---

## G5 — Per-device event groups not surfaced north-bound

**Class:** C (model not surfaced) · **Confidence:** ✅ client-verified · 🔍 daemon-located

**Symptom (HA):** the `event` platform is set up **without bootstrap entities**;
keypress/impulse event groups are unavailable. The integration catches a
`NotImplementedError` rather than failing the entry.

**Client/integration evidence (✅):** `homematicip_local/event.py:59-66` —
`control_unit.central.query_facade.get_event_groups(...)` raises
`NotImplementedError`; "The loom backend does not model per-device event groups
yet." Confirmed in the client compat layer (the query facade has no event-group
implementation).

**Daemon location (🔍):** an event-group model **does** exist in the daemon —
`internal/model/event/group.go`, `internal/central/queryfacade.go`,
`internal/central/adapter/device_pipeline.go`. The gap is therefore most likely
on the **north-bound exposure** (no REST/WS query the client can call) and/or the
client adapter not wiring it. Pin which side before implementing: if the daemon
already exposes groups, this is a client-only wiring fix; if not, add the
north-bound query.

**What the daemon must deliver (if exposure is missing):** a north-bound query
returning per-device event groups, consumable by the client's `get_event_groups`.

---

## G6 — Hub singletons have no push channel (client polls every 30 s)

**Class:** D (no push channel) · **Confidence:** ✅ client-verified

**Symptom (efficiency, not correctness):** the client runs a dedicated **30 s
poll loop** for hub singletons (messages, inbox, metrics, system-update,
install-mode, connectivity) because there is no event topic to subscribe to —
the one remaining polling island in an otherwise push-driven client.

**Client evidence (✅):** `compat/aiohomematic/central/adapter.py:1114-1128`
(`_HUB_REFRESH_INTERVAL = 30`) — "the daemon has no push channel for these yet."

**What the daemon must deliver:** add WebSocket broadcast topics for the hub
singletons (e.g. under `hub.*` / `system.*`) so the client can drop the poll
loop. Naturally pairs with G4 (model the singletons *and* push them).

---

## G7 — Generic switch `set_on_time` not wired

**Class:** E (to verify) · **Confidence:** ✅ client-verified (symptom); root side open

**Symptom:** `set_on_time` on a *generic* switch is a no-op (debug log only) in
the client compat layer.

**Client evidence (✅):** `compat/aiohomematic/model/generic/__init__.py:215-227`
— only a debug log, no daemon call.

**Open question:** determine whether the daemon already exposes a generic
`ON_TIME` write operation (then this is a client wiring fix) or not (then the
daemon must add it). File on the correct side after verification.

---

## Not gaps — deliberate configuration relocation (do not re-file)

These show up as "missing" from the HA UI but are **by design**: the daemon owns
the behaviour, configured per-central in the daemon, not in Home Assistant.

- **Reduced advanced-settings schema** —
  `homematicip_local/config_flow.py:753-777`: "The daemon owns CCU-behaviour
  parity (hub scans, markers, light/cover behaviour, firmware, device creation)
  per-central, so only the HA-side toggles remain configurable here. Callbacks,
  MQTT, command pacing and interface/parameter options are the daemon's concern."
- **Trimmed options menu** — `homematicip_local/config_flow.py:1789`: the
  `interfaces` and `programs_sysvars` steps are dropped because the daemon owns
  interfaces and program/sysvar scanning.

---

## Summary

| ID | Gap | Class | Confidence | Side to fix |
|---|---|---|---|---|
| G1 | Light colour/effect read-back | A | ✅ / 🔍 | daemon (state serialization) |
| G2 | Text-display option lists | A | ✅ / 🔍 | daemon (config exposure) |
| G3 | Sysvar `extended` marker | B | ✅ / 🔍 | daemon (wire field) |
| G4 | Hub singleton data points | B | ✅ / 🔍 | daemon (north-bound surface) |
| G5 | Per-device event groups | C | ✅ / 🔍 | daemon exposure or client wiring (verify) |
| G6 | Hub-singleton push channel | D | ✅ | daemon (WS topics) |
| G7 | Generic `set_on_time` | E | ✅ | verify daemon vs. client |

The dominant theme: most genuine gaps are **north-bound exposure**, not missing
capability — the daemon already models colour, effects, text-display devices and
event groups internally; what is missing is serializing those values into the
wire contract (state payloads, summaries, push topics) the HA client consumes.

---

## Verification & resolution (2026-06-21)

A code-level verification of the daemon-side claims (the 🔍 items above —
explicitly flagged as "confirm before implementing") reclassified three items and
closed the four that were genuine daemon gaps. The ✅ client-verified *symptoms*
all held; what needed pinning was the *fix side*. The north-bound additions ship
under **APIVersion 1.18.0** (`assets/openapi.yaml`, `assets/wsapi.json`).

### Corrections to the original classification

- **G1 — reclassified (Class A → B; fix side daemon → client).** The light CDP
  *does* serialize colour/effect: `ColorTempLightState` / `EffectLightState` carry
  `color_temp_kelvin` and `effect`, and the client reads those keys correctly. The
  one real defect is the HS-colour **shape mismatch**: the daemon emits
  `color: {h, s}` + `color_mode: "hs"`
  (`internal/model/custom/light/payload.go`, `internal/payload/state.go`) while the
  client reads flat `hue` / `saturation`. Not "state not exposed" — a key/shape
  alignment, fixable client-side (or by the daemon emitting flat aliases).
- **G3 — withdrawn (not a gap).** The `extended` marker is implemented end-to-end:
  read on the south side (`internal/central/adapter/hub_wiring.go`), modelled
  (`internal/model/hub/sysvar.go`), serialized as `SysvarSummary.is_extended`
  (`internal/north/rest/handlers/hub.go`), declared in the types package, and read
  by the client. A read-only symptom now points at the CCU description or a parse
  bug, not a missing wire field.
- **G7 — resolved as client wiring.** The daemon already exposes a generic
  `ON_TIME` write through the standard parameter route
  `PUT /devices/{addr}/channels/{no}/data-points/{param}/value`
  (`internal/north/rest/handlers/devices.go`), identical to the dedicated
  custom-switch `set_on_time` path. The client compat layer just needs to call it.

### Daemon gaps closed

- **G2 — text-display option lists.** `TextDisplayState` now serializes
  `available_background_colors`, `available_text_colors`, `available_alignments`,
  `available_repetitions` and `available_intervals` alongside the existing
  icons/sounds (`internal/model/custom/textdisplay/payload.go`,
  `internal/payload/state.go`). The getters already existed; only the projection
  was missing.
- **G4 — hub singleton data points.** New aggregating endpoint
  `GET /hub/data-points` returns the hub singletons (alarm/service messages,
  inbox, firmware update, metrics, per-interface connectivity and install-mode)
  in one coordinator-shaped response per central, so the client can build a
  complete hub coordinator from a single fetch and re-enable the orphan-cleanup
  sweep (`internal/north/rest/handlers/hub_data_points.go`).
- **G5 — per-device event groups.** New endpoint
  `GET /devices/{addr}/channels/{no}/event-groups` projects the channel's
  keypress / impulse / device-error groups (`channel_address`, `kind`,
  `event_types`, `parameters`, `available`, `last_triggered_event`) from the
  existing model (`internal/north/rest/handlers/event_groups.go`). The WebSocket
  command variant is intentionally **deferred**: event groups are bootstrap
  entities fetched once at entry setup, for which the REST query suffices.
- **G6 — hub-singleton push channel.** WebSocket broadcast topics added so the
  client can drop its 30 s poll loop
  (`internal/north/rest/ws/hub_events.go`):
  `hub.<central>.alarm_messages`, `hub.<central>.service_messages`,
  `hub.<central>.inbox`, `hub.<central>.metrics` (via the hub model's `OnUpdate`
  hooks) and `hub.<central>.connectivity.<interface_id>` (via the event bus —
  the connectivity tracker is attached lazily during readiness-gated bring-up,
  so a model hook wired at subscriber-start would miss it).

### Remaining (client-side) follow-ups

These now live in the client / types repos, not the daemon: consume the new G2
fields, the G4 `/hub/data-points` endpoint, the G5 `/event-groups` endpoint and
the G6 push topics; and apply the three reclassified fixes (G1 read `color`/
`color_mode`, G3 re-verify against a real CCU, G7 wire `set_on_time` to the
generic value route).
