# Matter Bridge — UI Concept

**Status:** Implemented · **Date:** 2026-05-07 · **Target version:** v1.1
**Related:** [ADR 0012](../../docs/adr/0012-matter-pure-go-implementation.md),
[`pkg/interfaces/matter.go`](../../pkg/interfaces/matter.go),
[`assets/openapi.yaml`](../../assets/openapi.yaml),
[`assets/wsapi.json`](../../assets/wsapi.json)

This document describes the user interface for the Matter Bridge in
the Svelte 5 SPA under `assets/ui/`. It is the UX complement to ADR
0012, which defines the *technical* bridge architecture — cluster
mappings, source surface interfaces, crypto strategy. ADR 0012 states
*what* can be bridged to Matter; this document states *how the user
decides what is actually exposed*.

---

## 1. Guiding Principles

1. **Allowlist instead of denylist.** The Matter Bridge starts empty.
   Nothing is exposed automatically. A device appears in Matter
   if and only if the user explicitly selects it. This mirrors
   the ADR 0012 principle *"rich model, dumb bridge"*: the model knows
   *what is possible*; the user decides *what is visible*.
2. **Unit of selection = HA MQTT Discovery entity.** Everything that
   the MQTT HA Discovery exposes as an independent entity is also
   a potential Matter selection object: Custom DPs (Light, Cover,
   Climate, Switch, …), Generic DPs with a Discovery entry (e.g.
   BidCos `STATE` on a plain switch actuator without a Custom DP wrapper),
   Calculated DPs (DerivedSensor, DerivedBinarySensor), and Combined
   DPs. Non-Discovery DPs (internal MASTER parameters, service DPs)
   remain invisible. This makes the unit of selection identical to
   what the user already knows from HA — no
   *"Why don't I see BidCos switch X?"* or
   *"Where is my Calculated Climate Sensor?"* confusion. Which of these
   DPs can actually be mapped to a Matter endpoint is governed by
   §4a (Mapping Eligibility); non-mappable DPs are shown with ⚠ and
   are permanently disabled (visible, but not activatable).
3. **Multi-step commissioning UX.** Three clearly separated phases in
   the UI:
   1. *Select what is exposed*
   2. *Pair the bridge with a controller* (QR / setup code)
   3. *Fabric management* (which ecosystems are paired, re-pair,
      unpair)
4. **Multi-CCU-aware.** Allowlist entries are
   `(central_name, device_address, channel_no, dp_kind, dp_key)`
   tuples. `dp_key` is the profile for Custom DPs (`light.RGBWLight`,
   …) or the parameter name for Generic / Calculated DPs (`STATE`,
   `ACTUAL_TEMPERATURE`, …). An entity that reappears under a different
   `central_name` after a CCU migration is treated as *new* and is
   not exposed by default.
5. **Feature-flag gate.** When `matter.enabled = false`, the entire
   sidebar entry is invisible — no teasing of functionality that is
   disabled.

---

## 2. Data Model (Backend, context for the UI)

New table `matter_exposures`, persisted in
`internal/store/sqlite/`:

| Column           | Purpose                                                                   |
| ---------------- | ------------------------------------------------------------------------- |
| `central_name`   | Multi-CCU scope                                                           |
| `device_address` | e.g. `0001D3C9A1F2B7`                                                    |
| `channel_no`     | Channel of the DP host                                                    |
| `dp_kind`        | `custom` \| `generic` \| `calculated` \| `combined`                       |
| `dp_key`         | Profile for `custom` (`light.RGBWLight`, …), parameter otherwise (`STATE`, …) |
| `enabled`        | bool — the allowlist bit                                                  |
| `friendly_name`  | optional, for `BridgedDeviceBasicInformation`                             |
| `endpoint_id`    | assigned by the assembler (1‥65534), stable across restarts               |
| `created_at`     | Audit                                                                     |
| `updated_at`     | Audit                                                                     |
| `actor`          | Audit (user subject who last modified)                                    |

