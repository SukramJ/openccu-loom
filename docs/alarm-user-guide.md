# Alarm and Security & Safety — Operator Guide

This guide covers the two related daemon-level domains reachable from
the Config UI: the built-in **alarm system** ("Alarmanlage") — zones,
arming, sensors, sirens — and **Security & Safety** — the always-on
hazard, tamper and fault reporting that runs whether or not the alarm
system is configured. It describes what each page and each setting
does, in operator terms; it does not repeat the design rationale. The
full safety model and engineering constraints for the alarm system live
in [`alarm-concept.md`](./alarm-concept.md) (researched device
behaviour is in [`alarm-assumptions.md`](./alarm-assumptions.md)); the
same for Security & Safety lives in
[`security-safety-concept.md`](./security-safety-concept.md).

The alarm section is reachable at `#/alarm` and has seven tabs —
Overview, Sensors, Outputs, Policies, Codes, Journal, Walk test — plus
the re-runnable Setup wizard. Security & Safety is reachable at
`#/security` and has three tabs — Overview, Sources, Faults.

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
12. [Security & Safety](#12-security--safety)

---

## 1. Concepts in two minutes

| Term | Meaning |
|---|---|
| **Zone** | An independently armable partition ("Ground floor", "Garage") with its own sensors, outputs, delays and state. You can have one or many; a zone may span multiple CCUs. |
| **Arm mode** | A named protection level of a zone: `perimeter` (shell only — doors and windows), `full` (everything, including motion), plus optional `night`, `vacation` and `custom`. Each mode selects a sensor subset and its own delays and output behaviour. |
| **Sensor** | A trigger source bound to a device data point — a window contact, a motion detector, a smoke detector, a panic button. Each sensor carries a mode assignment and behaviour flags. |
| **Output** | An alarm consequence: a siren, an alarm light, a chirp tone, a notification event, or a CCU system variable mirror. |
| **Incident** | One trigger episode: cause, timestamps, which outputs fired, whether it was silenced. Silence and acknowledge act on the incident. |
| **Journal** | The persistent log of everything the alarm engine does or observes. |

A typical flow: you arm a zone into a mode → the exit delay counts down
→ the zone is armed. A sensor activation either triggers instantly or
starts the entry delay (pending). If nobody disarms in time, the zone
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
per zone at any time:

1. **Zones** — create at least one zone. One per floor is a good start.
2. **Sensors** — opens the sensor picker (see [Sensors](#5-sensors)).
3. **Outputs** — opens the output picker (see [Outputs](#6-outputs)).
4. **Delays & chirps** — per mode: exit delay (time to leave), entry
   delay (time to disarm after coming in), and alarm duration (how long
   one alarm phase runs, capped at 600 s).
5. **Codes & users** — managed on the Codes tab.
6. **Walk test** — verify every sensor before relying on the system.

The zone is created disarmed. Run a walk test before you trust it.

## 4. Overview — the panel

One card per zone:

- **Mode buttons** arm the zone into a mode or disarm it. Each button
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
    remaining re-fire of this incident. The zone stays in triggered
    state; notifications still go out. A genuinely new incident may
    sound again.
  - **Disarm** — ends the alarm and returns the zone to disarmed
    (implies silence). Shows the PIN pad if a code is required.
  - **Acknowledge** — marks the incident as seen in the journal. No
    state effect.
- **Silence all** appears in the toolbar while any zone is triggered.
- The **health badge** in the toolbar summarizes alarm health (sirens
  reachable, pending faults). Details appear in the journal.

## 5. Sensors

The sensor picker assigns sensors to a zone and to arm modes. Loom
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
| **Door chime while disarmed** | Plays the door-chime tone on chirp outputs when the sensor activates while the zone is disarmed. |
| **Silent panic (duress)** | Panic sensors only: activations fire the panic policy with all acoustic outputs suppressed — notifications only. |
| **Hold time (s)** | The activation must persist this long before it counts — filters twitchy motion sensors and doors rattling in wind. A cleared activation is discarded. Never applied to always-on sensors. |
| **Cross-zoning group** | Sensors sharing a group name only trigger when a second member activates within 60 seconds; a lone activation is journaled but does not sound the alarm. Kills single-PIR false alarms. |

### Sensor health and readiness

Sensor health continuously feeds the per-mode *ready to arm* state shown
on the Overview: open sensors, unreachable devices and sabotage block
arming by default; a low battery warns. How each class behaves
(block / warn / ignore) is a zone-level setting.

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
auto-stops that output while the zone is disarmed). Chirp outputs carry
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

Per-zone rules beyond plain arm/disarm. Select the zone at the top;
Save writes the whole set.

### Codes

When a code must be entered. These switches apply to anonymous surfaces
only (MQTT, keypad, remote key) — logged-in operator sessions and
host-local `hmcli` always pass, which is the documented break-glass
path.

- **Require code to arm** — off by default; arming is the safe
  direction.
- **Require code to disarm** — *Automatic* (default) requires a code as
  soon as the zone has an enabled code; a zone without codes never
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

- **Return to armed** (default) — the zone stays armed in its previous
  mode.
- **Disarm** — the zone disarms. With *Auto re-arm after (s)* it
  re-arms into the pre-incident mode after that many quiet seconds; any
  sensor activity restarts the countdown.

### Schedules

Time-of-day entries per zone, evaluated in the daemon's local time
zone. Each entry has a time, optional weekdays (none selected = every
day) and a target mode. With **Auto-arm** the zone actually arms at
that time; without it the entry only raises a reminder when the zone is
not in the expected mode at that time.

## 8. Codes

Alarm codes are separate from Loom login accounts — household members
who never see this UI still get arm/disarm PINs. PINs are stored as
salted hashes and never shown again.

- **Type**: *PIN* (typed on the PIN pad or anonymous surfaces),
  *Keypad slot* and *Remote key* (bind a hardware keypad user slot or a
  radio remote, so its actions are attributed to this name).
- **Permissions**: what the code may do — arm, disarm, silence.
- **Zones**: restrict a code to specific zones; nothing selected means
  all zones.
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
zone it applies to. Raw JSON binding remains available as an expert
fallback, and is the only way to bind a virtual remote channel.

Wrong codes are rate-limited with escalating lockout per source;
operator sessions are exempt from the lockout (an attacker spamming a
wall keypad cannot lock you out of your own panel).

## 9. Walk test

A walk test verifies sensors without arming the zone and without firing
any alarm: start a session, walk the house and trip each sensor — every
activation turns its checklist row green with a timestamp. The progress
line shows `n/total sensors verified`; untested sensors stand out. Stop
the session when done; the result is recorded in the journal.

Run a walk test after initial setup, after adding or moving sensors,
and periodically (battery-powered contacts fail silently).

## 10. Journal

The persistent, filterable log of everything the alarm engine does or
observes — arming and disarming (with identity), triggers, bypasses,
silences, faults, tests and configuration changes. Filter by zone,
event class and time range; *Export CSV* downloads the current view.

The journal is the anti-silent-failure surface: every degradation the
engine accepts (an unreachable siren, a failed stop verification, a
restart-restored incident) lands here instead of being swallowed.

## 11. Integrations

- **MQTT / Home Assistant**: each zone appears as an
  `alarm_control_panel` entity via MQTT discovery (plus an aggregate
  master panel). Arm modes map to the HA vocabulary
  (`armed_home` = perimeter, `armed_away` = full, `armed_night`,
  `armed_vacation`, `armed_custom_bypass`). Enrolled notification
  outputs additionally publish a `NOTIFICATION` entry on the zone's
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

## 12. Security & Safety

Security & Safety is a separate, always-on domain reachable at
`#/security`. It answers three questions the alarm system alone does
not: **what is wrong right now**, **what is broken and since when**,
and **what should someone be told**. It runs independently of the
alarm system — a house with nothing but a couple of smoke detectors
and a water sensor, and no zones configured at all, still gets the
full domain: classification, fault tracking and notifications. There
is no separate enable switch and no configuration section; it starts
with the daemon.

### The classes

Every data point the domain watches is classified into one of nine
classes: **smoke**, **water**, **gas**, **co**, **tamper**,
**battery**, **technical**, **intrusion**, **panic**. `smoke`,
`water`, `gas`, `co`, `intrusion` and `panic` are hazard classes — an
acute danger; `tamper`, `battery` and `technical` are fault classes —
a degradation of the installation. `intrusion` and `panic` are
projections of the alarm engine's own incidents and stay empty if no
alarm system is configured; the others work off the device fleet
directly.

A class with no classified source in the fleet is **not published at
all** — not shown, not permanently off. There is no producer for
`gas` in the Homematic device family today, for example, so no `gas`
entity exists anywhere until a source is classified into it (including
by an operator override, see below). A class that appeared once and
then lost its last source is retracted the same way.

### The Sources page

A **source** is one classified data point: a device channel and
parameter the classifier has placed into one of the nine classes. The
Sources page lists every source the daemon knows about, filterable by
class, CCU and zone, with switches to narrow to only the relevant or
only the currently active ones.

**Relevant** means the source counts towards its class tile and the
fault list. Hazard-class sources (smoke, water, gas, co, intrusion,
panic) are always relevant. Fault-class sources (tamper, battery,
technical) are relevant only when they sit on a device that also
carries an alarm role — otherwise `technical`/`battery`/`tamper` would
sit permanently lit across an entire fleet and stop meaning anything.
A source that is not relevant is still listed, so you can find it, but
it is not being watched.

You only need to touch this page when the classifier got something
wrong: a detector filed under the wrong class, or a data point that
should not raise anything at all. Each row has an override: pick a
class to correct the verdict, or turn off *Included* to drop the
source out of every aggregate entirely while keeping it listed. An
override changes what the aggregates report — it does **not** change
the alarm system, which is configured separately per zone on the
Sensors tab. Overriding a source out of the `water` class, say, stops
it counting towards `security/class/water`; it has no effect on
whether that same data point is enrolled as an alarm sensor.

### Faults

A fault opens when a fault-class source (tamper, battery, technical)
becomes active on a relevant device — an unreachable device, a
depleted battery, a sabotage contact, a jammed actuator. Its `since`
timestamp is recorded when it opens and **survives a daemon restart**;
a fault that was open before a restart is still shown with its
original start time afterward, not reset to "just now".

**Acknowledging** a fault only records that an operator has seen it.
It does not clear the condition — the fault stays open, with its
original `since`, until whatever caused it actually resolves.
Acknowledging is for triage, not for making a problem go away.

### Notifications

Every alarm and fault event the domain renders carries a **subject**
(one line, for a message title) and a **message** (a full sentence
naming the cause, the place and the time) — ready to drop straight
into a Home Assistant automation's notification title and body. Next
to the rendered text, every notification also carries `i18n_key` and
`args`: a machine-readable key plus its substitution values, so a
consumer — the SPA, a REST/WS client, a third-party integration — can
re-render the same notification in its own locale instead of only
forwarding the daemon's configured language.

### What reaches Home Assistant

Beyond the alarm system's `alarm_control_panel` entities (§11), the
Security & Safety device card publishes:

- One **folded state** sensor — the single-glance severity (`ok` /
  `info` / `warning` / `alarm` / `critical`) across the whole domain.
- An **alarm** flag (`binary_sensor`) — a hazard class is active.
- A **problem** flag (`binary_sensor`) — a fault-class condition is
  open on a relevant device.
- An **engine health** flag — the alarm engine reports itself
  unhealthy (an unreachable siren, a failed stop verification).
- Two **event entities** — one for alarms, one for faults — the
  automation primitive: they fire on every occurrence, including a
  second identical one, which a plain state sensor would not.
- Two **retained last-report sensors** — the last alarm and the last
  fault, each timestamped and carrying the full rendered report
  (subject, message, sources) as an attribute, so a dashboard survives
  a daemon restart even though the event entities themselves reset to
  unknown.
- One tile **per class** that currently has a classified source (see
  above).
- One sensor **per zone**, when the alarm engine is configured —
  active-source count for that zone, broken down by class.

### Duress and silent panic

A duress code use, or a sensor marked *silent panic*, is deliberately
covert: the person triggering it may be standing next to whoever is
watching a screen, so nothing may appear where that observer could
see it. How far the report is allowed to travel is the
`alarm.duress_visibility` setting, with three levels:

- **`hidden`** — the Security & Safety plane stays silent entirely.
  Only a configured **webhook** is notified. An installation that
  runs no webhook receiver is told **nothing** at this level — that is
  the trade-off of choosing it.
- **`notify_only`** (default) — additionally fires the non-retained
  notification event, so a phone is reached, but never the retained
  last-alarm sensor and never a local screen surface.
- **`full`** — treated like any other alarm: reaches the retained
  sensor, the SPA and REST/WS as well.

Choose `hidden` only where you have a working webhook receiver and
have deliberately decided that Home Assistant itself must never show
the trigger.

### What to expect elsewhere

A few things an operator might otherwise wait for are deliberately not
built:

- **A lost CCU does not open a fault.** If one of several configured
  centrals disappears, its sources are quietly dropped from the
  aggregates instead of raising a `technical` fault for the outage
  itself.
- **Truncated lists carry no link to the rest.** The `sources`/`faults`
  attribute on a class, zone, alarm or problem entity caps at 30
  entries and reports `truncated: true` plus a total count when there
  are more — but not where to find the remainder. Use the Sources or
  Faults page for the complete list.
- **No WebSocket surface.** The domain is reachable over REST and MQTT
  only; there are no `security.*` WebSocket broadcasts or commands.
