# External sources — concept sketch

**Status:** sketch, not a decision. No code, no schedule.
**Question it answers:** the HmIP-HCU plugin forum is dominated by one
wish — *my third-party device should live inside the Homematic world*.
Should OpenCCU-Loom serve that wish, and if so, how narrowly?

**Second pass (2026-08-15):** §1–§3 are unchanged — the case for the
feature and the case against a plugin catalogue both still hold. Everything
from §4 on is rewritten around a concrete shape: the operator **browses the
already-connected broker in the UI**, assigns what they find to a **room**
and to a **purpose** (alarm system, Security & Safety), and the daemon
turns it into an ordinary model device. That shape changes the design more
than it looks — see §4.1 for why.

---

## 1. The uncomfortable question first

Home Assistant is the general-purpose smart-home system. It carries
thousands of maintained integrations; Shelly, Hue, Fronius, Modbus,
Gardena, plant sensors — every device the plugin forum asks for is
already solved there, by people who do nothing else.

So the honest starting position is: **we should not build integrations.**
Any catalogue we start would be a worse copy of one that already exists,
and it would compete for the maintenance budget of the thing that is
genuinely ours — the CCU side.

If that were the whole picture, this document would end here with "use
Home Assistant". It does not, for one specific reason.

## 2. The one reason that survives

**The add-ons live in the daemon, and add-ons need inputs.**

The alarm system runs inside OpenCCU-Loom, not in Home Assistant. Its
zones, delays, hold times and outputs are daemon state. The moment an
operator wants a non-Homematic contact as an alarm sensor, or a
non-Homematic siren as an output, Home Assistant cannot help: the logic
that must read that sensor is on this side of the wire.

The same holds for every add-on that follows the alarm's pattern:

| Add-on (existing or sketched) | Input it will eventually want |
|---|---|
| Alarm system | third-party contacts, motion, sirens |
| Security & Safety | third-party smoke, water, gas, CO detectors |
| Heating régie | a room sensor that is not a Homematic thermostat |
| Shading | wind and brightness, often from a weather station |
| Energy | inverter, battery, meter — none of them Homematic |
| Presence simulation | nothing external; stays self-contained |

That is the whole justification, and it is narrow on purpose: we need
**values the daemon's own logic consumes**, not a device catalogue.

Everything that does *not* feed daemon logic — a Hue lamp an operator
simply wants to switch, a robot mower's status on a dashboard — belongs
in Home Assistant, and this concept deliberately does not serve it.

## 3. What follows from that: consume, do not integrate

If the requirement is "values in", not "devices supported", then the
cheapest correct implementation is not a plugin API. It is a single
ingest path over a protocol every relevant producer already speaks:

**MQTT.**

Home Assistant publishes to MQTT. So do Zigbee2MQTT, ESPHome, evcc,
Shelly natively, Tasmota, and the maintainer's own bridges for inverter,
battery and heat pump. The integration work is already done — by Home
Assistant and by the device vendors — and we consume the result.

This inverts the usual framing in a way worth stating plainly: **Home
Assistant becomes an upstream source for OpenCCU-Loom**, not only a
downstream consumer of it. The MQTT link, which today runs
daemon → broker → HA, gains a second direction for a small, declared set
of topics.

Consequences:

- No vendor plugins, no plugin runtime, no third-party code in our
  process. Nothing to sandbox, nothing to review, no supply chain.
- An operator who already runs Home Assistant configures nothing new on
  the HA side beyond an MQTT publish they very likely already have.
- An operator who runs *no* Home Assistant still has a path — most
  device classes people ask about publish MQTT on their own.

---

## 4. The shape of the thing

### 4.1 Mint HmIP-shaped devices, do not invent a parallel model

The first pass of this document sketched a `sources:` YAML block with
per-topic `type`, `unit` and JSON pointer. That shape is wrong, and the
reason is worth stating precisely because it determines the size of the
whole feature.

Every consumer the feature exists for is already keyed on the **CCU model
shape**:

- The alarm's sensor picker classifies candidates from
  `(model, channel type, parameter)` through two tables, both keyed on
  that triple: `safety.Classify` (`internal/model/safety/classify.go`)
  for the hazard classes — `{WATER_DETECTION_TRANSMITTER, ALARMSTATE}`
  and its siblings — and `sensorTypeByChannelType`
  (`internal/alarm/sensor_candidates.go`) for the intrusion roles,
  `SHUTTER_CONTACT` → window, `MOTIONDETECTOR` → motion.
