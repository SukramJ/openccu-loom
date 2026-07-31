# OpenCCU-Loom

**A standalone Go daemon that bridges Homematic and HomematicIP CCUs to
MQTT, a REST + WebSocket API, a web Config UI, and Matter.**

OpenCCU-Loom talks to Homematic / HomematicIP CCUs (CCU2, CCU3,
RaspberryMatic) over XML-RPC, BIN-RPC, and JSON-RPC, and exposes them
on the north side to standard protocols — so you can use your devices
from MQTT, REST/WebSocket clients, a browser, or a Matter controller
without running Home Assistant.

It is a Go port of the Python library
[`aiohomematic`](https://github.com/SukramJ/aiohomematic) that adds the
standalone-daemon surface on top. The two projects coexist:
`aiohomematic` powers the Home Assistant integration *Homematic(IP)
Local*; OpenCCU-Loom serves users who want MQTT / REST / UI / Matter
access on their own.

---

## What it gives you

- **MQTT** — Home Assistant Discovery **and** a raw topic plane, in
  parallel.
- **REST + WebSocket API** — full device/parameter control and a live
  event stream.
- **Config UI** — a Svelte SPA for setup, device configuration, and
  diagnostics.
- **Matter** — a native-Go Matter bridge (opt-in) so your CCU devices
  appear in Apple Home, Google Home, and Alexa.
- **Multi-CCU** — one daemon, many CCUs, first-class from day one.

## Single static binary

OpenCCU-Loom ships as a single static binary (`CGO_ENABLED=0`) and as
multi-arch Docker images (amd64, arm64, armv7). Persistence is pure-Go
SQLite plus the filesystem — no external database, no CGo.

---

## Where to next

Pick the lane that matches what you want to do.

<div class="grid cards" markdown>

- :material-account: **For end users**

    ---

    Get a CCU bridged and use your devices from a browser, MQTT, or a
    Matter controller.

    [:octicons-arrow-right-24: Getting Started](getting-started.md)

    [:octicons-arrow-right-24: Concepts](user/concepts.md) ·
    [Web UI](user/web-ui.md) ·
    [Matter](user/matter.md)

    Running Home Assistant? Start with
    [choosing your integration path](user/home-assistant.md).

- :material-server-network: **For administrators**

    ---

    Install, secure, and operate the daemon — install methods, the
    config reference, and the security model.

    [:octicons-arrow-right-24: Installation & First Steps](user-guide.md)

    [:octicons-arrow-right-24: Configuration reference](admin/configuration.md) ·
    [Security](SECURITY.md)

- :material-code-tags: **For developers & integrators**

    ---

    Talk to the REST/WebSocket API, understand the architecture, and
    build from source.

    [:octicons-arrow-right-24: REST + WebSocket API](integrations/rest-ws.md)

    [:octicons-arrow-right-24: Architecture](developer/architecture.md) ·
    [Dev setup](developer/setup.md)

</div>

## Project links

- Source: [github.com/SukramJ/openccu-loom](https://github.com/SukramJ/openccu-loom)
- Reference library: [aiohomematic](https://github.com/SukramJ/aiohomematic)
- Upstream OS: [OpenCCU](https://github.com/OpenCCU/OpenCCU) ·
  [openccu.de](https://openccu.de)

!!! note "Documentation language"
    The documentation is built to be multilingual. For now it is
    published in **English** only; German pages will be added over
    time.
