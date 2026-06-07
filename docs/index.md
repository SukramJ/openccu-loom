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

<div class="grid cards" markdown>

- :material-rocket-launch: **[Getting Started](getting-started.md)** —
  install, first run, and the ports you need to open.
- :material-book-open-variant: **[User Guide](user-guide.md)** —
  operator install + configuration walkthrough.
- :material-transit-connection-variant: **[Integrations](mqtt-topic-schema.md)** —
  MQTT topics, the [MCP server](external-clients/mcp.md), and the
  external-client wire contract.

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