Primary key: `(central_name, device_address, channel_no, dp_kind,
dp_key)`. The composite key allows the same channel to be exposed
both via a Custom DP and via a Generic/Calculated DP — which is
rarely useful, but does not need to be artificially prohibited;
the UI warns about double-exposure (see §4.4).

**Relation:** The API returns *every* exposable Discovery entity
— even when `enabled=false` or no entry exists yet. The endpoint
assembler filters on `enabled=true` ∧ `mappable=true` (see §4a).

---

## 3. REST Surface

All endpoints are gated on the feature flag `matter.enabled` and are
declared in `assets/openapi.yaml`. Naming convention: action verbs use
path segments (not `:open`/`:close`) for consistency with the rest of
the REST surface.

```
GET    /api/v1/matter/status                       → bridge state, endpoint count,
                                                      fabric count, exposure count,
                                                      advertise on/off, window state
GET    /api/v1/matter/exposable                    → flat list of every candidate
                                                      with current enabled bit + verdict
PUT    /api/v1/matter/exposable                    → {key fields, enabled, friendly_name}
POST   /api/v1/matter/exposable/bulk               → {items: [{key, enabled, …}]}
GET    /api/v1/matter/fabrics                      → list {fabric_index, fabric_id,
                                                      node_id, vendor_id, label,
                                                      compressed_id, root_public_key}
DELETE /api/v1/matter/fabrics/{id}                 → unpair (admin)
GET    /api/v1/matter/setup-payload                → {qr_code, manual_code,
                                                      discriminator, passcode}
POST   /api/v1/matter/commissioning/window         → opens pairing window
                                                      {duration_seconds}
                                                      → {qr_code, manual_code, …}
POST   /api/v1/matter/commissioning/window/close   → close early (admin)
POST   /api/v1/matter/share                        → share bridge with a second
                                                      controller (admin)
```

WebSocket commands (in `assets/wsapi.json`):

- `matter.exposable_changed`
- `matter.commissioning_window_opened`
- `matter.commissioning_progress`
- `matter.fabric_added`
- `matter.fabric_removed`
- `matter.endpoint_assembled`

---

## 4. SPA Integration

### 4.1 Sidebar

New cluster `sidebar.cluster.bridges` (alternatively an entry in the
existing `cluster.system`), visible only when
`/api/v1/matter/status` returns 200 (i.e. `matter.enabled = true`):

```
🔗 Bridges
   • Matter           #/matter
   (• MQTT)            (follow-up wave, same cluster)
```

### 4.2 Route Structure

Hash routes, parallel to the existing convention in `App.svelte`:

| Path                | Component                       | Purpose                              |
| ------------------- | ------------------------------- | ------------------------------------ |
| `#/matter`          | `Matter.svelte`                 | Overview: status card + tab bar      |
| `#/matter/expose`   | (Tab) `MatterExposureList.svelte` | Allowlist management               |
| `#/matter/fabrics`  | (Tab) `MatterFabrics.svelte`    | Paired controllers                   |
| `#/matter/pair`     | (Tab) `MatterPair.svelte`       | Open pairing window, show QR/code    |

The three tabs form a tab bar *within* a single top-level route —
no separate `Route` child per tab. This keeps the hash router lean
while still allowing deep links.

### 4.3 Status Card (`Matter.svelte`)

Pinned at the top, visible in all tabs:

```
┌────────────────────────────────────────────────────────────┐
│  Matter Bridge                                             │
│  ● Active     Endpoints: 12   Fabrics: 2   Advertising: yes│
│  Vendor: 0xFFF1 (Test)   Bridge Endpoint: 0                │
└────────────────────────────────────────────────────────────┘
```

Updated via WebSocket. *Inactive* means: `matter.enabled = true`,
but no listener bound — e.g. because nothing is exposed yet and the
assembler has therefore not built any endpoints.

