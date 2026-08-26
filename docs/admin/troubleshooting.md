# Troubleshooting

Symptom-driven fixes for the problems operators hit most: missing CCU
events, degraded health, latency that is hard to attribute,
authentication lockouts, and lost encryption keys.

!!! info "Who this page is for"
    Operators diagnosing a running (or failing-to-run) daemon. Each
    section is symptom → likely cause → fix. For where each setting
    lives, see the [configuration reference](configuration.md).

## Where logs go

The daemon writes structured logs to **stdout**. The format follows
`logging.format` (`json`, `text`, or `text-color`) and the verbosity
follows `logging.level` (`debug`, `info`, `warn`, `error`).

=== "Docker"

    ```sh
    docker compose logs -f openccu-loom
    ```

=== "systemd"

    ```sh
    journalctl -u openccu-loom -f
    ```

To raise verbosity without editing YAML or restarting, use the
runtime log-level controls described below.

## No events from the CCU / state never updates

**Symptom.** Devices appear, but values never change; nothing is
published to MQTT or pushed over the WebSocket.

**Cause.** The CCU pushes events to the daemon's callback servers. If
the CCU cannot reach those listeners, no events arrive. The callback
host/ports must be reachable **from the CCU's network**, not just from
where the daemon runs.

**Fix.**

- The XML-RPC callback (HmIP/BidCos) listens on `callback.port`
  (default `8120`); the BIN-RPC callback (CUxD) listens on
  `callback.bin_port` (default `8129`). Both must be reachable from the
  CCU.
- Set `callback.host` to an address on a network the CCU can route to.
  Behind NAT, set `callback.public_host` to the externally reachable
  address.
- Open ports `8120/tcp` and `8129/tcp` (or your configured values)
  toward the CCU on any firewall in between.
- If you use dynamic ports (`callback.port: 0`), the OS assigns an
  ephemeral port. The daemon re-advertises the **effective** port to
  the CCU on every `init()` and reconnect, so this is supported — but
  any firewall rule must allow the configured `port_range`.
- Look for `callback.listen` / `callback.binrpc.listen` lines at boot
  to confirm the bound address, and `callback.start.failed` if a bind
  failed.

## Health shows Degraded or Unhealthy

**Symptom.** `/health` (or the SPA status) reports `degraded` or
`unhealthy`.

**Cause.** Health is aggregated per central from the interface
ping/pong window and connection checks. A `degraded` state usually
means one non-primary interface is marginal; `unhealthy` means the
primary interface (default: the `HmIP-RF`-matching one) is down.

**Fix.**

- Confirm the CCU is reachable: `username`/`password` correct, `host`
  resolvable, `tls`/`json_rpc_port` matching the CCU's web port.
- Check whether the failing interface is configured at all and whether
  the CCU has that interface enabled.
- If your primary surface is BidCos-RF rather than HmIP-RF, set
  `centrals[].primary_interface` so health aggregation scores the right
  one.
- The status states are `healthy`, `degraded`, `unhealthy`. For the
  metrics and probes behind them, see the observability admin page
  (`docs/admin/observability.md`).

## Something is slow — but which leg?

**Symptom.** The UI lags, a switch takes a moment to react, or Home
Assistant updates late. Every component reports `available`, so health
says nothing is wrong.

**Cause.** "Slow" is never one distance. Three separate legs sit between
a click and a relay, and a healthy component says nothing about any of
them:

| Leg | Reading | Where to find it |
|---|---|---|
| Your browser → the daemon | `ws.heartbeat_rtt_ms` | `GET /api/v1/diagnostics` → `health.gauges` |
| The daemon → the CCU | `connection_latency_ms` | `GET /api/v1/system/metrics`, or the `<base>/<central>/system/latency` MQTT topic |
| The daemon → the MQTT broker | `mqtt.publish_ack_ms` | `GET /api/v1/diagnostics` → `health.gauges` |

A fourth, `matter.controller_rtt_ms`, reports the distance to a paired
Matter controller (an Apple TV or HomePod acting as a Home hub, for
instance) and appears only when the Matter bridge is enabled.

**Fix.** Read all three before changing anything — they routinely differ
by an order of magnitude, and the largest one is the only one worth
acting on.

```sh
curl -s -u admin:PASSWORD http://localhost:8119/api/v1/diagnostics \
  | jq '.health.gauges | with_entries(select(.key | test("rtt|ack")))'
```

A daemon on the same LAN as its CCU typically shows well under a
millisecond to a local browser and a few milliseconds to the CCU. Reached
through Home Assistant Ingress or a public hostname, the browser leg
alone can run to tens of milliseconds while the CCU link stays perfectly
healthy — which is why these figures are never added together or shown
as one number.

!!! tip "Each reading has a companion that says whether to trust it"
    `ws.heartbeat_rtt_ms` is paired with `ws.heartbeat_rtt_samples`: the
    number of connections that have completed a timed heartbeat. A client
    that answers without echoing the heartbeat token stays connected but
    unmeasured, so a browser tab can be open while the sample count is 0.

    `mqtt.publish_ack_ms` and `matter.controller_rtt_ms` are paired with a
    cumulative `*_total`. If the total stops advancing, the median describes
    the past rather than the present. A total of **0** for MQTT is normal
    rather than broken — see the note below.

    Each is also paired with a `*_max_ms`. When the maximum sits far above
    the median, the problem is an occasional stall, not a slow link; the
    median alone hides exactly that case.

