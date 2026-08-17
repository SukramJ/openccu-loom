# REST & WebSocket API

OpenCCU-Loom exposes a versioned REST API and a single multiplexed WebSocket stream for clients that want to read state, drive devices, and follow live changes without going through MQTT.

!!! info "Who this page is for"
    API integrators building external clients, dashboards, or automations against the daemon's HTTP surface. For the configuration of the daemon itself, see [Configuration](../admin/configuration.md).

## Base URL and transport

The REST API and the WebSocket endpoint share one HTTP listener. By default it binds `:8119`, so the base URL is:

```
http://<host>:8119/api/v1
```

The bind address is the `north.rest.listen` field in the config (default `":8119"`); see [Configuration](../admin/configuration.md). All REST paths below are relative to `/api/v1`.

## Source of truth

Both surfaces are spec-driven. The published files in the repository are authoritative — this page is an orientation, not the contract:

| Surface | Contract file |
| --- | --- |
| REST | [`assets/openapi.yaml`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| WebSocket | [`assets/wsapi.json`](https://github.com/SukramJ/openccu-loom/blob/main/assets/wsapi.json) |

When a field, status code, or schema differs between this page and those files, the files win.

## Authentication

Most endpoints require authentication. A handful of routes are
mounted outside the `AuthRequire` group by design — they either serve
before any credentials exist (first-run) or must work for a logged-out
client:

- `GET /api/v1/info`, `GET /api/v1/health` — public status probes.
- `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`,
  `GET /api/v1/auth/me` — the SPA's login lifecycle (`/auth/me` is
  the startup probe; it returns 401 with no active session).
- `GET /api/v1/setup/status`, `POST /api/v1/setup` — first-run
  onboarding, unauthenticated because no admin exists yet.
- `GET /api/v1/devices/{addr}/icon` — device-model icon proxy, public
  like `/health` since it serves only non-sensitive artwork and must
  resolve from a bare `<img>` tag.
- `GET /api/v1/auth/oidc/start`, `GET /api/v1/auth/oidc/callback` —
  the OIDC redirect dance, only mounted when OIDC is configured.

See `internal/north/rest/router.go` for the authoritative route
mounting. Everything else sits behind `AuthRequire`. Supported credentials are Basic auth, session cookies (after `POST /auth/login`), API tokens, and OIDC. Two role gates apply on top of authentication: an **operator** gate (`op`) for state-changing device calls, and an **admin** gate for system, user, and config administration. See [Authentication](../admin/auth.md) and the [Security guide](../SECURITY.md) for the full model.

## Endpoint overview

The table groups the major endpoint families. Sample paths are illustrative; the OpenAPI file lists every path, parameter, and response schema.

| Group | Sample paths | Auth |
| --- | --- | --- |
| System / info | `GET /info`, `GET /health` | public |
| Auth | `POST /auth/login`, `POST /auth/logout`, `GET /auth/me` | public (login) / authenticated |
| Tokens & users (v1) | `GET /auth/users`, `GET\|POST /auth/tokens`, `DELETE /auth/tokens/{id}` | admin |
| Config (read) | `GET /config`, `GET /config/schema`, `GET /config/effective` | authenticated / admin |
| Config admin | `GET\|PUT\|DELETE /config/sections/{section}` | admin |
| Devices & channels | `GET /devices`, `GET /devices/{addr}`, `GET /devices/{addr}/channels`, `GET /devices/{addr}/channels/{no}` | authenticated |
| Data points | `GET .../data-points`, `GET .../data-points/{param}`, `PUT .../data-points/{param}/value` | read: auth / write: operator |
| Custom & calculated DPs | `GET /devices/{addr}/cdps`, `POST /devices/{addr}/cdps/{name}/{operation}`, `GET .../calc-dps` | read: auth / write: operator |
| Paramsets | `GET\|PUT /devices/{addr}/paramsets/{key}`, `GET\|PUT /devices/{addr}/link-ps/{peer}` | read: auth / write: operator |
| Schedules (climate) | `GET\|PUT /devices/{addr}/channels/{no}/schedule`, `POST .../schedule/active-profile` | read: auth / write: operator |
| Links (direct) | `GET\|POST\|DELETE /devices/{addr}/links`, `GET .../linkable-channels` | read: auth / write: operator |
| Hub | `GET /programs`, `POST /programs/{id}/execute`, `GET\|POST /sysvars`, `GET /inbox`, `GET /alarm-messages`, `GET /service-messages` | read: auth / write: operator |
| Matter | `GET /matter/status`, `GET /matter/fabrics`, `GET /matter/setup-payload`, `GET\|PUT /matter/exposable`, `POST /matter/commissioning/window` | read: auth / admin actions |
| Visibility | `GET\|PUT /visibility/unignore`, `GET /visibility/unignore/candidates` | read: auth / write: admin |
| Backup | `GET /backups`, `POST /backups`, `GET /backups/{id}/download`, `POST /backups/{id}/restore` | read: auth / write: admin |
| Audit | `GET /audit` | authenticated |
| Diagnostics | `GET /diagnostics/logs`, `GET\|PUT /diagnostics/log-level`, `GET /diagnostics`, `GET /metrics` | mixed (admin for most) |
| Interfaces | `GET /interfaces`, `GET /interfaces/{id}`, `POST /interfaces/{id}/reconnect` | read: auth / admin reconnect |
| System admin | `GET /system/ccu`, `POST /system/restart`, `POST /install-mode` | admin |
| v2 CRUD | `GET\|POST /users`, `PATCH\|DELETE /users/{subject}`, `GET\|POST /centrals`, `PUT\|DELETE /centrals/{name}` | admin |
| Snapshot | `GET /snapshot` | authenticated |

!!! note "Multi-CCU scoping"
    Device, channel, and hub resources are scoped per CCU. The `central` dimension threads through addresses and snapshot payloads. See [Multi-CCU](../user/multi-ccu.md).

## WebSocket stream

Connect a WebSocket to:

```
ws://<host>:8119/api/v1/events
```

This is one multiplexed channel. The same connection carries command replies and topic broadcasts. The schema in [`assets/wsapi.json`](https://github.com/SukramJ/openccu-loom/blob/main/assets/wsapi.json) defines both invokable commands and server-pushed broadcasts in one `commands` array; run these against your checkout for the current counts rather than trusting a number here, since the catalogue grows every release: `jq '[.commands[] | select(.kind!="broadcast")] | length' assets/wsapi.json` for invokable commands, `jq '[.commands[] | select(.kind=="broadcast")] | length' assets/wsapi.json` for broadcasts.

### Envelope

Every frame the daemon emits uses the same envelope:

| Field | Type | Meaning |
| --- | --- | --- |
| `seq` | uint64 | Monotonic daemon-assigned cursor. Store the last value seen and pass it back as `since` to resume after a reconnect. |
| `kind` | enum | `initial` \| `change` \| `refresh`. Event-family discriminator; `change` is the dominant case. |
| `topic` | string | Routing key the client subscribed to (broadcasts only; absent on command replies). |
| `type` | string | Names the event family; matches the `name` of the matching broadcast entry. |
| `ts` | string | RFC3339Nano timestamp, UTC. |
| `payload` | object | Per-frame body; broadcast entries name the OpenAPI schema that pins its structure. |

### Subscribe and resume

Send `{"op":"subscribe", "topics":[...]}` to start receiving broadcasts. The daemon echoes `{"op":"subscribed", "topics":[...]}` as an acknowledgement.

To resume after a reconnect, append `since: N` (your last seen `seq`). The daemon replays buffered events with `seq > N` matching your topics, then sends one control frame:

- `{"op":"replay_done", "seq":N}` — replay finished; `seq` is the last replayed value.
- `{"op":"replay_lost", "oldest_seq":M}` — `since` precedes the oldest buffered event; take a fresh `GET /api/v1/snapshot` to resync.

The replay buffer holds up to ~1024 events. The daemon also sends `{"op":"ping"}` heartbeats; respond with `{"op":"pong"}` to keep the connection open. Resume cursor and `kind` semantics are specified in [ADR 0022](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0022-ws-resume-and-kind.md).

### Example

```javascript
const ws = new WebSocket("ws://localhost:8119/api/v1/events");

ws.onopen = () => {
  ws.send(JSON.stringify({ op: "subscribe", topics: ["devices/#"], since: lastSeq }));
};

ws.onmessage = (ev) => {
  const frame = JSON.parse(ev.data);
  if (frame.op === "ping") { ws.send(JSON.stringify({ op: "pong" })); return; }
  if (frame.op === "replay_lost") { /* GET /api/v1/snapshot and resync */ return; }
  if (frame.seq !== undefined) lastSeq = frame.seq;
  if (frame.kind) handleEvent(frame); // initial | change | refresh
};
```

## Related reading

- [External Client Wire Contract](../external-clients/topic-hierarchy.md) — the topic hierarchy and wire guarantees external clients depend on.
- [MCP server](../external-clients/mcp.md) — driving the daemon through the Model Context Protocol.
- [MQTT topic schema](../mqtt-topic-schema.md) — the parallel MQTT plane.
