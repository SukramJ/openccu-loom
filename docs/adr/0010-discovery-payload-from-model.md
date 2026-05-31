# ADR 0010 — Move HA-Discovery Payload Construction into the Model

- **Status**: Accepted
- **Date**: 2026-04-30
- **Extends**: [ADR 0007](./0007-strong-model-source-interface.md), [ADR 0008](./0008-aggregated-state-default-flip.md), [ADR 0009](./0009-service-method-command-topics.md)
- **Related**: `internal/north/mqtt/discovery_aggregate.go`,
  `internal/model/custom/*/payload.go`

## Context

ADR 0008 step B removed the legacy dual-mode discovery code path,
shrinking `discovery_aggregate.go` from ~924 to ~598 LOC. ADR 0007's
target was **<200 LOC**. The remaining 598 LOC are seven `buildX`
functions that each:

1. Compose the HA-Discovery payload skeleton (`name`, `unique_id`,
   `availability`, `device`, `origin` — shared via `channelBaseBody`).
2. Reference the aggregated state topic with HA-platform-specific
   `*_state_topic` / `*_state_template` keys that match HA's MQTT
   platform expectations (climate, cover, lock, light, siren, valve,
   text).
3. Reference command topics — wire-parameter today; service-method
   after ADR 0009.
4. Pull static config from `Source.ConfigPayload()` into HA-platform-
   specific config keys (`min_temp` / `max_temp` from
   `ConfigPayload["min_temp"]`, etc.).

The aggregator is, in effect, **n×7 boilerplate** — one platform per
custom-DP type. HA's MQTT platform conventions are stable and
specific: a climate entity needs exactly these keys, a cover entity
needs exactly those. Each `buildX` is a short, mechanical projection
from `Source` payloads onto the HA platform's required field set.

The boilerplate sits in the bridge. Adding a new custom-DP type today
means: write the model + write a `buildX` in the bridge. The bridge
is the wrong owner — the model has all the type-specific knowledge
(which fields exist, what they mean, how they map to HA semantic
keys). Splitting the knowledge across two packages causes drift.

## Decision

Each custom-DP type owns its **own** HA-Discovery payload builder, in
its `payload.go` file:

```go
// internal/model/custom/climate/payload.go
package climate

import "github.com/SukramJ/openccu-loom/internal/payload"

// HADiscoveryPayload returns the platform-specific HA-Discovery
// payload skeleton for a Climate custom DP. The bridge calls this
// from `aggregateChannel`, attaches the shared availability /
// device / origin block, and publishes the result.
func (c *Climate) HADiscoveryPayload() (component string, body map[string]any) {
    body = map[string]any{
        "current_temperature_template":   "{{ value_json.current_temperature }}",
        "temperature_state_template":     "{{ value_json.target_temperature }}",
        "mode_state_template":            "{{ value_json.hvac_mode }}",
        "preset_mode_value_template":     "{{ value_json.preset_mode }}",
        "temperature_unit":               "C",
    }
    if c.Capabilities.SupportsBoost {
        body["preset_modes"] = []string{"none", "boost"}
    }
    cfg := c.ConfigPayload()
    if v, ok := cfg["min_temp"]; ok {
        body["min_temp"] = v
    }
    // …
    return "climate", body
}
```

The bridge (`internal/north/mqtt/discovery_aggregate.go`) becomes a
single dispatch:

```go
func (d *DefaultDiscoveryBuilder) aggregateChannel(ev Event) (...) {
    if ev.Source == nil {
        return "", "", "", nil, false
    }
    builder, ok := ev.Source.(haDiscoveryPayloadBuilder)
    if !ok {
        return "", "", "", nil, false
    }
    component, body := builder.HADiscoveryPayload()
    body["state_topic"] = d.aggregatedStateTopic(ev)
    overlayBaseBody(body, d.channelBaseBody(ev, displayChannelName(ev), uniqueID))
    payload, err := json.Marshal(body)
    if err != nil { return "", "", "", nil, false }
    return component, nodeID, objectID, payload, true
}
```

Estimated post-refactor `discovery_aggregate.go` size: **<150 LOC** —
beats the ADR 0007 target.

### Payload-builder contract

```go
// internal/payload/discovery.go
package payload

// HADiscoveryPayloadBuilder is the optional Source extension that
// custom-DP types implement when they want to drive HA's MQTT
// auto-discovery. The bridge attaches the platform-agnostic
// availability / device / origin block; the builder fills in the
// platform-specific fields (state_topic templates, mode lists,
// command topic references, …).
//
// A type that does not implement this interface falls through to the
// per-parameter classifyComponent path — same as today's
// `ev.Source == nil` fallback.
type HADiscoveryPayloadBuilder interface {
    HADiscoveryPayload() (component string, body map[string]any)
}
```

Compile-time guarantee per type:

```go
var _ payload.HADiscoveryPayloadBuilder = (*climate.Climate)(nil)
```

Pinned by `tests/contract/discovery_builder_completeness_test.go`.

## Trade-offs

- **Coupling: model knows about HA**. A pure-Go data model that
  knows about HA platform names is a mild layering concern — the
  model imports nothing, but it carries strings like
  `"current_temperature_template"`. Mitigation: keep the strings
  inside one method per type, name the method `HADiscoveryPayload`
  (not `Payload` or `HAPayload`) so the binding is explicit. Future
  Matter / OpenHAB / Web Things bridges can add their own builders
  without touching the model layer.
- **Test relocation**: today's `discovery_*_test.go` cases live in
  `internal/north/mqtt/`. After this ADR they migrate to
  `internal/model/custom/*/payload_test.go` next to the builders. Fan-
  out increases (test files are shorter but more numerous);
  ergonomics improves (each Climate / Cover test sits next to the
  Climate / Cover builder).
- **Bridge becomes truly generic**: a new device type means a new
  Custom-DP with its `HADiscoveryPayload()`. Bridge edits never
  needed for new device types after this lands. This is the same
  shape aiohomematic2mqtt has had since day one (the `MqttClimate`
  class in `aiohomematic2mqtt/platforms/climate.py` is a typed
  builder that knows climate semantics; the bridge dispatches by
  data-point class).

## Why not simpler shapes

- **Keep `buildX` in the bridge, just refactor for clarity**: doesn't
  remove the layering issue; new device types still require bridge
  edits.
- **Generate `buildX` from a declarative spec** (YAML / TOML /
  Go struct): adds a code-generator dependency for marginal
  benefit. The model methods are already concise; further
  abstraction loses readability without saving lines.
- **Move only the templates / static config but keep the topic
  references in the bridge**: half-measure. Topic shapes are
  platform-specific (HA names) so they belong with the platform
  knowledge.

## Consequences

- **Bridge: routing shell.** `discovery_aggregate.go` < 150 LOC.
- **Model: HA-aware.** Custom-DP packages import
  `internal/payload` for the interface; no other coupling.
- **Adding a new device type**: one file (the model + its
  `HADiscoveryPayload`). No bridge edits.
- **ADR 0007's <200 LOC target** is reached, completing the
  Strong-Source-Model migration.

## Status notes

The `HADiscoveryPayloadBuilder` contract is implemented across all
custom-DP packages (Climate, Cover, Light, Lock, Siren, Switch,
TextDisplay, Valve), the seven legacy `buildX` functions in the MQTT
bridge are gone, and the per-DP test files live next to their
builders. Future bridges (Matter, OpenHAB) follow the same pattern
— declare an interface in the adapter package, custom-DPs implement
it side by side; the bridge stays generic.
