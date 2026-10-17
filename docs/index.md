# OpenCCU-Loom

**A standalone Go daemon that bridges Homematic and HomematicIP CCUs to
MQTT, a REST + WebSocket API, a web Config UI, and Matter.**

OpenCCU-Loom talks to Homematic / HomematicIP CCUs (CCU2, CCU3,
OpenCCU) over XML-RPC, BIN-RPC, and JSON-RPC — and to
[openccu-lite](admin/openccu-lite.md) boxes over occulited's HTTP API
and event stream — and exposes them on the north side to standard
protocols, so you can use your devices from MQTT, REST/WebSocket
clients, a browser, or a Matter controller without running Home
Assistant.

OpenCCU-Loom began as a Go port of
[`aiohomematic`](https://github.com/SukramJ/aiohomematic) and now develops
on its own. The two projects coexist: `aiohomematic` powers the Home
Assistant integration *Homematic(IP) Local*; OpenCCU-Loom serves users
who want MQTT / REST / UI / Matter access without it. It is open source
under the Apache License 2.0.

---

## What it gives you

- **MQTT** — Home Assistant Discovery **and** a raw topic plane, in
  parallel.
- **REST + WebSocket API** — full device/parameter control and a live
  event stream.
- **Config UI** — a Svelte SPA for setup, device configuration, and
  diagnostics.
- **Matter** — a pure-Go Matter bridge (opt-in, built on
  [go-fabric](https://github.com/SukramJ/go-fabric)) so your CCU devices
  appear in Apple Home, Google Home, and Alexa.
- **Alarm system and Security & Safety** — zones, arming, codes and a
  fault overview across your sensors, on MQTT, REST and the web UI.
- **History, webhooks and MCP** — measurement history, outbound and
  inbound webhooks, and an MCP server so an AI assistant can drive the
  daemon.
- **Multi-CCU** — one daemon, many centrals, first-class from day one —
  full CCUs and [openccu-lite](admin/openccu-lite.md) boxes side by side.

## Single static binary

OpenCCU-Loom ships as a single static binary (`CGO_ENABLED=0`) and as
multi-arch Docker images (amd64, arm64, armv7). Persistence is pure-Go
SQLite plus the filesystem — no external database, no CGo.

---

## Where to next

The navigation follows three tracks — pick the one that matches what you
want to do.

<div class="grid cards" markdown>

- :material-account: **Use**

    ---

    Get a CCU bridged and use your devices from the web UI, MQTT, or a
    Matter controller.

    [:octicons-arrow-right-24: Getting Started](getting-started.md)

    [:octicons-arrow-right-24: Concepts](user/concepts.md) ·
    [Web UI](user/web-ui.md) ·
    [Matter](user/matter.md) ·
    [Alarm](alarm-user-guide.md)

    Running Home Assistant? Start with
    [choosing your integration path](user/home-assistant.md).

- :material-server-network: **Operate & integrate**

    ---

    Install, configure, secure and run the daemon; connect MQTT, REST,
    WebSocket and MCP clients.

    [:octicons-arrow-right-24: Installation & First Steps](user-guide.md)

    [:octicons-arrow-right-24: Configuration](admin/configuration.md) ·
    [Security](SECURITY.md) ·
    [REST + WebSocket](integrations/rest-ws.md) ·
    [Reference](reference/configuration.md)

- :material-code-tags: **Develop & architecture**

    ---

    How the daemon is built and why: architecture, the Matter boundary,
    wiring, testing, releases and the decision records.

    [:octicons-arrow-right-24: Architecture](developer/architecture.md)

    [:octicons-arrow-right-24: Dev setup](developer/setup.md) ·
    [Decisions](developer/adr-index.md)

</div>

## Project links

- Source: [github.com/SukramJ/openccu-loom](https://github.com/SukramJ/openccu-loom)
- Matter stack: [go-fabric](https://github.com/SukramJ/go-fabric)
- Sibling project (Home Assistant integration):
  [aiohomematic](https://github.com/SukramJ/aiohomematic)
- Upstream OS: [OpenCCU](https://github.com/OpenCCU/OpenCCU) ·
  [openccu.de](https://openccu.de)

!!! note "Documentation language"
    The documentation is built to be multilingual. For now it is
    published in **English** only; German pages will be added over
    time.
