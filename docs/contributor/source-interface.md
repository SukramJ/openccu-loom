# `payload.Source` — Author Guide

!!! info "Who this page is for"
    Contributors extending the model layer. It is internal developer
    documentation — end users and administrators do not need it. For the
    big picture see [Architecture](../developer/architecture.md).

The `payload.Source` interface is the single contract every domain object
implements so that the north-bound adapters (REST, WebSocket, MQTT, Matter)
can project it uniformly. This guide walks a contributor through making a
new model type satisfy that contract.

This guide is for contributors adding a new domain object to the OpenCCU-Loom
model layer. It explains how to make that object satisfy the universal
`payload.Source` contract introduced in
[ADR 0007](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0007-strong-model-source-interface.md).

The contract is short:

```go
type Source interface {
    InfoPayload()        map[string]any
    ConfigPayload()      map[string]any
    StatePayload()       map[string]any
    ServiceMethodNames() []string
    Invoke(ctx context.Context, name string, params map[string]any, priority hmenum.CommandPriority) error
}
```

Every type listed in `tests/contract/source_completeness_test.go`
**must** satisfy `payload.Source` — adding a new type without doing so
fails that test at compile time.

---

## Decision tree

When you add a new model type, walk this tree:

1. **Does the type embed an existing `Source`-bearing type by pointer?**
   (e.g. `*generic.Switch`, `*generic.Float`, `*Cover`, `*Sysvar`)
   - **Yes** → `Source` is inherited by method promotion. Done.
     Verify with the contract test.
   - **No** → continue.

2. **Does your type have its own service methods that should be
   reachable from the bridge / REST?**
   - **Yes** → embed `payload.ServiceRegistry` as an anonymous field.
     Register handlers in the constructor:
     ```go
     func New(cfg Config) *MyType {
         m := &MyType{...}
         m.RegisterService("turn_on", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
             return m.TurnOn(ctx, priority)
         })
         return m
     }
     ```
   - **No** → still embed `payload.ServiceRegistry`. Its zero value
     gives you a no-op `ServiceMethodNames` (returns nil) and `Invoke`
     (returns `payload.ErrUnknownServiceMethod`) for free.

3. **Watch out for the embed-shadow gotcha.** If your type already
   embeds `*generic.Float` (or any other type that itself embeds
   `payload.ServiceRegistry`), and you add a `payload.ServiceRegistry`
   field on the outer type to register your own service methods, the
   outer registry **shadows** the inner one. That is the right
   behaviour — your custom-DP's `set_position` and the underlying
   `*generic.Float`'s `set_value` should not collide on the same
   registry. See `Cover` for the canonical example
   (`internal/model/custom/cover/cover.go`).

4. **Implement the three read methods** in a separate file
   `payload.go` next to your type. Two recommended patterns:

   ### Pattern A — explicit map literals (custom DPs, hub DPs)

   ```go
   // internal/model/custom/foo/payload.go
   package foo

   import "github.com/SukramJ/openccu-loom/internal/payload"

   var _ payload.Source = (*Foo)(nil)

   func (f *Foo) InfoPayload() map[string]any {
       if f == nil { return nil }
       return map[string]any{
           "address": f.Address,
           "kind":    string(f.Kind),
       }
   }

   func (f *Foo) ConfigPayload() map[string]any { ... }
   func (f *Foo) StatePayload() map[string]any  { ... }
   ```

   Use this for types with conditional or computed fields.

   ### Pattern B — struct-tag reflection (generic DPs, simple records)

   Tag struct fields with `payload:"<kind>"` (or `payload:"<kind>,alt=<name>"`)
   and let `payload.For(...)` build the map. See `internal/payload/payload.go`
   for tag syntax. The generic-DP layer in
   `internal/model/generic/payload.go` mixes this pattern with computed
   fields by overlaying both in the method body.

5. **Use HA-friendly key names.** State fields should match what HA's
   MQTT platform expects (`hvac_mode`, `current_temperature`,
   `current_position`, `lock_state`, …). The MQTT bridge points HA at
   the aggregated state topic with `value_template:
   "{{ value_json.<field> }}"` — your key names show up verbatim in HA
   templates.

6. **Filter typed-nil values from your maps.** Empty optional fields
   should be **absent** from the map rather than present with zero
   values. Mirrors aiohomematic's `if value is not None` filter and
   keeps the JSON tight.

