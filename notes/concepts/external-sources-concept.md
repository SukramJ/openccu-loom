# External sources — concept sketch

**Status:** sketch, not a decision. No code, no schedule.
**Question it answers:** the HmIP-HCU plugin forum is dominated by one
wish — *my third-party device should live inside the Homematic world*.
Should OpenCCU-Loom serve that wish, and if so, how narrowly?

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

## 4. What it looks like in the model

An ingested value becomes a first-class model object, and that is where
the leverage is: `payload.Source` (ADR 0007) already projects every
model object uniformly. One mapping, and the value appears in REST, in
WebSocket broadcasts, in MQTT discovery, in Matter, in the history
recorder, and in the alarm's sensor picker — with no per-surface work.

Sketch of the mapping, deliberately thin:

```yaml
sources:
  - name: garden                     # scoping dimension, like a central
    type: mqtt
    broker: tcp://127.0.0.1:1883     # may be the same broker we publish to
    devices:
      - address: SHELLY-PLUG-01      # synthetic, stable, operator-chosen
        model: "Shelly Plus Plug S"
        channels:
          - number: 1
            data_points:
              - name: POWER
                topic: shellies/plug01/status/switch:0/apower
                path: $.apower       # optional JSON pointer
                type: FLOAT
                unit: W
              - name: STATE
                topic: shellies/plug01/status/switch:0/output
                type: BOOL
                writable:
                  topic: shellies/plug01/rpc
                  template: '{"method":"Switch.Set","params":{"id":0,"on":{{value}}}}'
```

Open design questions, not answered here: whether the synthetic address
space needs a reserved prefix so it can never collide with a real CCU
serial; whether a source is a peer of a central in the registry or a
lighter construct; how availability is expressed when a topic simply
stops arriving.

## 5. Boundaries — where this stops

Stated as hard non-goals, because the failure mode of this idea is
scope drift into "a worse Home Assistant":

- **No integration catalogue.** No `sources: type: fronius`. If a device
  needs protocol-specific work, that work belongs in a dedicated bridge
  process that publishes MQTT — outside this daemon, and reusable by
  anyone.
- **No plugin runtime.** No loading of third-party code, in any language.
- **No polling of HTTP APIs, no Modbus, no vendor clouds** in the first
  cut. Each is a bridge someone has already written.
- **No dashboards for external devices.** The config UI shows them
  because the add-ons need to reference them, not as a control surface
  competing with HA's.
- **Not a migration path off Home Assistant.** Operators who want a
  general-purpose system should keep it; this exists so that the *few*
  values our own logic needs are reachable.

## 6. Why this might still be the wrong call

Recorded honestly, because the case is not one-sided:

- The alarm system has shipped without external sensors, and nobody has
  reported the gap yet. This may be a solution ahead of its demand.
- An operator running Home Assistant *could* keep the alarm logic there
  too and skip the daemon's version entirely. Our add-ons compete with
  HA's equivalents for that audience regardless of this feature.
- MQTT ingest adds a failure mode the daemon has been free of so far:
  state that depends on a broker and on producers we neither control nor
  monitor. Availability semantics for a value that simply stops
  arriving need to be right before this is safe for alarm inputs.
- The forum evidence is about the HCU, whose users have *no* alternative.
  It does not prove the same demand exists among people who already run
  OpenCCU-Loom, and it may say more about eQ-3's walled garden than
  about a real gap here.

## 7. If it happens, the smallest honest first step

One source type (`mqtt`), read-only, no writes. One consumer: the alarm
system's sensor picker. If operators do not reach for it there, the idea
is answered and nothing further is built.

Writes, additional add-on consumers and Matter re-export are separate
decisions, each gated on the previous one being used in the field.

---

## Related

- [`notes/concepts/alarm-concept.md`](alarm-concept.md) — the add-on this pattern
  was extracted from.
- [ADR 0007](../../docs/adr/0007-strong-model-source-interface.md) — the
  `payload.Source` contract that makes one mapping reach every
  north-bound surface.
- [`docs/user/home-assistant.md`](../../docs/user/home-assistant.md) — how the
  daemon and Home Assistant divide the work today.
