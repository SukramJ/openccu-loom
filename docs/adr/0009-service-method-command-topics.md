# ADR 0009 — Service-Method Command Topics in HA-Discovery

- **Status**: Accepted
- **Date**: 2026-04-30
- **Extends**: [ADR 0007 — Strong Model: `Source` Interface for Read + Write](./0007-strong-model-source-interface.md), [ADR 0008 — AggregatedState Default-Flip and Legacy-Path Removal](./0008-aggregated-state-default-flip.md)
- **Related**: `internal/north/mqtt/discovery_aggregate.go`,
  `internal/north/mqtt/command_subscriber.go`,
  `internal/model/custom/*/payload.go` (Service-Method-Registrierung)

## Context

ADR 0007 introduced `payload.Source.ServiceMethodNames()` and
`Source.Invoke(...)` as the universal write contract. Each custom DP
registers semantic operations in its constructor — Climate exposes
`set_temperature`, `set_mode`, `set_profile`, `enable_boost`, …

ADR 0008 step B made HA-Discovery's **read** side semantic: state
fields are pulled from the aggregated state topic via
`value_json.<field>` templates. The **write** side, however, still
points at wire-parameter command topics with inline Jinja templates
that translate semantic strings back to wire values:

```go
// today, in buildClimate:
body["mode_command_topic"] = d.channelCommandTopic(ev, "SET_POINT_MODE")
body["mode_command_template"] = `{% if value == "auto" %}0` +
    `{% elif value == "heat" %}1` +
    `{% else %}0{% endif %}`
```

Two consequences:

1. **Inline Jinja stays in the bridge** — exactly the pattern ADR 0008
   removed from the read side. The wire mapping (heat→1, auto→0) is
   knowledge the model already encodes (`Climate.SetMode`); the
   bridge re-encodes it as Jinja.
2. **`payload.Source.Invoke` has no MQTT entry point** for HA — the
   only way to call `set_mode` from MQTT today is the generic
   `cdps/<dp>/<op>/invoke` topic, which HA does not consume directly
   (it only speaks `*_command_topic`). HA users land on the wire-
   parameter path; the service-method API is REST-/scripting-only.

## Decision

Adopt **per-service-method command topics** as HA-Discovery's
canonical write surface. Per channel, the bridge subscribes one MQTT
topic per registered service method:

```
<base>/<central>/<interface>/<address>/<channel>/svc/<method_name>/set
```

HA-Discovery's `*_command_topic` references the matching service-
method topic. The semantic value HA sends arrives unchanged at
`Source.Invoke(ctx, name, params, priority)` — no inline Jinja.

For Climate this means:

| HA field | Today (ADR 0008) | After ADR 0009 |
|---|---|---|
| `mode_command_topic` | `…/SET_POINT_MODE/set` | `…/svc/set_mode/set` |
| `mode_command_template` | inline Jinja int↔string | absent |
| `temperature_command_topic` | `…/SET_POINT_TEMPERATURE/set` | `…/svc/set_temperature/set` |
| `preset_mode_command_topic` | `…/BOOST_MODE/set` | `…/svc/set_profile/set` |

The MQTT-Subscriber routes a `svc/<method>/set` payload through
`bridge.cdpInvoke(ctx, channel, method, params, priority)` which
resolves the channel's custom DP and calls `Source.Invoke`.

### Payload shape

For backward compatibility with HA's plain-value command topology:

- A **scalar payload** (e.g. `"heat"`, `"22.5"`, `"true"`) wraps to
  `{"value": <payload>}` — the most-frequent service-method API takes
  exactly one keyword argument named `value` / `mode` / `temperature`
  / `position` / etc. The bridge maps the scalar to the canonical
  argument key per method (table-driven in
  `internal/north/mqtt/service_method_routing.go`, generated from
  `Source.ServiceMethodNames()`).
