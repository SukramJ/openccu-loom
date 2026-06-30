# Observability

Scrape metrics, probe health, and tune logging for a running
OpenCCU-Loom daemon.

!!! info "Who this page is for"
    Administrators wiring the daemon into Prometheus, an uptime monitor,
    or a log pipeline. For triage of specific failures, see the
    troubleshooting guide (`docs/admin/troubleshooting.md`).

## Metrics

The daemon exposes a Prometheus endpoint in the standard text exposition
format.

| Property | Value |
|---|---|
| Endpoint | `GET /api/v1/metrics` |
| Content type | `text/plain; version=0.0.4; charset=utf-8` |
| Auth | **Required** — the endpoint sits inside the authenticated route group. |

!!! warning "The metrics endpoint is authenticated"
    Unlike many Prometheus exporters, `/api/v1/metrics` is **not**
    anonymous — it is mounted inside the daemon's auth-required group.
    Your scrape job must present credentials.

### Prometheus scrape config

Use an API token (see the [authentication guide](auth.md)) so the
scraper carries a bearer credential, which also bypasses CSRF:

```yaml
scrape_configs:
  - job_name: openccu-loom
    metrics_path: /api/v1/metrics
    scheme: https            # if a TLS proxy fronts the daemon
    authorization:
      type: Bearer
      credentials: "<api-token>"
    static_configs:
      - targets: ["loom.example:8119"]   # north.rest.listen, default :8119
```

Basic auth works too, via Prometheus's `basic_auth` block, if you prefer
a Basic user over a token.

## Health

There are two health surfaces: a JSON endpoint on the REST API and a
server-rendered page on the UI port.

### REST health endpoint

| Property | Value |
|---|---|
| Endpoint | `GET /api/v1/health` |
| Auth | None — reachable without credentials so a load balancer can probe it. |
| Body | `{"status": "...", "components": [{ "name", "status", "note", "recorded_at" }]}` |
| HTTP code | `200` normally, `503` when the service is genuinely unavailable. |

The top-level `status` is a **service-availability collapse**, not the
raw worst component: a single south-bound interface or the MQTT bridge
going down only *degrades* the daemon — the REST/UI surface keeps
serving. Only a fatal dependency (the `sqlite` persistence layer or the
`central` coordinator) being unhealthy, or **every** interface being
down at once, maps to `503`.

### Component statuses

Each component reports one of four statuses:

| Status | Meaning |
|---|---|
| `healthy` | Last sample was good. |
| `degraded` | A single recent failure after a healthy run (flap-damped). |
| `unhealthy` | Repeated failure / hard fault. |
| `unknown` | Never reported yet, or its last sample is **stale** (older than ~90 s) — a component that has gone silent does not stay green. |

### UI health page

The bootstrap surface on the REST port (default `:8119`) serves a
server-rendered `/health` page (with `/` redirecting to it). It is the
SPA-down fallback for diagnosing the daemon when the JavaScript bundle
will not load; `/about` on the same port shows the version and license.

### Using health in a probe

Point your load-balancer or uptime monitor at the REST endpoint and
treat the HTTP code as the verdict:

```bash
# Healthy/degraded → 200; service down → 503
curl -fsS https://loom.example/api/v1/health
```

!!! tip "Probe code vs. body"
    For a binary up/down check, rely on the HTTP status (`503` = drain
    this instance). For a dashboard, parse the `components` array and
    render per-component status — a `degraded` body still returns `200`,
    which is intentional.

## Logging

Logging is structured (`log/slog`) and configured under the top-level
`logging` block.

```yaml
logging:
  level: info        # debug | info | warn | error (default: info)
  format: json       # json | text | text-color (default: json)
  overrides:         # optional per-subsystem static overrides
    openccu-loom.client.transport.xmlrpc: debug
```

- `level` sets the global default; invalid values are rejected at config
  load.
- `format` selects `json` (default), plain `text`, or colourised
  `text-color`.
- `overrides` maps a dot-separated subsystem path to a level. Overrides
  resolve hierarchically — an override on `openccu-loom.client` applies
  to every descendant unless that descendant has its own override.

### Dynamic per-subsystem log levels

You can change levels at runtime without editing YAML or restarting,
through admin-gated diagnostics endpoints:

| Method & path | Purpose |
|---|---|
| `GET /api/v1/diagnostics/log-levels` | List the default level and every active override. |
| `PUT /api/v1/diagnostics/log-levels/{path}` | Install/replace an override for a subsystem path. |
| `DELETE /api/v1/diagnostics/log-levels/{path}` | Remove an override (idempotent). |

The `PUT` body carries the level and an optional TTL:

```bash
curl -u admin:… -X PUT \
  https://loom.example/api/v1/diagnostics/log-levels/openccu-loom.client \
  -H 'Content-Type: application/json' \
  -d '{"level":"debug","ttl_seconds":600}'
```

- `ttl_seconds: 0` (or omitted) makes the override permanent (until
  reset or restart).
- A positive TTL auto-expires; the endpoint caps user-supplied TTLs at
  **24 hours** so a forgotten `debug` override does not run forever.

!!! tip "Use a TTL when debugging live"
    Set a bounded `ttl_seconds` when you raise verbosity on a busy
    subsystem in production — it reverts itself even if you forget.

### Redaction of secrets in logs

A redaction handler masks sensitive attribute values with
`***REDACTED***` before they reach any log sink. Matching is
case-insensitive and substring-based, so nested groups
(`oidc.client_secret`) and header-style keys (`X-Api-Key`) are caught.
The masked key set includes `password`, `passwd`, `secret`, `token`,
`api_key` / `api-key` / `apikey`, `authorization`, `auth_header`,
`cookie`, `set_cookie`, `session_id` / `sessionid`, `client_secret`,
`refresh_token`, `access_token`, `id_token`, `bearer`, and
`private_key`.

!!! note "Redaction is shallow on opaque values"
    Top-level attributes and `slog.Group` members are inspected;
    arbitrary map/struct values that arrive as a single opaque value are
    not introspected. Expose individual fields via `slog.Group` or
    `slog.Attr` if you need them redacted.

## Related

- [Security guide](../SECURITY.md) — auth, secrets at rest, TLS posture.
- [Authentication & users](auth.md) — minting the API token your scraper
  needs.
- Troubleshooting — failure triage lives in the admin troubleshooting
  guide (`docs/admin/troubleshooting.md`).