### 4.4 Tab "Expose" — Core Feature

This is the "nothing is created by default" flow. Layout:

```
┌────────────────────────────────────────────────────────────────────────────┐
│  [Search]  [▾ Kind: all | Custom | Generic | Calculated]                   │
│            [▾ Class: all | Light | Cover | Climate | Switch | Sensor |    │
│                        BinarySensor]                                       │
│                               [Bulk: expose selection] [… hide]            │
├────────────────────────────────────────────────────────────────────────────┤
│ ☐ │ ◯ │ Living Room Ceiling  HmIP-BDT  Ch. 4   Custom    · Light · Dim.   │
│ ☐ │ ● │ Dining Table RGB     HmIP-BSL  Ch. 3   Custom    · Light · ExtCol.│
│ ☑ │ ● │ Bedroom Blind        HmIP-BROLL Ch. 4  Custom    · Cover · Window…│
│ ☐ │ ● │ Bathroom Heating     HmIP-eTRV-2 Ch. 1 Custom    · Climate · Therm│
│ ☐ │ ● │ Hallway Switch       HM-LC-Sw1-Pl Ch. 1 Generic  · Switch (STATE) │
│ ☐ │ ● │ Outdoor Temp         HmIP-STH  Ch. 1   Calculated· Sensor (ACT_TMP│
│ ☐ │ ⛔│ Siren Tone Selection  HmIP-ASIR Ch. 3   Generic   · — (not mappable│
│ … │   │ …                                                                  │
└────────────────────────────────────────────────────────────────────────────┘
   ▲     ▲
   │     └─ State: ● = exposed · ◯ = not exposed · ⚠ = partially
   │                mappable · ⛔ = not mappable (toggle permanently disabled)
   └─ Bulk checkbox (grayed out for ⛔ rows)

Click on row → side drawer with detail:
   - Friendly Name (editable, default = device name + parameter if applicable)
   - Source:                  Custom DP `light.RGBWLight`
                              (or: Generic DP `STATE` (BOOL),
                               Calculated DP `ACTUAL_TEMPERATURE` (FLOAT))
   - Matter endpoint type preview:  ExtendedColorLight (0x010D)
                                    (or: OnOff Plug-in Unit (0x010A),
                                     Temperature Sensor (0x0302))
   - Cluster list:  OnOff · LevelControl · ColorControl · Descriptor
   - Toggle "Expose in Matter"  (disabled when eligibility ⛔)
   - Notes:  "Effects dispatch available via MQTT only" (from ADR 0012)
             "Already exposed via Custom DP `light.RGBWLight` —
              avoid double-exposure" (conflict warning)
   - [Save]   [Cancel]
```

**Important:** Rows with a *partially mappable* DP (Siren Tones,
Effect Light Effects, Modulating Valve) are shown with ⚠ and remain
activatable — the mappable part (OnOff, Level) goes to Matter,
the rest is labeled "MQTT-only" in the drawer. This mirrors
ADR 0012 §6 1:1.

Rows with `dp_kind ∈ {generic, calculated, combined}` whose
`(category, parameter, value_type)` has no entry in the cluster mapper
(see §4a) are shown with ⛔; the toggle is permanently disabled.
They remain visible so the user can understand *why* an HA entity
does not appear in Matter — the drawer shows the reason (e.g.
"ENUM parameter without cluster equivalent").

**Conflict warning:** If the same `(channel)` is already exposed via a
Custom DP and the user activates a Generic/Calculated DP on the same
channel, the drawer displays a warning (no hard block — there are edge
cases, e.g. when the Custom DP only uses a subset of the channel).
The conflict is reflected as a lint issue in
`/api/v1/matter/status`.

State source: new store `matterStore.svelte.ts` holds the list,
bulk selection, and the dirty set; uses the existing `dirty` pattern
(browser beforeunload guard).

### 4.5 Tab "Pair"

