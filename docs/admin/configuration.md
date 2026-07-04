# Configuration Reference

The authoritative reference for every setting OpenCCU-Loom reads at
startup: where configuration comes from, how the bootstrap and
database tiers interact, and the keys, types, defaults, and
environment overrides for each block.

!!! info "Who this page is for"
    Operators who run the daemon and need to know exactly which key
    controls what. If you are setting up for the first time, start with
    the [getting-started guide](../getting-started.md) and come back
    here for the details.

## How configuration is layered

OpenCCU-Loom resolves configuration from three sources, applied in
this order (later wins):

1. **Compiled-in defaults** — every field has a safe default.
2. **YAML config file** (`config.yaml`) — read at startup.
3. **Environment variables** (`OPENCCU_LOOM_*`) — a curated overlay.

On top of that, most settings are also editable at runtime through the
web Config UI; those edits are stored in the SQLite database and win
over the YAML on the next start.

!!! note "Bootstrap tier vs. database tier"
    A small **bootstrap tier** is read directly from YAML *before* the
    database opens — it has to be, because it tells the daemon where the
    database lives and how to listen. The bootstrap tier is:
    `data_dir`, `locale`, `logging`, the callback host/ports, and the
    REST/UI listen addresses.

    Everything else is the **database tier**. On the **first run** the
    daemon seeds the database from your YAML once. From then on the
    database values win, and the Config UI is the place to edit them —
    editing the YAML alone will not change a setting that is already
    seeded. To re-seed from YAML you must clear the corresponding rows
    (see `openccu-loom config import --replace`) or start with a fresh
    `data_dir`.

    This means: put `data_dir`, listen addresses, and secrets in
    YAML/environment; manage centrals, MQTT, auth, and Matter through
    the UI once the daemon is running.

## Config file discovery

When you start the daemon with `openccu-loom run` and **omit**
`--config`, it probes these locations in order and uses the first file
that exists:

| Order | Location | Notes |
|---|---|---|
| 1 | `$OPENCCU_LOOM_CONFIG` | explicit operator override |
| 2 | `./config.yaml` | current working directory (in Docker, `/app/config.yaml`) |
| 3 | `$XDG_CONFIG_HOME/openccu-loom/config.yaml` or `~/.config/openccu-loom/config.yaml` | per-user |
| 4 | `/etc/openccu-loom/config.yaml` | system-wide |

If none exists the daemon runs on built-in defaults and logs
`no config file found, running with defaults`. An explicit
`--config <path>` always wins over discovery.

```sh
openccu-loom run --config /etc/openccu-loom/config.yaml
```

## A minimal config.yaml

```yaml
data_dir: /var/lib/openccu-loom
locale: en

centrals:
  - name: home
    host: 172.18.4.29
    username: Admin
    password: "your-ccu-password"
    interfaces: [HmIP-RF, BidCos-RF]

north:
  rest:
    listen: ":8119"
```

