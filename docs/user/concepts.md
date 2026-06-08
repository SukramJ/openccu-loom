# Core Concepts

This page explains the handful of words OpenCCU-Loom uses for the things on your Homematic system, so the rest of the documentation makes sense. No prior knowledge of the internals is assumed.

!!! info "Who this page is for"
    End users running OpenCCU-Loom at home. You do not need to know Go or read any source code — this page is plain language with a small diagram.

## The CCU

A **CCU** is your Homematic central unit — the box (or piece of software) that talks to your Homematic radio devices. OpenCCU-Loom does not replace it; it connects to it and makes its devices available to other systems.

OpenCCU-Loom can talk to **more than one CCU at the same time**. Each configured CCU is given a **name** in the configuration (the `name` field of a CCU entry). This name — often called the `central_name` — is the stable label that identifies one CCU everywhere: in the web UI, in MQTT topics, and in the REST API. Pick a name once and keep it; renaming it changes how that CCU is addressed throughout the system.

For details on running several CCUs at once, see the [Multi-CCU guide](multi-ccu.md).

## Devices, Channels, and Data Points

Everything a CCU exposes is organised in a three-level hierarchy.

```text
CCU  (named, e.g. "home")
└── Device          a physical thing — a thermostat, a switch, a sensor
    └── Channel     one function of that device — button 1, the relay, the sensor
        └── DataPoint   one value or setting — STATE, LEVEL, TEMPERATURE, ...
```

| Term | What it is | Example |
|------|-----------|---------|
| **Device** | A physical Homematic device, identified by its address. | A wall thermostat, a switch-actuator, a door/window sensor. |
| **Channel** | One independent function of a device. Most devices have several. | A 4-button remote has one channel per button; a switch has a relay channel. |
| **Data Point** | A single named parameter you can read or, sometimes, write. | `STATE` (on/off), `LEVEL` (dim level), `TEMPERATURE`, `BATTERY_STATE`. |

When you browse your system in the [web UI](web-ui.md), you navigate exactly this hierarchy: pick a device, open a channel, look at its data points.

## Kinds of Data Points

Not every data point comes straight off the CCU wire. OpenCCU-Loom presents three kinds:

| Kind | Where it comes from | Examples |
|------|--------------------|----------|
| **Wire** | A direct CCU parameter, read and written as the CCU exposes it. | `STATE`, `LEVEL`, `TEMPERATURE`, `ACTUAL_TEMPERATURE`. |
| **Custom** | A composite surface assembled from several wire parameters, following a per-device profile, so a whole device behaves as one coherent thing. | A light, a cover/blind, a lock, a thermostat. |
| **Calculated** | A value the daemon derives from other data points — it does not exist on the CCU itself. | Dew point, apparent temperature, frost point, battery percentage. |

Custom and calculated data points are what let OpenCCU-Loom present a Homematic device as a clean "light" or "thermostat" to MQTT or Matter, rather than as a pile of raw parameters.

## Interfaces

A CCU does not use a single radio for everything. An **interface** is one radio or transport family inside the CCU. OpenCCU-Loom connects to each one it is configured for.

| Interface | What it carries | Transport |
|-----------|----------------|-----------|
| `HmIP-RF` | Homematic IP radio devices (this also hosts HmIP-Wired devices). | XML-RPC |
| `BidCos-RF` | Classic Homematic (BidCos) radio devices. | XML-RPC |
| `BidCos-Wired` | Wired classic Homematic devices. | XML-RPC |
| `VirtualDevices` | Virtual devices and groups (e.g. heating groups). | XML-RPC |
| `CUxD` | Devices exposed through the CUxD add-on. | BIN-RPC |

You normally do not interact with interfaces directly — you configure which ones a CCU should use, and OpenCCU-Loom takes care of the rest. See [Configuration](../admin/configuration.md) for how interfaces are declared.

## Bridging north-bound

"North-bound" is the direction **away** from the CCU and **towards** the systems you actually use. OpenCCU-Loom takes the devices it reads from your CCU(s) and re-publishes them through several north-bound bridges, all at once:

- **MQTT** — publishes device state to an MQTT broker, including Home Assistant Discovery and raw topic planes. See the [MQTT topic schema](../mqtt-topic-schema.md).
- **REST + WebSocket API** — a programmable HTTP interface for reading and writing values and subscribing to live updates.
- **Web Config UI** — the browser app for inspecting and configuring everything. See [Using the Web UI](web-ui.md).
- **Matter** — exposes selected devices to Apple Home, Google Home, and Alexa. Off by default; see [Matter bridge](matter.md).

One device read from the CCU can appear on several of these bridges simultaneously.

## Parameter visibility (ignore / un-ignore)

A real CCU device can expose dozens of parameters, many of which are internal, expert-only, or rarely useful. To keep things readable, OpenCCU-Loom **hides** some parameters by default — they are "ignored" for everyday views and north-bound publishing.

If you need a hidden parameter — for example, an expert tuning value or a diagnostic counter — you can **un-ignore** it. Once un-ignored, the parameter becomes visible in the UI and is published north-bound like any other data point. This is done from the web UI's un-ignore view (backed by the `/api/v1/visibility/unignore` endpoint); see [Using the Web UI](web-ui.md).

## Where to go next

- [Getting started](../getting-started.md) — install and first run.
- [Using the Web UI](web-ui.md) — the browser config app.
- [Matter bridge](matter.md) — pairing with Apple Home, Google Home, and Alexa.
- [Configuration](../admin/configuration.md) — full configuration reference.
