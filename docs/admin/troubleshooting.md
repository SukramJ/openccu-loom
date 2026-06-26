# Troubleshooting

Symptom-driven fixes for the problems operators hit most: missing CCU
events, degraded health, authentication lockouts, and lost encryption
keys.

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

## First run: setting the admin password

**Symptom.** Fresh install — you cannot log in because no user exists.

**Fix.** The HTMX bootstrap surface (served on the REST listener, default `:8080`) exposes a
first-run `/setup` wizard that creates the initial admin account.
Visit it before configuring anything else. `/` redirects to `/health`,
and `/health` is server-rendered so it works even when the SPA bundle
is unavailable.

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