For the complete annotated set of every key, see
[`example.config.full.yaml`](https://github.com/SukramJ/openccu-loom/blob/main/example.config.full.yaml)
in the repository, and
[`example.config.yaml`](https://github.com/SukramJ/openccu-loom/blob/main/example.config.yaml)
for a shorter starter file.

## Environment overlays

A narrow set of environment variables overrides the matching YAML
field. They are useful for container deployments where the image
carries the YAML and a handful of values are per-deployment. Variables
that are unset leave the YAML value intact.

| Variable | Overrides | Type |
|---|---|---|
| `OPENCCU_LOOM_CONFIG` | config-file path (discovery) | path |
| `OPENCCU_LOOM_LOCALE` | `locale` | string |
| `OPENCCU_LOOM_DATA_DIR` | `data_dir` | path |
| `OPENCCU_LOOM_LOG_LEVEL` | `logging.level` | string |
| `OPENCCU_LOOM_LOG_FORMAT` | `logging.format` | string |
| `OPENCCU_LOOM_CALLBACK_HOST` | `callback.host` | string |
| `OPENCCU_LOOM_CALLBACK_PORT` | `callback.port` | int |
| `OPENCCU_LOOM_CALLBACK_BIN_PORT` | `callback.bin_port` | int |
| `OPENCCU_LOOM_REST_LISTEN` | `north.rest.listen` | string |
| `OPENCCU_LOOM_REST_OPENAPI_VALIDATE` | OpenAPI request validation | bool |
| `OPENCCU_LOOM_REST_OPENAPI_SPEC_PATH` | external OpenAPI spec path | path |
| `OPENCCU_LOOM_MQTT_BROKER_URL` | `north.mqtt.broker_url` | string |
| `OPENCCU_LOOM_SECRET_KEY` | at-rest master key (see [Secrets](#secrets)) | base64 |
| `OPENCCU_LOOM_MQTT_PASSWORD` | MQTT broker password (runtime only) | string |
| `OPENCCU_LOOM_OIDC_CLIENT_SECRET` | OIDC client secret (runtime only) | string |

The daemon also reads an optional `.env` file from the working
directory (or the path named by `env_file:` in the config) at startup.
Real process-environment values always win over `.env` entries. See
[`example.env`](https://github.com/SukramJ/openccu-loom/blob/main/example.env)
for the full annotated list.

=== "Docker"

    ```yaml
    services:
      openccu-loom:
        image: ghcr.io/sukramj/openccu-loom:latest
        environment:
          OPENCCU_LOOM_DATA_DIR: /data
          OPENCCU_LOOM_CALLBACK_HOST: 192.0.2.10
        volumes:
          - openccu-loom-data:/data
    ```

=== "Binary"

    ```sh
    export OPENCCU_LOOM_DATA_DIR=/var/lib/openccu-loom
    export OPENCCU_LOOM_CALLBACK_HOST=192.0.2.10
    openccu-loom run --config /etc/openccu-loom/config.yaml
    ```

## Secrets

Secret-classed fields are encrypted at rest in the database with
AES-256-GCM and stored with the prefix `enc:v1:`. The master key is
resolved hybrid:

1. `OPENCCU_LOOM_SECRET_KEY` — a base64-encoded 32-byte key. Wins when
   present. Generate one with `openssl rand -base64 32`.
2. Otherwise an auto-generated key file at `<data_dir>/secret.key`
   (created with mode `0600` on first start).

If no key can be resolved or written, the daemon logs a warning and
stores secret values in plaintext rather than refusing to boot.

!!! warning "Back up `secret.key` with your database"
    Without the master key, encrypted secrets in a restored database
    cannot be decrypted. See [Backup & restore](backup.md) and the
    [security guide](../SECURITY.md).

Fields treated as secrets include `centrals[].password`,
`north.rest.auth.users`, `north.rest.auth.tokens`,
`north.rest.auth.oidc.client_secret`, `north.mqtt.password`, and the
Matter commissioning passcode/salt and DAC key path.

---

## Configuration blocks

Defaults shown are the compiled-in values. Keys marked **secret** are
encrypted at rest. The **env** column lists the overlay variable when
one exists.

### `locale`

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `locale` | string | `en` | `OPENCCU_LOOM_LOCALE` | no |

UI/label language. Catalogues ship for `en` and `de`.

### `data_dir`

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `data_dir` | path | `./var` | `OPENCCU_LOOM_DATA_DIR` | no |

Directory for the SQLite database (`<data_dir>/openccu-loom.db`), the
auto-generated `secret.key`, and other on-disk state. This is a
bootstrap-tier field — set it in YAML/environment.

### `logging`

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `logging.level` | string | `info` | `OPENCCU_LOOM_LOG_LEVEL` | no |
| `logging.format` | string | `json` | `OPENCCU_LOOM_LOG_FORMAT` | no |
| `logging.overrides` | map | — | — | no |

`level` is one of `debug`, `info`, `warn`, `error`. `format` is one of
`json`, `text`, `text-color`. `overrides` maps a dot-separated
subsystem path to a level, for static boot-time overrides; runtime
overrides are installed (with a TTL) via the diagnostics API.

### `callback`

The XML-RPC and BIN-RPC callback servers that receive push events from
the CCU. The host/ports here must be reachable **from the CCU**.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `callback.host` | string | `0.0.0.0` | `OPENCCU_LOOM_CALLBACK_HOST` | no |
| `callback.port` | int | `8120` | `OPENCCU_LOOM_CALLBACK_PORT` | no |
| `callback.bin_port` | int | `8129` | `OPENCCU_LOOM_CALLBACK_BIN_PORT` | no |
| `callback.port_range` | string | — | — | no |
| `callback.public_host` | string | — | — | no |

`port` is the XML-RPC callback (HmIP/BidCos); `bin_port` is the
BIN-RPC callback (CUxD). A value of `0` means the OS assigns an
ephemeral port. `port_range` (e.g. `"30000-30099"`) bounds dynamic
assignment. `public_host` overrides the host advertised to the CCU
when the daemon is behind NAT. The effective port is re-advertised to
the CCU on every reconnect, so dynamic ports survive restarts.

### `north.rest` and authentication

The REST + WebSocket server. This is the API surface and the backend
for the SPA.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.rest.enabled` | bool | `true` | — | no |
| `north.rest.listen` | string | `:8119` | `OPENCCU_LOOM_REST_LISTEN` | no |
| `north.rest.cors` | list | — | — | no |
| `north.rest.csrf_enabled` | bool | `true` | — | no |
| `north.rest.csrf_secure` | bool | `false` | — | no |
| `north.rest.openapi_validate` | bool | `true` | `OPENCCU_LOOM_REST_OPENAPI_VALIDATE` | no |
| `north.rest.openapi_spec_path` | path | — | `OPENCCU_LOOM_REST_OPENAPI_SPEC_PATH` | no |
| `north.rest.ws.replay_capacity` | int | `1024` | — | no |
| `north.rest.rate_limit.enabled` | bool | `false` | — | no |
| `north.rest.rate_limit.requests_per_second` | float | `10` | — | no |
| `north.rest.rate_limit.burst` | int | `30` | — | no |

Authentication lives under `north.rest.auth`:

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.rest.auth.basic_enabled` | bool (tri-state) | `true` | — | no |
| `north.rest.auth.bearer_enabled` | bool (tri-state) | `true` | — | no |
| `north.rest.auth.users` | map (subject→hash) | — | — | **yes** |
| `north.rest.auth.tokens` | map (token→role) | — | — | **yes** |
| `north.rest.auth.oidc.enabled` | bool | `false` | — | no |
| `north.rest.auth.oidc.issuer` | string | — | — | no |
| `north.rest.auth.oidc.client_id` | string | — | — | no |
| `north.rest.auth.oidc.client_secret` | string | — | `OPENCCU_LOOM_OIDC_CLIENT_SECRET` | **yes** |
| `north.rest.auth.oidc.redirect_url` | string | — | — | no |
| `north.rest.auth.oidc.role_claim` | string | — | — | no |

Authentication setup (Basic, Session, API tokens, OIDC) is covered in
detail on the auth admin page (`docs/admin/auth.md`).

### `north.ui`

The HTMX bootstrap surface (login, first-run `/setup` wizard,
server-rendered `/health` and `/about`). The Svelte SPA and the
bootstrap surface are both served on the REST listener (`:8119`
by default) since 0.14.0 — there is no separate UI listener.

`north.ui.listen` has been **removed** — the bootstrap UI shares the REST
listener, so there is no separate UI bind address. Delete the key (and the
`OPENCCU_LOOM_UI_LISTEN` env var) from existing configs.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.ui.enabled` | bool | `true` | — | no |

### `north.mqtt`

The MQTT bridge (Home Assistant Discovery and/or raw topic planes).

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.mqtt.enabled` | bool | `false` | — | no |
| `north.mqtt.broker_url` | string | — | `OPENCCU_LOOM_MQTT_BROKER_URL` | no |
| `north.mqtt.client_id` | string | — | — | no |
| `north.mqtt.username` | string | — | — | no |
| `north.mqtt.password` | string | — | `OPENCCU_LOOM_MQTT_PASSWORD` | **yes** |
| `north.mqtt.topic_base` | string | `openccu-loom` | — | no |
| `north.mqtt.raw_enabled` | bool | `false` | — | no |
| `north.mqtt.discovery_enabled` | bool | `false` | — | no |
| `north.mqtt.protocol_version` | string | `"5"` | — | no |
| `north.mqtt.payload_format` | string | `bare` | — | no |
| `north.mqtt.sub_devices_enabled` | bool | `false` | — | no |

`broker_url` is required when `enabled` is true; accepted schemes are
`tcp`, `mqtt`, `tls`, `ssl`, `mqtts` (or a bare `host:port`).
`payload_format` is `bare` (primitive scalars) or `json` (wraps state
in `{"value":..,"available":..}`). For the topic layout see the
[MQTT topic schema](../mqtt-topic-schema.md).

### `north.matter`

The native-Go Matter bridge. Disabled by default; opt-in.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.matter.enabled` | bool | `false` | — | no |
| `north.matter.listen` | string | `:5540` | — | no |
| `north.matter.prefer_ipv4` | bool | `false` | — | no |
| `north.matter.vendor_id` | uint16 | `0xFFF1` | — | no |
| `north.matter.product_id` | uint16 | `0x8000` | — | no |
| `north.matter.node_label` | string | `openccu-loom` | — | no |
| `north.matter.discriminator` | uint16 | `0xF00` | — | no |
| `north.matter.mdns_advertise` | string | `noop` | — | no |
| `north.matter.commissioning.passcode` | uint32 | — | — | **yes** |
| `north.matter.commissioning.salt` | string | — | — | **yes** |
| `north.matter.commissioning.iterations` | int | `1000` | — | no |
| `north.matter.commissioning.ephemeral_window` | bool | `false` | — | no |
| `north.matter.attestation.dac_key_path` | path | — | — | **yes** |

The default vendor/product IDs are development values from the
test block — never ship them in production. Matter pairing and
commissioning are covered on the Matter user page (`docs/user/matter.md`).

### `north.mcp`

The Model Context Protocol server for LLM agents. Disabled by default;
read-only even when enabled until `allow_writes` is also set.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.mcp.enabled` | bool | `false` | — | no |
| `north.mcp.allow_writes` | bool | `false` | — | no |
| `north.mcp.path` | string | `/mcp` | — | no |

The MCP transport is mounted on the existing REST listener. See the
[MCP client guide](../external-clients/mcp.md).

### `north.discovery.mdns`

LAN self-advertisement so zeroconf-aware clients (e.g. Home Assistant)
can auto-discover the daemon.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.discovery.mdns.enabled` | bool | `true` | — | no |
| `north.discovery.mdns.instance_name` | string | OS hostname | — | no |

The advertised port mirrors `north.rest.listen`. Disable for
security-sensitive deployments where LAN visibility is unwanted.

### `centrals[]`

One entry per configured CCU. OpenCCU-Loom is multi-CCU from day one;
see the [multi-CCU guide](../user/multi-ccu.md).

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `name` | string | — (**required**, unique) | — | no |
| `host` | string | — (**required**) | — | no |
| `interfaces` | list | — (**required**, ≥1) | — | no |
| `username` | string | — | — | no |
| `password` | string | — | — | **yes** |
| `port` | int | — | — | no |
| `ports` | map (iface→port) | — | — | no |
| `json_rpc_port` | int | `80`/`443` | — | no |
| `tls` | bool | `false` | — | no |
| `tls_insecure_skip_verify` | bool | `false` | — | no |
| `primary_interface` | string | (HmIP-RF heuristic) | — | no |
| `visibility.un_ignore` | list | — | — | no |
| `check_connection_interval` | duration | `30s` | — | no |

`interfaces` accepts a short list of names or a long form with per-
interface overrides:

```yaml
centrals:
  - name: home
    host: 172.18.4.29
    username: Admin
    password: "your-ccu-password"
    interfaces:
      - HmIP-RF
      - name: BidCos-RF
        port: 2001
        rpc_type: xmlrpc
      - name: CUxD          # CUxD is reached over BIN-RPC
```

`rpc_type` is `xmlrpc` or `binrpc` (empty derives it from the
interface name). `json_rpc_port` defaults to `80` (plain) or `443`
(TLS); set it when the CCU sits behind a non-standard proxy. Set
`tls_insecure_skip_verify` only against a self-signed CCU on a trusted
network. `check_connection_interval` of `0` uses the `30s` default; a
negative value disables the background connection check.

## Validating a config file

Use the admin CLI to check a file before deploying it:

```sh
hmcli config validate /etc/openccu-loom/config.yaml
```

## See also

- [Backup & restore](backup.md) — and why `secret.key` must travel with the database.
- [Troubleshooting](troubleshooting.md) — callback reachability, lockouts, lost keys.
- [Security guide](../SECURITY.md) — at-rest encryption and hardening.
- [Multi-CCU guide](../user/multi-ccu.md) — running several CCUs from one daemon.
- REST + WebSocket API reference (`docs/integrations/rest-ws.md`).