- A **JSON object payload** is forwarded as-is into the `params`
  argument of `Source.Invoke` — lets advanced HA automations send
  multi-arg calls (e.g. `{"hours": 4, "away_temperature": 17.0}` for
  `set_away_for_duration`).

### Subscription wiring

Per channel that exposes a custom DP, the bridge resolves
`src.ServiceMethodNames()` at `PublishState` time and ensures the
matching `svc/<m>/set` topics are subscribed. New methods registered
post-startup are picked up on the next `PublishState` of the
channel — a known limitation that matches today's HA-Discovery
re-publish cadence.

Existing topics that remain unchanged:

- The per-parameter raw plane (`<chan>/<PARAMETER>/set`) keeps
  working for direct wire-level control by Node-RED / scripts.
- The generic `cdps/<dp>/<operation>/invoke` envelope keeps working
  for REST/WebSocket-bridged invokes that need explicit JSON-body
  control.

The `svc/<method>/set` topology is **additive** to both.

## Trade-offs

- **Three ways to write a value**: per-parameter wire,
  `cdps/.../invoke`, and `svc/<method>/set`. Operators may find the
  choice confusing. Mitigation: HA-Discovery references only the
  `svc/`-form, so the average HA user never sees the alternatives.
  The other forms are diagnostic / scripting tools.
- **Service-method-name stability**: renaming a service method
  becomes an HA-Discovery payload-shape change. Mitigation: existing
  `ServiceMethodNames` are documented in `docs/contributor/source-interface.md`
  and pinned by a contract test similar to
  `source_completeness_test.go`.
- **Subscription churn**: each channel has 3-10 service-method topics.
  For 100 thermostats this is 300-1000 extra MQTT subscriptions.
  Acceptable on standard brokers (Mosquitto handles 10k+
  subscriptions per client).
- **Param-key convention**: the scalar-payload-to-named-arg mapping
  is per-method bookkeeping. The bridge uses a small lookup table
  (`set_mode → "mode"`, `set_temperature → "temperature"`, etc.).
  New methods need a one-line entry; missed entries default to
  `value`. Pinned by a contract test.

## Why not simpler shapes

- **One topic per channel `…/<chan>/command` with method in the JSON
  body**: HA's MQTT Climate platform expects independent
  `mode_command_topic` / `temperature_command_topic` references. A
  single multiplexed command topic forces inline Jinja again.
- **Re-use `cdps/<dp>/<op>/invoke` directly in HA-Discovery**: the
  envelope is `{"params": {...}, "priority": "..."}`; HA's plain-value
  command write doesn't match. We could template in HA, but that
  reintroduces Jinja — exactly what ADR 0008 step B removed.
- **Keep wire-parameter command topics**: leaves the inline Jinja in
  HA-Discovery, defeats the ADR 0007/0008 architecture. Rejected.

## Consequences

- HA-Discovery payload becomes pure routing — no inline Jinja on
  either read or write side. The bridge is symmetric.
- The MQTT write surface aligns with the REST write surface: both
  hit `Source.Invoke`. No third translation path.
- `discovery_aggregate.go` shrinks further as the inline write-Jinja
  templates drop out (≈ 80-100 LOC of `*_command_template` strings).
- `Source.ServiceMethodNames()` becomes contractually load-bearing.
  Renames are observable schema changes — pinned by tests, called
  out in CHANGELOG.

## Status notes

[ADR 0011](./0011-mqtt-topic-and-payload-architecture.md) renames the
service-method command-topic shape from `<...>/<ch>/svc/<method>/set`
to `<...>/<ch>/custom/<kind>/set/<method>` for symmetry with the
read-side custom-DP namespace and switches the payload contract from
the scalar-wrapping convention to a JSON object with named arguments.
The principle of this ADR — one HA-Discovery command topic per
registered service method, dispatched through `Source.Invoke` —
remains in force.

Any residual cleanup (deletion of `service_method_routing.go` once
the scalar-wrapping shim is no longer needed) lands as an ordinary
refactor when touched.
