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

## Versions

The REST and WebSocket contracts carry their own version numbers,
independent of the daemon's release version:

- **REST** — `GET /api/v1/info` answers with `api_version` (the same
  value as `info.version` in `assets/openapi.yaml`) and a
  `capabilities` list. Read both at runtime instead of assuming a
  daemon release; optional features announce themselves as capability
  tokens.
- **WebSocket** — the top-level `version` field of `assets/wsapi.json`.
  The WebSocket command `system.commands` lists the commands a running
  daemon registers.

The full, generated lists are in the [REST reference](../reference/rest.md)
and the [WebSocket reference](../reference/websocket.md).

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
- `GET /api/v1/devices/{addr}/icon` — device-model icon proxy. It is **not**
  public: it answers differently for known and unknown addresses, so it sits
  behind authentication like the rest of the API; the web UI loads it with
  its session cookie
  like `/health` since it serves only non-sensitive artwork and must
  resolve from a bare `<img>` tag.
- `GET /api/v1/auth/oidc/start`, `GET /api/v1/auth/oidc/callback` —
  the OIDC redirect dance, only mounted when OIDC is configured.

See `internal/north/rest/router.go` for the authoritative route
mounting. Everything else sits behind `AuthRequire`. Supported credentials are Basic auth, session cookies (after `POST /auth/login`), API tokens, and OIDC. Two role gates apply on top of authentication: an **operator** gate (`op`) for state-changing device calls, and an **admin** gate for system, user, and config administration. See [Authentication](../admin/auth.md) and the [Security guide](../SECURITY.md) for the full model.

## Endpoint overview

Every path, with its method, summary and the minimum role it requires, is
listed in the generated [REST API reference](../reference/rest.md); the
OpenAPI file carries the parameters and response schemas.

!!! note "Multi-CCU scoping"
    Device, channel, and hub resources are scoped per CCU. The `central` dimension threads through addresses and snapshot payloads. See [Multi-CCU](../user/multi-ccu.md).

## Per-central features and the `feature_unavailable` problem

`GET /api/v1/system/ccu` reports, per central, `system_type` (`ccu` or
`openccu-lite`) and a `features` map keyed by feature (`hub.sysvars`,
`device.rename`, `system.reboot`, `taxonomy.tree`, …). Every known key
is present with `{available: bool, reason?, scope?}` — a CCU offers
everything it always did, while what an openccu-lite central offers
depends on its API token's scopes. The WebSocket broadcast
`central.features_changed` (topic `central.{name}.features`) carries
the complete set whenever it changes — a token re-paired, a scope
revoked, or the central finishing its first bring-up. See
[Connecting an openccu-lite system](../admin/openccu-lite.md#feature-keys-and-what-they-need)
for the full key-to-scope table.

An operation a central does not offer right now — its system has no
such thing, its token lacks the scope, or it has not finished bring-up
— never fails silently:

- **REST** answers `422 Unprocessable Entity`,
  `Content-Type: application/problem+json`, problem type
  `feature_unavailable`, header `X-Problem-Code: feature_unavailable`,
  and a `feature` member: `{central, key, reason, scope?}` — `scope` is
  present only when `reason` is `missing_scope`.
- **WebSocket** commands answer error code `feature_unavailable` with
  `details` carrying the same `{central, key, reason, scope?}` shape.
- **MCP** hub list tools (`list_sysvars`, `list_programs`, …) name such
  a central under an `unavailable` member instead of returning an empty
  list, so an absent feature is never mistaken for "nothing configured".

## Taxonomy: rooms, functions, and other enums

Rooms, functions and whatever else a system organises devices by are
modelled as a **taxonomy** of nested nodes, backend-neutral across a
CCU's flat rooms/functions and an openccu-lite system's nested tree.
`DeviceSummary`, channel summaries and the MQTT `device/info` payload
keep their unchanged `rooms`/`functions` **names** (the display name of
each device's directly-assigned node — a device assigned to
"Ground floor › Kitchen" is in "Kitchen") and additionally carry a
`taxonomy` array: `[{enum, path, name, parent_path?}]`.

`GET /taxonomy[?central=]` is the one surface that also shows **empty**
nodes — `{centrals: [{central, revision, writable, tree, enums:
[{id, names, nodes: [{id, path, name, icon?, children}]}]}]}` —
`writable` reflects `taxonomy.edit`, `tree` reflects `taxonomy.tree`
(false on a CCU, whose rooms/functions are flat). Node CRUD:
`POST /taxonomy/{central}/{enum}/nodes {parent_path?, name}`,
`PATCH .../nodes?path=... {name?, parent_path?, position?}`,
`DELETE .../nodes?path=...` — the matching WebSocket commands are
`taxonomy.list`, `taxonomy.node_create`, `taxonomy.node_update`,
`taxonomy.node_delete`. Creating or moving a node below another needs
`taxonomy.tree`; on a CCU only a root node of `room`/`function` can be
created, and a nested `parent_path` or a move answers
`422 feature_unavailable`.

`GET /rooms` and `GET /functions` stay name-merged across centrals as
before and additionally carry `refs: [{central, path, parent_path?}]`,
so a client can tell two same-named rooms on different systems apart.
`DevicePatchRequest`/`ChannelPatchRequest` gain `room_paths`/
`function_paths`; when given, they win over `rooms`/`functions` and are
how you pick one of two rooms that share a name. An ambiguous name
write (a plain `rooms: ["Küche"]` matching more than one node) answers
`409` with the candidate paths; an unknown name answers `422`.

`CreatedNamedResource` (the answer to `POST /rooms` and
`POST /functions`) carries an **integer** `id` on a CCU; on a system
whose nodes have no numeric id (openccu-lite) `id` is absent and `path`
names the new node instead — this is the sole breaking change in REST
API 12.0.0 (see [`CHANGELOG.md`](https://github.com/SukramJ/openccu-loom/blob/main/CHANGELOG.md)).

`ise_id` (on `DeviceSummary` and the MQTT `device/info` payload) and
the sysvar `vid` field are CCU-only and omitted on an openccu-lite
central, which has no ReGa object ids.

## WebSocket stream

Connect a WebSocket to:

```
ws://<host>:8119/api/v1/events
```

This is one multiplexed channel. The same connection carries command replies and topic broadcasts. The schema in [`assets/wsapi.json`](https://github.com/SukramJ/openccu-loom/blob/main/assets/wsapi.json) defines both invokable commands and server-pushed broadcasts in one `commands` array; run these against your checkout for the current counts rather than trusting a number here, since the catalogue grows every release: `jq '[.commands[] | select(.kind!="broadcast")] | length' assets/wsapi.json` for invokable commands, `jq '[.commands[] | select(.kind=="broadcast")] | length' assets/wsapi.json` for broadcasts.

The protocol — envelope fields, subscribe/unsubscribe, resume with
`since` and the `replay_lost` causes, connection limits, in-band `reauth`
and the heartbeat with its round-trip measurement — is specified in the
[External Client Wire Contract](../external-clients/topic-hierarchy.md);
every broadcast and command is listed in the
[WebSocket reference](../reference/websocket.md). Resume cursor and
`kind` semantics are decided in
[ADR 0022](../adr/0022-ws-resume-and-kind.md).

### Example

```javascript
const ws = new WebSocket("ws://localhost:8119/api/v1/events");

ws.onopen = () => {
  ws.send(JSON.stringify({ op: "subscribe", topics: ["device.*"], since: lastSeq }));
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