Three-step wizard:

```
Step 1 — Open pairing window
  [Duration: 5 min ▾]   [Start pairing]

Step 2 — Connect with controller
  ┌──────────────┐
  │  [QR Code]   │   Manual:  1234-567-8901
  │              │   Discriminator: 3840
  │              │   Setup code: 20202021
  └──────────────┘
  Open Apple Home / Google Home / Alexa / SmartThings,
  "Add device" → "More options" → scan QR.

Step 3 — Done
  ✔ Fabric "Apple Home (markus@…)" added — 12 endpoints visible
```

The WS event `matter.fabric_added` unlocks step 3. The pairing
window is automatically closed by the backend when it expires;
the frontend shows the remaining time as a progress ring.

**Security note:** The QR payload is not written to the browser
cache (`Cache-Control: no-store` on the endpoint; the component
clears the state on tab switch / logout). An audit entry is created
for every open/close.

### 4.6 Tab "Fabrics"

Table of all paired controllers with re-pair / unpair:

```
Vendor                Label              Nodes   Last Activity      Action
Apple    0x1349       "markus@…"         3       2 min ago          [Unpair]
Google   0x6006       "Apartment"        1       1 h ago            [Unpair]
[+ Share bridge with another controller]   ← reopens step 1 flow
```

Unpair goes through `ConfirmDialog` (existing component) —
destructive action, clearly marked as such.

---

## 4a. Mapping Eligibility

Which selection leads to a well-formed Matter endpoint is determined
by a cluster mapper in the backend. The UI only retrieves and displays
the result — it does *not* make mapping decisions.

### 4a.1 Input Table

One source per `dp_kind` for the mapping table:

| `dp_kind`    | Source                                                                                                | Endpoint type determination                                |
| ------------ | ----------------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `custom`     | Profile registry (`internal/model/custom`)                                                            | Profile → fixed endpoint type (e.g. `light.RGBWLight` → `ExtendedColorLight 0x010D`) |
| `generic`    | HA Discovery lookup (`internal/north/mqtt/entity_descriptions_generated.go` + `entity_descriptions.go`) | `(component, value_type, unit)` → endpoint type heuristic (e.g. BOOL Switch → `OnOff Plug 0x010A`, BinarySensor → `Contact Sensor 0x0015`) |
| `calculated` | Calculated registry (`internal/model/calculated`) + HA Discovery lookup for the derived entity        | same as `generic`                                          |
| `combined`   | Combined registry (`internal/model/combined`)                                                         | Endpoint type from dominant sub-DP                         |

The table is **not** duplicated — it is derived at build time from the
sources listed above, in parallel with the existing Discovery build
(`go generate` path in `internal/north/mqtt`).

### 4a.2 Eligibility States

The mapper returns a state per `(channel, dp_kind, dp_key)`:

| State                 | Meaning                                                                | UI symbol | Toggle   |
| --------------------- | ---------------------------------------------------------------------- | --------- | -------- |
| `mappable`            | fully mappable to a Matter endpoint type + cluster set                 | ●/◯       | active   |
| `partially_mappable`  | partial mapping available (e.g. OnOff/Level), remainder stays MQTT-only | ⚠        | active   |
| `unmappable`          | no cluster equivalent (ENUM without mapping, free text, service DP)   | ⛔         | inactive |

The `reason` column from the mapper result is shown in the drawer in
readable form (e.g. "Parameter is ENUM without cluster equivalent",
"FLOAT parameter without unit — sensor cluster requires unit").

### 4a.3 Known Non-Mappable Categories (initial)

Derived from aiohomematic / homematicip_local, without claim of
completeness:

- ENUM parameters without clear cluster semantics (`ACOUSTIC_ALARM_SELECTION`,
  `OPTICAL_ALARM_SELECTION`, `PARTY_MODE_SUBMIT`, `WEEK_PROGRAM_*`).
