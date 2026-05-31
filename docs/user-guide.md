# OpenCCU-Loom — User Guide

OpenCCU-Loom is a standalone Go daemon that bridges Homematic CCU
systems to MQTT, a REST+WebSocket API, and a browser-based config
UI. This guide covers the operator perspective: install, configure,
monitor.

## Install

### Docker (recommended)

```sh
docker run -d \
  -p 8080:8080 -p 8081:8081 -p 8120:8120 -p 8129:8129 \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  -v openccu-loom-data:/app/var \
  ghcr.io/sukramj/openccu-loom:latest run --config /app/config.yaml
```

### Binary

Download the matching archive from the releases page and extract:

```sh
./openccu-loom run --config ./config.yaml
```

## Configuration (YAML)

```yaml
locale: en        # de | en
data_dir: ./var

logging:
  level: info     # debug | info | warn | error
  format: json    # json | text

callback:
  host: 0.0.0.0
  port: 8120      # XML-RPC callback listener
  bin_port: 8129  # BIN-RPC callback listener (CUxD)

north:
  rest:
    enabled: true
    listen: ":8080"
    cors: ["https://my-ui.example"]
    auth:
      basic_enabled: true
      bearer_enabled: true
      session_enabled: true
      users: { "alice": "change-me" }
      tokens: { "tok-abc": "admin" }

  ui:
    enabled: true
    listen: ":8081"

  mqtt:
    enabled: true
    broker_url: "tcp://mosquitto:1883"
    client_id: openccu-loom
    username: ""
    password: ""
    topic_base: openccu-loom
    raw_enabled: true
    discovery_enabled: true

centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces: [HmIP-RF, BidCos-RF]

ccu_data:
  # Optional — point at extracted OCCU metadata archives.
  # See script/README.md for the extractor invocation.
  translations_path: ./var/ccu_data/translation_extract.json.gz
  easymode_path:     ./var/ccu_data/easymode_extract.json.gz
```

## First-run setup

1. Start the daemon with the minimum config (no users yet).
2. Open `http://localhost:8081/setup` — create the first admin account.
3. Sign in at `/login`.

## API quickstart

```sh
# Health check
curl http://localhost:8080/api/v1/health

# List devices (Basic)
curl -u alice:change-me http://localhost:8080/api/v1/devices

# Subscribe to events via WebSocket
websocat ws://localhost:8080/api/v1/events
> {"op":"subscribe","topics":["device.*"]}

# Set a value
curl -X PUT \
  -u alice:change-me \
  -H 'Content-Type: application/json' \
  -d '{"value": true, "priority": "high"}' \
  http://localhost:8080/api/v1/devices/0001ABCD/channels/1/data_points/STATE/value
```

## MQTT topic layout

Raw plane (always on when MQTT is enabled):

```
<base>/<central>/<interface>/<addr>/<channel>/<parameter>       state (retained)
<base>/<central>/<interface>/<addr>/<channel>/<parameter>/set   command
<base>/<central>/<interface>/<addr>/availability                online|offline (retained)
<base>/<central>/hub/programs/<id>                              state (retained)
<base>/<central>/hub/programs/<id>/trigger                      command
<base>/<central>/hub/sysvars/<name>                             value (retained)
<base>/<central>/hub/sysvars/<name>/set                         command
<base>/bridge/status                                            LWT (retained)
```

Home Assistant Discovery plane (same state topics, separate config messages):

```
homeassistant/<component>/openccu-loom/<object_id>/config
```

## Troubleshooting

- `/api/v1/health` reports `unhealthy`: inspect `/api/v1/incidents`.
- MQTT bridge not publishing: confirm `north.mqtt.enabled: true` and `broker_url` is reachable.
- CCU device list empty: check `centrals[].host`, inspect daemon logs for `central.start` + `pipeline.ingest` lines.

## Log viewer (`#/logs`)

Admin-only. The log viewer in the config UI streams daemon log entries in real time via Server-Sent Events (SSE with resume), separate from the device WebSocket.

**Log level.** The dropdown at the top changes the global default log level (`debug` / `info` / `warn` / `error`) at runtime without a restart. The change takes effect immediately for new entries; the config-file level is the startup default.

**Aggregated vs. Detail view.**
- *Aggregated* — shows only `warn` and above; repeated identical messages are collapsed into a single row showing the last timestamp and a repeat count (`×N`).
- *Detail* — shows every entry unfiltered. Rows that carry additional structured fields (attrs) can be expanded inline.

**Live / Pause.** Scrolling up pauses auto-scroll so new entries do not pull the view back to the bottom. A pill reading "▼ N new · Jump to live" appears while paused; clicking it or scrolling back to the bottom resumes live mode.

**Filter and download.** A text filter matches against the message, logger name, and structured fields. The download button exports the last 100 / 200 / 500 / 1 000 entries as NDJSON (one JSON object per line, gzip optional).

## Diagnostics and recording

The Diagnostics page (admin-only) provides a recording hub for three capture types.

### Capture types

| Type | What is captured | Output format |
|---|---|---|
| **Debug-log capture** | RAM-buffered slog archive for a time window | gzip-NDJSON, downloadable |
| **RPC session recorder** | All XML-RPC / JSON-RPC / BIN-RPC traffic to and from the CCU | per-CCU download, two formats |
| **Diagnostics dump** | Point-in-time snapshot of health, interfaces, incidents, and log levels | JSON |

### Starting a recording

Click **New recording** and fill in:

- **Type** — Debug-log, RPC traffic, or both.
- **Duration** — seconds; `0` means open-ended. The server caps all recordings at 60 minutes regardless of the value entered.
- **CCU scope** — all configured CCUs or a specific one.
- **Anonymise** — when enabled, device-identifying values in RPC traces are hashed before writing to disk.

### RPC recorder behaviour

An active RPC recording survives a daemon restart (the active-recording marker is persisted to disk). The recording stops automatically when the configured duration elapses or after the 60-minute cap. Downloads are available per CCU in two formats:

- **map** — a slot-map keyed by call signature, useful for ad-hoc inspection.
- **golden** — an ordered replay list suitable as input for the golden-file test harness under `tests/golden/`.

## Further reading

- `docs/SECURITY.md` — security model + audit checklist
- `SPECIFICATION.md` — complete design spec
- `assets/openapi.yaml` — REST API contract
