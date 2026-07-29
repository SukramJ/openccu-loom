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
docker run -d --restart unless-stopped \
  -p 8119:8119 -p 8120:8120 -p 8129:8129 \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  -v openccu-loom-data:/app/var \
  ghcr.io/sukramj/openccu-loom:latest run --config /app/config.yaml
```

Multi-arch images (amd64, arm64, armv7) are published to
`ghcr.io/sukramj/openccu-loom`.

`--restart unless-stopped` (already set in the repository's
`docker-compose.yaml`) is what makes the Config UI's **Restart** action
work: the daemon exits and Docker brings the container back. Without a
restart policy, a restart from the UI leaves the daemon down.

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

### Home Assistant add-on

Add `https://github.com/SukramJ/openccu-loom` as a repository under
**Settings → Add-ons → Add-on Store → ⋮ → Repositories**, then install
**OpenCCU-Loom**. The daemon runs on the HA host, the Config UI appears
as a sidebar panel (Ingress) and on `:8119`, and state persists in the
add-on's `/data` — no `config.yaml` needed. Optional Ingress auto-login
is described under [First-run setup](#first-run-setup) below.

A second add-on, **OpenCCU-Loom Remote**
([ADR 0054](adr/0054-remote-ingress-proxy-addon.md)), does not run a
daemon of its own — it proxies an instance running elsewhere (Docker,
CCU add-on) into the same sidebar panel. Layout and build details:
[`packaging/ha-addon/README.md`](https://github.com/SukramJ/openccu-loom/blob/main/packaging/ha-addon/README.md).

### CCU / RaspberryMatic add-on

Runs the daemon directly on the CCU. Download
`openccu-loom-ccu-<version>.tar.gz` from the
[releases page](https://github.com/SukramJ/openccu-loom/releases) and
install it under **Settings → Control panel → Additional software**.
On RaspberryMatic / OpenCCU the add-on installs and starts in place with
no reboot; stock CCU3 firmware restarts its WebUI as part of every
add-on install.

Supported platforms are CCU3 (armv7l) and RaspberryMatic / OpenCCU in
all flavours (armv7l, aarch64, x86-64). CCU1 and CCU2 are not supported.

Two behaviours are specific to this install: it defaults to
CCU-delegated login (see [Authentication](admin/auth.md)), and it can
update itself from the project's GitHub releases
([ADR 0057](adr/0057-addon-self-update.md)) — a check button, a
boot-delayed check and a periodic check, with the downloaded package
verified against the release checksums. The daemon also resolves the
callback host per central, so a co-located CCU gets `127.0.0.1`
automatically. Reverse-proxy setup, port clashes and the data directory
are covered in
[`packaging/ccu-addon/README.md`](https://github.com/SukramJ/openccu-loom/blob/main/packaging/ccu-addon/README.md).

## Ports

| Port | Purpose | Direction |
| --- | --- | --- |
| `8119` | REST + WebSocket API, Config UI (Svelte SPA), MCP route | inbound from clients and browsers |
| `8120` | XML-RPC push callback server (HmIP-RF, BidCos, …) | inbound from the CCU |
| `8129` | BIN-RPC push callback server (CUxD) | inbound from the CCU |
| `5540` | Matter bridge (UDP; **off by default**) | inbound from controllers |

The REST API and the Svelte SPA share port `8119`. Login, first-run
onboarding, and the OIDC callback all live inside the SPA (ADR 0045);
the only server-rendered pages are a minimal no-JS `/health` and
`/about`, which act as a diagnostic anchor when the SPA cannot load.
The separate `:8081` listener has been removed, along with the
`north.ui.listen` config key — there is no separate UI bind address.

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
    listen: ":8119"

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
- [`example.config.yaml`](https://github.com/SukramJ/openccu-loom/blob/main/example.config.yaml)
  — the minimal bootstrap-tier file.
- [`example.config.full.yaml`](https://github.com/SukramJ/openccu-loom/blob/main/example.config.full.yaml)
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
2. Open `http://localhost:8119/` — the SPA redirects automatically to
   `/setup` when no admin user exists. Create the first admin account.
3. Sign in at `/login`. OIDC is supported when configured (see
   [Authentication](admin/auth.md)).

**HA add-on users**: the Ingress panel in the HA sidebar opens the same
`http://…:8119/` entrypoint. On a fresh install the redirect to `/setup`
works through Ingress — no SSH access is required. After the first admin
is created, the SPA loads normally through the panel.

**Optional HA Ingress auto-login**: when running as the supervised HA
add-on you can enable `north.rest.auth.ha_ingress.enabled: true` so that
requests arriving through the HA Supervisor are automatically accepted as
an authenticated admin — no separate Loom credential is needed. This
relies on `panel_admin: true` in the add-on's `config.yaml` to ensure
the Supervisor only forwards requests from HA admins. See
[ADR 0044](adr/0044-single-port-onboarding-and-ha-ingress-auth.md)
for the full security model.

## API quickstart

```sh
# Health check
curl http://localhost:8119/api/v1/health

# List devices (Basic)
curl -u alice:change-me http://localhost:8119/api/v1/devices

# Subscribe to events via WebSocket
websocat ws://localhost:8119/api/v1/events
> {"op":"subscribe","topics":["device.*"]}

# Set a value
curl -X PUT \
  -u alice:change-me \
  -H 'Content-Type: application/json' \
  -d '{"value": true, "priority": "high"}' \
  http://localhost:8119/api/v1/devices/0001ABCD/channels/1/data-points/STATE/value
```

## MQTT topic layout

Raw plane (always on when MQTT is enabled):

A `<bucket>` segment (`values` | `master` | `calculated`) sits between
the channel and the parameter:

```
<base>/<central>/<interface>/<addr>/<channel>/<bucket>/<parameter>       state (retained)
<base>/<central>/<interface>/<addr>/<channel>/<bucket>/<parameter>/set   command
<base>/<central>/<interface>/<addr>/availability                        online|offline (retained)
<base>/<central>/hub/programs/<id>/state                                state (retained)
<base>/<central>/hub/programs/<id>/trigger                              command
<base>/<central>/hub/sysvars/<name>/state                               value (retained)
<base>/<central>/hub/sysvars/<name>/set                                 command
<base>/bridge/status                                                    LWT (retained)
```

Home Assistant Discovery plane (same state topics, separate config messages):

```
homeassistant/<component>/<node_id>/<object_id>/config
```

The `node_id` is `<central>_<address>` (lower-cased) — there is no
literal `openccu-loom` segment. Embedding `central_name` keeps
discovery IDs collision-free across CCUs.

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
- [Alarm system](alarm-user-guide.md) — set up and operate the built-in
  alarm system (areas, sensors, outputs, policies, codes, walk test).
- [Security](SECURITY.md) — security model + hardening checklist.
- [REST + WebSocket API](integrations/rest-ws.md) — the full API contract.
- [MQTT topic schema](mqtt-topic-schema.md) — topic layout reference.
- [Multi-CCU](user/multi-ccu.md) — run many CCUs from one daemon.