- Free-text / JSON parameters (schedule payloads, week profile strings).
- Service DPs (`UNREACH`, `STICKY_UNREACH`, `CONFIG_PENDING`,
  `UPDATE_PENDING`) — these are Discovery diagnostics (`entity_category
  = diagnostic`), but do not belong in Matter since Matter provides its
  own reachability / update clusters.
Note: `PRESS_SHORT` / `PRESS_LONG` etc. are **not** in the
non-mappable set. Press parameters (`PRESS`, `PRESS_SHORT`,
`PRESS_LONG`, …) classify as `MatterMeasurementMomentarySwitch`
(`internal/model/generic/matter.go`) and resolve to a **Mappable**
verdict via `eligibility.go` `DeriveMatterEligibility`. The HM
`Button` / `Action` DP is exposed through the GenericSwitch cluster
(0x003B) implemented in `cluster/wire/genericswitch.go`, one Matter
endpoint per button with a press-cycle state machine.

The mapper output is delivered via `GET /api/v1/matter/exposable`
together with the allowlist, so the UI works with a single round-trip.

---

## 4b. One Endpoint per Device (ADR 0049)

Since 0.31.0 the bridge emits **one Matter endpoint per physical
device** rather than one endpoint per Discovery entity
([ADR 0049](../../docs/adr/0049-matter-one-endpoint-per-device.md)). This
reshapes the candidate list the "Expose" tab renders:

- **Secondary / group-STATE constituents are folded by default.**
  `eligibility.go` `CollectCandidates` drops a custom-DP entity's
  non-primary constituents when `exposeSecondary` is false (the
  default). Only the device's primary host entity surfaces as a
  candidate. The redundant per-channel group-STATE rows no longer
  clutter the list.
- **Expert opt-in.** Set `north.matter.expose_secondary_channels`
  (expert config flag, default false) to reveal the folded secondary
  channels as independent candidates — matching the pre-0.31 flat
  per-entity view for operators who need it.
- **Visibility gate.** The `/api/v1/matter/exposable` list also
  applies the visibility gate, so entities marked ignored / no_create
  are dropped from the candidate list up front.
- **Measurements ride the host endpoint.** Measurement DPs such as
  battery / power / energy are attached to the device's host endpoint
  as measurement clusters (device-type 0), instead of each becoming
  its own standalone endpoint.

---

## 5. i18n

New keys in `internal/i18n/catalogs/{en,de}.json`:

```
nav.matter
sidebar.cluster.bridges
matter.tab.expose
matter.tab.fabrics
matter.tab.pair
matter.expose.empty
matter.expose.filter_kind            ← "Kind: all | Custom | Generic | Calculated"
matter.expose.filter_class           ← "Class: all | Light | Cover | …"
matter.expose.kind.custom
matter.expose.kind.generic
matter.expose.kind.calculated
matter.expose.kind.combined
matter.expose.unmappable_hint
matter.expose.unmappable_reason.enum_no_cluster
matter.expose.unmappable_reason.float_no_unit
matter.expose.unmappable_reason.service_dp
matter.expose.unmappable_reason.event_only
matter.expose.conflict_hint          ← "Already exposed via Custom DP"
matter.pair.window_open
matter.pair.qr_caption
matter.fabric.unpair_confirm
…
```

---

## 6. Auth & Audit

- Access is role-based, not gated by a named `matter.*` permission.
  Mutations require the `admin` role (`router.go` gates
  `DELETE /fabrics/{id}`, `PUT`/`POST /exposable`,
  `POST /commissioning/window` + `/close`, `POST /share` via
  `pr.With(admin)`). The read endpoints (`GET status` / `fabrics` /
  `setup-payload` / `exposable`) are available to any authenticated
  identity, including viewer/operator.
- Every mutation (`PUT /exposable`, `POST /exposable/bulk`,
  `POST /commissioning/window`, `POST /commissioning/window/close`,
  `POST /share`, `DELETE /fabrics/{id}`) produces an audit log entry
  (existing `internal/audit` path) with the `actor` from the
  authenticated request context.
