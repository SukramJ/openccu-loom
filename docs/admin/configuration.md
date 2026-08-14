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
    database lives and how to listen. The bootstrap tier is exactly:
    `data_dir`, `logging`, `north.rest.listen` (via `listen.rest`),
    `bootstrap.allow_first_run_setup`, and `env_file`.

    Everything else is the **database tier** — including `locale`, the
    `callback.*` host/ports (they come up after the database opens),
    `centrals`, `north.mqtt`, auth, and Matter. On the **first run** the
    daemon seeds the database from your YAML once. From then on the
    database values win, and the Config UI is the place to edit them —
    editing the YAML alone will not change a setting that is already
    seeded. To re-seed from YAML you must clear the corresponding rows
    (see `openccu-loom config import --replace`) or start with a fresh
    `data_dir`.

    This means: put `data_dir`, the REST listen address, and secrets in
    YAML/environment; manage centrals, MQTT, callback ports, auth, and
    Matter through the UI once the daemon is running.

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
| `OPENCCU_LOOM_CALLBACK_PUBLIC_HOST` | `callback.public_host` | string |
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
| `callback.public_host` | string | — | `OPENCCU_LOOM_CALLBACK_PUBLIC_HOST` | no |
| `callback.max_connections` | int | `64` | — | no |
| `callback.restrict_source_ips` | bool | `false` | — | no |

`port` is the XML-RPC callback (HmIP/BidCos); `bin_port` is the
BIN-RPC callback (CUxD). A value of `0` means the OS assigns an
ephemeral port. `port_range` (e.g. `"30000-30099"`) applies to the
XML-RPC listener and **takes precedence over `port`**: when it is set
the listener binds the first free port inside the range and `port` is
not used. A malformed range is rejected when the config is saved, not
only at the next boot. `public_host` overrides the host advertised to
the CCU when the daemon is behind NAT. The effective port is
re-advertised to the CCU on every reconnect, so dynamic ports survive
restarts.

`max_connections` caps the number of concurrent connections each
callback listener accepts (`0` uses the default of `64`) — a guard
against a misbehaving or hostile peer exhausting file descriptors.
`restrict_source_ips`, when `true`, makes the listeners accept
callbacks only from the configured CCU IPs (plus loopback); leave it
`false` unless every CCU address is static and known.

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

Two further login sources delegate authentication elsewhere.
**CCU-delegated login** (ADR 0043) validates the login form against a
CCU's own user database and maps the CCU UserLevel onto a Loom role —
so a break-glass local admin always remains a fallback:

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.rest.auth.ccu.enabled` | bool (tri-state) | add-on: `true`, else `false` | — | no |
| `north.rest.auth.ccu.primary` | bool (tri-state) | `true` (CCU first, local fallback) | — | no |
| `north.rest.auth.ccu.central` | string | first configured central | — | no |
| `north.rest.auth.ccu.min_user_level` | int | `1` (guest) | — | no |
| `north.rest.auth.ccu.role_mapping` | map (level→role) | ADR 0043 defaults | — | no |

**HA Ingress passthrough** (ADR 0044) trusts a request proxied by the
Home Assistant Supervisor as an admin without a local login. It is a
deliberate, supervised-only auth bypass; a genuine token or session
always wins over it:

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.rest.auth.ha_ingress.enabled` | bool (tri-state) | supervised add-on: `true`, else `false` | — | no |
| `north.rest.auth.ha_ingress.trusted_proxy_cidr` | string | `172.30.32.0/23` (Supervisor) | — | no |
| `north.rest.auth.ha_ingress.role` | string | `admin` | — | no |

Authentication setup (Basic, Session, API tokens, OIDC, CCU-delegated
login, and HA Ingress passthrough) is covered in detail on the auth
admin page (`docs/admin/auth.md`).

### `north.ui`

`north.ui.enabled` gates only the minimal **no-JS server-rendered
diagnostic surface** — `/health` and `/about` — that stays reachable
when the Svelte SPA cannot load. Login, logout, OIDC, and the
first-run onboarding wizard all live in the Svelte SPA (ADR 0045),
served on the REST listener (`:8119` by default); there is no
server-rendered HTMX login or `/setup` template.

`north.ui.listen` has been **removed** — the diagnostic surface shares the
REST listener, so there is no separate UI bind address. Delete the key (and
the `OPENCCU_LOOM_UI_LISTEN` env var) from existing configs.

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
| `north.mqtt.payload_format` | string | — | — | no |
| `north.mqtt.sub_devices_enabled` | bool | `false` | — | no |
| `north.mqtt.retain_cleanup_window_ms` | int | `2000` | — | no |

