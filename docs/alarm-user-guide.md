# Alarm System — Operator Guide

This guide explains how to set up and operate the built-in alarm system
("Alarmanlage") from the Config UI. It describes what each page and each
setting does, in operator terms. The design rationale, the full safety
model, and the engineering constraints live in
[`alarm-concept.md`](./alarm-concept.md); researched device behaviour is
in [`alarm-assumptions.md`](./alarm-assumptions.md).

The alarm section is reachable at `#/alarm` and has seven tabs —
Overview, Sensors, Outputs, Policies, Codes, Journal, Walk test — plus
the re-runnable Setup wizard.

---

## Table of contents

1. [Concepts in two minutes](#1-concepts-in-two-minutes)
2. [The safety promise](#2-the-safety-promise)
3. [Getting started: the Setup wizard](#3-getting-started-the-setup-wizard)
4. [Overview — the panel](#4-overview--the-panel)
5. [Sensors](#5-sensors)
6. [Outputs](#6-outputs)
7. [Policies](#7-policies)
8. [Codes](#8-codes)
9. [Walk test](#9-walk-test)
10. [Journal](#10-journal)
11. [Integrations](#11-integrations)

---

## 1. Concepts in two minutes

| Term | Meaning |
|---|---|
| **Area** | An independently armable partition ("Ground floor", "Garage") with its own sensors, outputs, delays and state. You can have one or many; an area may span multiple CCUs. |
| **Arm mode** | A named protection level of an area: `perimeter` (shell only — doors and windows), `full` (everything, including motion), plus optional `night`, `vacation` and `custom`. Each mode selects a sensor subset and its own delays and output behaviour. |
| **Sensor** | A trigger source bound to a device data point — a window contact, a motion detector, a smoke detector, a panic button. Each sensor carries a mode assignment and behaviour flags. |
| **Output** | An alarm consequence: a siren, an alarm light, a chirp tone, a notification event, or a CCU system variable mirror. |
| **Incident** | One trigger episode: cause, timestamps, which outputs fired, whether it was silenced. Silence and acknowledge act on the incident. |
| **Journal** | The persistent log of everything the alarm engine does or observes. |

A typical flow: you arm an area into a mode → the exit delay counts down
→ the area is armed. A sensor activation either triggers instantly or
starts the entry delay (pending). If nobody disarms in time, the area
triggers: outputs fire according to the mode's policy and an incident
opens. You silence the sirens or disarm; the incident is recorded in the
journal.

## 2. The safety promise

Two properties hold everywhere, in every configuration:

- **Every siren activation is finitely bounded.** One trigger phase runs
  at most 600 seconds (default 180); acoustic outputs additionally carry
  a device-side or engine-watchdog stop. There is no configuration in
  which a siren sounds forever.
- **A siren can always be silenced.** The Silence action works on the
  first tap, from every surface, without a code by default — and it also
  cancels every remaining scheduled re-fire of the current incident,
  including after a daemon restart. Notification outputs are never
  cancelled by silence.

Everything else — delays, policies, code requirements — is configuration
on top of these two invariants.

## 3. Getting started: the Setup wizard

Launch the wizard from the header of the alarm section (button "Setup
wizard"). It walks through six steps, each skippable, and can be re-run
per area at any time:

1. **Areas** — create at least one area. One per floor is a good start.
2. **Sensors** — opens the sensor picker (see [Sensors](#5-sensors)).
3. **Outputs** — opens the output picker (see [Outputs](#6-outputs)).
4. **Delays & chirps** — per mode: exit delay (time to leave), entry
   delay (time to disarm after coming in), and alarm duration (how long
   one alarm phase runs, capped at 600 s).
5. **Codes & users** — managed on the Codes tab.
6. **Walk test** — verify every sensor before relying on the system.

The area is created disarmed. Run a walk test before you trust it.

## 4. Overview — the panel

One card per area:

- **Mode buttons** arm the area into a mode or disarm it. Each button
  carries a readiness dot: green = ready, amber = warnings (e.g. a low
  battery), red = blocked (e.g. an open window). Hovering shows the
  blockers.
- **Arming with blockers** opens the bypass sheet: the exact list of
  blocking sensors with per-sensor bypass checkboxes and a *Force arm*
  action. Nothing is ever bypassed silently; bypasses are recorded and
  end at the next disarm.
- **During exit/entry delay** a countdown ring runs around the active
  mode button.
- **When triggered** the card switches to a red surface with two large
  actions and one small one:
  - **Silence** — stops all sounding outputs *now* and cancels every
    remaining re-fire of this incident. The area stays in triggered
    state; notifications still go out. A genuinely new incident may
    sound again.
  - **Disarm** — ends the alarm and returns the area to disarmed
    (implies silence). Shows the PIN pad if a code is required.
  - **Acknowledge** — marks the incident as seen in the journal. No
    state effect.
- **Silence all** appears in the toolbar while any area is triggered.
- The **health badge** in the toolbar summarizes alarm health (sirens
  reachable, pending faults). Details appear in the journal.

## 5. Sensors

The sensor picker assigns sensors to an area and to arm modes. Loom
knows the device model and channel type of every candidate, so new
sensors arrive with a proposed type (door / window / motion / tamper /
hazard / panic) and sensible default modes — review, don't build from
zero.

- **Cards view**: one card per sensor with live state, type badge, mode
  chips and a flag summary. **Matrix view**: rows = sensors, columns =
  modes, the fastest way to audit many sensors.
- **Bulk bar**: select several sensors (or *Select all filtered*) and
  assign a mode to all of them at once.
- **Add sensor**: pick a device channel and parameter; the *Show all
  channels* toggle widens the list beyond the curated candidates.

### Per-sensor settings (detail drawer)

| Setting | Effect |
|---|---|
| **Modes** | Which arm modes the sensor participates in. A sensor not in the current mode is ignored entirely. |
| **Exit delay** | The sensor may be active while you leave: activations during the exit delay are ignored. Without it they trigger instantly. |
| **Entry delay** | An activation starts the pending countdown instead of triggering instantly — your time to disarm after entering. A valid disarm during the countdown produces **no** alarm. |
| **Entry delay override (s)** | Replaces the mode's entry delay for this sensor — e.g. 60 s for the garage door while the front door keeps 15 s. |
| **Always on** | Fires around the clock, independent of the armed state — for hazard sensors (smoke, water, gas) and panic buttons. Outputs follow the hazard/panic policies from the Policies tab, and incidents obey the same silence and bounding rules. |
| **Allow open after arming** | The sensor may stay open (e.g. a tilted window) through arming; only a fresh activation after it cleared triggers. |
| **Arm after closing** | Closing the sensor during the exit delay completes arming early, after a short settle time. |
| **Auto-bypass** | If the sensor would block arming, it is bypassed automatically until the next disarm instead of failing the arm. |
| **Trigger when unavailable** | Treats the sensor becoming unreachable while armed as an activation; off raises only a warning. |
| **Door chime while disarmed** | Plays the door-chime tone on chirp outputs when the sensor activates while the area is disarmed. |
| **Silent panic (duress)** | Panic sensors only: activations fire the panic policy with all acoustic outputs suppressed — notifications only. |
| **Hold time (s)** | The activation must persist this long before it counts — filters twitchy motion sensors and doors rattling in wind. A cleared activation is discarded. Never applied to always-on sensors. |
| **Cross-zoning group** | Sensors sharing a group name only trigger when a second member activates within 60 seconds; a lone activation is journaled but does not sound the alarm. Kills single-PIR false alarms. |

### Sensor health and readiness

Sensor health continuously feeds the per-mode *ready to arm* state shown
on the Overview: open sensors, unreachable devices and sabotage block
arming by default; a low battery warns. How each class behaves
(block / warn / ignore) is an area-level setting.

## 6. Outputs

Outputs are the consequences an alarm drives. The class you declare —
not the device type — decides which safety rules apply.

| Class | What it is | Notes |
|---|---|---|
| **Acoustic siren** | A real siren device (HmIP-ASIR family, HM-Sec-Sir-WM, MP3 players) | Tone and duration configurable; every activation bounded and stop-verified. |
| **Plug-in siren** | A mains siren behind a switch actuator | Convenience grade: no sabotage contact, no battery backup, trivially unpluggable. The actuator must support device-side auto-off (`ON_TIME`); ineligible actuators are refused. |
| **Smoke-detector sounder** | Enrolled smoke detectors double as intrusion sounders | Costs detector battery, usually sounds the whole smoke-detector group, no live test fire. Best assigned to full protection only. |
| **Optical siren** | The optical channel of a siren | Signals without noise; may run longer than the acoustic cap. |
| **Alarm light** | Any switch/dimmer actuator | On at trigger, off at silence or disarm. |
| **Chirp** | Short confirmation tones | Arm/disarm squawks, countdown ticks, door chime — never the loud alarm. |
| **Notification** | MQTT / WebSocket / webhook event | One-shot at fire time, for every mode it's enrolled in — including silent policies; never cancelled by silence. Toggle the MQTT and webhook delivery planes independently per output (both on by default). Push delivery to a phone is your notification tooling's job — Loom guarantees the event. |
| **Sysvar mirror** | A CCU system variable mirroring the alarm state | Two variable targets: a managed value-list variable Loom creates and keeps in sync, or an existing ALARM-type variable you already own — Loom then only writes `true`/`false` to it and never changes its type or creates it. |

**Add output**: pick a class, then a device channel — the dialog lists
only channels the live model confirms can carry that class (e.g.
sirens gated on their acoustic/optical capability, plug-in sirens on
`ON_TIME` support). The *Show all channels* toggle (expert) widens the
list to every modelled actuator, for wiring the automatic gate misses.
Saving an enrollment whose channel cannot carry its class is rejected;
a channel the CCU cannot currently reach still saves — the fault
journal covers it once it's back. Sysvar-mirror outputs skip the
device picker entirely: choose the central and either let Loom manage
the value-list variable, or tick *Existing variable* and name a sysvar
you already created as ALARM type — saving without a name is rejected.

Per output you can set the mode assignment, tone / light pattern
(device value-list labels; empty = device default), duration (acoustic
activations hard-capped at 600 s), an *Outdoor* marker (so policies can
exclude outdoor sirens), and *Shared with CCU programs* (Loom then never
auto-stops that output while the area is disarmed). Chirp outputs carry
three tone labels — arm squawk, disarm squawk, and the tick tone used
for countdown ticks, entry warnings and the door chime; an empty label
skips that chirp kind on the output. Notification outputs instead carry
the *Notify via MQTT* / *Notify via webhook* toggles; sysvar-mirror
outputs carry the variable name and, for the managed variant only,
*Allow disarm* (the existing-variable target never accepts inbound
intents, so the toggle does not apply to it).

**Test fire**: every output (except smoke sounders) offers a short,
bounded live test from its card — with an optical-only option for the
neighbours' sake. The test drives the real device.

## 7. Policies

Per-area rules beyond plain arm/disarm. Select the area at the top;
Save writes the whole set.

### Codes

When a code must be entered. These switches apply to anonymous surfaces
only (MQTT, keypad, remote key) — logged-in operator sessions and
host-local `hmcli` always pass, which is the documented break-glass
path.

- **Require code to arm** — off by default; arming is the safe
  direction.
- **Require code to disarm** — *Automatic* (default) requires a code as
  soon as the area has an enabled code; an area without codes never
  demands one, so you cannot lock yourself out. *Always* / *Never*
  override in either direction.
- **Require code to silence** — per source (MQTT / keypad / remote
  key), off by default: silencing a siren must never be gated harder
  than necessary.

### Hazard outputs / Panic outputs

The always-on output policies for hazard-class (smoke, water, gas) and
panic-class sensors. These fire around the clock, independent of the
armed mode:

- **Silent** — suppresses all acoustic outputs; notifications, optical
  signals and alarm lights still fire.
- **Exclude outdoor outputs** — skips outputs marked outdoor.
- **Enroll smoke-detector sounders** — additionally sounds the smoke
  detectors; use deliberately (battery, group fan-out).

A sensor marked as *silent panic* (duress panic) suppresses acoustic
outputs for its activations regardless of this policy — notifications
only.

### Pre-alarm

Per mode: a quiet pre-alarm phase before full escalation. For the
configured number of seconds only chirp, notification and light outputs
fire; then the full output policy escalates. Silencing during the
pre-alarm cancels the escalation. `0` disables it.

### Post-trigger & auto re-arm

One trigger phase is always time-limited; sirens stop when it ends no
matter what. *When the trigger phase ends* decides what happens next:

- **Return to armed** (default) — the area stays armed in its previous
  mode.
- **Disarm** — the area disarms. With *Auto re-arm after (s)* it
  re-arms into the pre-incident mode after that many quiet seconds; any
  sensor activity restarts the countdown.

### Schedules

Time-of-day entries per area, evaluated in the daemon's local time
zone. Each entry has a time, optional weekdays (none selected = every
day) and a target mode. With **Auto-arm** the area actually arms at
that time; without it the entry only raises a reminder when the area is
not in the expected mode at that time.

## 8. Codes

Alarm codes are separate from Loom login accounts — household members
who never see this UI still get arm/disarm PINs. PINs are stored as
salted hashes and never shown again.

- **Type**: *PIN* (typed on the PIN pad or anonymous surfaces),
  *Keypad slot* and *Remote key* (bind a hardware keypad user slot or a
  radio remote, so its actions are attributed to this name).
- **Permissions**: what the code may do — arm, disarm, silence.
- **Areas**: restrict a code to specific areas; nothing selected means
  all areas.
- **Validity window**: optional from/until timestamps — guest codes.
- **Duress code**: disarms exactly like a normal code but silently
  raises a duress event to the notification targets. Nothing appears in
  the visible journal until the incident is resolved. Never hand out a
  duress code casually.

**Binding a remote key**: the editor lists every physical remote/
wall-button key channel that can drive a code — short- and long-press
buttons read straight from the live model; virtual remote channels are
not listed here. Security keyfobs such as the HmIP-KRCA sort to the top
with an *alarm keyfob* badge. Pick the key, then the trigger (short or
long press), the action (an arm mode or disarm) and, optionally, the
area it applies to. Raw JSON binding remains available as an expert
fallback, and is the only way to bind a virtual remote channel.

Wrong codes are rate-limited with escalating lockout per source;
operator sessions are exempt from the lockout (an attacker spamming a
wall keypad cannot lock you out of your own panel).

## 9. Walk test

A walk test verifies sensors without arming the area and without firing
any alarm: start a session, walk the house and trip each sensor — every
activation turns its checklist row green with a timestamp. The progress
line shows `n/total sensors verified`; untested sensors stand out. Stop
the session when done; the result is recorded in the journal.

Run a walk test after initial setup, after adding or moving sensors,
and periodically (battery-powered contacts fail silently).

## 10. Journal

The persistent, filterable log of everything the alarm engine does or
observes — arming and disarming (with identity), triggers, bypasses,
silences, faults, tests and configuration changes. Filter by area,
event class and time range; *Export CSV* downloads the current view.

The journal is the anti-silent-failure surface: every degradation the
engine accepts (an unreachable siren, a failed stop verification, a
restart-restored incident) lands here instead of being swallowed.

## 11. Integrations

- **MQTT / Home Assistant**: each area appears as an
  `alarm_control_panel` entity via MQTT discovery (plus an aggregate
  master panel). Arm modes map to the HA vocabulary
  (`armed_home` = perimeter, `armed_away` = full, `armed_night`,
  `armed_vacation`, `armed_custom_bypass`). Enrolled notification
  outputs additionally publish a `NOTIFICATION` entry on the area's
  event topic and forward to configured webhook receivers — toggle
  each plane per output on the Outputs tab.
- **REST / WebSocket**: the full surface lives under `/api/v1/alarm`
  and the WS category `alarm_panel` — see `assets/openapi.yaml` and
  `assets/wsapi.json`. The `alarm.notification` broadcast mirrors the
  same notification-output firings for WS clients.
- **hmcli**: `hmcli alarm` is the host-local break-glass control —
  whoever has shell access to the daemon host already owns the system.

For the CCU side (existing programs), use the *Sysvar mirror* output
class and the sysvar-based arm intents described in
[`alarm-concept.md`](./alarm-concept.md) §13.5.
