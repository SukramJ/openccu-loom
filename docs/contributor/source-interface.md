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
    Info()               InfoPayload
    Config()             ConfigPayload
    State()              StatePayload
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
   `payload.go` next to your type, each returning a typed payload struct:

   ```go
   // internal/model/custom/foo/payload.go
   package foo

   import "github.com/SukramJ/openccu-loom/internal/payload"

   var _ payload.Source = (*Foo)(nil)

   func (f *Foo) State() payload.StatePayload {
       if f == nil {
           return nil
       }
       return &payload.FooState{ /* fields with json tags */ }
   }

   func (f *Foo) Info() payload.InfoPayload     { ... }
   func (f *Foo) Config() payload.ConfigPayload { ... }
   ```

   `InfoPayload`, `ConfigPayload` and `StatePayload` are marker types;
   the concrete payloads are domain structs in `internal/payload`
   (`state.go`, `config.go`, …) whose `json` tags are the wire keys.
   `internal/model/custom/lock/payload.go` (`Lock.State` returning
   `*payload.LockState`) is a compact reference.

5. **Use HA-friendly key names.** State fields should match what HA's
   MQTT platform expects (`hvac_mode`, `current_temperature`,
   `current_position`, `lock_state`, …). The MQTT bridge publishes the
   aggregate as the `val` of an mqtt-smarthome status object (ADR 0083)
   and points HA at it with `value_template:
   "{{ value_json.val.<field> }}"` — your key names show up verbatim in HA
   templates.

6. **Decide per field whether "unknown" is absent or a default.** An
   optional field that may be unknown is a pointer with `omitempty`, so
   it is absent from the JSON rather than a misleading zero. Where Home
   Assistant templates would warn on a missing key, publish a documented
   default instead (the lock reports `UNLOCKED` before its first event).

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

## HA-Discovery: implement `HADiscoveryEntityBuilder`

Custom-DP types that should surface as a Home Assistant MQTT-Discovery
entity implement `payload.HADiscoveryEntityBuilder`
([ADR 0010](../adr/0010-discovery-payload-from-model.md),
`internal/payload/discovery_entity.go`):

```go
HADiscoveryEntity() hamodel.Entity
```

The entity describes itself on the shared Home Assistant model
(`hamodel`, from the go-hamqtt module): a description, the bindings and
the platform's own vocabulary. It carries **no topics** — it names
`hamodel.Slot` coordinates, and the bridge renders them through its own
topic layout, so the topic schema stays the bridge's business. The
bridge also renders the frame (`unique_id`, `availability`, `device`,
`origin`, `name`) from the model, because Home Assistant drops
undeclared keys silently and keys its registries on `unique_id` and the
device identifiers. Return a literal `nil` to decline (a data point with
no Home Assistant surface); a typed nil pointer is not `nil` to the
bridge.

`HADiscoveryEntityBuilder` replaces the older
`HADiscoveryComponentBuilder` on the channel-aggregate plane. References:
`internal/model/custom/climate/payload.go` and
`internal/model/custom/valve/payload.go`.

---

## What you do **not** need to do

- **No bridge edits.** The bridge is a routing shell since ADR 0008
  step B + ADR 0010. New custom-DP types do not require touching
  `internal/north/mqtt/discovery_aggregate.go` or any other file
  under `internal/north/mqtt/`.
- **No REST handler edits** for the basic invoke path — the generic
  `cdps/<dp>/<op>/invoke` route dispatches through `Source.Invoke`.
- **No HA-Discovery template edits** for the read side — state is
  consumed via `value_json.val.<field>` templates that already match the
  StatePayload key set you choose.
- **No service-method-command-topic wiring.** The CommandSubscriber
  subscribes a wildcard (`{base}/set/+/+/+/+/custom/+/+`) and dispatches by
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
