# Administration: Installation & First Steps

!!! info "Who this page is for"
    Administrators installing and operating the daemon. End users who
    just want to use an already-running instance should start at
    [Getting Started](getting-started.md). For the full list of every
    config key, see the [Configuration reference](admin/configuration.md).

OpenCCU-Loom is a standalone Go daemon that bridges Homematic CCU
systems to MQTT, a REST+WebSocket API, a Matter bridge, and a
browser-based config UI. This page covers install methods, the ports
to open, first-run setup, and the bootstrap config tier. The complete
config-key reference, auth model, backup, and observability each have
their own pages — linked below.

## Install

### Docker (recommended)

```sh
docker run -d \
  -p 8080:8080 -p 8081:8081 -p 8120:8120 -p 8129:8129 \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  -v openccu-loom-data:/app/var \
  ghcr.io/sukramj/openccu-loom:latest run --config /app/config.yaml
```

Multi-arch images (amd64, arm64, armv7) are published to
`ghcr.io/sukramj/openccu-loom`.

### Binary

Download the matching archive from the releases page and extract. The
binary is fully static (`CGO_ENABLED=0`) with no runtime dependencies:

```sh
./openccu-loom run --config ./config.yaml
```

The daemon auto-discovers a config when `--config` is omitted (first
existing wins): `$OPENCCU_LOOM_CONFIG`, `./config.yaml`,
`~/.config/openccu-loom/config.yaml`, `/etc/openccu-loom/config.yaml`.

Other subcommands: `openccu-loom version`, `openccu-loom backup`,
`openccu-loom config`. Validate a config file ahead of time with
`hmcli config validate ./config.yaml`.

## Ports

| Port | Purpose | Direction |
| --- | --- | --- |
| `8080` | REST + WebSocket API (the MCP route mounts here too) | inbound from clients |
| `8081` | Config UI (Svelte SPA + HTMX bootstrap) | inbound from browsers |
| `8120` | XML-RPC push callback server (HmIP-RF, BidCos, …) | inbound from the CCU |
| `8129` | BIN-RPC push callback server (CUxD) | inbound from the CCU |
| `5540` | Matter bridge (UDP; **off by default**) | inbound from controllers |

The two callback ports are how the CCU pushes value changes back to
the daemon, so they must be reachable **from** the CCU. The Matter
listener only binds when `north.matter.enabled: true`.

## The bootstrap config tier

A small `config.yaml` covers only the **bootstrap tier** — the values
the daemon needs before its database opens (data dir, bind addresses,
log handler, default UI language, callback ports). On a fresh install,
anything you list there is seeded into the database on first start;
after that the database wins and the [web UI](user/web-ui.md) is the
place to make changes.

```yaml
locale: en            # de | en — default UI language
data_dir: ./var       # SQLite DB + per-central state live here

logging:
  level: info         # debug | info | warn | error
  format: json        # json | text

callback:
  host: 0.0.0.0
  port: 8120          # XML-RPC callback listener (0 = dynamic)
  bin_port: 8129      # BIN-RPC callback listener (CUxD; 0 = dynamic)

north:
  rest:
    listen: ":8080"
  ui:
    listen: ":8081"

centrals:
  - name: ccu-01
    host: 192.168.1.10
    interfaces: [HmIP-RF, BidCos-RF]
```

This is enough to boot and reach the first-run setup. MQTT, Matter,
REST auth, OIDC, and everything else are configured from the UI (or
seeded once via the full config). For the annotated reference of every
key, see:

- **[Configuration reference](admin/configuration.md)** — every option,
  grouped by area.
- [`config.example.yaml`](https://github.com/SukramJ/openccu-loom/blob/main/config.example.yaml)
  — the minimal bootstrap-tier file.
- [`config.example.full.yaml`](https://github.com/SukramJ/openccu-loom/blob/main/config.example.full.yaml)
  — an annotated reference of every option.
- [`example.env`](https://github.com/SukramJ/openccu-loom/blob/main/example.env)
  — every environment variable; prefer env for secrets.

!!! warning "Secrets at rest"
    Secret-classed fields (passwords, OIDC client secret, …) are
    encrypted in the database at `<data_dir>/openccu-loom.db`. The
    master key comes from `OPENCCU_LOOM_SECRET_KEY` (base64, 32 bytes)
    or an auto-generated `<data_dir>/secret.key` (mode `0600`).
    **Back up `secret.key` together with the database** — without it,
    restored secrets cannot be decrypted. See
    [Backup](admin/backup.md) and [Security](SECURITY.md).

## First-run setup

1. Start the daemon with the bootstrap config (no users yet).
2. Open `http://localhost:8081/setup` — create the first admin account.
3. Sign in at `/login`. OIDC is supported when configured (see
   [Authentication](admin/auth.md)).

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

A few quick checks; the full catalogue is in
[Troubleshooting](admin/troubleshooting.md).

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

- [Configuration reference](admin/configuration.md) — every config key.
- [Authentication](admin/auth.md) — Basic / Session / OIDC / API tokens.
- [Backup](admin/backup.md) — what to back up and how to restore.
- [Observability](admin/observability.md) — health, metrics, tracing.
- [Troubleshooting](admin/troubleshooting.md) — common failure modes.
- [Security](SECURITY.md) — security model + hardening checklist.
- [REST + WebSocket API](integrations/rest-ws.md) — the full API contract.
- [MQTT topic schema](mqtt-topic-schema.md) — topic layout reference.
- [Multi-CCU](user/multi-ccu.md) — run many CCUs from one daemon.