- Pairing codes do *not* appear in the audit log — only the fact
  that a window was opened/closed, plus `actor` and duration.

---

## 7. Deliberately Omitted

- **Per-cluster toggle.** No UI for "ColorControl yes, LevelControl
  no" — that would be endpoint tinkering and would contradict Matter
  device type conformance (HA / Apple reject incomplete endpoints).
  The endpoint type is determined by the Custom DP profile or the
  cluster mapper for Generic/Calculated DPs, end of story.
- **Arbitrary parameter selection.** No UI to expose *every* Generic DP
  parameter. Only DPs with an entry in the HA MQTT Discovery table
  (`entity_descriptions_generated.go`) are selection candidates —
  what the HA entity view hides (internal MASTER parameters, service
  DPs) remains invisible to Matter as well.
- **Aggregated multi-parameter endpoints from Generic DPs.** No UI
  to bundle, for example, temperature + humidity + battery of a Generic
  channel into a composite `Air Quality Sensor` endpoint. Under the
  one-endpoint-per-device model (ADR 0049, see §4b) measurement DPs
  (battery / power / energy, and similar) ride the device's host
  endpoint as measurement clusters rather than each becoming its own
  standalone endpoint; standalone Generic/Calculated sensor endpoints
  are still created where a device carries no primary custom-DP host.
  Bundling of a device's own parameters is otherwise driven by the
  profile (where the Custom DP knows which parameters belong together).
- **BLE pairing.** ADR 0012 excludes BLE in v1.1 → the "Pair" tab
  shows exclusively the on-network path.

---

## 8. Implementation Stages

| Stage | Content                                                                                               | Depends on                              |
| ----- | ----------------------------------------------------------------------------------------------------- | --------------------------------------- |
| U1    | Backend table `matter_exposures` (5-tuple key) + endpoint assembler filter                            | M5 (endpoint assembler)                 |
| U1b   | Cluster mapper (§4a) for `custom`; eligibility schema in REST response                                | U1                                      |
| U2    | REST `/exposable` + GET `/status` + sidebar entry + `Matter.svelte` skeleton with status card         | U1b                                     |
| U3    | Tab "Expose" incl. drawer + bulk + dirty tracking + kind/class filter                                 | U2                                      |
| U3b   | Cluster mapper extension for `generic` (BidCos switches, BinarySensors, Sensors with unit)            | U3                                      |
| U3c   | Cluster mapper extension for `calculated` + `combined`; conflict lint in `/status`                    | U3b                                     |
| U4    | Tab "Pair" + QR generator + commissioning window flow                                                 | M2 (Spake2+ / on-network commissioning) |
| U5    | Tab "Fabrics" + unpair + share bridge flow                                                            | M3 (CASE / fabric persistence)          |

---

## 9. Resolved decisions

1. **Bulk granularity** (was OQ): the filter + multi-select + bulk
   toolbar covers every realistic use case; no separate per-device /
   per-profile bulk modes were added.
2. **Double-exposure**: soft warning, no hard block. The UI surfaces
   the conflict in the detail drawer; the assembler accepts the
   double exposure as long as both rows are mappable.
3. **Default friendly name**: device name + channel label, falling
   back to device address when both are empty. Operator overrides
   per row via the drawer.
4. **Pairing window default duration**: 900 s (15 min) — matches
   Matter §11.19.8.1 default, validated by the
   `CommissioningWindow.OpenWindow` bounds check.

---

*Top-level design document — not an ADR. The bridge wire-protocol
design rules from chip-tool bring-up live in
[ADR 0013](../../docs/adr/0013-matter-commissioning-bring-up.md); the
implementation form + cluster subset live in
[ADR 0012](../../docs/adr/0012-matter-pure-go-implementation.md).*
