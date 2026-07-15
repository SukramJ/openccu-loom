# ADR 0052 — Daemon-level alarm MQTT topics (extends ADR 0011)

- **Status**: Accepted
- **Date**: 2026-07-15
- **Related**:
  [ADR 0011 — MQTT topic & payload architecture](./0011-mqtt-topic-and-payload-architecture.md),
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md)

## Context

The alarm engine's areas (`docs/alarm-concept.md` §14) are daemon-level
objects: an area's sensors and outputs reference `(central_name,
DataPointKey)` pairs, and an area routinely spans more than one
configured CCU — the common case for anything beyond a single-CCU
household. ADR 0011 fixed every state and command topic to carry a
`<central>` segment immediately below `<base>`, so several CCUs can
share one broker namespace without collision. An alarm area has no
single owning CCU to put in that segment; forcing it onto one
central's namespace, or publishing it once per central, would
misrepresent the domain model and break as soon as an area's sensors
span two CCUs.

The only existing precedent for a topic without `<central>` is the
read-only `<base>/bridge/status` / `<base>/bridge/health` pair (ADR
0011, topic hierarchy) — genuinely daemon-level facts that predate any
CCU connection. The alarm subtree (`docs/alarm-concept.md` §13.3) is
the first **writable**, domain-carrying use of that same exception.

## Decision

Alarm topics live directly under `<base>/alarm/<area>/`, omitting the
`<central>` segment, mirroring the `bridge/*` precedent: `state`
(retained), `availability` (retained), `event` (JSON, not retained),
and `set` (command). Wire shapes, the HA/JSON command vocabulary, and
the loom-specific `SILENCE` extension are pinned in
`docs/mqtt-topic-schema.md`, not restated here.

`<area>` is either a configured area id or the reserved pseudo-area
`master`, published whenever 2 or more areas are configured. Master
topics aggregate every area: any `triggered` wins, else any `pending`,
else any `arming`, else all-`disarmed`, else the shared mode token if
every armed area agrees, or `armed_away` when mixed. Master **arm** is
best-effort — each area arms independently and a failure surfaces as a
per-area `FAILED_TO_ARM` detail rather than failing the whole request,
per the lean recorded in `docs/alarm-concept.md` §18 item 5 ("matches
G5"). Master **disarm** disarms every area unconditionally.

HA MQTT Discovery still advertises one `alarm_control_panel` entity
per area (plus master) the normal way — this ADR fixes the topic
*shape* only, not the discovery mechanism, which stays exactly as ADR
0011 describes it for every other component.

## Consequences

- `docs/mqtt-topic-schema.md` documents `<base>/alarm/<area>/*` as a
  second explicit exception to the "every topic carries `<central>`"
  rule, alongside `bridge/*`.
- Subscribers that blanket-wildcard `<base>/+/...` expecting the
  second segment to always be a central name must special-case `alarm`
  — no different from the existing `bridge/*` exception they already
  had to handle.
- No change to the per-CCU topic tree (`<base>/<central>/...`); this
  ADR only adds the daemon-level `alarm` subtree next to the existing
  daemon-level `bridge` subtree.
- The `master` pseudo-area is a read/write convenience, not a new
  domain object — the alarm engine still applies commands per real
  area; the master command handler fans a request out and never
  becomes an area of its own in the store schema.