`broker_url` is required when `enabled` is true; accepted schemes are
`tcp`, `mqtt`, `tls`, `ssl`, `mqtts` (or a bare `host:port`). For the
topic layout see the [MQTT topic schema](../mqtt-topic-schema.md).

`retain_cleanup_window_ms` is how long (500–30000 ms; `0` = 2000)
the bridge waits at boot for the broker to deliver retained messages
before evicting stale discovery/legacy topics — raise it on
high-latency brokers.

!!! note "`payload_format` is currently a no-op / reserved"
    The field is still validated (`bare` / `json`) but the bridge does
    not read it: per-DP state topics **always** carry the JSON envelope
    `{"value":..,"available":..}`. There is no primitive-scalar (`bare`)
    output mode today. Treat this key as reserved.

### `north.matter`

The native-Go Matter bridge. Disabled by default; opt-in.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.matter.enabled` | bool | `false` | — | no |
| `north.matter.listen` | string | `:5540` | — | no |
| `north.matter.prefer_ipv4` | bool | `false` | — | no |
| `north.matter.expose_secondary_channels` | bool | `false` | — | no |
| `north.matter.vendor_id` | uint16 | `0xFFF1` | — | no |
| `north.matter.product_id` | uint16 | `0x8000` | — | no |
| `north.matter.node_label` | string | `openccu-loom` | — | no |
| `north.matter.discriminator` | uint16 | `0xF00` | — | no |
| `north.matter.mdns_advertise` | string | `zeroconf` | — | no |
| `north.matter.dev_rotate_unique_ids` | bool | `false` | — | no |
| `north.matter.enable_time_sync` | bool (tri-state) | `false` | — | no |
| `north.matter.commissioning.passcode` | uint32 | — | — | **yes** |
| `north.matter.commissioning.salt` | string | — | — | **yes** |
| `north.matter.commissioning.iterations` | int | `1000` | — | no |
| `north.matter.commissioning.concurrent_pairings` | bool | `false` | — | no |
| `north.matter.commissioning.ephemeral_window` | bool | `false` | — | no |
| `north.matter.case.node_id` | uint64 | `0` (CASE off) | — | no |
| `north.matter.case.fabric_id` | uint64 | `0` | — | no |
| `north.matter.attestation.dac_path` | path | — | — | no |
| `north.matter.attestation.pai_path` | path | — | — | no |
| `north.matter.attestation.cd_path` | path | — | — | no |
| `north.matter.attestation.dac_key_path` | path | — | — | **yes** |

The default vendor/product IDs are development values from the
test block — never ship them in production. Matter pairing and
commissioning are covered on the Matter user page (`docs/user/matter.md`).

- `expose_secondary_channels` (expert, ADR 0049) controls the
  one-endpoint-per-device folding. Default `false`: a physical device
  projects a **single** Matter endpoint from its primary channel. Turn
  it on to also fan out a device's secondary actor / status channels as
  extra endpoints (they otherwise appear as duplicates in Apple/Google
  Home). Matter-only — MQTT, HA-Discovery and REST/WS always carry every
  channel.
- `enable_time_sync` (expert) mounts the `TimeSynchronization` cluster
  (0x0038) on the Root endpoint. Leave it off unless a controller needs
  it: Apple Home's HAP service mapper may **reject the bridge at
  pairing** when an unexpected RootNode cluster is present. Re-pair after
  changing it.
- `dev_rotate_unique_ids` (expert, dev/test only) mixes a per-boot salt
  into every endpoint's Matter UniqueID. Enabling it in production breaks
  accessory recognition — controllers must re-link every device after
  each restart. Keep the default `false`.
- `case.node_id` / `case.fabric_id` pin the operational node/fabric
  identity; `node_id = 0` disables CASE. `attestation.dac_path`,
  `pai_path`, and `cd_path` point at production Device Attestation
  material (DAC + PAI + CD). When all resolve, the bridge presents that
  material to commissioners; otherwise it uses an ephemeral development
  DAC that only validates under chip-tool's
  `--bypass-attestation-verifier`. The DAC private key path
  (`attestation.dac_key_path`) and the commissioning passcode/salt are
  secrets encrypted at rest.
- `mdns_advertise` selects the mDNS advertiser: `zeroconf` (default,
  publishes the commissionable records) or `noop` (in-memory only, no
  multicast — a hermetic-test opt-out; QR-scan pairing cannot work in
  this mode).

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

### `north.webhook`

A bidirectional HTTP bridge, both directions disabled by default.
**Outbound** POSTs a signed JSON payload to an operator URL on
datapoint / system-status / incident events. **Inbound** mounts REST
routes (`POST /api/v1/webhook/value`, `POST /api/v1/webhook/program`)
that external systems call to set a datapoint or trigger a program;
these are real device writes, so they carry full authorization weight.
Both directions are wired once at boot — toggling is restart-required.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.webhook.enabled` | bool | `false` | — | no |
| `north.webhook.url` | string | — | — | no |
| `north.webhook.secret` | string | — | — | **yes** |
| `north.webhook.events` | list | — (all) | — | no |
| `north.webhook.centrals` | list | — (all) | — | no |
| `north.webhook.parameter_glob` | string | — | — | no |
| `north.webhook.timeout_ms` | int | `10000` | — | no |
| `north.webhook.inbound.enabled` | bool | `false` | — | no |
| `north.webhook.inbound.token` | string | — | — | **yes** |

