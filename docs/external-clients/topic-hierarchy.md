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
  "topic": "device.0001ABCDE.channels.1.data_points.LEVEL",
  "type":  "datapoint.value_changed",
  "ts":    "2026-05-24T08:42:13.456789Z",
  "payload": { /* per-event-type schema */ }
}
```

- `topic` — present on broadcasts; absent on command replies. The
  field a client matched a subscription against.
- `type` — discriminator for the `payload` schema. Matches the `name`
  field of the corresponding `broadcast` entry in `wsapi.json`.
- `ts` — RFC3339Nano UTC timestamp of when the event was emitted.
- `payload` — see the schema referenced under
  [`components.schemas`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) by the broadcast
  entry's `payload` field.

## Subscription operations

The WS client exchanges three frame ops:

```json
{"op": "subscribe",   "topics": ["device.*", "hub.*"]}
{"op": "unsubscribe", "topics": ["hub.*"]}
{"op": "pong"}
```

The server responds with `subscribe`/`unsubscribe` acks (omitted here)
and emits broadcast frames whose topics match any active subscription
pattern.

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
| `matter.*` | Matter bridge lifecycle (commissioning, fabrics, allowlist) | `matter.*` |

`{central}` is the CCU name as defined in the daemon's config
(`cfg.Centrals[*].Name`); single-central setups can use the literal
configured name or subscribe via wildcard.

## Broadcast catalogue

The full broadcast catalogue is machine-readable in
[`assets/wsapi.json`](https://github.com/SukramJ/openccu-loom/blob/main/assets/wsapi.json) — every entry with
`"kind": "broadcast"` carries `topic` (pattern) and `payload` (schema
name in `openapi.yaml`). `assets/wsapi.json` holds 23 broadcast
entries (17 non-Matter + 6 Matter); the table below is the
human-readable view of all 17 non-Matter broadcasts emitted today —
`tests/contract/ws_broadcast_emitter_test.go::TestWSBroadcastsHaveProductionEmitter`
enforces that every one of them has a production emitter, so this
list does not drift from reality.

### Core broadcasts (daemon-emitted, openapi-described)

| Type (`name`) | Topic pattern | Payload schema |
|---|---|---|
| `datapoint.value_changed` | `device.{address}.channels.{channel}.data_points.{parameter}` | [`DataPointValueChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `datapoint.optimistic_rolled_back` | `device.{address}.channels.{channel}.data_points.{parameter}` | [`OptimisticRollbackPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `custom_data_point.state_changed` | `device.{address}.cdps.{name}` | [`CustomDataPointStateChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `device.trigger` | `device.{address}.channels.{channel}.trigger` | [`DeviceTriggerPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `device.created` | `device.{address}.lifecycle` | [`DeviceCreatedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `device.removed` | `device.{address}.lifecycle` | [`DeviceRemovedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `device.availability_changed` | `device.{address}.lifecycle` | [`DeviceAvailabilityChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `central.state_changed` | `central.{name}.state` | [`CentralStateChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `system.status_changed` | `system.{central}.status` | [`SystemStatusChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `hub.sysvar_changed` | `hub.{central}.sysvars.{name}` | [`SysvarChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `hub.program_executed` | `hub.{central}.programs.{id}` | [`ProgramExecutedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `hub.install_mode_changed` | `hub.{central}.install_mode` | [`InstallModeChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `hub.alarm_message` | `hub.{central}.alarm_messages` | [`HubCountChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `hub.service_message` | `hub.{central}.service_messages` | [`HubCountChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `hub.inbox_changed` | `hub.{central}.inbox` | [`HubCountChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `hub.metrics_changed` | `hub.{central}.metrics` | [`HubMetricChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `connectivity.changed` | `hub.{central}.connectivity.{interface_id}` | [`HubConnectivityChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `hub.system_update_changed` | `hub.{central}.system_update` | [`HubSystemUpdateChangedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |

### Matter broadcasts

| Topic (also used as `type` in the envelope) | Payload schema |
|---|---|
| `matter.exposable_changed` | [`MatterExposureBulkUpdate`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `matter.commissioning_window_opened` | [`MatterCommissioningWindowResponse`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) (credential redacted, see below) |
| `matter.commissioning_progress` | [`MatterCommissioningProgressPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `matter.fabric_added` | [`MatterFabric`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `matter.fabric_removed` | [`MatterFabricRemovedPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |
| `matter.endpoint_assembled` | [`MatterEndpointAssembledPayload`](https://github.com/SukramJ/openccu-loom/blob/main/assets/openapi.yaml) |

`matter.exposable_changed` always carries the affected rows under
`items`: one entry for a single-row `PUT /api/v1/matter/exposable`, the
whole batch for `POST /api/v1/matter/exposable/bulk`. One operator
action is one broadcast, so a client mirroring the allowlist applies
every entry of the envelope rather than expecting one frame per row.

`matter.commissioning_window_opened` **redacts the pairing credential**:
`passcode` is `0` and `qr_code` / `manual_code` are empty. Subscribing
to a topic requires no role, so the credential that commissions the
bridge onto a new fabric never leaves the admin-gated
`POST /api/v1/matter/commissioning/window` response. The broadcast
carries `discriminator` and `duration_seconds`, which is what a client
needs to show that a window is open and when it closes.

Matter broadcast frames set `type` to the full dotted broadcast name —
the same convention core broadcasts use (e.g. `type:
"matter.exposable_changed"` on topic `matter.exposable_changed`,
`type: "datapoint.value_changed"` on topic
`device.{address}.channels.{channel}.data_points.{parameter}`). The
`wsapi.json` `name` field reflects the topic in both cases.

## Reserved namespaces

To avoid colliding with future broadcast additions, external clients
should treat the following prefixes as **reserved**:

- `device.*`, `central.*`, `system.*`, `hub.*`, `matter.*` (currently
  in use)
- `recovery.*`, `connection.*`, `client.*`, `connectivity.*`,
  `scheduler.*`, `data.*`, `rpc.*`, `cache.*`, `health.*`,
  `reconciliation.*`, `link.*` (defined in `pkg/hmevent/catalogue.go`,
  may be surfaced over WS in the future)

Clients that publish or proxy events back through the daemon (none
exist today) must not invent topics in any reserved namespace.
