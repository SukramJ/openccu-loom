# Alarm System Concept for OpenCCU-Loom

**Status:** Shipped — the alarm engine landed in 0.42.0 and has been
extended through 0.43.x (capability-derived enrollment, guided
remote-key bindings, real notification outputs). This document is kept
in sync with the implementation; where they disagree, the code and
`CHANGELOG.md` win.
**Date:** 2026-07-14
**Scope:** A native, local-first intrusion-alarm engine ("alarm panel") inside
the OpenCCU-Loom daemon, driving Homematic / Homematic IP sensors and sirens,
with a first-class SPA surface, REST/WS API, and MQTT (Home Assistant
`alarm_control_panel`) integration.

---

## Table of contents

1. [Motivation & goals](#1-motivation--goals)
2. [Hard safety invariants](#2-hard-safety-invariants)
3. [Prior art](#3-prior-art)
4. [Core concepts & terminology](#4-core-concepts--terminology)
5. [Arm-state machine](#5-arm-state-machine)
6. [Sensor model](#6-sensor-model)
7. [Output model: loud, silent, and everything between](#7-output-model-loud-silent-and-everything-between)
8. [Siren safety: "a siren can always be silenced"](#8-siren-safety-a-siren-can-always-be-silenced)
9. [Direct device links: with / without](#9-direct-device-links-with--without)
10. [Resilience & recovery](#10-resilience--recovery)
11. [Users, codes & permissions](#11-users-codes--permissions)
12. [UI / UX concept](#12-ui--ux-concept)
13. [API & integration surface](#13-api--integration-surface)
14. [Architecture inside OpenCCU-Loom](#14-architecture-inside-openccu-loom)
15. [Feature catalogue & phasing](#15-feature-catalogue--phasing)
16. [Security considerations](#16-security-considerations)
17. [Testing strategy](#17-testing-strategy)
18. [Open questions](#18-open-questions)
19. [Implementation kickoff](#19-implementation-kickoff)
20. [References](#20-references)

---

## 1. Motivation & goals

OpenCCU-Loom already models every Homematic security-relevant device class
(window/door contacts, rotary handles, motion detectors, sirens, keypads,
remotes, smoke detectors, water sensors) and owns the full transport path to
the CCU. What is missing is the **coordinating layer**: an arm-state machine
that turns those devices into an alarm system.

Today users must choose between:

- the **Homematic IP cloud app** (HAP): polished but cloud-bound, no CCU;
- the **HCU1**: local, with a developer-mode WebSocket "Connect API" and a
  community Home Assistant integration, but no CCU support, no MQTT/REST
  planes, and no CCU-class device openness;
- **hand-rolled CCU programs**: powerful but fragile, no real UX, easy to
  get siren handling dangerously wrong;
- **Home Assistant + Alarmo**: excellent panel, but requires a full HA
  install and knows nothing about Homematic specifics (sabotage contacts,
  duty cycle, siren parameter contracts, direct links).

### Goals

- **G1 — Local-first**: the entire alarm logic runs inside the daemon. No
  cloud, no external dependency. Works with every CCU variant loom supports.
- **G2 — Perimeter & full protection** (Hüllschutz / Vollschutz) as first-class
  arm modes, extensible to night/vacation/custom modes.
- **G3 — Loud and silent alarming**, selectable per zone and per mode.
- **G4 — Best-in-class sensor/actor selection UX**: type-aware auto-proposals,
  live state, a mode-assignment matrix, room grouping — better than both the
  HmIP app (no per-sensor flags) and Alarmo (no device awareness).
- **G5 — Resilience and operator control**: the user can always take over.
  Bounded siren activations, multi-path silencing, state recovery after
  restart, degraded-mode behaviour that is predictable and documented.
- **G6 — Integration-friendly**: HA MQTT discovery (`alarm_control_panel`),
  REST/WS parity, webhook events, audit trail.
- **G7 — Multi-CCU**: zones may span centrals; every reference is
  `central_name`-scoped (ADR 0002).

### Non-goals (for the first iteration)

- No certified-alarm (EN 50131 / VdS) compliance claims. This is a
  smart-home alarm, not a certified intrusion system.
- No camera/video verification pipeline (journal hooks are provided).
- No Matter exposure: as of today's matter.js HEAD schema there is no
  intrusion-alarm-panel device type to mirror; revisit when CHIP ships one.
- No reproduction of the HmIP app's "Scharfschalten pro" decentral direct-link
  provisioning — that is a HAP/cloud feature not reachable via XML-RPC
  (see §9).

---

## 2. Hard safety invariants

These invariants override every feature. Each one is enforced by code at the
lowest possible layer **and** pinned by a contract test (§17). They exist
because the single worst failure mode of a DIY alarm is *"the siren is
screaming and nothing can stop it."*

- **S1 — Bounded activation, on every path.** Every siren activation the
  engine **sends or provisions** carries a **finite duration**.
  Engine-sent commands: default 180 s, hard ceiling 600 s, configurable per
  output but never unbounded; the engine never writes an acoustic tone with
  `DURATION_VALUE = 0` or an effectively infinite duration. Acoustic
  outputs backed by generic switch actuators (plug-in sirens, §7) are
  activated with a device-side auto-off (`ON_TIME`) written atomically
  with the switch-on; an actuator that cannot express one is refused as an
  acoustic output. Provisioned direct links (§9, Tiers B/C): the LINK
  profile **must encode a bounded on-time**, verified by read-back after
  provisioning; link profiles that cannot express a bound (permanent-ON)
  are refused. The bound is also
  **cumulative**: each incident carries a persisted acoustic-seconds ledger
  and re-trigger cycle counter (§10.2), so crash/restart loops cannot sum
  bounded activations into an unbounded one.
- **S2 — Watchdogged stop.** Every engine-sent activation schedules an
  engine-side stop at `T + duration + grace`. The stop is *verified* by
  reading back `ACOUSTIC_ALARM_ACTIVE` / `OPTICAL_ALARM_ACTIVE`; failure
  retries with `CommandPriorityCritical` — but only until the device-side
  bound has provably elapsed. After that the device has self-terminated
  (S1) and the unverifiable stop converts into a health incident instead
  of retrying forever (a siren smashed off the wall must not burn radio
  budget indefinitely).
- **S3 — Silencing is never gated.** "Silence sirens" works without a PIN
  (configurable per surface, default on for human surfaces), without a
  confirm dialog, from every surface (SPA, REST, WS, MQTT, `hmcli`), in
  every state. It is the one deliberate exception to the SPA rule that
  destructive actions go through the confirm dialog: silencing must be a
  single tap. Silence is **incident-scoped and persistent** (§5): it
  cancels every remaining scheduled activation of the current incident —
  including restore-driven ones — and never cancels notification outputs.
- **S4 — Reconciliation adopts before it stops.** On daemon start and on
  every central reconnect, the engine reads the active-state of all known
  sirens and reconciles **state-aware**: a sounding siren whose zone is
  armed (or that is targeted by an always-hot link tier or hazard policy)
  is **adopted** as a triggered incident — journaled, notified, kept
  sounding within S1 bounds — because it is evidence of a trigger during
  the blind window, not an error. Only a siren whose owning zone is
  disarmed, with no always-hot link and no declared third-party owner
  (per-siren "shared with CCU programs" flag), is stopped immediately.
- **S5 — Stop beats everything.** Stop/silence commands use
  `CommandPriorityCritical`, bypass coalescing/throttling queues, and are
  attempted even when the circuit breaker for the interface is open
  (single probe attempt). Within the engine's own queue, radio budget is
  arbitrated in strict order **stop > trigger > chirp**: re-triggers are
  rate-limited, and chirp/countdown output degrades first (ticks thin out,
  then drop) when `DUTY_CYCLE` headroom shrinks. The CCU-side duty-cycle
  limiter is a shared resource loom monitors but cannot reserve within —
  the engine keeps its own transmissions frugal so headroom for a stop
  exists in practice.
- **S6 — Arm ≠ trap.** Disarm is always possible from at least one
  strongly-authenticated surface in every failure mode, and the document
  names which (§10.3, §11): operator-session SPA/REST and host-local
  `hmcli` survive code-store failure and PIN-source lockouts. For Tier B
  zones (§9.2), *engine disarm* and *physical disarm* are distinguished:
  when the ARMSTATE mirror write cannot be confirmed, disarm returns a
  partial-success with a loud, persistent "hardware still armed" warning
  instead of pretending completion. A broken configuration must never
  produce a state where every disarm path is refused.
- **S7 — Fail-visible.** Every failed output command, every unreachable
  siren, every skipped notification surfaces as a journal entry, a health
  signal, and (when armed) a UI warning. Silent degradation is a real
  hazard in DIY alarm deployments — e.g. an alarm that has quietly been
  non-functional for weeks after a broken update — so loom exposes an
  explicit alarm-health status instead (§12.5).

---

## 3. Prior art

Three systems were analysed in depth; their designs directly shaped this
concept.

### 3.1 Homematic IP app (HmIP-HAP cloud / HmIP-HCU1 local)

The official solution (Anwenderhandbuch §8.4–8.5) defines exactly three
security modes:

- **Unscharf** — everything off.
- **Hüllschutz** (perimeter) — an explicitly user-curated subset of security
  devices, picked in a per-room checklist (Hauptmenü → Sicherheit →
  Hüllschutz). Typically door/window contacts and rotary handles.
- **Vollschutz** (full) — *all* devices assigned to the "Sicherheit"
  solution; there is deliberately no separate picker.

Further characteristics worth mirroring or consciously rejecting:

- **Einbruchalarm vs Gefahrenalarm**: intrusion sensors only alarm while
  armed; hazard sensors (smoke, water) alarm 24/7 independent of mode. This
  split is adopted here as the *always-on* sensor class (§6).
- **Scharfschaltmodus pro/basic**: "pro" refuses to arm while any sensor is
  open or unreachable; "basic" arms anyway and admits sensors as they
  close. (Details reported by the community but to be verified in the live
  app: whether pro also checks battery state, and whether the refusal
  names the offending sensor.) Loom's per-sensor flags subsume both (§6).
- **Exit delay** ("Scharfschaltverzögerung") exists; a true **entry delay
  does not** — the community works around it with per-contact report delays,
  which is exploitable (closing the door inside the window resets the timer
  and no alarm fires). Loom implements a real entry delay in the engine.
- **Sirens**: separate indoor/outdoor config blocks; on-time default 3 min
  (4/5/6 selectable); 9 acoustic tones, 4 optical patterns, test-alarm
  button, optional arm/disarm confirmation chirp. Outdoor sirens are legally
  capped at 3 min continuous in Germany; the ASIR-O reportedly auto-stops
  acoustics at 3 min in firmware (unconfirmed — §18).
- **Silent alarm** ("Stiller Alarm"): suppress sirens and alarm light, push
  notification only — adopted as a per-mode output policy (§7).
- **Rauchwarnmelder-Alarm** option: enrolls all HmIP smoke detectors as
  additional sounders during an intrusion alarm (the manual carries a
  battery-life caveat). Adopted as the smoke-sounder output class,
  assignable per mode — e.g. Vollschutz only (§7).
- **Acknowledgement**: the app's alarm message offers "Abbrechen" and
  "Bestätigen", where "Bestätigen" dismisses the alarm **and disarms** the
  active protection mode (per the manual; the exact current-app semantics
  of "Abbrechen" remain to be verified). Loom separates the three verbs
  cleanly: *silence* (outputs off, alarm stays), *acknowledge* (journal),
  *disarm* (state change) — see §5.
- **Offline story**: with a HAP in basic mode, no cloud ⇒ **no alarm at
  all**. In pro mode the HAP provisions direct radio links sensor→siren so
  alarming survives an internet outage (with a 10 s delay), and the KRCA key
  fob can disarm the siren offline. This provisioning is HAP-internal and
  **not reproducible via a CCU / XML-RPC** (§9). The HCU1 runs automations
  locally — the same local trust model loom offers, though its integration
  surface (developer-mode Connect API) is far narrower than loom's
  MQTT/REST/WS planes.
- **Event log**: originally a security-only "Alarmprotokoll" (100 entries);
  current app versions ship a combined "Ereignisprotokoll" listing the last
  500 entries. Loom's journal is SQLite-backed with retention policies
  instead (§13).

### 3.2 Alarmo (Home Assistant, github.com/nielsfaber/alarmo)

The strongest DIY prior art and the UX reference for sensor configuration:

- **Zones × modes**: each zone is its own panel entity with per-mode exit /
  entry / trigger delays; five modes (`armed_away/home/night/vacation/
  custom_bypass`); optional master panel spanning zones (all-or-nothing
  arming semantics surprised users — loom lets a master arm report partial
  failure instead).
- **Sensor types as presets**: door / window / motion / tamper /
  environmental preset the mode matrix and flags; everything individually
  overridable. Adopted (§6) — and improved, because loom *knows* the device
  type from the CCU channel/profile and can preset with high confidence.
- **Per-sensor flags** (adopted nearly verbatim, §6): use exit delay, use
  entry delay (with per-sensor entry-delay override), always-on,
  allow-open-after-arming, arm-after-closing, auto-bypass,
  trigger-when-unavailable.
- **Sensor groups**: 2 distinct sensors within a time window to trigger
  (cross-zoning for false-alarm reduction). Adopted, generalised to N-of-M.
- **Failure handling**: `failed_to_arm` event with blocking-sensor list;
  force-arm with bypass list; ready-to-arm signal per mode. All adopted.
- **Known pain points deliberately avoided**: no built-in countdown chirp
  (loom orchestrates chirps on sirens/MP3 players, §7); its actions engine
  is a second automation system users find opaque (loom emits events and
  keeps automation in the user's tooling); a sensor-hold/trigger-duration
  delay ("sensor must stay active for N seconds") is a long-open request
  (issues #1289, #1014 — loom ships it as a debounce/hold flag, §6.2);
  silent-failure risk after broken updates (loom: alarm-health surface,
  S7).

### 3.3 Native CCU & the Homematic hardware contract

- The classic WebUI offers the "Alarmzone 1" ALARM sysvar and alarm-message
  acknowledgement — a primitive but battle-tested pattern. Loom can
  optionally **mirror its zone state into CCU sysvars** so existing CCU
  programs and other CCU consumers interoperate (§13.5).
- **HmIP-ASIR / ASIR-O / ASIR-2** (already modelled by loom's siren custom
  data point): one writable channel (`:3`,
  `ALARM_SWITCH_VIRTUAL_RECEIVER`) with four write-only parameters —
  `ACOUSTIC_ALARM_SELECTION` (18 values: continuous `FREQUENCY_*` alarm
  tones plus short confirmation tones like `DISARMED`,
  `EXTERNALLY_ARMED`, `DELAYED_*`, `EVENT`, `ERROR`),
  `OPTICAL_ALARM_SELECTION` (blink patterns + `CONFIRMATION_SIGNAL_0..2`),
  `DURATION_UNIT` (S/M/H) and `DURATION_VALUE` (0–16343). All four must be
  written together as **one `putParamset`** (a lone `setValue` has no
  effect). Stopping = writing the disable-defaults. Feedback via
  `ACOUSTIC_ALARM_ACTIVE` / `OPTICAL_ALARM_ACTIVE` events. The short
  confirmation tones are the natural building block for arm/disarm chirps
  and countdown feedback.
- **HmIP sirens have no on-device arm state.** Unlike the classic
  HM-Sec-Sir-WM (channel 4 `ARMSTATE`: `DISARMED / EXTSENS_ARMED /
  ALLSENS_ARMED / ALARM_BLOCKED`), an ASIR is a dumb output. Consequence:
  under a CCU, HmIP arming state can only live in the central — this drives
  the direct-link decision in §9.
- **HmIP-WKP keypad**: validates PINs on-device (8 user slots). The wire
  contract: channels 1–16 are ACCESS_TRANSCEIVER pairs, two per user slot
  — the odd channel emits `PRESS_LOCK`, the even channel `PRESS_UNLOCK`
  (each with a writable `ACCESS_AUTHORIZATION` to enable/disable the
  slot); `CODE_ID`, `CODE_STATE`, the lockout flags
  (`BLOCKED_TEMPORARY`/`BLOCKED_PERMANENT`), `USER_AUTHORIZATION_01..08`
  and `SABOTAGE` all live on channel `:0`. Ideal arm/disarm intent source
  with built-in user attribution (§11).
- **Remotes (KRCA/KRC4)**: `PRESS_SHORT/LONG` events per key; mapping to
  arm modes is a pure engine convention.
- **Health signals** an alarm engine must watch: `SABOTAGE` (+
  `SABOTAGE_STICKY`), `UNREACH`/`STICKY_UNREACH`, `LOW_BAT`/`LOWBAT`,
  `OPERATING_VOLTAGE_STATUS`, `DUTY_CYCLE`, `CONFIG_PENDING`, `ERROR_CODE`.
- **Duty cycle**: HmIP devices/CCU are limited to 1 % TX time per hour.
  Siren re-triggers are radio-expensive; the engine rate-limits them and
  prioritises stop commands within its own queue (S5).

### 3.4 Market scan takeaways (Ajax, Ring, Bosch, ABUS, DSC, Heimdall)

Features that shaped the catalogue in §15: Ajax **night mode** as a true
third mode and **group/partition model** with followed groups; Ring **entry
chirps**, **door chime while disarmed**, and **duress codes**; SIA CP-01
**cross-zoning** and **swinger shutdown** (auto-bypass a zone after N trips
per arm cycle); DSC **bell squawk** (1 chirp on arm, 2 on disarm);
walk-test mode; Ajax supervision intervals and jamming detection (loom
analogue: cyclic-report monitoring + duty-cycle metrics); escalation chains
with per-user ordering; geofencing as *reminders*, not silent auto-arm.

---

## 4. Core concepts & terminology

| Term | Meaning |
|---|---|
| **Zone** | An independently armable partition ("Erdgeschoss", "Garage") with its own state machine, sensor set, outputs, and delays. Minimum one; zones may span multiple centrals. |
| **Arm mode** | A named protection level of a zone. Built-in: `perimeter` (Hüllschutz), `full` (Vollschutz). Optional: `night`, `vacation`, `custom`. Each mode selects a sensor subset and its own delays/output policy. |
| **Sensor** | A binary trigger source bound to a data point (`central_name` + `DataPointKey`), e.g. a window contact's `STATE`, a motion detector's `MOTION`, a sabotage flag. Carries a type, a mode matrix, and behaviour flags. |
| **Output** | An alarm consequence: siren (acoustic/optical), light actuator, chirp emitter, notification target (MQTT/webhook/WS), CCU sysvar mirror. Grouped into *output policies* per mode (loud/silent/…). |
| **Incident** | One trigger episode of a zone: cause, timestamps, output activations, silenced flag, re-trigger counter, acoustic-seconds ledger. The unit that silence/acknowledge act on. |
| **Journal** | The persistent, filterable event log of everything the alarm engine does or observes. |
| **Master panel** | Optional synthetic panel aggregating all zones (mirrors Alarmo/HA semantics). |

Mode-naming mapping (UI is localized, wire names are stable):

| Engine mode | German UI | English UI | HA / MQTT state |
|---|---|---|---|
| `disarmed` | Unscharf | Disarmed | `disarmed` |
| `perimeter` | Hüllschutz | Perimeter | `armed_home` |
| `full` | Vollschutz | Full protection | `armed_away` |
| `night` | Nachtschutz | Night | `armed_night` |
| `vacation` | Urlaub | Vacation | `armed_vacation` |
| `custom` | Benutzerdefiniert | Custom | `armed_custom_bypass` |

The HmIP mental model (Hüllschutz = curated subset, Vollschutz = everything)
is preserved as the *default preset*: new door/window/handle sensors are
proposed for `perimeter`+`full`+`night`, motion sensors for `full` only —
but unlike the HmIP app, every assignment is editable per sensor and per
mode (Alarmo's matrix, §6.2).

**Naming note.** Loom already has an unrelated "alarm" surface: the CCU
*Alarmmeldungen* (REST `/alarm-messages`, WS `alarm_messages.*`,
`hub.alarm_message`). The alarm engine deliberately uses a distinct
namespace — WS category `alarm_panel`, REST under `/api/v1/alarm/`, UI
section "Alarmanlage" / "Alarm system" — and UI copy + docs consistently
distinguish "CCU alarm messages" (upstream CCU notifications) from the
alarm engine.

---

## 5. Arm-state machine

One instance per zone. States follow the HA `alarm_control_panel`
vocabulary so every integration maps 1:1:

```
                 arm(mode)                 exit delay elapsed
   ┌───────────┐ ───────────► ┌─────────┐ ─────────────────► ┌────────────┐
   │ disarmed  │              │ arming  │                    │ armed_<m>  │
   └───────────┘ ◄─────────── └─────────┘                    └────────────┘
        ▲   ▲       cancel /       │                              │
        │   │       failed-arm     │ instant sensor               │ delayed sensor
        │   │                      ▼ (no exit delay flag)         ▼
        │   │                 ┌───────────┐   entry delay    ┌─────────┐
        │   └──── disarm ──── │ triggered │ ◄──────────────  │ pending │
        │                     └───────────┘   elapsed        └─────────┘
        │                          │                              │
        └────────── disarm ────────┘◄──────────── disarm ─────────┘

   triggered ──(trigger time elapsed)──► post-trigger policy:
       • return to armed_<m>  (default; siren already stopped by S1/S2)
       • disarm               (opt-in "disarm after trigger")
       • re-trigger up to N cycles per incident (persisted counter;
         swinger shutdown caps it; silence cancels all remaining cycles)
```

Rules:

- **Arming** (`disarmed → arming`): the engine evaluates readiness first.
  Blocking sensors (open, unreachable, sabotage, low-battery — each class
  configurable as *block / warn / ignore*) either fail the arm (default,
  HmIP-"pro"-like) or are bypassed (`force=true`, bypass list recorded and
  broadcast, Alarmo-like). Exit delay per mode (0 = immediate). Sensors
  flagged `arm_after_closing` complete the arming early when they close.
- **Pending** (`armed → pending`): only sensors flagged `use_entry_delay`
  route here; others trigger instantly. The pending countdown is
  broadcast (WS/MQTT) for UI countdowns and chirp orchestration. A valid
  disarm during `pending` produces **no alarm** — this is the real entry
  delay HmIP lacks.
- **Triggered**: outputs fire according to the mode's output policy (§7).
  The triggering sensor, timestamp, and cause are recorded as an
  **incident** and pushed to every surface. Further sensor events during
  `triggered` are journaled (they matter for verification) but do not
  restart output cycles beyond the incident's re-trigger policy.
- **Three distinct verbs** (differences matter — the HmIP app conflates
  them):
  - `silence` — stop all sounding outputs now **and cancel every remaining
    scheduled activation of the current incident** (including
    restore-driven re-fires after a restart — the silenced flag is
    persisted with the incident). State stays `triggered`; notification
    outputs are never cancelled. Silence also covers always-on
    hazard/panic output policies, which otherwise bypass the state
    machine. A genuinely *new* incident may sound again.
  - `disarm` — end the alarm, go to `disarmed` (implies silence).
  - `acknowledge` — mark the journal incident as seen (no state effect).
- **Always-on sensors** (hazard class, §6) bypass the arm-state machine:
  they trigger their own output policy 24/7, exactly like HmIP's
  Gefahrenalarm — but their incidents obey the same silence/bounds rules.
- Every transition is journaled, audited (with identity), and published as
  a bus event + WS broadcast + MQTT state/event message.

Timer implementation note: all countdowns run on the injected `clock.Clock`
seam. Persistence stores a redundant tuple per timer — wall-clock deadline,
remaining duration, persist-time wall timestamp, boot counter — so restarts
can restore or expire them deterministically *and* detect implausible
clocks (§10.2).

---

## 6. Sensor model

### 6.1 Sensor types & auto-detection

Unlike Alarmo (which sees anonymous HA `binary_sensor`s), loom knows the
device model, channel type, and profile of every candidate. Sensor typing
is therefore **derived, not hand-assigned**:

| Type | Derived from (examples) | Default modes | Default flags |
|---|---|---|---|
| `door` | SHUTTER_CONTACT on door-class devices, lock channels | perimeter, full, night, vacation | entry delay, arm-after-closing |
| `window` | SHUTTER_CONTACT / ROTARY_HANDLE | perimeter, full, night, vacation | — |
| `motion` | MOTION_DETECTOR / PRESENCEDETECTOR channels | full, vacation | exit delay, entry delay |
| `tamper` | SABOTAGE / ERROR_SABOTAGE flags of enrolled devices | all modes **+ disarmed (warn)** | — |
| `hazard` | SMOKE_DETECTOR_ALARM_STATUS, WATERLEVEL/MOISTURE, gas/CO | always-on | always-on, own output policy |
| `panic` | keypad/remote keys, wall buttons designated as panic | always-on | always-on, loud or silent per assignment |

The proposal engine reuses the existing custom-data-point registry and the
calculated intrusion/smoke sensors (`internal/model/calculated/
derived_binary.go`). Everything remains overridable per sensor.

### 6.2 Per-sensor configuration

Each enrolled sensor carries:

- **Mode matrix** — checkboxes: participates in `perimeter` / `full` /
  `night` / `vacation` / `custom`.
- **Flags** (Alarmo-compatible semantics):
  - `use_exit_delay` — may be active while leaving.
  - `use_entry_delay` — routes to `pending` instead of instant trigger;
    an optional per-sensor entry-delay override (seconds) replaces the
    mode default — the "garage door needs 60 s but the front door 15 s"
    case (Alarmo supports this too).
  - `always_on` — armed-state-independent (hazard/panic class).
  - `allow_open_after_arming` — may remain open through arming; only a
    re-activation after clearing triggers.
  - `arm_after_closing` — closing during exit delay completes the arm
    (5 s debounce).
  - `bypass_auto` — if blocking at arm time, exclude until next disarm
    instead of failing the arm.
  - `trigger_when_unavailable` — treat `UNREACH`/vanishing while armed as
    an activation (default: warn only).
  - `hold_time` — activation must persist for N seconds before it counts
    (debounce/hold; the long-open Alarmo request #1289/#1014, useful for
    twitchy PIRs and doors that rattle in wind).
- **Group membership** — optional cross-zoning group: a group fires only
  when ≥ N distinct members activate within T seconds (defaults N=2,
  T=60 s). Kills single-PIR false alarms.
- **Swinger shutdown** — after N activations of the same sensor within one
  arm session (default 2), the sensor is auto-bypassed and a warning is
  raised (SIA CP-01 behaviour; prevents the broken-contact-all-night
  scenario).

### 6.3 Health gating

Sensor health continuously feeds the **ready-to-arm** computation per mode:

- `UNREACH`/`STICKY_UNREACH` → blocking (configurable to warn),
- `SABOTAGE` → blocking + 24/7 tamper event,
- `LOW_BAT` → warning badge, configurable to blocking,
- stale cyclic report (no update for > device-specific interval) →
  warning ("supervision", Ajax-style),
- central disconnected (`ConnectivityChangedEvent`) → all its sensors
  degrade; policy per zone (§10.3).

Ready-to-arm state per mode is pushed to the UI and MQTT so dashboards can
show *why* an arm would fail before the user tries.

---

## 7. Output model: loud, silent, and everything between

Outputs are grouped into a per-mode **output policy**, so "Vollschutz is
loud, Hüllschutz is silent, night mode is indoor-chirp-only" is plain
configuration:

| Output class | Backing | Notes |
|---|---|---|
| **Acoustic siren** | siren CDP (`HmIP-ASIR*`, `HM-Sec-Sir-WM`, MP3 players) | tone + duration per policy; bounded per S1. |
| **Switched siren (plug-in)** | any switch actuator (Schaltsteckdose, e.g. HmIP-PS/PSM, wall/DIN switch actuators) driving a mains plug-in siren | mirrors the HmIP app, where pluggable switches serve as alarm actuators. Declared acoustic-class by the user; activated as `ON_TIME` + `STATE` in one write so the device auto-offs (S1); actuators without `ON_TIME` are refused for this class. UI labels it convenience-grade: no sabotage contact, no battery backup, trivially unpluggable. |
| **Smoke-detector sounders** | smoke-siren CDP (`HmIP-SWSD`: `SMOKE_DETECTOR_COMMAND` = `INTRUSION_ALARM` / `INTRUSION_ALARM_OFF`) | HmIP "Rauchwarnmelder-Alarm" parity: enrolled smoke detectors double as intrusion sounders, assignable per mode — typically `full` only. The SWSD exposes no duration parameter, so the device-side bound S1 relies on is unverified (§18); until confirmed they are engine-watchdogged only (S2) and the UI states that plus the battery-life caveat. Feedback-loop safe: the derived smoke hazard sensor maps only `PRIMARY/SECONDARY_ALARM`, never the commanded `INTRUSION_ALARM`. |
| **Optical siren** | ASIR optical channel | may run longer than acoustic (still bounded); useful after the 3-min acoustic cap. |
| **Alarm light** | any switch/dimmer actuator | "Alarm-Licht": on at trigger, off at silence/disarm; optionally flashing. |
| **Chirps & chimes** | ASIR confirmation tones (`EXTERNALLY_ARMED`, `DISARMED`, `EVENT`, …), MP3 player, actuator pulse | arm/disarm squawk (1×/2× DSC-style), exit-delay countdown ticks (accelerating), entry-delay warning tone, optional door chime while disarmed. Chirps yield first under duty-cycle pressure (S5). |
| **Notifications** | MQTT event topic, webhook adapter, WS broadcast | shipped (0.43.1, APIVersion 2.26.0): each enrolled notification output fires a dedicated, one-shot `alarm_panel.notification` event at incident-fire time — for every mode it's enrolled in, including silent policies — never cancelled by silence; per-output `notify_mqtt`/`notify_webhook` toggles gate the MQTT and webhook planes independently (both default on). Push delivery to a phone is still delegated to HA companion / ntfy / user tooling; loom guarantees the event, not the phone. |
| **Sysvar mirror** | CCU ALARM/value-list sysvars | optional interop with existing CCU programs (§13.5); targets either a managed value-list variable (created by loom) or an existing operator-owned ALARM-type variable (shipped 0.43.1). |

**Capability-derived output enrollment (shipped 0.43.0, APIVersion 2.25.0).**
`GET /api/v1/alarm/output-candidates` enumerates, from the live domain
model, every channel that can back a device-backed class (acoustic/
optical siren, switched siren gated on `ON_TIME`, smoke sounder, alarm
light, chirp) plus the device's real ENUM label lists (ASIR acoustic
tones and optical patterns, MP3P soundfiles). The SPA add-output dialog
lists these ground-truth candidates per class with a channel picker;
an expert toggle falls back to the unfiltered device list for wiring
the automatic gate misses. `PUT .../outputs` soft-validates the saved
set: a resolvable channel that cannot carry its declared class is
rejected with 422, while an unresolvable target (CCU down) still saves
— the fault journal remains its safety net.

**Generic actuator outputs (expert).** Beyond the built-in classes, *any*
actuator loom models (switch, dimmer, relay, …) can be enrolled as an
alarm output. The user declares the class, and the class — not the device
type — decides which invariants apply: **acoustic** enrollments get the
full siren treatment (device-side `ON_TIME` bound written atomically with
the switch-on, for dimmers together with `LEVEL`; silence coverage; stop
verification per S2), while **visual/other** enrollments follow the
alarm-light lifecycle (on at trigger, off at silence/disarm — an alarm
light staying on is annoying, not dangerous, so no hard bound is forced).
This keeps the model generic without weakening the safety story.

**Silent alarm** = an output policy without acoustic outputs: notifications,
optical signal and alarm light only (HmIP's "Stiller Alarm"). **Loud** adds
acoustic sirens. Policies also split **indoor vs outdoor** sirens (mirroring
the HmIP app's separate blocks) so an outdoor ASIR-O can be excluded at
night while indoor sirens still fire.

Panic inputs choose their own policy: a *loud panic* (all sirens, no entry
delay) and a *silent panic / duress* (notifications only — nothing audible
locally) are distinct built-ins.

Test mode: every output offers a **test fire** (short duration, reduced
pattern — optical-only option for neighbours' sake) from the UI, mirroring
the HmIP "Test-Alarm" button.

Sharing: the same device channel may be enrolled as an output in more
than one zone. The output manager arbitrates stops per channel: a stop
from one zone is deferred while any other zone still demands the
channel, so a shared siren keeps sounding until the **last** alarming
zone silences it. Demand tracking is in-memory — after a daemon restart
every stop proceeds (the safe direction, device off).

---

## 8. Siren safety: "a siren can always be silenced"

This section makes the S-invariants concrete for Homematic hardware.

### 8.1 Command discipline

- All ASIR writes go through the existing siren CDP, which batches
  `ACOUSTIC_ALARM_SELECTION`, `OPTICAL_ALARM_SELECTION`, `DURATION_UNIT`,
  `DURATION_VALUE` into **one** `putParamset` (a partial write is a no-op
  on the device). Priority `CommandPriorityCritical` for stop, `High` for
  trigger.
- The engine's output driver **clamps every acoustic duration** to
  `min(policy duration, 180 s default, 600 s hard cap)` (S1). Longer alarm
  phases are realised as *bounded re-triggers* with duty-cycle-aware
  spacing — never as one long activation — and count against the
  incident's persisted cycle cap and acoustic-seconds ledger.
  Optical-only activations may use longer bounds (no legal/noise
  constraint) but stay finite.
- Stops write the disable-defaults (`DISABLE_ACOUSTIC_SIGNAL` +
  `DISABLE_OPTICAL_SIGNAL` + default duration) — the only stop mechanism
  the hardware offers.
- **Switch-backed acoustic outputs** (plug-in sirens, generic actuators
  declared acoustic, §7): activation writes `ON_TIME` + `STATE` (dimmers:
  `ON_TIME` + `LEVEL`) atomically via the collector — never a bare
  `STATE = true`, which would leave the device latched on if the engine
  dies before the stop. Stop writes `STATE = false` (`LEVEL = 0`) and is
  verified by reading the state back (S2). Actuator channels without a
  usable `ON_TIME` are rejected for the acoustic class at enrolment time,
  not at alarm time.
- **Smoke-detector sounders** (§7): on = `SMOKE_DETECTOR_COMMAND`
  `INTRUSION_ALARM`, stop = `INTRUSION_ALARM_OFF`, verified via
  `SMOKE_DETECTOR_ALARM_STATUS` read-back. No duration parameter exists,
  so until the §18 live test confirms a device-side self-termination,
  the S2 stop watchdog is their only bound — the enrolment UI says so
  explicitly, and a failed stop escalates like any unverified siren
  stop.

### 8.2 The silence path, end to end

Redundant, independent ways to stop a sounding siren — ordered from most to
least convenient:

1. **SPA**: full-screen triggered view with a giant `SILENCE` button
   (no PIN, no confirm — S3). Also on the dashboard tile.
2. **REST/WS/MQTT**: `silence` command on the zone (and a global
   `silence-all`), usable from any automation, wall tablet, or script.
3. **`hmcli`**: `hmcli alarm silence --all` for shell/SSH emergencies —
   and the documented break-glass path (§11).
4. **Keypad/remote**: WKP `PRESS_UNLOCK` / KRCA disarm key → engine disarm
   (implies silence). Works whenever daemon+CCU are alive.
5. **Hardware timeout**: engine-sent activations self-terminate at their
   bounded duration (S1) even if daemon *and* CCU die mid-alarm.
   Link-fired activations (Tiers B/C) self-terminate at the bounded
   on-time the engine wrote into the LINK profile at provisioning time —
   this is why S1 refuses unboundable link profiles. The ASIR-O
   reportedly also hard-stops acoustics at 3 min in firmware (§18).
   Exception: smoke-detector sounders have no known device-side bound
   (§7) — their stop depends on the engine watchdog, which is why they
   carry an explicit UI caveat until §18 resolves.
6. **Last resort** (documented in the user guide): siren battery/power
   removal — never required by design, listed for completeness.

The engine treats *failure to silence* as its highest-severity health
incident: if `ACOUSTIC_ALARM_ACTIVE` does not drop within the verify
window, it retries at critical priority until the device-side bound has
provably elapsed (S2), then converts to a persistent health incident +
UI banner + notification.

**Honest limit:** while daemon or CCU are down, a link-fired siren (Tier
B/C) cannot be silenced remotely — only its device-side bound (and, where
supported, a directly-linked key fob) ends it. The tier picker states this
in the UI before a user enables link tiers (§9.2).

### 8.3 What we deliberately do not do

- No unbounded "siren until disarm" option, even behind an expert flag.
  Continuous alarming is expressed as verified re-trigger cycles that stop
  the moment the engine stops asking (or, if the engine is dead, at the
  bound).
- No PIN requirement on `silence` from human surfaces by default. An
  attacker gaining "silence" can quiet sirens, but cannot suppress
  notifications or the journal (§16 discusses the automated-adversary
  case and the per-surface policy). A resident locked out of silencing is
  the worse failure. (Operators can still require a PIN per surface —
  with a stern config-help warning.)

---

## 9. Direct device links: with / without

"Mit/Ohne Direktverknüpfung" needs a differentiated answer, because the
hardware families genuinely differ.

### 9.1 The facts

- Loom can already enumerate, create, and delete direct links
  (`GetLinks`/`AddLink`/`RemoveLink`, LINK paramsets, link-profile presets)
  and has audit + UI surfaces for them.
- The ASIR receiver channel `:3` exposes a full LINK profile (condition
  values, JT state machine, on/off delays), so sensor→siren links are
  *encodable* — but community/ELV evidence says the CCU refuses at least
  KRCA→ASIR-2 and SWSD→ASIR-2 pairings, and sensor→ASIR links are
  unverified on CCU firmware (open question §18).
- **HmIP sirens have no arm state** (§3.3). A static direct link
  window-contact→ASIR fires **whenever the window opens — armed or not**.
  Link conditions (`COND_VALUE_*`) filter on the transmitted value, not on
  any central arm state.
- Toggling links per arm cycle is not viable: link changes are CONFIG
  writes that battery devices only pick up on wakeup, and they burn radio
  budget.
- The HmIP app's offline-capable "pro" arming provisions links through the
  HAP's own security plumbing (`ALARM_COND_SWITCH` multicast zones) — not
  accessible via XML-RPC.
- Classic BidCos sirens (HM-Sec-Sir-WM) **do** have on-device `ARMSTATE`,
  and classic sensor→siren links honour it. Decentral, arm-aware alarming
  is a solved problem on that hardware generation.

### 9.2 The concept

Three operating tiers, selectable per zone (and mixable):

- **Tier A — Central logic (default, "ohne Direktverknüpfung").** The
  engine is the single decision point. Full feature set (modes, delays,
  silent/loud, cross-zoning, journal). Availability = daemon + CCU;
  hardened per §10. This is the only tier that can implement Hüllschutz/
  Vollschutz semantics on HmIP hardware.
- **Tier B — Arm-aware direct links (classic hardware, "mit
  Direktverknüpfung").** For HM-Sec-Sir-WM-class sirens the engine
  *provisions static sensor→siren direct links once* — always with a
  **bounded on-time written into the LINK profile and verified by
  read-back** (S1) — and then merely mirrors the zone's arm state into
  the siren's `ARMSTATE` (`EXTSENS_ARMED` ≈ perimeter, `ALLSENS_ARMED` ≈
  full, `DISARMED`). Result: triggering keeps working with the daemon
  **and** the CCU dead; the engine adds journaling, delays and
  notifications on top when alive.
  **Degraded-disarm contract:** when the `ARMSTATE` mirror write cannot
  be confirmed (central down), the engine's disarm returns a
  *partial-success*: engine state goes `disarmed`, but the UI and a
  notification carry a loud, persistent warning — "hardware still armed;
  opening a linked door will fire the siren" — a critical-priority mirror
  write is queued for reconnect, and the window is journaled. Engine
  disarm and physical disarm are never silently conflated (S6).
- **Tier C — Always-armed direct links (niche, "mit Direktverknüpfung,
  ungated").** Static links (same bounded-on-time rule) for inputs whose
  semantics are arm-independent: panic buttons → siren, sabotage chains,
  a shed/garage loop that is *always* hot. Explicitly labelled in the UI:
  "fires even when disarmed; while loom or the CCU is down it can only be
  silenced by its device-side time bound". Offered only where the CCU
  accepts the pairing.

Link-fired activations bypass the engine's queue entirely: S5 rate
limiting and silence-vs-re-trigger arbitration do not apply to them while
they happen; the engine observes them (`ACOUSTIC_ALARM_ACTIVE`), adopts
them as incidents (S4), and counter-stops them when silenced.

The setup wizard explains the tiers in one screen and recommends A (+C for
panic) for HmIP-only installations and A+B for mixed/classic ones. The
sensor picker badges every sensor/siren with its tier capability so the
"why can't my ASIR work offline?" question is answered *in the UI*, not in
a forum thread.

---

## 10. Resilience & recovery

### 10.1 Threat model for availability

| Failure | Behaviour |
|---|---|
| Daemon crash/restart | State restored from SQLite (§10.2); engine-sent sirens bounded per S1, link-fired sirens bounded by their provisioned LINK on-time; reconciliation adopts-or-stops (S4). systemd/container restart policy recommended in docs; the restart-loop breaker (§10.2) prevents restore-driven re-fire loops. |
| CCU reboot | Readiness-gated reconnect (existing `ccu_readiness` machinery); on reconnect: reconcile siren states (adopt-before-stop, S4), re-evaluate sensor states (a window opened during the gap is detected by value refresh), journal the blind window. |
| Single sensor lost while armed | Per-sensor `trigger_when_unavailable` or warn; health badge; journal. |
| Whole central lost while armed | Zone policy: `alert` (default — loud notification, stay armed with degraded coverage shown) or `trigger` (paranoid) — never silent. Tier B zones additionally surface the physical-disarm limitation (§9.2). |
| MQTT broker down | Alarm logic unaffected (engine is domain-core, not MQTT-dependent); events buffered per existing MQTT reconnect semantics; WS/REST unaffected. |
| Clock jumps (NTP, RTC-less hosts) | Timers persist a redundant tuple (§5); restores that detect an implausible clock never auto-escalate (§10.2). |
| Radio duty-cycle exhaustion | Stop > trigger > chirp arbitration inside the engine's queue (S5); chirps degrade first; DUTY_CYCLE surfaces as health warning while armed. |

### 10.2 State persistence & restart semantics

Persisted continuously (SQLite): zone state, active mode, bypass list,
the active **incident** (trigger cause/time, **silenced flag, re-trigger
cycle counter, cumulative acoustic-seconds ledger, trigger-time
deadline**), timer tuples (wall deadline + remaining duration +
persist-time timestamp + boot counter), output activations in flight,
journal.

**Clock-plausibility rule.** On restore the engine first checks the
current wall clock against the persisted timestamps. If the clock is
implausible — before the persist time, or jumped beyond a sanity bound
(typical on RTC-less hosts like a Raspberry Pi where the daemon starts
before NTP sync) — elapsed time is treated as *unknown*: timers restore
to their conservative pre-timer state (armed, with a journal warning),
and the engine **never auto-escalates to `triggered` or auto-completes an
arm off an untrusted clock**. Timer evaluation resumes once the clock is
plausible.

Restore policy on boot (given a plausible clock):

- `armed_*` → restore armed; immediately re-evaluate all member sensors
  against fresh values (an opened window during downtime becomes a trigger
  or a pending, per its flags).
- `arming` (exit delay) → if the deadline passed while down, complete the
  arm (readiness re-checked); otherwise resume the remaining countdown.
- `pending` (entry delay) → if the deadline passed, escalate to
  `triggered` (better a late alarm than a silently swallowed one);
  otherwise resume the countdown.
- `triggered`, trigger-time deadline **not yet elapsed**, incident not
  silenced → restore `triggered` and resume the output policy, counting
  restored activations against the incident's persisted cycle cap and
  acoustic ledger (S1) — a reboot can neither kill an alarm nor create an
  eternal siren.
- `triggered`, trigger-time deadline **elapsed while down** → do not
  re-fire; execute the post-trigger policy (return to armed / disarm) and
  journal the missed window.
- `triggered`, incident silenced → stay `triggered`, outputs stay silent
  (S3 persistence).
- **Restart-loop breaker**: after K restore-driven output re-fires of the
  same incident (default K=3), the incident degrades to optical +
  notifications only and raises a health incident — a crash-looping
  daemon must not turn bounded activations into an unbounded neighbour
  nuisance.
- Always: reconciliation pass per S4 (adopt-before-stop).

### 10.3 The user can always take over

- Disarm/silence work in every state, from every surface (S3/S6), and are
  role-gated but never state-gated. Degradation tiers for code-store
  failure and lockouts are specified in §11.
- **Maintenance mode** per zone: suspends evaluation without deleting
  configuration (Ajax-style deactivation) — for renovations, battery swap
  sessions, sensor repairs. Journaled, visually loud in the UI.
- Every automation input (schedules, presence hints, MQTT commands) can be
  overridden manually at any time; manual actions always win and are
  attributed (`changed_by`).

---

## 11. Users, codes & permissions

- **Alarm codes are separate from loom accounts.** Household members who
  will never see the config UI still need arm/disarm PINs. Codes are
  stored as salted argon2id hashes; never logged, never round-tripped
  (masked like `cfg:"secret"` values).
- Per code: display name, PIN, permissions (`arm`, `disarm`,
  `silence-without-code` override), zone restriction, optional validity
  window (guest codes), enabled flag.
- **Policy toggles** per zone: code required for arming (default off),
  code required for disarming (default on when codes exist), code required
  for silence (default **off** — S3; configurable per surface, §16).
- **Duress code** (optional, off by default): disarms normally in the UI
  but emits a silent `duress` event to notification targets. No local
  trace in the visible journal until resolved (attacker-visible screens
  stay clean); full audit trail persists internally.
- **Keypad/remote identities**: WKP user slots (channel pairs, §3.3) and
  remote addresses map to named alarm users, so `changed_by` is populated
  for hardware interactions too. Whether WKP on-device PIN slots mirror
  engine codes or stay independent is an open question (§18).
- **Guided remote-key bindings** (shipped 0.43.0/0.43.1, APIVersion
  2.25.0): `GET /api/v1/alarm/remote-key-candidates` enumerates every
  physical remote/wall-button key channel — `PRESS_SHORT`/`PRESS_LONG`
  as ordinary VALUES data points, read straight from the live model;
  virtual remote channels are excluded. The codes editor's guided
  builder assembles the binding document from a key picker plus
  trigger/action/zone selects; raw JSON remains the expert fallback and
  the only path for virtual remote channels. Security keyfobs
  (HmIP-KRCA and peers) sort first in the picker with an "alarm keyfob"
  badge.
- **Wrong-code handling**: rate limiting + exponential lockout per source,
  journal entries, optional tamper event after N failures (keypads
  additionally lock out on-device).
- **Degradation & break-glass (S6):**
  - On code-store read failure, disarm remains possible **only** for
    strongly-authenticated surfaces — a logged-in operator session
    (SPA/REST) or host-local `hmcli` — never for anonymous MQTT / sysvar /
    keypad paths. The event is journaled as a security incident.
  - Authenticated operator-role sessions are exempt from PIN-source
    lockout (the session is the second factor) — an attacker spamming
    wrong codes at a wall tablet or keypad cannot lock the owner out of
    their own panel.
  - Host-local `hmcli` is the documented break-glass: whoever has shell
    access to the daemon host already owns the system.
- REST/WS surfaces require the existing operator role for *configuration*.
  For arm/disarm/silence, a narrower **`alarm-control`** permission is
  proposed so wall tablets can get a least-privilege token — note that
  loom's auth model today is a strict three-role hierarchy without token
  scopes, so this is a cross-cutting auth extension (own work item,
  likely its own ADR), not an alarm-package detail.

---

## 12. UI / UX concept

A new top-level SPA section **Alarm** with six views. All surfaces use the
shared design system (LoadingState/EmptyState/ErrorState, toasts, confirm
dialog — with the documented silence exception), full de/en localization,
and dark-mode tokens.

### 12.1 Overview (the panel)

One card per zone + optional master card:

```
┌─ Erdgeschoss ────────────────────────────────────────────┐
│   ● ARMED — Vollschutz            since 22:41, by Markus │
│                                                          │
│   [ Unscharf ]  [ Hüllschutz ]  [ VOLLSCHUTZ ]  [ Nacht ]│
│                                                          │
│   Ready ✓ perimeter   ✓ full   ⚠ night (1 sensor open)  │
│   ├─ ⚠ Fenster Bad — offen                               │
│   └─ 🔋 Bewegungsmelder Flur — Batterie schwach          │
└──────────────────────────────────────────────────────────┘
```

- Mode buttons show per-mode readiness inline (tooltip lists blockers);
  arming with blockers opens the **bypass sheet**: the exact sensor list
  with per-sensor "bypass" checkboxes and a `Force arm` action — nothing
  is bypassed silently.
- During exit/entry delay: a **countdown ring** around the active mode
  button plus remaining seconds, mirrored by chirp output.
- **Triggered** replaces the card content with a red, high-contrast
  surface:

```
┌─ Erdgeschoss ─────────────────────── ⏱ 00:47 ────────────┐
│   ██  ALARM — Einbruch                                   │
│   Ausgelöst: Terrassentür (Wohnzimmer), 03:12:41         │
│                                                          │
│   ┌──────────────────────┐  ┌───────────────────────┐    │
│   │   🔇  SIRENEN AUS     │  │   ⛨  UNSCHARF (PIN)   │    │
│   └──────────────────────┘  └───────────────────────┘    │
│   Verlauf: 03:12:41 Terrassentür → 03:12:44 Sirenen an   │
└──────────────────────────────────────────────────────────┘
```

  `SIRENEN AUS` acts on first tap (S3). Disarm shows the PIN pad only if a
  code is required.

### 12.2 Sensor selection — the centrepiece

The user's requirement: selecting the relevant sensors/actors with
excellent UX and visual clarity. Design:

- **Two-pane layout.** Left: filter rail — rooms (from CCU room
  assignments), interface, sensor type, assignment status
  ("unassigned only"), free-text search. Right: a responsive card grid,
  grouped by room.
- **Live cards.** Every candidate sensor is a card with device icon, name,
  room, type chip, and a **live state badge** (`zu`/`offen` with colour,
  motion pulse animation, tamper/battery/unreach glyphs). Selection is a
  checkbox overlay; bulk actions operate on the current filter result
  ("assign all 12 filtered window contacts to Hüllschutz").
- **Mode matrix per card.** Compact toggle chips on each card:

```
┌─ 🪟 Fenster Küche West ──────────── ● zu ─┐
│  Fensterkontakt · Küche · HmIP-SWDO       │
│  Modi:  [✓ Hülle] [✓ Voll] [✓ Nacht]      │
│  Flags: Eintrittsverzögerung ▸ 15 s       │
└───────────────────────────────────────────┘
```

- **Detail drawer** per sensor for the full flag set (§6.2), group
  membership, delay overrides — progressive disclosure keeps the grid
  scannable.
- **Type-preset onboarding.** The picker opens pre-populated: loom's
  device knowledge proposes type, modes, and flags (§6.1); the user
  reviews room by room instead of building from zero. This beats both
  references — HmIP offers only a bare checklist, Alarmo starts empty.
- **Matrix view toggle.** Power users flip the same data into a dense
  table: rows = sensors, columns = modes + key flags, sticky header,
  keyboard navigation — the fastest way to audit 60 sensors.
- **Actor/output picker** mirrors the pattern: siren cards (with tier
  badge per §9.2 including its silencing caveat, test-fire button,
  tone/duration preview), switch actuators for plug-in sirens (declared
  acoustic, with a convenience-grade badge: unpluggable, no sabotage
  contact), light actuators, chirp outputs, notification targets — each
  assigned to output policies with a loud/silent/indoor/outdoor toggle
  group. An **expert toggle** widens the list from the curated
  siren/light candidates to *every* modelled actuator (switch, dimmer,
  relay, …); enrolling one asks for the class declaration (§7), and
  acoustic-class candidates without `ON_TIME` are shown greyed-out with
  the reason instead of failing later.

### 12.3 Setup wizard

First-run guided flow: ① name the zone → ② select sensors inline
(security-device candidates with search + show-all toggle) → ③ select
outputs inline (from the output-candidate list, with the same search box)
→ ④ delays per mode
(trigger time capped at the engine's 600 s ceiling) → ⑤ codes pointer
(managed on the Codes tab once the zone exists) → ⑥ summary + finish.
Both candidate lists (② sensors, ③ outputs) additionally offer a room
filter, a function ("Gewerk") filter, and a name/room/model sort — each
row shows the device's model label and its room/function assignment so
the operator can tell candidates apart without leaving the wizard.
Finish applies everything against the API in order (create zone →
sensors → outputs); a partial failure keeps the created zone id so a
retry updates instead of duplicating. Each step is skippable (skip
clears that step's collected data); wizard progress lives in a store
and survives navigating away and back. Chirp fine-tuning, per-mode
loud/silent grouping, and the walk test stay on their dedicated tabs
after finish.

### 12.4 Walk test

Live checklist view (DSC-style): arm-less test session where every sensor
activation ticks its row green with a timestamp, optionally with a local
chirp as feedback. Progress "23/28 sensors verified"; un-tripped sensors
stand out. Ends with a journal report.

### 12.5 Journal & health

- Filterable journal (per zone, event class: arm/disarm/trigger/bypass/
  fault/test, time range, user) with CSV export.
- **Alarm-health panel**: sirens reachable, last successful siren test,
  sensor supervision status, duty-cycle headroom, pending warnings, silence
  storms (§16) — the anti-silent-failure surface (S7). A traffic-light
  summary of this panel is embedded on the Overview.

---

## 13. API & integration surface

### 13.1 REST (`assets/openapi.yaml`, APIVersion bump)

CRUD + control, all `central_name`-agnostic (zones are daemon-level).
The namespace is `/api/v1/alarm/…` — distinct from the existing CCU
alarm-message surface (`/api/v1/alarm-messages`), see the naming note in
§4:

```
GET/POST/PUT/DELETE  /api/v1/alarm/zones[/{id}]
GET/PUT              /api/v1/alarm/zones/{id}/sensors
GET/PUT              /api/v1/alarm/zones/{id}/outputs
GET                  /api/v1/alarm/output-candidates      (channels per device-backed class; APIVersion 2.25.0)
GET                  /api/v1/alarm/remote-key-candidates  (physical remote/wall-button key channels; APIVersion 2.25.0)
POST                 /api/v1/alarm/zones/{id}/arm        {mode, code?, force?, skip_delay?, bypass?[]}
POST                 /api/v1/alarm/zones/{id}/disarm     {code?}
POST                 /api/v1/alarm/zones/{id}/silence
POST                 /api/v1/alarm/silence-all
GET                  /api/v1/alarm/zones/{id}/readiness   (per-mode, with blocker list)
GET                  /api/v1/alarm/journal?zone=&class=&from=&to=
POST                 /api/v1/alarm/zones/{id}/walktest/…  (start/stop/status)
GET/POST/PUT/DELETE  /api/v1/alarm/codes[/{id}]
POST                 /api/v1/alarm/outputs/{id}/test
```

### 13.2 WebSocket (`assets/wsapi.json`)

Commands mirror REST (`alarm.arm`, `alarm.disarm`, `alarm.silence`, …)
with role gating, registered under a **new WS category `alarm_panel`** so
they never collide with the existing `alarm_messages.*` commands;
broadcasts: `alarm.state_changed`, `alarm.countdown` (tick),
`alarm.readiness_changed`, `alarm.triggered`, `alarm.journal_appended`,
`alarm.health_changed`.

### 13.3 MQTT (HA discovery + raw plane)

- New discovery component **`alarm_control_panel`** per zone (+ master):
  state topic with HA state vocabulary, command topic
  (`ARM_HOME`/`ARM_AWAY`/`ARM_NIGHT`/`ARM_VACATION`/`ARM_CUSTOM_BYPASS`/
  `DISARM` + JSON form with `code`), `code_arm_required`/
  `code_disarm_required` reflecting zone policy. Discovery names come from
  the i18n catalogues like every other discovery entity.
- Event topic (JSON, Alarmo-compatible spirit): `TRIGGER`,
  `FAILED_TO_ARM` (+ blocking sensors), `INVALID_CODE`, `SILENCED`,
  `DURESS`, with `zone`, `changed_by`, `open_sensors`, `delay` fields.
  `NOTIFICATION` (shipped 0.43.1, APIVersion 2.26.0) extends this
  vocabulary: one entry per enrolled notification output at fire time,
  carrying an `output` field (the output's name, or its id when
  unnamed) — see `docs/mqtt-topic-schema.md`.
- Raw command plane: `<base>/alarm/<zone>/set`. Note this is a
  **deliberate extension** of the topic schema: zones are daemon-level,
  so the topic omits the `<central>` segment that every existing command
  topic carries — precedented only by the read-only `<base>/bridge/*`
  topics. `docs/mqtt-topic-schema.md` (and ADR 0011) get an amendment
  when this ships.

### 13.4 Webhook & events

Every journal-grade event is published on the internal bus (new
`hmevent.Alarm*Event` types) and fans out through the existing webhook
adapter — escalation chains, DECT-call bridges, ntfy etc. stay user-land.
Notification-output firings (shipped 0.43.1) forward under event type
`alarm_panel.notification`, carrying `output_id`/`output_name` in the
detail payload, gated per output by its own `notify_webhook` toggle.

### 13.5 CCU sysvar mirror (optional)

Per zone, the engine can maintain a CCU system variable so existing CCU
programs, other CCU frontends, and legacy logic interoperate. Shipped
0.43.1: the output's add dialog no longer asks for a device — it asks
for the central and the variable target, which comes in two kinds:

- **Managed value-list variable** (default) — Loom creates it on the
  CCU automatically (e.g. `Loom-Alarm-EG`: Unscharf/Hüllschutz/
  Vollschutz/Alarm) and owns its lifecycle. `sysvar_name` is editable
  from the output card (previously not settable from the SPA at all,
  which left the enrollment a silent no-op).
- **Existing operator-owned ALARM-type variable** — a pre-existing
  bool sysvar the operator already created. The mirror writes `true`
  while the zone is `triggered` and `false` otherwise, and never
  creates or retypes the variable. Because a bool carries no mode, this
  target accepts **no inbound intents** at all — `sysvar_allow_disarm`
  does not apply to it, only to the managed value-list variant.

Saving an output set with class `sysvar_mirror` and no `sysvar_name` is
rejected with 422 (a nameless mirror would be a silent no-op).

The mirror is split by direction, because a sysvar write cannot carry a
PIN and the CCU's auth model is far weaker than loom's:

- **Outbound (state export)** — always safe, on by default when the
  mirror is enabled.
- **Inbound (intents)** — applies only to the managed value-list
  variant (the existing-variable target takes no inbound intents at
  all, above); per-zone policy, default **arm-only**: a third-party
  sysvar write may arm (escalate) but never disarm or
  silence. Disarm-via-sysvar can only be enabled for zones whose
  operator has explicitly disabled the disarm-code requirement *and*
  acknowledged a warning that anything able to write a ReGa sysvar
  (CCU WebUI, CCU programs, LAN XML-RPC clients) could then disarm the
  alarm. "A sysvar write cannot disarm a code-protected zone" is pinned
  by a contract test (§17).

---

## 14. Architecture inside OpenCCU-Loom

Hexagonal placement — the engine is domain core, adapters stay thin:

```
internal/alarm/
├── engine/        — per-zone state machines, readiness, incidents,
│                    timers (clock.Clock)
├── outputs/       — output drivers (siren, light, chirp, sysvar, notify)
├── codes/         — domain facade: hashing, rate limiting, lockout
├── journal/       — domain facade: journal semantics, retention policy
└── wiring.go      — bus subscriptions, registry iteration, health hooks
```

- **Inputs**: `events.Subscribe` on `DataPointValueChangedEvent`,
  `DeviceTriggerEvent` (keys), `SysvarChangedEvent` (mirror intents),
  `ConnectivityChangedEvent` / `ClientStateChangedEvent` (degradation),
  with `WithKey` filters for cheap per-central scoping.
- **Outputs** reuse the siren CDP, generic switch DPs, and the existing
  command path (collector, priorities, circuit breaker) — no new transport
  code.
- **Persistence**: new goose migration (`alarm_zones`, `alarm_sensors`,
  `alarm_outputs`, `alarm_codes`, `alarm_state`, `alarm_incidents`,
  `alarm_journal`). Per repo convention the SQL access structs live under
  `internal/store/sqlite/` (following the incident-store pattern);
  `internal/alarm/{codes,journal}` are domain-level facades over those
  stores — the same split `configstore` uses over `config_sections`.
- **Configuration split**: relational alarm data (zones/sensors/outputs/
  codes) is first-class domain data managed via REST/UI — *not* config-file
  material. Global engine settings (enabled, defaults, journal retention,
  MQTT exposure) form a new config section; every `cfg:` leaf ships
  `config.field.*`/`config.help.*` labels in both locales per the standing
  rule.
- **Multi-CCU**: sensors/outputs reference `(central_name, DataPointKey)`;
  the engine iterates the `CentralRegistry` and stays correct with any
  number of centrals (ADR 0002).
- **Observability**: Prometheus metrics (`alarm_state`,
  `alarm_triggered_total`, `alarm_siren_active`, `alarm_ready`,
  `alarm_output_failures_total`), health-tracker integration, audit
  entries (`ActionAlarmArm/Disarm/Silence/ConfigChange/CodeChange`) with
  identity.
- **SPA**: `routes/Alarm*.svelte` + nav entry, generated API types,
  Playwright e2e + visual baselines for panel, picker, triggered view
  (light+dark).

---

## 15. Feature catalogue & phasing

| # | Feature | Phase | Notes |
|---|---|---|---|
| 1 | Zones with perimeter/full modes, exit/entry delays, ready-to-arm, force/bypass | **MVP** | core |
| 2 | Loud/silent output policies, indoor/outdoor split, alarm light, plug-in sirens via `ON_TIME`-bounded switch actuators | **MVP** | §7 |
| 3 | Siren safety invariants S1–S7, incident ledger, reconciliation | **MVP** | non-negotiable |
| 4 | Sensor picker UI + presets + matrix + live badges | **MVP** | §12.2 |
| 5 | Journal + audit + WS/MQTT/REST surface + HA discovery panel | **MVP** | §13 |
| 6 | Codes (PIN arm/disarm), `changed_by`, keypad/remote intents | **MVP** | §11 |
| 7 | Always-on hazard & panic classes (24/7) | **MVP** | HmIP Gefahrenalarm parity |
| 8 | Smoke-detector sounders (Rauchwarnmelder-Alarm parity, per-mode e.g. Vollschutz) | **MVP** | §7; gated on the §18 stop-semantics live test |
| 9 | State restore across restarts (incl. loop breaker, clock plausibility) | **MVP** | §10.2 |
| 10 | Night/vacation/custom modes | P2 | trivial on top of the matrix |
| 11 | Chirp orchestration (countdown, squawk) | P2 | ASIR confirmation tones |
| 12 | Cross-zoning groups + swinger shutdown + sensor hold time | P2 | false-alarm reduction |
| 13 | Walk test + alarm-health panel + siren self-test scheduler | P2 | §12.4/12.5 |
| 14 | Maintenance mode, auto-bypass unavailable | P2 | Ajax-style |
| 15 | Generic actuator outputs (any switch/dimmer/relay, expert, user-declared class) | P2 | §7 |
| 16 | Tier B classic-hardware ARMSTATE mirroring + static links | P2 | §9.2, needs classic sirens |
| 17 | Guest codes with validity windows, duress code | P2 | §11 |
| 18 | Sysvar mirror (outbound; inbound arm-only intents) | P2 | §13.5; two target kinds shipped 0.43.1 — managed value-list variable, or an existing operator-owned ALARM-type variable (no inbound intents) |
| 19 | Schedules & auto-arm reminders (presence hints via API) | P3 | reminders, not silent auto-arm |
| 20 | Escalation chains with acknowledgement | P3 | webhook/notify ordering |
| 21 | Pre-alarm stage (internal chime before sirens) | P3 | Bosch-style |
| 22 | Auto-rearm after quiet period | P3 | retail/ABUS pattern |
| 23 | Door chime while disarmed | P3 | ASIR `EVENT` tone / MP3P; Ring-style |
| 24 | Tier C always-armed direct links (panic/sabotage) | P3 | pending live verification §18 |
| 25 | Camera snapshot hook into journal | P3 | webhook-based, no pipeline in loom |
| 26 | Per-token `alarm-control` scope (auth-model extension) | P3 | own ADR, §11 |
| 27 | Matter security-panel exposure | future | blocked on Matter/matter.js device type |

---

## 16. Security considerations

- Codes: argon2id-hashed, constant-time compare, rate-limited, never in
  logs/MQTT/journal payloads; REST DTOs mask them like `cfg:"secret"`.
- Duress events are excluded from user-visible surfaces until resolved but
  fully retained in the internal audit trail.
- Role model: configuration requires operator role; arm/disarm/silence sit
  behind authenticated surfaces (the narrower `alarm-control` token scope
  is a follow-up auth extension, §11).
- **Threat: compromised MQTT broker / rogue publisher.** Honest
  assessment: since `silence` is deliberately ungated (S3), an automated
  adversary on the command plane *can* suppress loud alarming by looping
  silence — what it cannot do is disarm a code-protected zone, recall
  events already published, or touch notification/journal delivery
  (silence never cancels those). Mitigations: **silence-storm detection**
  (N silences within one incident → journal escalation + health alert +
  notification), and a per-surface silence policy — operators who treat
  MQTT as untrusted can require a code for MQTT-originated silence while
  keeping SPA/`hmcli` ungated.
- Inbound sysvar intents are arm-only by default; disarm-via-sysvar
  requires an explicit, warned opt-in (§13.5) — the CCU's weak auth model
  never silently becomes a disarm surface.
- All state-changing actions are audited with identity and source surface.
- The journal is append-only from the engine's perspective; deletion is a
  privileged, audited retention operation.

---

## 17. Testing strategy

- **Contract tests** (`tests/contract/`):
  - every engine-issued siren activation carries a bounded duration,
    every switch-backed acoustic activation writes `ON_TIME` atomically
    with the switch-on, and every provisioned LINK profile encodes a
    bounded on-time (S1) — walks the output driver and link provisioner,
    rejects any unbounded path;
  - no engine-issued acoustic activation after silence within the same
    incident — including across a simulated restart (S3 persistence);
  - reconciliation adopts a sounding siren of an armed zone instead of
    stopping it (S4 adopt-before-stop), and stops one of a disarmed,
    link-free, unshared siren;
  - a sysvar write cannot disarm a code-protected zone (§13.5);
  - silence/disarm command paths never require confirmation state (S3/S6);
  - alarm MQTT discovery payloads validate against the HA
    `alarm_control_panel` schema;
  - WS/REST surface parity for arm/disarm/silence.
- **Engine unit tests** with `clock.Fake`: full state-machine matrix
  (delays, force/bypass, cross-zoning, swinger, hold time), the complete
  restart-restore table from §10.2 (including elapsed-trigger-deadline,
  silenced-incident, restart-loop-breaker, and implausible-clock rows),
  deterministic timer assertions.
- **Integration** (`tests/integration/`, godevccu): end-to-end
  arm → window-open event → pending → triggered → siren putParamset
  observed → silence → verified stop; central-loss degradation; boot
  reconciliation with a pre-seeded sounding siren in both the armed
  (adopt) and disarmed (stop) variants.
- **SPA**: vitest component tests + Playwright e2e (panel flow, picker
  matrix, triggered view incl. S3 single-tap silence) + visual baselines
  light/dark.
- **Live-CCU validation** (per the standing rule): any real siren/write
  test requires explicit user approval of the target device; sirens are
  tested with short optical-only patterns first.

---

## 18. Open questions

1. Does the indoor ASIR/ASIR-2 firmware hard-cap acoustics at 3 min like
   the ASIR-O reportedly does — and is the ASIR-O firmware cap itself
   real (currently community-reported, not manufacturer-documented)?
   S1 clamps either way. → supervised live test.
2. Exact semantics of `DURATION_VALUE = 0` with a tone selected (no-op vs
   infinite) — never emitted by the engine, but worth knowing for the
   reconciliation logic. → supervised live test.
3. Will CCU firmware accept sensor→ASIR `:3` direct links at all
   (LINK paramset exists; KRCA/SWSD pairings are refused)? Determines
   whether Tier C is viable on HmIP. → supervised live test.
4. WKP integration depth: is `CODE_ID` reliably delivered via XML-RPC
   events on all firmware, and how should on-device PIN slots relate to
   engine codes (mirror vs independent)?
5. Should the master panel arm zones best-effort (partial success with
   report) or all-or-nothing (Alarmo)? Current lean: best-effort with
   explicit per-zone result — matches G5.
6. Journal vs incident store: reuse `internal/store/sqlite/incidents.go`
   with a new incident class, or a dedicated `alarm_journal` table?
   Current lean: dedicated table (different query patterns, retention, and
   privacy rules for duress).
7. Current-app verification of HmIP behaviours the concept cites from the
   (pre-HCU) manual: "Abbrechen" semantics on the alarm message, whether
   "Scharfschalten pro" checks battery state and names the blocking
   sensor, and whether the basic/pro toggle is unchanged on the HCU1.
8. SWSD smoke-detector sounders: does a commanded `INTRUSION_ALARM`
   self-terminate after a device-side time, or does it sound until
   `INTRUSION_ALARM_OFF`? And does it propagate to grouped detectors
   (`SECONDARY_ALARM`) or stay local? Decides whether smoke sounders can
   ship on by default or stay an engine-watchdog-only opt-in.
   → supervised live test.

---

## 19. Implementation kickoff

Written for a fresh agent starting the implementation in a clean
environment — everything needed is in the repo; no prior session context
is assumed.

### 19.1 Reading order

1. `CLAUDE.md` — process rules that bind this work: the live-CCU write
   rule (explicit user approval **and** user-named target device), the
   three test pillars, spec-first REST changes, the interaction protocol
   (describe approach before coding).
2. This document end-to-end. §2 (invariants) and §17 (testing strategy)
   define the acceptance bar; §15 fixes the MVP scope.
3. The touch-point code, in this order: `internal/central/events` (bus)
   and `pkg/hmevent`, `internal/model/custom/siren/`, `internal/clock`,
   `internal/store/sqlite/incidents.go` + `migrations/` (store pattern),
   `internal/north/rest/handlers/incidents.go` + `router.go` (handler
   pattern), `internal/north/rest/ws/` (commands + broadcasts),
   `internal/north/mqtt/discovery.go` + `category_component.go`,
   `internal/audit`, `internal/configstore`.

### 19.2 Repo guards this work will trip — and their remedies

- **Reachability ratchet.** Every new `hmevent.Alarm*Event` type is
  unreachable for the dead-code analyzer (events dispatch through the
  generic bus), so the CI ratchet fails. Remedy: run
  `make reachability` and commit the refreshed
  `notes/parity/dead-code-*` baseline. Do **not** wire artificial callers
  or annotate the types.
- **API contract guard.** Any edit to `assets/openapi.yaml` /
  `assets/wsapi.json` needs **both** `make export-schemas` (schema
  digest) and an APIVersion bump (`internal/north/rest/handlers/
  info.go`); the PR-only guard rejects either half alone.
- **Generated SPA types.** After OpenAPI changes, run `make ui-types` to
  regenerate `assets/ui/src/lib/api/types.generated.ts`.
- **Config field labels.** Every new `cfg:` leaf needs
  `config.field.<path>` + `config.help.<path>` in both locales of
  `assets/ui/src/lib/i18n.ts` (`TestConfigFieldsHaveLabelsAndHelp`
  fails the build otherwise).
- **SPA visual baselines** are committed per platform; Linux baselines
  regenerate only inside the Playwright container (see CLAUDE.md,
  Testing Guidelines).

### 19.3 Suggested MVP slicing (one reviewable PR each)

1. **Store + engine core** — goose migration, zone/sensor/output/
   incident stores, per-zone state machine on `clock.Clock`, the full
   §10.2 restore table as unit tests. No north surface yet.
2. **Output drivers + siren safety** — siren/switch/smoke drivers, the
   S1/S2/S4/S5 behaviours, and the §17 contract tests. Gate: every
   S-invariant test green before anything user-facing exists.
3. **REST + WS surface** — openapi/wsapi specs, handlers, broadcasts,
   audit actions (plus the guards from §19.2).
4. **SPA** — panel, sensor/actor picker, triggered view; vitest + e2e +
   visual baselines.
5. **MQTT** — `alarm_control_panel` discovery, event/command topics,
   `docs/mqtt-topic-schema.md` amendment (§13.3).
6. **Codes, keypad intents, hazard/panic classes** — rounds out the
   remaining MVP rows of §15.

Each slice lands green on `make test` + `make lint`; integration tests
join from slice 2 on (the godevccu simulator fleet already contains
HmIP-ASIR/SWSD/WKP devices).

### 19.4 What not to decide alone

- The §18 live tests (siren stop semantics, direct-link acceptance)
  involve writes to real devices — user approval and a user-named
  target device are mandatory (CLAUDE.md critical rule).
- The `alarm-control` token scope (§11) is a cross-cutting auth-model
  extension: own ADR + user sign-off before building it.
- Deviations from the §5/§10.2 state-machine semantics or the §2
  invariants are design changes, not implementation details — surface
  them instead of silently adapting.

---

## 20. References

- Homematic IP Anwenderhandbuch §8.4–8.5 (security modes, Alarmkonfiguration):
  <https://homematic-ip.com/sites/default/files/downloads/homematic-ip-anwenderhandbuch.pdf>
- HmIP-ASIR-2 / HmIP-WKP / HmIP-KRCA manuals:
  <https://homematic-ip.com/sites/default/files/downloads/hmip-asir-2-um-web.pdf>,
  <https://homematic-ip.com/sites/default/files/downloads/hmip-wkp-um-web.pdf>,
  <https://www.eq-3.de/Downloads/eq3/downloads_produktkatalog/homematic_ip/bda/HmIP-KRCA_UM_web.pdf>
- eQ-3 on offline security via direct links:
  <https://homematic-ip.com/de/news/neue-sicherheitsloesung-funktioniert-auch-offline>
- HmIP app Ereignisprotokoll (500 entries):
  <https://homematic-ip.com/de/news/zahlreiche-neuerungen-fuer-homematic-ip-app>
- HmIP-HCU1 local architecture: <https://de.elv.com/elvjournal/homematic-ip-hcu1-home-control-unit/>;
  HCU Connect API: <https://github.com/homematicip/connect-api>;
  community HA integration: <https://github.com/Ediminator/homematicip-hcu>
- Alarmo: <https://github.com/nielsfaber/alarmo>; alarmo-card:
  <https://github.com/nielsfaber/alarmo-card>; open sensor-hold requests:
  <https://github.com/nielsfaber/alarmo/issues/1289>,
  <https://github.com/nielsfaber/alarmo/issues/1014>
- HA MQTT alarm panel schema:
  <https://www.home-assistant.io/integrations/alarm_control_panel.mqtt/>
- Siren CCU-program contract (4-parameter write):
  <https://technikkram.net/blog/2020/07/31/quicktipp-ansprechen-einer-hmip-alarmsirene-im-zentralenprogramm/>
- CCU alarm-central tutorial:
  <https://technikkram.net/blog/2019/02/05/tutorial-alarmzentrale-mit-homematic-ip-ccu3-oder-raspberrymatic/>
- Scharfschalten basic/pro:
  <https://technikkram.net/blog/2019/04/20/kurzerklaerung-hmip-access-point-scharfschalten-basic-scharfschalten-pro/>
- ASIR-O 3-minute limit (community report):
  <https://technikkram.net/blog/2019/06/29/homematic-ip-alarmsirene-aussen-asir-o-endlich-verfuegbar/>
- Direct-link refusals (KRCA/SWSD ↔ ASIR-2):
  <https://homematic-forum.de/forum/viewtopic.php?t=61950>,
  <https://homematic-forum.de/forum/viewtopic.php?t=78041>
- Ajax night mode / groups / deactivation:
  <https://support.ajax.systems/en/what-is-night-mode/>,
  <https://support.ajax.systems/en/ajax-group-mode/>,
  <https://support.ajax.systems/en/devices-auto-deactivation/>
- Ring duress code:
  <https://ring.com/support/articles/p5isp/Understanding-Duress-Code-for-Ring-Alarm>
- SIA CP-01 (cross-zoning, swinger shutdown):
  <https://www.securityindustry.org/industry-standards/cp-01-2019/>