- Alarm enrollment routes events by
  `(central, interface, channel address, parameter)`
  (`internal/alarm/inputs.go`).
- Security & Safety builds its classification index by walking
  `Registry.List()` → devices → channels → data points
  (`internal/security/index.go`).
- MQTT discovery, REST, WS, Matter, the history recorder and the
  visibility gate all project the same model objects.

A parallel "external value" type would have to be taught to every one of
those, one by one, and each omission is silent. The alternative costs
almost nothing:

> **An external source becomes an ordinary `model/device.Device` with an
> ordinary channel type and ordinary parameter names.** A Zigbee door
> contact is minted as a device whose channel type is `SHUTTER_CONTACT`
> and whose data point is `STATE`. From the moment it exists, no consumer
> can tell it apart from a HmIP-SWDO — because there is nothing to tell
> apart.

The whole feature then reduces to two pieces: an **ingest** that keeps
those synthetic data points fed, and a **mapping table** from what the
broker declares onto (channel type, parameter). Everything downstream is
already built.

This is also the answer to "what about the Heating régie / Shading /
Energy add-ons later": they will classify on the same shape, so they
inherit the feature without a second ingest.

### 4.2 The declaration is already on the broker

The second design decision follows from the first, and it is what makes
the UX good rather than merely present.

The operator should not have to type a topic, a JSON path, a data type
and a unit. In the overwhelming majority of deployments they do not have
to, because the producer already published a machine-readable
self-description **retained** on the same broker:

```
homeassistant/<component>/<node_id>/<object_id>/config
```

Zigbee2MQTT, ESPHome, Tasmota, Shelly Gen2 and the Nabu-Casa-style
bridges all publish it. The payload carries `name`, `device` (manufacturer,
model, identifiers, `suggested_area`), `device_class`, `state_topic`,
`value_template`, `unit_of_measurement`, `payload_on` / `payload_off`,
`availability_topic` and often `expire_after`. That is every field the
mint in §4.1 needs, and more.

The daemon already reads that namespace. `RunDiscoveryOrphanCleanupOnce`
(`internal/north/mqtt/retain_cleanup.go`) subscribes `homeassistant/#`,
waits out a snapshot window, and parses the four-segment topic shape — it
just discards the payload today because it only cares about orphaned
topics of its own. Reading the payload instead of dropping it is the
entire mechanism.

So the browser has two levels, and the ordering matters:

**Level 1 — declared devices (the default view).** A list of *devices*,
not topics: name, manufacturer, model, entity count, the producer that
declared them (derived from `node_id` / the topic prefix). One click
adopts a device; the operator never sees a topic string unless they ask.

**Level 2 — the raw topic tree (the fallback).** For producers without
HA discovery. A collapsible tree of observed topics with the last payload
rendered, a proposed extraction (JSON pointer if the payload parses as an
object, whole-payload otherwise), a proposed type and unit, and a live
preview of what the value would become. The operator confirms or corrects.

The good UX is not a prettier topic tree. It is that in most deployments
the topic tree is never opened.

### 4.3 A device class is not a channel type, and that gap is the mapping table

`device_class: door` is not `SHUTTER_CONTACT`. The mapping between them
is the one piece of genuinely new knowledge the feature adds, and it is
small, static and testable:

| HA `device_class` | minted channel type | parameter |
|---|---|---|
| `door`, `window`, `opening`, `garage_door` | `SHUTTER_CONTACT` | `STATE` |
| `motion`, `occupancy`, `presence` | `MOTIONDETECTOR` | `MOTION` |
| `smoke` | `SMOKE_DETECTOR` | `SMOKE_DETECTOR_ALARM_STATUS` |
| `moisture` | `WATER_DETECTION_TRANSMITTER` | `ALARMSTATE` |
| `gas` | `GAS_DETECTOR` (does not exist on HmIP — see below) | — |
| `tamper` | (device-level) | `SABOTAGE` |
| `temperature`, `humidity`, `illuminance`, `power`, … | measurement channel | typed parameter |