7. **Service-method handlers are simple functions.** Decode `params`
   with the helpers in `internal/payload/params.go`:
   - `payload.ParamBool`
   - `payload.ParamFloat64`
   - `payload.ParamInt32`
   - `payload.ParamString`

   Each returns a wrapped `payload.ErrServiceMissingParam` /
   `payload.ErrServiceInvalidParam` on bad input. Your handler returns
   that error verbatim — the bridge logs it.

8. **Add the type to the contract test.** Append a line to
   `tests/contract/source_completeness_test.go`:

   ```go
   _ payload.Source = (*foo.Foo)(nil)
   ```

   This is the tripwire that prevents future drift.

---

## HA-Discovery: implement `HADiscoveryPayloadBuilder`

Custom-DP types that should surface as a HA-MQTT-Discovery entity also
implement [ADR 0010](../adr/0010-discovery-payload-from-model.md)'s
`HADiscoveryPayloadBuilder` interface:

```go
HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any)
```

The returned `body` carries the HA-platform-specific keys (`min_temp`,
`mode_state_template`, `preset_modes`, …); the bridge attaches
`name`, `unique_id`, `default_entity_id`, `availability`, `device`, `origin`
on top.

Use the `ctx` helpers to assemble topic references:

- `ctx.AggregatedStateTopic()` — the channel's aggregated state topic.
  Read references (`*_state_topic`) point here with
  `value_template: "{{ value_json.<field> }}"`.
- `ctx.ServiceMethodCommandTopic(method)` — write references
  (`*_command_topic`) point at the per-service-method topic. The
  CommandSubscriber dispatches into `Source.Invoke(method, …)`.
- `ctx.WireParameterCommandTopic(parameter)` — fallback for
  HA-platform multiplex topics where one `command_topic` accepts
  multiple `payload_*` strings (Cover open/close/stop, Lock
  lock/unlock, Siren on/off). Use sparingly.
- `ctx.WireParameterStateTopic(parameter)` — same fallback for state.

Pattern (Climate as reference; see
`internal/model/custom/climate/payload.go`):

```go
var _ payload.HADiscoveryPayloadBuilder = (*Foo)(nil)

func (f *Foo) HADiscoveryPayload(ctx payload.HADiscoveryContext) (string, map[string]any) {
    body := map[string]any{
        "min_temp":                 f.MinTemp(),
        "max_temp":                 f.MaxTemp(),
        "mode_state_topic":         ctx.AggregatedStateTopic(),
        "mode_state_template":      "{{ value_json.hvac_mode }}",
        "mode_command_topic":       ctx.ServiceMethodCommandTopic("set_mode"),
    }
    return "climate", body
}
```

The contract test
`tests/contract/source_completeness_test.go` enforces every Custom-DP
type implements `Source`; `tests/contract/source_no_dual_source_test.go`
enforces the no-dual-state-source rule. Add your new type to both.

## What you do **not** need to do

- **No bridge edits.** The bridge is a routing shell since ADR 0008
  step B + ADR 0010. New custom-DP types do not require touching
  `internal/north/mqtt/discovery_aggregate.go` or any other file
  under `internal/north/mqtt/`.
- **No REST handler edits** for the basic invoke path — the generic
  `cdps/<dp>/<op>/invoke` route dispatches through `Source.Invoke`.
- **No HA-Discovery template edits** for the read side — state is
  consumed via `value_json.<field>` templates that already match the
  StatePayload key set you choose.
- **No service-method-command-topic wiring.** The CommandSubscriber
  subscribes a wildcard (`{base}/+/+/+/+/svc/+/set`) and dispatches by
  method name. `Source.Invoke` does the rest.

---

## References

- [ADR 0007 — Strong Model: `Source` Interface for Read + Write](../adr/0007-strong-model-source-interface.md)
- [ADR 0008 — AggregatedState Default-Flip and Legacy-Path Removal](../adr/0008-aggregated-state-default-flip.md)
- `internal/payload/source.go` — interface definition
- `internal/payload/registry.go` — `ServiceRegistry` helper
- `internal/payload/params.go` — service-method param decoders
- `tests/contract/source_completeness_test.go` — completeness tripwire
- aiohomematic reference: `aiohomematic/property_decorators.py` and
  `aiohomematic/support/mixins.py` (PayloadMixin) for the Python
  origin of the pattern.
