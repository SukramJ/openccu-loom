# Plan C3 — Expose `AdditionalInformation` north-bound

**Status:** ready to implement (genuine gap, verified). Self-contained;
no prior conversation context required.

## Summary

Several model types expose an enriched-metadata map via an
`AdditionalInformation()` method, but no north-bound adapter emits it.
This plan merges that metadata **additively** into the MQTT per-DP state
payload, the hub-aggregate payloads, and the REST device/datapoint
serialization, under a single new key `additional_information`. The
change is strictly additive — every existing field keeps its current
shape and position, so existing consumers are unaffected. The repo owner
is the sole tester and has waived a versioned-schema gate, so no MQTT
schema-version bump is required; the additive guarantee still holds.

This closes by-design entry **A1-BD01** in `docs/parity/by_design.md`,
which must be flipped to RESOLVED as part of the change.

## Current state (verified)

Method surfaces that already exist and are tested:

- `internal/model/generic/datapoint.go:1270` —
  `func (d *DataPoint[T]) AdditionalInformation() map[string]any { return nil }`.
  Base implementation returns `nil`; concrete custom DPs override it.
- `internal/model/calculated/voltage.go:129` —
  `func (s *OperatingVoltageLevelSensor) AdditionalInformation() map[string]any`.
  Returns battery metadata when a battery config resolved, else `nil`.
  Keys: `"Battery Qty"` (int), `"Battery Type"` (string),
  `"Low Battery Limit"` (string `"<V>V"`), `"Low Battery Limit Default"`
  (string), `"Voltage max"` (string).
- `internal/model/hub/messages.go:493` —
  `func (s *ServiceMessages) AdditionalInformation() []map[string]any`
  (one map per active service message; keys `id,name,address,device_name,
  type,priority,quittable,counter,timestamp` + optional `display_name,
  description,rooms,functions`).
- `internal/model/hub/messages.go:531` —
  `func (a *AlarmMessages) AdditionalInformation() []map[string]any`
  (one map per active alarm message).

North-bound assembly today:

- Per-DP slot state envelope: `internal/payload/wrapper.go:21`
  `type PerDPState struct { Value any; Available bool; ModifiedAt float64;
  RefreshedAt float64 }` — JSON keys `value`, `available`, `modified_at`,
  `refreshed_at`. **No metadata field.**
- Typed per-source state structs live in `internal/payload/state.go`
  (`ClimateState`, `CoverState`, `LightState`, …); the source contract is
  `internal/payload/source.go:62` `State() StatePayload` where
  `StatePayload = any` (`source.go:27`).
- MQTT publish entry points: `internal/north/mqtt/bridge.go` —
  `PublishSlotState(ctx, central, iface, slot, state PerDPState)` (`:723`),
  `PublishSourceState(...)` (`:627`), `PublishCustomDPState(...)` (`:661`).
  Each `json.Marshal`s the state value directly.
- REST device/datapoint serialization: `internal/north/rest/handlers/devices.go`.
- by-design rationale: `docs/parity/by_design.md` §A1-BD01 (~line 2678) —
  states the merge "would change the schema of an established payload and
  is therefore scoped to a versioned MQTT-schema extension." The owner has
  waived the version gate; keep the merge additive.

## Design decisions

1. **Single canonical key: `additional_information`.** Same key on every
   surface (MQTT per-DP state, hub aggregate payloads, REST DTO) so
   consumers learn one name.
2. **`omitempty` everywhere.** The field is `map[string]any` (or
   `[]map[string]any` for hub aggregates) tagged `,omitempty`. The base
   generic DP returns `nil` → the key is elided → existing per-DP payloads
   are byte-identical for the common case. Only DPs that override
   `AdditionalInformation()` (currently `OperatingVoltageLevelSensor`)
   carry the field. This is the additive guarantee.
3. **Populate at the publish boundary, not in the model.** The model
   methods already exist; wiring belongs where `PerDPState` /
   the REST DTO is built, so the model layer is untouched.
4. **Three independent surfaces, three small edits.** Per-DP MQTT state,
   hub-aggregate MQTT/REST payloads, and the REST datapoint DTO are
   wired separately; none depends on another.
5. **No new config.** The merge is unconditional and additive. (If a
   gate is ever wanted, add `north.mqtt.emit_additional_information`
   later — out of scope here.)

## Implementation steps

1. **Extend the per-DP envelope.** In `internal/payload/wrapper.go`, add
   to `PerDPState`:
   ```go
   // AdditionalInformation carries enriched model metadata (battery
   // type/quantity/limits, …) when the data point provides it. Absent
   // (nil) for plain scalar DPs, elided from JSON via omitempty.
   AdditionalInformation map[string]any `json:"additional_information,omitempty"`
   ```
2. **Populate it where `PerDPState` is built.** Find the constructor of
   the slot-state envelope (search: `PerDPState{` in
   `internal/central/adapter/` and `internal/payload/`; the EventBridge
   slot-state path `publishSlotState` is the likely owner). At that site
   the source data point is in scope — call its
   `AdditionalInformation()` and assign. Guard against a nil DP:
   ```go
   if ai := dp.AdditionalInformation(); len(ai) > 0 {
       st.AdditionalInformation = ai
   }
   ```
   The `AdditionalInformation()` method is on the generic
   `DataPoint[T]`; expose it on whatever DP interface the slot-state
   builder already holds (add the one method to that consumer-side
   interface if not present).
