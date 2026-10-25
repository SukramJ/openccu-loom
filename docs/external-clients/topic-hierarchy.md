# WebSocket Topic Hierarchy

!!! info "Who this page is for"
    Integrators writing an external WebSocket client against the daemon.
    For the request/response side of the API see
    [REST + WebSocket API](../integrations/rest-ws.md).

**Status:** Normative reference
**Owner:** OpenCCU-Loom Maintainer

## Purpose

This document is the source of truth for the WebSocket topic namespace
external clients subscribe to. It complements:

- [`assets/wsapi.json`](https://github.com/SukramJ/openccu-loom/blob/main/assets/wsapi.json)
  — per-broadcast catalogue (event name → topic pattern → payload schema reference)
- [`assets/openapi.yaml`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml)
  — payload schema definitions under `components.schemas`

## Envelope

Every WebSocket frame the daemon emits follows the same shape:

```json
{
  "seq":   1247,
  "kind":  "change",
  "topic": "device.0001ABCDE.channels.1.data_points.LEVEL",
  "type":  "datapoint.value_changed",
  "ts":    "2026-05-24T08:42:13.456789Z",
  "payload": { /* per-event-type schema */ }
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `seq` | uint64 | Monotonic daemon-assigned cursor. Store the last value seen and pass it back as `since` to resume after a reconnect. |
| `kind` | enum | `initial` \| `change` \| `refresh`. Event-family discriminator; `change` is the dominant case ([ADR 0022](../adr/0022-ws-resume-and-kind.md)). |
| `topic` | string | Present on broadcasts, absent on command replies; the field a subscription is matched against. |
| `type` | string | Discriminator for the `payload` schema; matches the `name` of the broadcast entry in `wsapi.json`. |
| `ts` | string | RFC3339Nano UTC timestamp of when the event was emitted. |
| `payload` | object | The schema named by the broadcast entry's `payload` field, under [`components.schemas`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml). |

## Subscription operations

The WS client exchanges three frame ops:

```json
{"op": "subscribe",   "topics": ["device.*", "hub.*"]}
{"op": "unsubscribe", "topics": ["hub.*"]}
{"op": "pong"}
```

The server answers `{"op":"subscribed","topics":[...]}` and
`{"op":"unsubscribed","topics":[...]}` — unconditionally, also for an
empty topic list — and emits broadcast frames whose topics match any
active subscription pattern.

### Limits

- A connection keeps at most 1024 subscribed topics. Further topics in a
  `subscribe` are ignored without an error; the subscriptions already
  held keep working.
- The outbound queue per connection holds 1000 frames. A client that
  reads too slowly loses the oldest queued events, not its connection,
  and receives `replay_lost`.

## Resume after reconnect

Every broadcast envelope carries a monotonic `seq` cursor. Clients
that store the last received `seq` can resume the stream after a
reconnect:

```json
{"op": "subscribe", "topics": ["device.*"], "since": 1247}
```

The daemon then:

1. Replays the buffered events with `seq > since` matching the
   supplied topic patterns (via the standard outbound event frames),
2. Sends a `{"op": "replay_done", "seq": N}` control frame where
   `N` is the last replayed seq (or the original `since` if nothing
   matched).

If `since` precedes the oldest event still in the buffer:

```json
{"op": "replay_lost", "oldest_seq": 901}
```

In that case the client must perform a fresh `GET /snapshot` to
resync state — relying on the event stream alone would silently
miss the events that aged out of the buffer.

`replay_lost` is also sent outside a resume. Its causes:

- `since` precedes the oldest buffered event.
- `since` is above the daemon's current sequence — a cursor from before a
  daemon restart, since the counter restarts at 0.
- The connection fell behind and the daemon dropped queued events to keep
  it open (one frame per overflow episode).
- The daemon has just published a CCU's whole model once that CCU
  finished coming up; instead of one frame per data point, every
  connection gets a single `replay_lost`.

The replay buffer ceiling is **1024 events** in the default
configuration. It is a first-class operator knob:
`north.rest.ws.replay_capacity` (`cfg:"expert"`, default `1024`) —
bursty operators on multi-CCU deployments raise it in the daemon
config; the daemon applies it to the hub via `Hub.SetReplayCapacity`
at startup.

## Auth lifecycle on long-lived connections

The bearer token / session cookie presented at the HTTP Upgrade
handshake fixes the connection's identity. That identity holds until
the client sends a `{op: "reauth"}` frame, until either side closes
the connection, or until the daemon closes it because the credential
behind it stopped being valid — see *Revocation and expiry* below.

### In-band reauth

```json
{"op": "reauth", "token": "<new-bearer-token>"}
```

The daemon re-resolves the supplied token via its configured
`TokenStore` and either:

- swaps the connection's identity and acks with
  `{"op": "reauth_ok"}` — the existing subscriptions stay in place,
  no replay is triggered;
- on unknown / empty token (or when no token store is wired):
  `{"op": "reauth_failed"}`, then the daemon closes the connection.

This is the supported path for rotating credentials on a long-lived
WS without reconnecting. Useful when an operator revokes the active
token via `DELETE /api/v1/auth/tokens/{id}` and the client wants to
present a freshly-issued one without losing its subscription state.

### Revocation and expiry

The daemon closes a connection whose credential stops being valid; it
never silently keeps dispatching on the identity captured at the
upgrade. Three events reach an open socket:

- **Token revocation.** `DELETE /api/v1/auth/tokens/{id}` and
  `DELETE /api/v1/auth/tokens/v2/{fingerprint}` both close every
  connection that authenticated with the revoked token.
- **Credential change on a principal.** A logout, a role change, an
  account deletion or a password change closes that subject's
  connections.
- **Expiry.** A session past its TTL and a bearer token past its
  `expires_at` close the connection at the deadline; a command that
  arrives in the meantime is refused with `unauthorized`.

Clients reconnect and re-present credentials — the upgrade re-runs the
full auth chain, which is the authority on what the principal may
still do. A client holding a freshly-issued token can instead send
`reauth` (above) to swap the identity without losing its subscription
state.

## Multi-central addressing

Every push payload carries an explicit `central` field naming the
CCU the event came from. Subscriptions can scope per-central via the
hierarchical topic prefix:

| Subscription | Scope |
|---|---|
| `device.*` | events from every device across every central |
| `central.home.state` | only the `home` central's lifecycle |
| `system.*.status` | status events from every central |
| `system.daemon_status` | the daemon itself starting to shut down |
| `hub.home.sysvars.*` | sysvar changes on the `home` central only |

A single openccu-loom daemon can manage multiple CCUs (see ADR-0002).
The `central.*`, `hub.*` and `system.*` topic prefixes embed the
central name, so a multi-central client distinguishes which CCU emitted
those events by topic alone. Single-central deployments can subscribe
to the broader wildcards (`device.*`, `central.*.state`) without
ambiguity — there is only one central to fan out from.

### NORMATIVE — device events scope by payload, not by topic (P3)

The **`device.*` / `device.{addr}.*` topics do NOT embed the central
name** — there is no `device.{central}.*` form. A multi-central client
therefore **cannot** restrict device value events to one CCU by topic.
The binding contract for clients:

- Subscribe broadly (`device.*`) and **filter every device event by the
  payload `central` field**. This applies to at least
  `datapoint.value_changed` and `custom_data_point.state_changed`
  (and any future `device.*` event) — each carries `central`.
- `central` equals the canonical central name
  (`central == SystemCCUEntry.name == payload.central`, see
  [ADR-0024](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0024-instance-and-ccu-identity.md) and the openapi
  `SystemCCUEntry` normative note); resolve it from a CCU `serial` via
  `GET /system/ccu`.
- Hub / lifecycle / status events, by contrast, MAY be scoped by topic
  (`hub.{central}.*`, `central.{central}.state`, `system.{central}.*`).

This pairs with the REST `?central=` filter on `/devices` and
`/snapshot` (P2): single-central is a clean story either way; multi-CCU
clients filter device events by payload and scope REST reads by query.

HA today wires one `homematicip_local` config entry per central; the
daemon's multi-central support is forward-compatible with a single
config entry covering several CCUs if HA's component grows that
capability.

## Heartbeat

The daemon sends `{"op": "ping"}` every **30 seconds** on every
connected WebSocket. Clients MUST respond with `{"op": "pong"}` within
**60 seconds** of the most recent ping, otherwise the daemon closes
the connection.

Clients behind NAT or mobile-data proxies that drop idle TCP
connections faster than 60 s should additionally emit their own
`{"op": "pong"}` frames pre-emptively — the daemon accepts
unsolicited pongs without disconnecting.

The interval and timeout are documented in `wsapi.json` under
the root-level `heartbeat` object so codegen tools can surface
them; current values are stable for the v1 API contract. Changes
to these constants would be a major-version bump per ADR-0020.

### Round-trip time

The ping carries an `echo` token: `{"op":"ping","echo":"<token>"}`. Copy
it back verbatim — `{"op":"pong","echo":"<token>"}` — and the daemon
times the round trip and reports it on the following ping as `rtt_ms`:

```json
{"op":"ping", "echo":"7", "rtt_ms":12.4}
```

It is absent on the first ping of a connection and after every pong that
carried no echo. Echoing is optional — a bare `{"op":"pong"}` stays a
valid heartbeat — and the token is opaque: its encoding is not part of
the contract. `rtt_ms` measures the link between the client and the
daemon only; the daemon's own link to the CCU is the separate
`connection_latency_ms` hub metric, and the two are routinely orders of
magnitude apart. Never add them or present them as one number.

## Topic-matching semantics

Matching is implemented by `matchTopic(pattern, topic)` in
`internal/north/rest/ws/hub.go`:

| Pattern | Matches |
|---|---|
| `"*"` | every topic |
| `"prefix.*"` | the literal `prefix` plus any topic starting with `prefix.` |
| any other string | only the exact topic |

There is no segment-level wildcard (no MQTT-style `+`). A pattern that
ends in `.*` consumes the rest of the topic hierarchy below that
prefix.

Examples:

| Subscription | Matches | Does not match |
|---|---|---|
| `device.*` | `device.0001ABCDE.channels.1.data_points.LEVEL`, `device.0001ABCDE.cdps.main` | `central.home.state` |
| `device.0001ABCDE.*` | every event for one device (DPs, CDPs, future device-scoped topics) | `device.0002XYZ.*` |
| `device.0001ABCDE.channels.1.data_points.LEVEL` | only that exact DP | anything else |
| `hub.home.*` | every sysvar + program event for the `home` central | `hub.other.*` |

## Top-level namespaces

| Prefix | Scope | Subscribe with |
|---|---|---|
| `device.{addr}.*` | per-device events (DataPoint values, CDP state) | `device.0001ABCDE.*` |
| `central.{name}.*` | CentralUnit lifecycle | `central.home.*` |
| `system.{central}.*` | aggregated system-status / health | `system.home.*` |
| `hub.{central}.*` | CCU programs + system variables | `hub.home.*` |
| `node.*` | the Matter bridge's node (go-fabric-ui's events: fabrics, the commissioning window, the endpoints, the exposure selection) | `node.*` |

`{central}` is the CCU name as defined in the daemon's config
(`cfg.Centrals[*].Name`); single-central setups can use the literal
configured name or subscribe via wildcard.

## Broadcast catalogue

The full broadcast catalogue is machine-readable in
[`assets/wsapi.json`](https://github.com/SukramJ/openccu-loom/blob/main/assets/wsapi.json) — every entry with
`"kind": "broadcast"` carries `topic` (pattern) and `payload` (schema
name in `openapi.yaml`). It is the complete, authoritative list;
`tests/contract/ws_broadcast_emitter_test.go::TestWSBroadcastsHaveProductionEmitter`
enforces that every broadcast in it has a production emitter. The
generated [WebSocket reference](../reference/websocket.md#broadcasts)
renders every broadcast with its topic pattern, payload schema and
description, grouped by category.

The `node.*` broadcasts are go-fabric-ui's events (the same six the
handler's own stream `GET /api/v1/node/stream` carries), published onto
the hub under the `node.` prefix with the stream's payload unchanged:
`node.fabric_added` and `node.fabric_removed` carry a `FabricEvent`,
`node.window_opened` and `node.window_closed` a `WindowEvent`,
`node.exposure_changed` an `ExposureEvent` with the changed items' keys,
and `node.endpoints_changed` an empty object — reload
`GET /api/v1/node/endpoints`.

`node.window_opened` **carries no credential**: only the discriminator.
Subscribing to a topic requires no role, so the passcode and the
pairing codes that commission the bridge onto a new fabric never leave
the admin-gated `POST /api/v1/node/pairing/window` and
`GET /api/v1/node/pairing` responses.

Node broadcast frames set `type` to the full dotted broadcast name —
the same convention core broadcasts use (e.g. `type:
"node.fabric_added"` on topic `node.fabric_added`,
`type: "datapoint.value_changed"` on topic
`device.{address}.channels.{channel}.data_points.{parameter}`). The
`wsapi.json` `name` field reflects the topic in both cases.

## Reserved namespaces

To avoid colliding with future broadcast additions, external clients
should treat the following prefixes as **reserved**:

- `device.*`, `central.*`, `system.*`, `hub.*`, `node.*` (currently
  in use)
- `recovery.*`, `connection.*`, `client.*`, `connectivity.*`,
  `scheduler.*`, `data.*`, `rpc.*`, `cache.*`, `health.*`,
  `reconciliation.*`, `link.*` (defined in `pkg/hmevent/catalogue.go`,
  may be surfaced over WS in the future)

Clients that publish or proxy events back through the daemon (none
exist today) must not invent topics in any reserved namespace.
