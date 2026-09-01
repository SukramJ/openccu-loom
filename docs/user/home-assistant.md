# Home Assistant: choosing your integration path

OpenCCU-Loom and Home Assistant overlap. Both can own the CCU
connection, both can expose your Homematic devices, and there are three
different ways to get devices from the daemon into HA. That freedom
creates a real question: **which combination should you actually run?**

This page answers it. It maps every scenario, states which combinations
are mutually exclusive, and names the anti-patterns that produce
duplicate entities or a bridge nobody needs.

!!! info "Who this page is for"
    Home Assistant users who already run — or are considering —
    OpenCCU-Loom. No Go knowledge required. If you do not use Home
    Assistant at all, skip to [Scenario 6](#s6-standalone-no-home-assistant);
    everything else on this page is about coexistence.

---

## The short answer

Work down this list and stop at the first match.

1. **You use HA, one CCU, only HA, and you are happy.**
   → Keep *Homematic(IP) Local* talking to the CCU directly. Adding
   OpenCCU-Loom buys you nothing. [Scenario 1](#s1-home-assistant-only-no-daemon)
2. **You want one connection to the CCU and a lean path into HA** —
   or you run several CCUs, want CCU administration outside the CCU
   WebUI, a local alarm system, or measurement history.
   → Run the daemon and feed HA with **MQTT Discovery**.
   [Scenario 2](#s2-daemon-owns-the-ccu-ha-fed-by-mqtt-discovery)
3. **A third system (Node-RED, InfluxDB/Telegraf, evcc, ioBroker, a
   dashboard) needs CCU data, and HA should stay as it is.**
   → Run the daemon with the **raw topic plane only**, Discovery
   **off**. [Scenario 4](#s4-daemon-as-a-data-source-discovery-off)
4. **You want Homematic devices in Apple Home, Google Home or Alexa.**
   → If you run HA, use HA's own Matter or HomeKit bridge. Turn on
   loom's Matter bridge only for the reasons in
   [Scenario 5](#s5-matter-bridge). It is alpha.
5. **You want the Config UI inside the HA sidebar.**
   → That is [Scenario 9](#s9-the-config-ui-as-an-ha-panel) and it
   combines with every other scenario. It is not a device path.
6. **Whichever path you picked: where should the daemon run?**
   → **On the CCU**, via the CCU / OpenCCU add-on, unless one of
   the exceptions applies.
   [Recommended topology](#recommended-run-the-daemon-on-the-ccu)

---

## The five axes

Every setup is a point in five independent dimensions. Confusing two of
them is what makes this topic feel complicated.

| Axis | Question | Options |
|---|---|---|
| **A — Ownership** | Who holds the CCU connection? | HA directly · the daemon · both in parallel |
| **B — Device path** | How do devices reach HA? | MQTT Discovery · *Homematic(IP) Local* with the loom backend · Matter · not at all |
| **C — Topology** | Where does the daemon run? | HA host (add-on) · on the CCU · separate host |
| **D — Logic** | Where do automations live? | CCU programs · HA automations · the daemon |
| **E — Extra surfaces** | What else consumes the daemon? | raw MQTT · REST/WebSocket · MCP · webhooks · the SPA |

**Axis B is the one with a hard constraint. Axes A, C, D and E are free
choices.**

---

## The one rule: exactly one device path per device

MQTT Discovery, the *Homematic(IP) Local* loom backend and the Matter
bridge each create **their own** Home Assistant entities for the **same**
physical device. They do not share identities:

- MQTT Discovery emits daemon-namespaced unique IDs
  (`<central>_<address>_<channel>_<parameter>` under the daemon's own
  node namespace) — deliberately distinct and pinned, because changing
  them would orphan every existing MQTT entity.
- The HA integration builds HA registry unique IDs from the shared
  routing-key contract (`loom_<serial-suffix>_…`), see
  [HA unique-ID migration](../external-clients/ha-unique-id-migration.md).
- Matter identities are assigned by the Matter fabric and have nothing
  to do with either.

Run two of them at once and you get two entity sets for one lamp: two
histories, two names, two switches in the dashboard picker, and two
independent command paths that can fight each other in an automation.

!!! warning "Pick one path per device — and it really is per device"
    The rule is per device, not per installation. Exposing *only* your
    covers via Matter to Apple Home while everything else arrives over
    MQTT Discovery is fine and common. Exposing the same cover through
    both is the mistake.

---

## Scenario catalogue

### S1: Home Assistant only, no daemon

**Setup:** HA with *Homematic(IP) Local* (aiohomematic) talking to the
CCU directly. No OpenCCU-Loom anywhere.

**Choose this when** you have one CCU, HA is your only consumer, you
configure devices in the CCU WebUI, and nothing hurts today.

**What you give up:** multi-CCU as one fleet, CCU administration from a
modern UI, the daemon's alarm system, measurement history, and any
non-HA consumer.

**Cost of adding the daemon later:** low, but not zero — entity
identities change on the way (see [Switching paths](#switching-paths-later)).

---

### S2: Daemon owns the CCU, HA fed by MQTT Discovery

**Setup:** OpenCCU-Loom holds the CCU connection(s). `north.mqtt.enabled`
and `north.mqtt.discovery_enabled` are on, pointed at your MQTT broker.
HA's MQTT integration picks up the devices. *Homematic(IP) Local* is
**not** installed (or not pointed at the same CCU).

`north.mqtt.raw_enabled` belongs to this setup too: the discovery
payloads only declare entities, while the values they read live on the
raw topic plane. Leaving it off would give you every device in HA with
every entity stuck at `unavailable`, so the daemon switches it on for you
and logs a warning — set it explicitly to keep your config honest.

This is the mainstream path today.

**What you get in HA:** `sensor`, `binary_sensor`, `switch`, `light`,
`cover`, `climate`, `lock`, `siren`, `valve`, `button`, `select`,
`number`, `text`, `event` and `update` entities, plus system variables
and programs as hub entities, plus an `alarm_control_panel` when the
daemon's alarm system is configured. Entity names are localized by the
daemon (`de` / `en`). `north.mqtt.sub_devices_enabled` additionally
groups channels as HA sub-devices.

**Why people choose it:**

- **One connection to the CCU.** HA restarts, updates and reloads no
  longer re-initialise the CCU connection; the daemon keeps running and
  the CCU never notices.
- **A lean protocol into HA.** No custom integration in the HA process,
  no inbound XML-RPC callback listener inside HA; just MQTT.
- **Multi-CCU as one fleet**, with the central name in every topic.
- **Everything else the daemon does** — administration, history, alarm,
  Matter, third-party consumers — comes along for free.

**What you give up compared to the HA integration:**

- No `homematicip_local.*` services (`put_paramset`, `set_device_value`,
  `set_install_mode`, schedule copying, …). Their equivalents live in the
  daemon's SPA and REST API instead.
- No HA *device triggers* for buttons. Button presses arrive as `event`
  entities — fully usable in automations, but a different trigger idiom.
- MASTER paramset editing happens in the loom SPA, not in HA.
- You run an MQTT broker. (If you already run Zigbee2MQTT or similar,
  you do anyway.)

**Read next:** [MQTT topic schema](../mqtt-topic-schema.md),
[Multi-CCU](multi-ccu.md).

---

### S3: Daemon owns the CCU, HA fed by *Homematic(IP) Local* (loom backend)

**Setup:** *Homematic(IP) Local* runs with `backend: loom` and talks to
the daemon over REST + WebSocket instead of to the CCU over XML-RPC. The
daemon owns the CCU connection; the integration becomes a thin client.

!!! warning "Preview — not user-selectable today"
    The backend is fully wired in the integration (config-flow entries,
    mDNS discovery of daemons, in-place backend switch), but a master
    switch keeps it out of the UI: `LOOM_BACKEND_SELECTABLE = False` in
    *Homematic(IP) Local* 2.8.4. New config entries can only target the
    direct-CCU backend. Treat this scenario as **the planned path**, not
    as something you can enable in the add-on store today.

**What it will give you** that S2 does not: native HA entity semantics
(device triggers, the full `homematicip_local.*` service surface, the
integration's own device/area handling) **plus** the daemon's benefits
(one CCU connection, multi-CCU, no XML-RPC callback listener inside HA,
the loopback hop when the daemon runs on the CCU).

**The alarm system comes along natively.** The integration ships an
`alarm_control_panel` platform that exists *only* on the loom backend:
one panel entity per alarm area, plus an aggregate master panel once you
have two or more areas. State tokens are computed by the daemon and map
1:1 onto Home Assistant's alarm states; `ARM_HOME`/`ARM_AWAY`/
`ARM_NIGHT`/`ARM_VACATION`/`ARM_CUSTOM_BYPASS` map onto the daemon's
`perimeter` / `full` / `night` / `vacation` / `custom` protection modes,
exactly like the MQTT command plane. On a direct-CCU install the platform
spawns nothing — aiohomematic has no alarm engine to pair it with.

**Contract to keep in mind:** one config entry = one central. The
integration resolves its CCU by serial via `GET /system/ccu` and scopes
every request by the resulting central name.

**Watch out:** with this path you turn MQTT Discovery **off**. That is
the point (no duplicates), but retract the retained discovery configs
before flipping the flag — see [Anti-patterns](#anti-patterns).

**Read next:**
[HA drop-in identity & scoping](../external-clients/ha-drop-in-identity-and-scoping.md).

---

### S4: Daemon as a data source, Discovery off

**Setup:** HA keeps its existing device path (usually
*Homematic(IP) Local* directly on the CCU, S1). The daemon runs
alongside with `north.mqtt.enabled: true`, `raw_enabled: true` and
**`discovery_enabled: false`**.

**Choose this when** third-party systems need CCU data — Node-RED flows,
InfluxDB/Telegraf scrapers, evcc, ioBroker, custom dashboards — and you
do not want to touch a working HA setup.

**Why it works:** the raw topic plane is stable, documented and
independent of HA's discovery schema. With Discovery off the daemon
creates **no** HA entities, so there is nothing to duplicate.

**The cost of running both owners (axis A = "both"):** two logic-layer
clients register with the CCU, each with its own interface ID. The CCU
supports that, and push events cost no extra radio traffic — but each
client performs its own boot-time inventory and paramset reads. See
[Caching & performance](../caching.md) for what a cold start actually
costs on the radio.

**Read next:** [MQTT topic schema](../mqtt-topic-schema.md),
[External client wire contract](../external-clients/topic-hierarchy.md).

---

### S5: Matter bridge

**Setup:** `north.matter.enabled: true`, paired with Apple Home, Google
Home or Alexa.

**The honest positioning: if you run Home Assistant, you usually do not
need this.** HA can already publish *any* of its entities to Apple Home
(HomeKit Bridge) or to Matter controllers — including the Homematic
entities it got from S2 or S3. Bridging the same devices a second time
via loom's Matter bridge means two accessories per device in your Home
app.

**Turn it on anyway when:**

- **You do not run Home Assistant** — the standalone case, where Matter
  is the only way into Apple/Google/Alexa. This is the primary use.
- **You want ecosystem access that survives HA downtime.** A Matter
  bridge served by the daemon keeps working while HA updates, restarts
  or breaks. That is an availability argument, not a feature argument.
- **You deliberately split the fleet** — e.g. exactly the six devices
  your household controls by voice go to Matter, everything else goes
  to HA. Exclusive per device, so no duplicates.

**Constraints to know before enabling:** the bridge is **alpha** and off
by default; only mapped device types are exposed; Alexa commissions only
on UDP 5540 and caps out around 80–100 accessories per bridge; the
default test vendor identity makes ecosystems warn about an uncertified
accessory.

!!! danger "Port 5540 collides with HA's Matter Server"
    If HA's *Matter Server* add-on runs on the same host as the daemon,
    both want UDP 5540. Alexa cannot commission a bridge on any other
    port. Do not co-locate them unless you are prepared to give up
    Alexa commissioning.

**Read next:** [Matter bridge](matter.md).

---

### S6: Standalone, no Home Assistant

**Setup:** the daemon, its SPA, MQTT and/or Matter — no HA at all.

**What you get:** device control through the SPA, MQTT for anything
scriptable, Matter for Apple/Google/Alexa, the local alarm system,
measurement history and charts, and full CCU administration without the
CCU WebUI.

This is the configuration the Matter bridge exists for.

---

### S7: Multi-CCU with Home Assistant

One daemon can hold several CCUs; every store, topic and coordinator is
scoped by central name.

| Device path | How multi-CCU appears in HA |
|---|---|
| MQTT Discovery (S2) | Every topic carries `<central>`; devices from all CCUs land in one HA instance, distinguishable by name and topic. One daemon, one broker connection. |
| Loom backend (S3, preview) | **One config entry per central.** The entry resolves its CCU by serial and scopes all requests by central name. |
| Matter (S5) | One bridge for the whole fleet — mind Alexa's accessory cap when several CCUs add up. |

Migrating a device between CCUs leaves retained topics behind; the
cleanup pattern is in [Multi-CCU §2.3](multi-ccu.md#23-migrating-between-ccus).

---

### S8: The daemon as the logic host

Axis D is independent of how devices reach HA. Three homes for automation
logic, and they coexist:

| Logic lives in | Good for | Survives |
|---|---|---|
| **CCU programs** | Radio-local reactions, anything that must work with everything else down | CCU reboot only |
| **HA automations** | Cross-vendor logic, notifications, dashboards, anything needing HA context | HA uptime |
| **The daemon** | Alarm zones, calculated and combined data points, week profiles / schedules, webhooks | Daemon uptime — independent of HA |

Moving CCU programs into HA automations is a legitimate and common goal
("programs in HA instead of in the CCU"): HA's editor, versioning and
notification integrations beat the CCU's script editor. The trade is
availability — an automation in HA is dead while HA updates.

The daemon is the middle ground for the few things that must not depend
on HA. The **alarm system** is the clearest example: zones, arming,
delays, sirens, keyfob bindings and the journal all run in the daemon,
and HA only *operates* it through an `alarm_control_panel` entity — via
MQTT Discovery (S2) or the loom backend's native alarm platform (S3).
Pull the HA plug and the alarm still arms, triggers and sirens.

**Read next:** [Alarm system](../alarm-user-guide.md).

---

### S9: The Config UI as an HA panel

Orthogonal to everything above: the SPA can live in the HA sidebar
regardless of which device path you chose, or even with no device path
at all.

| Where the daemon runs | How the panel gets there |
|---|---|
| On the HA host | The **OpenCCU-Loom** add-on. Ingress handles authentication; `host_network: true` is required so the daemon can advertise a CCU-reachable callback IP. Keep `rest_port` at 8119 or the sidebar panel stops working. |
| On the CCU / OpenCCU (*recommended*, see [below](#recommended-run-the-daemon-on-the-ccu)) | The CCU add-on runs the daemon next to the CCU (loopback hop, self-updating); the **OpenCCU-Loom Remote** add-on proxies its UI into the HA sidebar. |
| Anywhere else (Docker, a server, a second site) | Same as above: the **Remote** add-on, which also handles several daemon instances at once under `/i/<name>/`. |

This is the answer to "the SPA as an alternative to the CCU WebUI panel":
device pairing, MASTER paramsets, links, groups, programs, system
variables, rooms and firmware all live in the SPA, inside HA's frontend,
with HA's login in front of them.

**Read next:**
[HA add-on packaging](https://github.com/SukramJ/openccu-loom/blob/main/packaging/ha-addon/README.md),
[ADR 0054 — Remote ingress proxy](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0054-remote-ingress-proxy-addon.md).

---

## Combination matrix

Read a row as "if I already run this, can I add the column?"

|  | HA direct (aiohomematic) | MQTT Discovery | Loom backend (preview) | Matter bridge | Raw MQTT | REST / WS / MCP / webhooks | SPA panel |
|---|---|---|---|---|---|---|---|
| **HA direct (aiohomematic)** | — | ❌ duplicates | ❌ same integration, other backend | ⚠️ duplicates in the Home app | ✅ | ✅ | ✅ |
| **MQTT Discovery** | ❌ duplicates | — | ❌ duplicates | ⚠️ per-device exclusive | ✅ | ✅ | ✅ |
| **Loom backend (preview)** | ❌ | ❌ duplicates | — | ⚠️ per-device exclusive | ✅ | ✅ | ✅ |
| **Matter bridge** | ⚠️ | ⚠️ per-device exclusive | ⚠️ per-device exclusive | — | ✅ | ✅ | ✅ |
| **Raw MQTT** | ✅ | ✅ | ✅ | ✅ | — | ✅ | ✅ |
| **REST / WS / MCP / webhooks** | ✅ | ✅ | ✅ | ✅ | ✅ | — | ✅ |

- ❌ — do not combine for the same devices; you get two entity sets.
- ⚠️ — allowed, but only when the device sets are disjoint (see
  [the one rule](#the-one-rule-exactly-one-device-path-per-device)).
- ✅ — freely combinable. The raw plane, REST/WebSocket, the MCP server,
  webhooks and the SPA are read/write surfaces, not HA entity producers,
  so they never duplicate anything.

---

## Anti-patterns

**Discovery on *and* the HA integration pointed at the same CCU.**
The classic duplicate. Every device exists twice in HA with different
identities. Decide, then switch the other one off.

**Turning Discovery off without retracting the retained configs.**
HA Discovery payloads are retained at QoS 1. The daemon's automatic
orphan cleanup runs **only while Discovery is enabled** — flip the flag
off and the broker keeps serving the old configs, so HA re-creates every
phantom entity on its next restart. Retract first, then flip:

```sh
script/clean-mqtt-discovery.sh   # see the script header for options
```

**Losing the alarm panel by accident.** The `alarm_control_panel`
discovery config is gated by the same `discovery_enabled` flag as every
other entity — so switching Discovery off removes the panel too. Two
cases, two answers:

- Moving to the **loom backend** (S3): nothing to do. The integration
  brings its own native alarm platform (see
  [S3](#s3-daemon-owns-the-ccu-ha-fed-by-homematicip-local-loom-backend)).
- Moving to **Matter** or back to a **direct-CCU** setup: neither has an
  alarm surface. The alarm **state and command topics are published
  regardless of the Discovery flag**, so keep MQTT on and configure HA's
  MQTT alarm panel manually against `<base>/alarm/<zone>/…` — see
  [Alarm topics](../mqtt-topic-schema.md#alarm-topics-daemon-level-no-central).

**Bridging to Matter what HA already bridges.** If HA publishes the same
lamp to Apple Home through its HomeKit/Matter bridge and loom publishes it
through its own Matter bridge, you get two accessories and two states.

**Running loom's Matter bridge next to HA's Matter Server on one host.**
UDP 5540 collision; Alexa commissioning breaks.

**Changing `rest_port` in the HA add-on.** Ingress proxies to the static
port 8119. Change it and the sidebar panel goes dark — reach the SPA at
`http://<ha-host>:<rest_port>/app/` instead, or keep 8119.

**Expecting the daemon in a bridge-networked container to receive CCU
events.** The daemon advertises a callback IP per central; behind
container NAT it advertises an address the CCU cannot reach. That is why
the add-on uses `host_network: true`.

---

## Deployment topologies

| Topology | CCU hop | HA hop | Notes |
|---|---|---|---|
| **Daemon as HA add-on** | LAN | loopback | Simplest. Ingress panel included. Needs `host_network`. Daemon and HA share a fate: host down = both down. |
| **Daemon on the CCU** (CCU/OpenCCU add-on) — *recommended* | **loopback** | LAN | Least LAN traffic on the noisy side, self-updating add-on, CCU-delegated login. HA reaches it via MQTT/REST and the Remote add-on for the panel. |
| **Daemon on a separate host** (Docker, server, VM) | LAN | LAN | Both hops on the LAN. Best isolation, independent restarts. Use the Remote add-on for the panel. |

Availability is the part people underestimate: with the daemon separate
from HA, an HA update no longer interrupts the CCU connection, the alarm
system or the Matter bridge. With the daemon on the CCU, a HA host reboot
touches nothing on the Homematic side at all.

### Recommended: run the daemon on the CCU

If your CCU is an OpenCCU box (or a CCU3) and you have no
reason not to, **install the CCU add-on and run the daemon there**. It is
the topology the split architecture was designed for.

**Why it is the best default:**

- **The chatty hop disappears from the LAN.** XML-RPC, BIN-RPC and the
  event callbacks all run over loopback. What crosses the network is the
  compact north-bound traffic — MQTT deltas or one WebSocket — instead of
  every paramset read and every callback.
- **Callback addressing stops being a problem.** The daemon resolves the
  callback host per central from the egress interface, so a co-located
  CCU gets `127.0.0.1` automatically. No `host_network`, no container
  NAT, no "central heartbeat degraded" because the CCU cannot reach back.
- **Fate-sharing lands where it belongs.** The daemon is up exactly when
  the CCU is up. HA updates, reboots, container rebuilds and HA-OS
  upgrades no longer touch the Homematic side at all — including the
  alarm system and the Matter bridge.
- **It maintains itself.** The add-on runs under monit, enables the SPA's
  **Restart** action, updates itself from the project's GitHub releases
  ([ADR 0057](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0057-addon-self-update.md)),
  and defaults to CCU-delegated login — so there is no second user
  database to maintain.
- **Installing costs nothing on OpenCCU** — the add-on
  installs and starts in place, no reboot. (Stock CCU3 firmware reboots
  on every add-on install; that is the CCU WebUI's behaviour, not ours.)

**Choose a separate host instead when:**

- **You run several CCUs.** Only the CCU hosting the daemon gets the
  loopback benefit; the others are back on the LAN. With a large fleet, a
  neutral host is the more honest topology — or put the daemon on the CCU
  that carries the most devices.
- **You want the heavy optional features on stronger hardware.** A stock
  CCU3 is the weakest supported platform (armv7l); the Matter bridge,
  measurement history across many data points and large device fleets are
  more comfortable on OpenCCU on a Pi 4 / x86 box, or off-CCU
  entirely. Start on the CCU, move if you actually feel it.
- **Storage matters to you.** Persistent state (SQLite + files) lives in
  `/usr/local/addons/openccu-loom/var` — on the CCU's own SD card or
  eMMC. Measurement history is the one feature that turns that into
  continuous write load; enable it per data point rather than wholesale,
  and keep CCU backups.
- **You need the daemon to outlive the CCU.** Rebooting the CCU takes the
  daemon with it. If a bridge (MQTT consumers, Matter accessories) should
  stay reachable across a CCU restart, keep it off-box.

**Setup notes for this topology:** add a central pointing at `127.0.0.1`,
install the **OpenCCU-Loom Remote** add-on in HA to get the Config UI into
the sidebar, and set `north.rest.public_url` if you reach the SPA through
a reverse proxy — see
[the CCU add-on README](https://github.com/SukramJ/openccu-loom/blob/main/packaging/ccu-addon/README.md).

---

## Switching paths later

**HA direct → daemon + MQTT Discovery.** Entity identities change; the
new entities arrive with fresh unique IDs. Plan for renaming entities or
accepting new IDs, and expect to touch dashboards and automations. Do it
in one pass: disable the HA integration first, then enable Discovery, so
the two entity sets never coexist.

**MQTT Discovery → loom backend (once it ships).** Retract the retained
discovery configs *before* switching (`script/clean-mqtt-discovery.sh`),
otherwise HA keeps the MQTT entities alongside the new integration
entities. The integration side carries a one-time registry migration to
the loom/serial unique-ID scheme —
[HA unique-ID migration](../external-clients/ha-unique-id-migration.md).

**Adding Matter to an existing HA setup.** Restrict the Matter exposure
list to devices you do *not* forward from HA to the same ecosystem. The
Matter view has a per-device selection for exactly this.

**Moving a CCU between daemons or centrals.** Retained topics under the
old central name survive; clean them as shown in
[Multi-CCU §2.3](multi-ccu.md#23-migrating-between-ccus).

---

## Feature comparison

What each device path actually delivers in Home Assistant.

| Capability | HA direct (aiohomematic) | MQTT Discovery | Loom backend (preview) |
|---|---|---|---|
| Device entities | ✅ | ✅ | ✅ |
| System variables & programs | ✅ | ✅ | ✅ |
| Button presses | device triggers + `event` entities | `event` entities only | device triggers + `event` entities |
| Firmware `update` entities | ✅ | ✅ | ✅ |
| `homematicip_local.*` services | ✅ | ❌ (SPA / REST instead) | ✅ |
| MASTER paramset editing | via service calls | ❌ (SPA instead) | via service calls |
| Alarm system panel | ❌ (aiohomematic has no alarm engine) | ✅ `alarm_control_panel` per area | ✅ native platform, per area + master |
| Multi-CCU in one HA | one entry per CCU | ✅ one daemon, all CCUs | one entry per central |
| Survives HA restart without CCU re-init | ❌ | ✅ | ✅ |
| Inbound listener inside HA | XML-RPC callback server | none | none |
| Needs an MQTT broker | ❌ | ✅ | ❌ |
| Available today | ✅ | ✅ | ❌ preview |

---

## Where to go next

- [Getting started](../getting-started.md) — install the daemon and add
  your first CCU.
- [Core concepts](concepts.md) — devices, channels, data points.
- [MQTT topic schema](../mqtt-topic-schema.md) — every topic, payload and
  retain rule.
- [Matter bridge](matter.md) — enabling and pairing.
- [Multi-CCU operations](multi-ccu.md) — scoping, callbacks, migrations.
- [Alarm system](../alarm-user-guide.md) — zones, sensors, outputs.
- [REST & WebSocket API](../integrations/rest-ws.md) and
  [MCP server](../external-clients/mcp.md) — the non-HA surfaces.