`secret` is the shared key for the `X-OpenCCU-Signature` HMAC-SHA256
body signature (empty = no signature header). `events` and `centrals`
are allowlists (empty = all); `parameter_glob` further filters
datapoint events by parameter name. `inbound.token` is an optional
bearer token accepted in addition to the normal REST auth chain, so a
header-only caller (e.g. a doorbell) can POST without a session.

### `north.discovery.mdns`

LAN self-advertisement so zeroconf-aware clients (e.g. Home Assistant)
can auto-discover the daemon.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.discovery.mdns.enabled` | bool | `true` | — | no |
| `north.discovery.mdns.instance_name` | string | OS hostname | — | no |

The advertised port mirrors `north.rest.listen`. Disable for
security-sensitive deployments where LAN visibility is unwanted.

### `north.discovery.ssdp`

Active SSDP / UPnP discovery of Homematic / OpenCCU central units on
the LAN (ADR 0046). When enabled the daemon periodically multicasts an
`M-SEARCH`, follows each responder's `basic_dev.cgi`, and surfaces
matching CCUs in the UI so the operator can adopt or ignore them.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `north.discovery.ssdp.enabled` | bool | `true` | — | no |
| `north.discovery.ssdp.interval` | duration | `60s` | — | no |

This is a **read-only LAN scan** — a multicast probe only; no data
about the daemon leaves the LAN, and it simply finds nothing where
multicast is unavailable (some container networks). It is on by
default; disable it if unsolicited multicast probes are unwanted.

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

`rpc_type` pins the transport and must agree with the interface name —
CUxD speaks `binrpc`, every other interface `xmlrpc`. Leave it empty
unless you want the pin; a contradicting value is refused at startup.
`remote_path` overrides the URL path the XML-RPC calls of that interface
go to (default `/RPC2`, `/groups` for VirtualDevices); set it only when
a reverse proxy re-routes the interface, and give an absolute path on
the same host. `json_rpc_port` defaults to `80` (plain) or `443`
(TLS); set it when the CCU sits behind a non-standard proxy. Set
`tls_insecure_skip_verify` only against a self-signed CCU on a trusted
network. `check_connection_interval` of `0` uses the `30s` default; a
negative value disables the background connection check.

Each central also carries an optional `behavior:` sub-block of expert
per-central toggles that shape how devices and hub entities are
modelled. All are hot-reloadable and default to sensible values:

| Key | Type | Default | Meaning |
|---|---|---|---|
| `behavior.light_last_brightness` | bool | `true` | Restore last brightness on turn-on (vs. full) |
| `behavior.use_group_channel_for_cover_state` | bool | `true` | Report cover position from the group channel |
| `behavior.enable_sysvar_scan` | bool | `true` | Fetch system variables as hub entities |
| `behavior.enable_program_scan` | bool | `true` | Fetch programs as hub entities |
| `behavior.include_internal_sysvars` | bool | `true` | Surface CCU-internal system variables |
| `behavior.include_internal_programs` | bool | `false` | Surface CCU-internal programs |
| `behavior.sysvar_markers` | list | — | Marker tokens steering how sysvars arrive (`HAHM`/`HX`/`INTERNAL`) |
| `behavior.program_markers` | list | — | Marker tokens steering how programs arrive (`HX`/`INTERNAL`) |
| `behavior.sysvar_scan_interval` | duration | `30s` | Per-central sysvar-refresh cadence; `0` selects the default, values below `3s` are rejected |
| `behavior.enable_device_firmware_check` | bool | `true` | Expose per-device firmware-update entities |
| `behavior.delay_new_device_creation` | bool | `false` | Defer a newly-paired device until its description is complete |

### `persistence`

Tuning for the SQLite-backed caches. The **VALUES cache**
(`persistence.values_cache`, ADR 0019) persists wire-DP values so they
survive a restart; it is **on by default**. The **measurement-history
recorder** (`persistence.history`, ADR 0040) is **opt-in**: enabling it
opens a dedicated `history.db`, records numeric VALUES samples, drives
the SPA charts (`GET /history`), and can optionally push each sample to
an external time-series store.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `persistence.values_cache.enabled` | bool | `true` | — | no |
| `persistence.values_cache.flush_interval` | duration | `60s` | — | no |
| `persistence.values_cache.disabled_centrals` | list | — | — | no |
| `persistence.history.enabled` | bool | `false` | — | no |
| `persistence.history.retention` | duration | `720h` (30 d) | — | no |
| `persistence.history.retention_hourly` | duration | `13 months` | — | no |
| `persistence.history.retention_daily` | duration | `0` (keep forever) | — | no |
| `persistence.history.flush_interval` | duration | `5s` | — | no |
| `persistence.history.include` | list | — (all numeric) | — | no |
| `persistence.history.exclude` | list | — | — | no |
| `persistence.history.disabled_centrals` | list | — | — | no |
| `persistence.history.energy_price_per_kwh` | number | `0` (no costs) | — | no |
| `persistence.history.energy_currency` | string | `€` | — | no |
| `persistence.history.export.enabled` | bool | `false` | — | no |
| `persistence.history.export.kind` | string | `influxdb` | — | no |
| `persistence.history.export.endpoint` | string | — | — | no |
| `persistence.history.export.org` | string | — | — | no |
| `persistence.history.export.bucket` | string | — | — | no |
| `persistence.history.export.token_env` | string | — | — | no |

### Sysvar and program markers

The marker lists do **not** decide whether a system variable or program is
imported — everything the CCU exposes is imported. They decide how an entry
arrives in a consumer such as Home Assistant:

| Marker | Effect |
|---|---|
| `HAHM` | Makes a **system variable** writable (switch / select / number / text instead of a read-only sensor). A program has no value to write, so the CCU editor does not offer this marker there. |
| `HX` | Free marker for your own filtering. |
| `INTERNAL` | Additionally includes the CCU's internal entries. This matters more than it sounds: the CCU flags most ordinary user programs as internal, so without it a program list can look almost empty. |

An entry matching any configured marker arrives **enabled**; every other
entry arrives **disabled** and is switched on per entity. With no markers
configured, everything is imported and everything arrives disabled.

The reference stack also recognises an `MQTT` marker, which steers a hand-off
this daemon does not need — its MQTT bridge publishes every hub entity
regardless — so the CCU editor does not offer it. A stored value is dropped
when the editor opens; the token is still stripped from the CCU description
so it never shows up in an entity name.

`include_internal_sysvars` / `include_internal_programs` are the equivalent
switch for operators who configure no markers at all; either they or the
`INTERNAL` marker suffice.

`history.retention` bounds raw samples (clamped up to a 1h floor);
`retention_hourly` and `retention_daily` bound the two rollup tiers.
`include`/`exclude` are parameter-name globs (`exclude` wins). The
export token is never inline — the daemon reads it from the env var
named by `export.token_env`.

`energy_price_per_kwh` makes the energy view show costs next to
consumption. Leave it at `0` to show no costs at all — a tariff of zero
would render every amount as `0.00`, which reads as "free" rather than
"not configured". `energy_currency` only labels those amounts; nothing
is ever converted.

### `reliability`

Overrides for the reliability stack (retry / throttle). Both fields
default to `0`, meaning "use the compiled-in behaviour"; set them only
to pin a specific timing (e.g. on heavily-loaded BidCos-RF).

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `reliability.command_retry_initial_delay` | duration | `0` (250 ms default) | — | no |
| `reliability.command_throttle_inter_command_delay` | duration | `0` (no pacing) | — | no |

### `ccu_data`

Optional filesystem overrides for the openccu-data metadata archives
(parameter translations and easymode profiles). Both are empty by
default — the daemon uses the embedded archives and falls back to raw
parameter / model names if a path is set but cannot be read.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `ccu_data.translations_path` | path | — | — | no |
| `ccu_data.easymode_path` | path | — | — | no |

### `backup`

Automatic, scheduled CCU backups. Off by default (a backup touches the
CCU and produces files, so it is opt-in); manual backups via the
REST/UI surface work regardless. Both fields are hot-reloaded.

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `backup.schedule` | duration | `0` (disabled) | — | no |
| `backup.keep_last` | int | `0` (keep all) | — | no |

`keep_last` prunes the oldest scheduled backups per central beyond the
given count after each successful run.

### `security`

| Key | Type | Default | Env | Secret? |
|---|---|---|---|---|
| `security.allow_plaintext_secrets` | bool | `false` | — | no |

When `false` (default and recommended) the daemon refuses to persist a
central's password in cleartext — it must be encryptable with the
master key (see [Secrets](#secrets)). Set it `true` only in a
throwaway/dev environment where no at-rest key is available; the
password is then stored in plaintext in the database.

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
