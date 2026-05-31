# WebSocket Topic Hierarchy

**Status:** Normative reference
**Owner:** OpenCCU-Loom Maintainer
**Letzte Aktualisierung:** 2026-05-24

## Purpose

This document is the source of truth for the WebSocket topic namespace
external clients subscribe to. It complements:

- [`assets/wsapi.json`](../../assets/wsapi.json) — per-broadcast catalogue
  (event name → topic pattern → payload schema reference)
- [`assets/openapi.yaml`](../../assets/openapi.yaml) — payload schema
  definitions under `components.schemas`
- [`asks.md`](./asks.md) — the backlog this hierarchy was extracted from
  (entries A1, A2, A3)

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
  [`components.schemas`](../../assets/openapi.yaml) by the broadcast
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
configuration; bursty operators on multi-CCU deployments can scale
it via `Hub.SetReplayCapacity` in code (no config knob today —
deferred until concrete operational need surfaces).

## Auth lifecycle on long-lived connections

The bearer token / session cookie presented at the HTTP Upgrade
handshake fixes the connection's identity until either side closes
the connection or the client sends a `{op: "reauth"}` frame.

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

### Token-revoke semantics

If a client does NOT send `reauth` after the operator revokes its
token, the existing WS connection continues to receive broadcasts —
matching the standard WebSocket security model (no automatic
in-band re-check of the upgrade-time credentials). Clients that
need hard-revoke semantics should react to operator-side revocation
(e.g. an audit-event tail) by either reauthing with a fresh token
or closing the connection.

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
Topic prefixes embed the central name so a multi-central client
distinguishes which CCU emitted each event without parsing the
payload. Single-central deployments can subscribe to the broader
wildcards (`device.*`, `central.*.state`) without ambiguity — there
is only one central to fan out from.

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
[`assets/wsapi.json`](../../assets/wsapi.json) — every entry with
`"kind": "broadcast"` carries `topic` (pattern) and `payload` (schema
name in `openapi.yaml`). The table below is the human-readable view
of what is emitted today.

### Core broadcasts (daemon-emitted, openapi-described)

| Type (`name`) | Topic pattern | Payload schema |
|---|---|---|
| `datapoint.value_changed` | `device.{address}.channels.{channel}.data_points.{parameter}` | [`DataPointValueChangedPayload`](../../assets/openapi.yaml) |
| `custom_data_point.state_changed` | `device.{address}.cdps.{name}` | [`CustomDataPointStateChangedPayload`](../../assets/openapi.yaml) |
| `central.state_changed` | `central.{name}.state` | [`CentralStateChangedPayload`](../../assets/openapi.yaml) |
| `system.status_changed` | `system.{central}.status` | [`SystemStatusChangedPayload`](../../assets/openapi.yaml) |
| `hub.sysvar_changed` | `hub.{central}.sysvars.{name}` | [`SysvarChangedPayload`](../../assets/openapi.yaml) |
| `hub.program_executed` | `hub.{central}.programs.{id}` | [`ProgramExecutedPayload`](../../assets/openapi.yaml) |
| `device.created` | `device.{address}.lifecycle` | [`DeviceCreatedPayload`](../../assets/openapi.yaml) |
| `device.removed` | `device.{address}.lifecycle` | [`DeviceRemovedPayload`](../../assets/openapi.yaml) |

### Matter broadcasts

| Topic (also used as `type` in the envelope) | Payload schema |
|---|---|
| `matter.exposable_changed` | [`MatterExposureUpdate`](../../assets/openapi.yaml) |
| `matter.commissioning_window_opened` | [`MatterCommissioningWindowResponse`](../../assets/openapi.yaml) |
| `matter.commissioning_progress` | [`MatterCommissioningProgressPayload`](../../assets/openapi.yaml) |
| `matter.fabric_added` | [`MatterFabric`](../../assets/openapi.yaml) |
| `matter.fabric_removed` | [`MatterFabricRemovedPayload`](../../assets/openapi.yaml) |
| `matter.endpoint_assembled` | [`MatterEndpointAssembledPayload`](../../assets/openapi.yaml) |

**Known divergence:** Matter broadcast frames currently set `type` to
the *trailing segment* after `matter.` (e.g. `type: "exposable_changed"`
on topic `matter.exposable_changed`), while core broadcasts use the
*full* event name as `type` (e.g. `type: "datapoint.value_changed"`).
The `wsapi.json` `name` field reflects the topic in both cases. This
asymmetry is tracked for future alignment; clients should not rely on
either convention for cross-namespace generic dispatch.

## Not yet pushed (deferred asks)

The following internal `hmevent` types exist but are not currently
broadcast over the WebSocket surface. They are tracked in
[`asks.md`](./asks.md) as deferred work:

- `DataPointOptimisticRolledBackEvent` — declared in
  `pkg/hmevent/catalogue.go` but **not emitted** by any producer.
  Loom handles optimistic-write conflicts differently (the
  `lastSentLevel`-slot, 60 s TTL, see CHANGELOG 2026-05-12) — the
  rollback-on-mismatch concept from aiohomematic has no wire
  representation here. Consumers MUST NOT simulate it from
  observed deltas.
- `InstallModeChangedEvent` — install-mode toggle
- `ConnectivityChangedEvent` — per-interface connectivity flips
- `AlarmMessageEvent` / `ServiceMessageEvent` — CCU message lifecycle

A future PR may surface any of these as broadcasts; each addition
follows the same flow: add `Publish*` helper + topic function in
`internal/north/rest/ws/`, subscribe in a subscriber file, annotate in
`wsapi.json`, add payload schema in `openapi.yaml`, append to this
table.

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