Two honest gaps this table surfaces immediately:

- **HmIP has no channel type for some classes.** CO and combustible gas
  have no HmIP equivalent, so there is no channel type to mint. Either the
  daemon introduces synthetic channel types for them (and the alarm /
  security classifiers learn them — a one-line table entry each, since
  both classify from a map), or those classes are out of scope in the
  first cut. This needs a decision; it is not a detail.
- **Not every producer sets `device_class`.** When it is absent the
  operator picks the purpose by hand in the wizard (§7). The mapping
  table is a *proposal engine*, never a gate.

The table belongs next to the two classifiers it feeds, and it needs a
contract test that asserts every entry produces a `(channel type,
parameter)` pair those classifiers actually recognise — driven off their
own tables, not off a copy. A mapping that mints a channel type nobody
classifies produces a device that appears in the UI and never fires: the
exact failure mode this project has paid for before, and one no
per-component test catches, because each half is correct on its own.

### 4.4 The address space

The first pass left this open. With ADR 0060 in place the answer is
straightforward:

- A new `hmenum.Interface` value for external sources — the five existing
  values are all CCU transports, and every capability gate that switches
  on the interface gets one clean, explicit test point.
- A reserved address prefix a CCU can never emit, so a synthetic address
  can never collide with a real serial. The daemon already reserves
  address space for hub pseudo-devices
  (`internal/routingkey/uniqueid.go`), so the pattern exists.
- The address itself derives deterministically from the producer's own
  stable identifier (the discovery `device.identifiers` entry, or the
  topic prefix for level-2 sources) — **not** from a counter and not from
  an operator-typed string. A device re-adopted after a broker wipe must
  land on the same address, or every alarm enrollment, every history
  series and every HA entity id built on it breaks.

Whether an external source is a peer of a central in the registry, or a
lighter construct hanging off one, stays open — but §6 argues it needs a
central-shaped scoping dimension either way.

---

## 5. Browsing the broker — the UX

The operator flow is three steps, and each has one job.

### 5.1 Discover → adopt → assign

**Step 1 — Discover.** The view opens on the connected broker and shows
what it finds, grouped by producer. Three counters carry the state
honestly: *declared devices found*, *raw topics observed*, *listening for
n s*. The last one matters more than it looks — see §5.2.

Rows carry a state badge: **new**, **adopted**, **ignored**. Ignoring is
first-class: a broker with a busy `evcc/#` tree must be dismissible in one
click, permanently, or the list is unusable on the second visit.

**Step 2 — Adopt.** One device at a time, with a preview of exactly what
will be minted: the device name, the channel type, the parameter, the
current value as the daemon would read it. Adopting without seeing the
resolved value is how an operator ends up with a sensor that is
permanently `false`.

**Step 3 — Assign.** Room and purpose (§6, §7). Both are optional at
adopt time and editable afterwards — an operator who adopts eight sensors
in one sitting should not be forced through eight wizards.

The whole flow is a candidate picker, and this codebase already has three
of them (alarm sensors, alarm outputs, security sources). It should look
like them, not like something new.

### 5.2 The browse session is not the bridge's subscriber

Today's snapshot passes ride on the production subscribe client:
`cleanupSubscriber()` returns `b.subscriber` — the same connection the
command subscriber uses. That is correct for a two-second boot-time
sweep. It is not obviously correct for an interactive browse that holds a
`#` subscription while an operator reads.

The browse session should therefore be its own short-lived MQTT session:
its own client id, its own subscription, an explicit lifetime (the view is
open, plus a bounded grace), and a visible end. Reasons, in order of how
much they cost when ignored:

- **A `#` subscription is not free.** On a broker carrying a large
  Zigbee2MQTT fleet it is a continuous firehose. Attaching it to the
  connection that carries alarm commands couples the two.
- **Retained is not the same as live.** A retained snapshot shows
  producers that set the retain flag. Many do not — an ESPHome sensor that
  publishes every 60 s is invisible for up to a minute. This is why the
  view must show *how long it has been listening* and keep updating rather
  than presenting one snapshot as complete. "I don't see my sensor" is
  otherwise the first support question, and the answer is "wait".
- **Brokers with ACLs may refuse `#`.** A refused subscribe must produce a
  named error in the UI, not an empty tree that reads as "nothing there".