!!! warning "`mqtt.publish_ack_ms` does not cover state topics by default"
    The probe times only publishes the broker acknowledges, which means
    QoS 1 and above. State topics ship at QoS 0 by default — the broker
    never answers them — so on a stock configuration this figure describes
    the discovery plane, and `mqtt.publish_ack_total` reads 0 on a
    deployment that publishes nothing else. That is an honest "nothing
    measurable here", not a fault.

!!! note "These are not in `/metrics`"
    The Prometheus endpoint renders a different registry. The latency
    gauges are served by `GET /api/v1/diagnostics` (and by the MCP
    `get_health` tool); `connection_latency_ms` additionally reaches Home
    Assistant as a per-CCU diagnostic sensor through MQTT discovery.

**Fixing what you found.**

- **Browser leg high.** A reverse proxy, VPN or Ingress tunnel is in the
  path. Compare a tab opened directly against the REST listener with one
  opened through the proxy — the daemon reports each connection
  separately, so the two readings isolate the hop.
- **CCU leg high.** The CCU is busy or the radio is congested. Check the
  duty cycle, and see [No events from the CCU](#no-events-from-the-ccu-state-never-updates)
  if the figure is missing entirely rather than large.
- **Broker leg high.** The broker is saturated or its disk is slow. The
  reading covers the daemon's own in-flight window as well as the network,
  so a backlog of queued publishes shows up here too.

Clients can read their own leg without polling: the daemon reports it on
the WebSocket heartbeat as `rtt_ms`. See the
[REST & WebSocket integration guide](../integrations/rest-ws.md) for the
frame shape.

## First run: setting the admin password

**Symptom.** Fresh install — you cannot log in because no user exists.

**Fix.** The Svelte SPA (served on the REST listener, default `:8119`)
runs the first-run onboarding wizard that creates the initial admin
account (ADR 0045). Open the UI before configuring anything else. If the
SPA bundle is unavailable, the minimal server-rendered `/health` and
`/about` diagnostic pages still respond (`/` redirects to `/health`).

## Authentication lockout

**Symptom.** You changed auth settings and can no longer reach the API
or SPA.

**Fix.**

- Auth settings live under `north.rest.auth`. Because the database tier
  wins over YAML after first run, editing the YAML alone may not undo a
  change made through the UI.
- API tokens and users are stored in the database (tokens/users are
  secret-classed). Use the REST admin endpoints or the SPA to repair
  them; the auth admin page (`docs/admin/auth.md`) covers recovery.
- As a last resort you can re-seed configuration from a known-good
  export with `openccu-loom config import --replace` (note: user and
  token rows are intentionally skipped on import).

## Lost `secret.key`

**Symptom.** After moving the database, the daemon logs
`encrypted value but no master key available`, or secrets behave as if
empty.

**Cause.** Secret-classed values are encrypted with the `enc:v1:`
scheme. The master key comes from `OPENCCU_LOOM_SECRET_KEY` or
`<data_dir>/secret.key`. Without the original key, ciphertext cannot be
recovered.

**Fix.**

- Restore the original `secret.key` (or the original
  `OPENCCU_LOOM_SECRET_KEY` value) alongside the database — they must
  travel together. See [Backup & restore](backup.md).
- If the key is truly gone, the encrypted values are unrecoverable.
  Re-enter the affected secrets (CCU passwords, tokens, MQTT/OIDC
  secrets) and let the daemon re-seal them under a new key.

!!! tip "Avoid this entirely"
    Back up `secret.key` with every database backup, or pin a stable
    `OPENCCU_LOOM_SECRET_KEY` from your secrets manager so the key never
    depends on an ephemeral file.

## Turning up logging to diagnose an issue

You can change log verbosity at runtime through the diagnostics API
without restarting:

| Method | Path | Effect |
|---|---|---|
| `GET` | `/api/v1/diagnostics/log-level` | read the current default level |
| `PUT` | `/api/v1/diagnostics/log-level` | change the default level (admin) |
| `GET` | `/api/v1/diagnostics/log-levels` | list per-subsystem overrides |
| `PUT` | `/api/v1/diagnostics/log-levels/{path}` | set an override for a subsystem (admin) |
| `DELETE` | `/api/v1/diagnostics/log-levels/{path}` | clear an override (admin) |

Per-subsystem overrides accept a TTL (`ttl_seconds`); a positive TTL is
capped at 24 hours so a forgotten `debug` override does not run forever
(`ttl_seconds: 0` is permanent). Static boot-time overrides can also be
declared under `logging.overrides` in the config.

## Config file not picked up

**Symptom.** Your edits to `config.yaml` have no effect.

**Causes and fixes.**

- The daemon may be running on **defaults**: with no `--config`, it
  searches `$OPENCCU_LOOM_CONFIG`, `./config.yaml`,
  `~/.config/openccu-loom/config.yaml`, `/etc/openccu-loom/config.yaml`
  in that order. Check the boot log for `using discovered config ...`
  or `no config file found, running with defaults`.
- The setting may be a **database-tier** field that was already seeded.
  After first run, the database wins over YAML — edit it through the
  SPA or re-import. Bootstrap-tier fields (`data_dir`, `locale`,
  `logging`, callback host/ports, listen addresses) are always read
  from YAML/environment.
- Validate the file first: `hmcli config validate <path>`.

## See also

- [Configuration reference](configuration.md)
- [Backup & restore](backup.md)
- Observability and health probes (`docs/admin/observability.md`)
- [Multi-CCU guide](../user/multi-ccu.md)