3. **Hub aggregates → MQTT + REST.** Where `ServiceMessages` /
   `AlarmMessages` state payloads are assembled for MQTT
   (`internal/north/mqtt/` hub publisher) and REST, add
   `additional_information` (`[]map[string]any`, `omitempty`) sourced
   from the existing `AdditionalInformation()` slices. The maps are
   already wire-ready; no transformation.
4. **REST datapoint DTO.** In `internal/north/rest/handlers/devices.go`,
   add `AdditionalInformation map[string]any json:"additional_information,omitempty"`
   to the datapoint response struct and populate from
   `dp.AdditionalInformation()` (nil-safe). Keep field ordering otherwise
   unchanged.
5. **Docs.** Update `docs/mqtt-topic-schema.md` to document the new
   optional `additional_information` key on the per-DP state topic and the
   hub service/alarm payloads. Add an example with the battery map.
6. **Flip the by-design entry.** In `docs/parity/by_design.md`, change
   §A1-BD01 from "post-0.1.0 enhancement / not emitted" to **RESOLVED**,
   noting the additive merge and the surfaces touched.

## Config / API / Doc changes

- **No new `cfg:`-tagged config field** → no `config.field.*` /
  `config.help.*` i18n entries required, and
  `TestConfigFieldsHaveLabelsAndHelp` is unaffected.
- **REST schema:** the datapoint DTO gains an optional field. If the DTO
  is reflected into `assets/openapi.yaml`, update the spec, then run
  `make export-schemas` (refreshes the digest) **and** bump the REST
  `APIVersion` (the PR-only "api contract guard" fails otherwise). A
  purely additive optional field is a minor version bump. If the
  datapoint state DTO is *not* in the OpenAPI surface, skip this — verify
  by grepping `assets/openapi.yaml` for the datapoint state shape.
- **MQTT:** additive only; document in `docs/mqtt-topic-schema.md`
  (step 5). No wsapi change unless the WS datapoint frame mirrors the DTO
  (check `assets/wsapi.json`; if so, same export-schemas + APIVersion
  discipline applies).

## Tests

- **Payload unit (`internal/payload/wrapper_test.go` or `state_test.go`):**
  marshal a `PerDPState` with and without `AdditionalInformation`; assert
  the key is present with the battery map in the first case and **absent**
  (no `additional_information` substring) in the second — this is the
  additive guarantee.
- **MQTT (`internal/north/mqtt/...test.go`):** publish a slot state for a
  DP whose `AdditionalInformation()` returns the battery map; assert the
  emitted JSON contains `additional_information` with the expected keys,
  and that a plain scalar DP's payload is unchanged.
- **Hub publisher test:** assert `ServiceMessages` / `AlarmMessages`
  payloads carry `additional_information` as a list of maps.
- **REST (`internal/north/rest/handlers/devices_test.go`):** assert the
  datapoint DTO carries `additional_information` for the voltage sensor
  and omits it for a scalar DP.
- Test file/function names describe the behaviour (e.g.
  `TestPerDPState_AdditionalInformationOmittedWhenNil`), never a coverage
  tag.

## Project-rule checklist

- [ ] SPDX header on any new file (none expected — all edits to existing files).
- [ ] No CGo; pure-Go only.
- [ ] Multi-CCU-safe: the merge reads per-DP/per-source data already
      scoped by `central_name`; no global state introduced.
- [ ] `context.Context` first arg preserved on any touched I/O method
      (publish methods already take `ctx`).
- [ ] No `panic` outside `main`/tests.
- [ ] No `any` without justification — `map[string]any` is the existing
      model surface (`AdditionalInformation()` returns it); acceptable and
      consistent.
- [ ] `make test` green, including the new additive-guarantee tests and
      `TestDocPurity` (keep code comments free of audit tags / German
      function-words).

## Acceptance criteria

- A battery-backed device's per-DP MQTT state topic and REST datapoint
  response include an `additional_information` object with `Battery Type`,
  `Battery Qty`, `Low Battery Limit`, `Low Battery Limit Default`,
  `Voltage max`.
- A non-battery scalar DP's payloads are byte-identical to before.
- Hub service-message / alarm payloads expose `additional_information` as
  a list of per-message maps.
- `docs/parity/by_design.md` §A1-BD01 reads RESOLVED.

## Effort

**S** (small). Three additive field edits + populate sites + docs + tests.
No model, schema-versioning, or config work.

## References

- `CLAUDE.md` → "Common Tasks → Add a REST endpoint" (DTO + spec
  discipline), "Add a translation key" (n/a here — no config field),
  "API contract change checklist" memory (export-schemas + APIVersion).
- `docs/parity/by_design.md` §A1-BD01 (the entry this plan resolves).
- `docs/mqtt-topic-schema.md` (doc to update).
- Model sources: `internal/model/calculated/voltage.go:129`,
  `internal/model/hub/messages.go:493/531`,
  `internal/model/generic/datapoint.go:1270`.