- **A browse session is an operator action and belongs in the audit log**
  with its own identity, the way every other operator-initiated broker
  operation does.

### 5.3 The daemon's own topics are excluded, not greyed out

The broker being browsed is the broker the bridge publishes to. Two
namespaces on it belong to the daemon: `<topic_base>/#` and its own
entries under `homeassistant/#`.

Binding a daemon-published topic as an external source creates a loop:
daemon publishes → daemon ingests as an external value → model changes →
daemon publishes. This is the same class of defect as the command-topic
self-echo (a broker without `NoLocal` re-delivering the daemon's own
publishes), and that one took a live reproduction to pin down.

The exclusion must be structural — the browser does not offer those
topics at all — rather than a visual hint the operator can click past.
The daemon knows both prefixes exactly; there is no guessing involved.

A related case that is *not* the same and must not be excluded: a device
that Home Assistant itself declared. Adopting one is legitimate (it is
how a Fronius inverter reaches the alarm's sibling add-ons), but it
creates the round-trip in §9.5.

### 5.4 "Why doesn't it fire" — the panel that decides supportability

Every ingest feature lives or dies on one screen. Per adopted source, at
minimum:

- the bound topic, verbatim
- the last raw payload, verbatim, with a timestamp
- the extracted value and the type it was coerced to
- the resulting model value, and whether it counts as *active*
- when a message was rejected: which step rejected it and why (JSON did
  not parse, pointer missed, value not coercible, value not in the
  configured active set)
- the availability verdict (§8) with its deadline

Without this the failure mode is an operator who is certain the sensor is
broken and a maintainer who cannot see the payload. With it, the same
question is self-service.

---

## 6. Rooms — the assignment that has no obvious home

This is the part of the request with a real modelling problem behind it,
and it is worth being explicit rather than assuming it is a form field.

**Rooms in this daemon are CCU-owned and central-scoped.** A room is the
pair `(central_name, room_name)` (ADR 0056); the list comes from the CCU's
ReGa. Areas — the operator-defined grouping one level up — are
daemon-owned, in the daemon's own SQLite, and a room belongs to exactly
one area. Filtering across device lists, alarm candidates and group views
is client-side, driven off the room list each row already carries.

An external source belongs to no CCU. So "assign it to a room" has three
possible readings, and they are not equivalent:

**(a) Reference an existing CCU room.** The external device carries
`Rooms() = ["Wohnzimmer"]` scoped to a chosen central. Everything already
built works untouched: the device list filter, the area derivation, the
alarm candidate grouping, MQTT discovery's `suggested_area`.
*Cost:* on a multi-CCU install the operator must pick a CCU the sensor has
nothing to do with. Conceptually crooked; invisible on the single-CCU
installs that are the large majority.

**(b) Give external sources their own room namespace.** Clean in theory.
In practice it puts two kinds of room in every picker and every filter —
precisely the ambiguity ADR 0056 spent a breaking rename to eliminate for
"Bereich". Every downstream filter would need to learn the second kind.

**(c) Assign only to an area.** Areas are daemon-owned already, so there
is no CCU coupling and no new concept. But areas are coarser than rooms,
and an operator who has not created any has nothing to assign to.

**Recommendation: (a), with (c) falling out for free.** Model the
assignment as a reference to an existing `(central, room)` pair. The area
follows automatically from the existing room→area table, so an operator
who works in areas gets them without a second assignment. On a multi-CCU
install the picker defaults to the central with the most rooms and says
plainly that the choice is a filing decision, not a connection.

Two consequences to accept deliberately:

- Renaming a CCU room drops the external assignment, exactly as it drops
  an area assignment today. Same rarity, same cheap fix, same behaviour —
  consistency is worth more here than a cleverer key.
- The daemon *can* create CCU rooms over ReGa (`create_room`,
  `set_device_rooms`). It should **not** do so for external sources: the
  external device does not exist on the CCU, so `set_device_rooms` has
  nothing to address. The assignment is daemon-side state only, and the
  CCU never learns about it. Worth writing down because the opposite is a
  natural assumption.

## 7. Purpose — two assignments, not one

The request names "alarm system" and "Security & Safety" together. In the
daemon they are two different mechanisms, and conflating them in the UI
would produce the wrong assignment about half the time.

| | Alarm system | Security & Safety |
|---|---|---|
| What is assigned | enrollment in a **zone** | a **classification** of the source |
| Vocabulary | `AlarmSensorType`: door, window, motion, tamper, hazard, panic | `SecurityClass`: smoke, water, gas, co, tamper, battery, technical, intrusion, panic |
| Scope | per zone; only effective while armed | always effective, independent of arming |
| Extra config | active values (which readings count as triggered) | hazard vs diagnostic follows from the class |
| Where it lives today | `alarm_sensors` rows + zone | classification index + operator override (`SecuritySource`) |

They diverge routinely: a water detector is `SecurityClass water` and
should usually **not** be an alarm trigger; a window contact is
`AlarmSensorType window` and carries no security class of its own; a smoke
detector is typically both, and its active-value set is the one place
where getting it wrong is dangerous (the HmIP smoke status value list
contains the alarm system's own intrusion-siren command — the existing
candidate builder already special-cases this).

So the assign step asks **two independent questions**, both skippable:

1. *Is this an alarm sensor?* → zone + sensor type + active values.
2. *Does this represent a hazard or fault class?* → security class.

And it pre-fills both from §4.3's mapping wherever `device_class` allows,
because the pre-fill is right far more often than it is wrong — but it
never hides the second question behind the first.

**The leverage from §4.1 shows up here.** Once the external source is a
device with a recognised channel type, it appears in the existing alarm
sensor-candidate list and the existing security source list by itself.
The assign step is then not a new subsystem — it is a shortcut into two
pickers the operator can also reach the normal way. That is worth
building it as: adopt writes the device, and the *same* enrollment call
the alarm view already makes does the rest.

## 8. Availability is the load-bearing part, not a polish item

The first pass listed availability as an open question. For an alarm
input it is the central safety property, and it deserves the strongest
statement in this document:

> **A source that has stopped arriving is not "unchanged". It is
> unknown, and unknown is not safe.**

A window contact whose Zigbee coordinator has been unplugged reports its
last value forever. Every surface shows it closed. The zone arms. This is
worse than not having the sensor, because it converts a known gap into an
invisible one.

What follows:

- **Every external source has a staleness deadline.** From the producer's
  `expire_after` where declared, from an `availability_topic` where one
  exists, and otherwise from an operator-set value the adopt step forces a
  choice on. There is no "no deadline" option for a source assigned to a
  purpose in §7.
- **Expiry produces a fault, not silence.** The Security & Safety fault
  ledger already models exactly this — a source that is unreachable is
  `SecurityClass technical`. An expired external source raises the same
  fault through the same path, which means it also reaches the same
  journal, the same MQTT plane (ADR 0059) and the same UI badge. Nothing
  new to build, and the operator learns about it on the surface they
  already watch.
- **A stale external sensor is an arming blocker, and the mechanism
  already exists.** `BlockerPolicies` (`internal/alarm/engine/config.go`)
  maps four sensor-health classes onto `block` / `warn` / `ignore`, and
  its `Unreachable` class defaults to `block`. A stale external source is
  exactly that class. It needs no new arming logic and no new policy knob
  — only the mapping from "deadline missed" onto the existing unreachable
  blocker, so the operator's configured policy governs it the same way it
  governs a HmIP device that has dropped off.
- **Retained replay must seed, not trigger.** After a daemon restart the
  broker re-delivers retained payloads immediately. A retained `ON` on a
  motion topic would otherwise read as a fresh detection at boot. The
  bridge already carries this distinction on the command side — `Message`
  exposes the PUBLISH retain bit precisely so a side-effecting handler can
  drop the replay. Ingest needs the same rule, stated as: a retained
  first payload sets state and never counts as an activation.

## 9. Trust, and five things the request did not mention

### 9.1 An external sensor is a weaker sensor, and the alarm should say so

A HmIP sensor is encrypted, paired, and supervised — the CCU notices when
it goes quiet. An MQTT topic is a string from anyone with write access to
the broker. For a lamp that is irrelevant; for the thing that decides
whether a siren sounds, it is not.

This does not mean refusing external alarm inputs. It means:

- The security posture is documented, and broker ACLs are stated as a
  requirement rather than a suggestion for anyone who enrols external
  alarm sensors.
- Every alarm journal entry names the source's origin, so an incident
  review can tell a HmIP trigger from an MQTT one.
- A configurable default worth considering: external sources may hold a
  zone *open* (preventing arming) without being allowed to *trigger* it.
  That is the asymmetry an operator usually wants, and it is a strictly
  safer default than full parity.

### 9.2 A vanished source must not silently disarm anything

A device removed from Zigbee2MQTT loses its retained discovery entry. The
daemon must not treat that as "delete the sensor" when the sensor is
enrolled in a zone — that is a silent partial disarm.

Rule: a vanished source becomes a fault (§8) and stays in the model.
Removal is an operator act, and if the source is enrolled anywhere the
removal confirms what it will disable.

### 9.3 Write-back: take the declaration, not a template language

The first pass sketched `writable` with a `template` field carrying a
`{{value}}` placeholder. That is a small programming language in a config
editor, with all the support surface that implies.

The discovery payload already carries `command_topic`, `payload_on` and
`payload_off`. Taking those covers every Zigbee2MQTT switch, every ESPHome
relay and every Shelly output without a template engine existing anywhere
in this daemon. Anything beyond that is a bridge someone else writes —
consistent with §10.

Writes stay out of the first cut regardless (§12); this is about what
they should look like when they arrive.

### 9.4 Matter re-export is the more attractive outcome than the alarm

Worth naming because it changes the priority argument. A Zigbee contact
adopted by the daemon reaches Apple Home over the Matter bridge with no
Home Assistant anywhere in the picture. For the HCU audience this concept
started from, that is a stronger draw than alarm inputs.

Nothing extra is needed for it if §4.1 holds: ADR 0049 gives one endpoint
per device, and the visibility gate already decides candidacy. It should
stay a separate, later decision — but the model should be built so that
it is a switch and not a project.

### 9.5 The round-trip: adopted-from-HA devices double up

Once an external source is a model device, the bridge publishes HA
discovery for it — onto the same broker it came from. For a device that
originated in Home Assistant, the operator now has two entities for one
sensor, and the second one's state comes back through the daemon with
extra latency.

Options: suppress north-bound MQTT republication for external sources by
default; or mark them and let the operator choose per source. Either is
fine; not deciding is not, because the first field report will be "every
sensor is in HA twice".

### 9.6 Names, storage, and the surfaces that get forgotten

Three smaller things that are cheap now and annoying later:

- **Naming.** The daemon is the single naming authority. An adopted
  device brings a foreign name (`Kontakt Küche Fenster` from
  Zigbee2MQTT); it must pass through the same naming pipeline as every
  other device, or the operator ends up with two naming schemes side by
  side.
- **Storage.** Adopted sources are operator state, not bootstrap config.
  They belong in SQLite next to areas, alarm zones and security overrides
  — not in `config.yaml`. A thing created in the UI and backed up only by
  editing a file is a thing operators lose.
- **The two surfaces that never fail loudly.** A new view needs a
  `surface` registry entry or it cannot be hidden, shown per operating
  mode, or found in the operator's own navigation editor. And the MCP
  server needs the new verbs, or the whole feature is invisible to every
  assistant-driven workflow. Neither breaks a test when omitted.

### 9.7 One broker in the UI, more than one in the model

The request says "the already-connected broker", and that is right for
the UI: browsing a broker the operator has not configured yet is a
different feature with its own credential handling.

But the *stored* source should carry a broker reference from day one.
Today it resolves to the single configured broker; adding a second one
later then costs a config field instead of a schema migration on rows
that are enrolled in alarm zones.

---

## 10. Boundaries — where this stops

Stated as hard non-goals, because the failure mode of this idea is
scope drift into "a worse Home Assistant":

- **No integration catalogue.** No `sources: type: fronius`. If a device
  needs protocol-specific work, that work belongs in a dedicated bridge
  process that publishes MQTT — outside this daemon, and reusable by
  anyone.
- **No plugin runtime.** No loading of third-party code, in any language.
- **No polling of HTTP APIs, no Modbus, no vendor clouds** in the first
  cut. Each is a bridge someone has already written.
- **No template language.** See §9.3.
- **No dashboards for external devices.** The config UI shows them
  because the add-ons need to reference them, not as a control surface
  competing with HA's.
- **No second broker in the UI** in the first cut (§9.7).
- **Not a migration path off Home Assistant.** Operators who want a
  general-purpose system should keep it; this exists so that the *few*
  values our own logic needs are reachable.

## 11. Why this might still be the wrong call

Recorded honestly, because the case is not one-sided:

- The alarm system has shipped without external sensors, and nobody has
  reported the gap yet. This may be a solution ahead of its demand.
- An operator running Home Assistant *could* keep the alarm logic there
  too and skip the daemon's version entirely. Our add-ons compete with
  HA's equivalents for that audience regardless of this feature.
- MQTT ingest adds a failure mode the daemon has been free of so far:
  state that depends on a broker and on producers we neither control nor
  monitor. §8 is the answer, and it is a real amount of work — it is not
  a feature that is cheap once availability is done properly.
- Minting synthetic devices with real HmIP channel types (§4.1) is
  leverage, but it also means a bug in the mapping table surfaces as a
  device that looks completely normal and behaves wrongly. The
  contract test in §4.3 is not optional.
- The forum evidence is about the HCU, whose users have *no* alternative.
  It does not prove the same demand exists among people who already run
  OpenCCU-Loom, and it may say more about eQ-3's walled garden than
  about a real gap here.

## 12. If it happens, the smallest honest first step

Read-only, one broker, one ingest, no writes:

1. Read `homeassistant/#` on the connected broker and list **declared
   devices** (level 1 only — no raw topic tree yet).
2. Adopt one device → mint one model device with a mapped channel type,
   fed by its `state_topic`.
3. Staleness deadline mandatory, expiry raises a `technical` fault
   (§8). Retained-first-payload seeds without triggering.
4. Assign a room (option (a), §6) and a purpose (§7), both through the
   existing alarm and security enrollment paths.
5. North-bound MQTT republication off for external sources (§9.5).

If operators do not reach for it there, the idea is answered and nothing
further is built. The raw topic tree (level 2), writes, additional add-on
consumers and Matter re-export are separate decisions, each gated on the
previous one being used in the field.

## 13. Decisions this document does not make

Listed so they are not mistaken for settled:

1. Rooms: option (a), (b) or (c) — §6 recommends (a), it is not decided.
2. Whether classes without a HmIP channel type (CO, combustible gas) get
   synthetic channel types in the first cut, or are out of scope — §4.3.
3. Whether an external source is a registry peer of a central or a
   lighter construct — §4.4.
4. Whether external sources may *trigger* the alarm or only hold a zone
   open — §9.1.
5. Whether north-bound republication is off by default or per-source —
   §9.5.
6. Whether the browse session gets its own MQTT connection — §5.2
   argues yes; it costs a second client id and a lifecycle.

---

## Related

- [`notes/concepts/alarm-concept.md`](alarm-concept.md) — the add-on this pattern
  was extracted from.
- [`notes/concepts/security-safety-concept.md`](security-safety-concept.md) — the
  classification vocabulary and fault ledger §7 and §8 build on.
- [ADR 0007](../../docs/adr/0007-strong-model-source-interface.md) — the
  `payload.Source` contract that makes one mapping reach every
  north-bound surface.
- [ADR 0011](../../docs/adr/0011-mqtt-topic-and-payload-architecture.md) — the
  topic and payload architecture the browser must exclude its own half of.
- [ADR 0049](../../docs/adr/0049-matter-one-endpoint-per-device.md) — why §9.4
  needs no extra Matter work.
- [ADR 0056](../../docs/adr/0056-room-areas-and-zone-naming.md) — rooms are
  CCU-owned and central-scoped; areas are daemon-owned. The constraint §6
  works around.
- [ADR 0059](../../docs/adr/0059-security-safety-mqtt-plane.md) — the plane an
  expired external source reaches for free.
- [ADR 0060](../../docs/adr/0060-loom-prefixed-interface-ids.md) — the
  precedent for a reserved, daemon-owned identifier namespace (§4.4).
- [`docs/user/home-assistant.md`](../../docs/user/home-assistant.md) — how the
  daemon and Home Assistant divide the work today.
