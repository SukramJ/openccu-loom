# ADR 0006 — Naming Conventions for REST + MQTT Surfaces

- **Status**: Accepted
- **Date**: 2026-04-28
- **Related**: ADR 0005 (visibility-as-outbound-filter), CHANGELOG entry
  "MQTT Topology Cleanup"

## Context

The first cut of the REST + WS + MQTT surface accreted ad-hoc names:
- `data_points` (snake_case) lived next to `custom-data-points` and
  `calculated-data-points` (kebab-case).
- Long path segments — `custom-data-points` is 18 chars, repeated in
  every URL.
- `PUT .../custom-data-points/{name}/state` with `{operation, params}`
  in the body conflated state-mutation (idempotent) with action
  invocation (non-idempotent).
- `POST /backup` was a singular outlier among `/backups/{id}/...`.
- MQTT had a redundant `hub/` namespace under `{base}/{central}/`
  that disambiguated nothing (every hub-entity sits there alone).
- HA Discovery used a hardcoded `openccu-loom` node — Multi-Daemon
  setups on the same broker collided.

This ADR records the conventions chosen for the v1.0 surface so
future endpoints stay consistent.

## Decision

### REST URL conventions

1. **Kebab-case** for all multi-word path segments.
2. **Plural collection nouns**: `/devices`, `/programs`, `/sysvars`,
   `/backups`, `/cdps`, `/calc-dps`, `/data-points`, `/link-ps`.
   The collection name is plural even when only one item is
   addressable in a request (`POST /backups` creates ONE backup;
   the URL still uses the plural collection).
3. **Approved abbreviations** for the data-point family:
   - `dp` / `data-points` → "data point" (the wire-level paramset DP)
   - `cdp` / `cdps` → "custom data point" (Light, Cover, Climate, …)
   - `calc-dp` / `calc-dps` → "calculated data point" (DewPoint, …)
   - `link-ps` → "link paramset" (peer-keyed paramset)
   These abbreviations are also used in WS command namespaces and in
   audit-log source tags.
4. **HTTP method choice** — strict semantic discipline:
   - `GET` for reads, no side effects.
   - `PUT` for idempotent state-set (write a value, replace a config).
     A second identical PUT is indistinguishable from the first.
   - `POST` for non-idempotent actions: invoke an operation,
     execute a program, ack a message, trigger a backup.
   - `PATCH` for partial-update of metadata (description / unit /
     value-list). Disjoint from PUT, which sets the runtime value.
   - `DELETE` for resource removal.
5. **Action endpoints** carry the operation in the URL, not the body:
   - `POST /devices/{addr}/cdps/{name}/{operation}` (not
     `PUT .../state` with `{operation: "..."}` in the body)
   - `POST /programs/{id}/execute` (the action verb after the id)
   - `POST /service-messages/{id}/ack`
   This makes the request log + audit trail self-describing.
6. **Filter parameters** as query string, not URL:
   `GET /audit?device=0001&op=rest:cdp&since=2026-04-28T12:00Z`.

### WS command conventions

1. **Dot-namespace** mirroring REST collection names:
   `cdp.list / .get / .invoke`, `calc_dp.list / .get`,
   `paramset.put / .get`, `program.execute`, `sysvar.put`.
2. WS uses **snake_case** internally because the dispatch keys are
   not URL segments — kebab would require quoting in many client
   libraries. (`cdp` / `calc_dp` are short enough to read either way;
   we picked snake to match the existing `paramset.put`-style.)
3. Categories in `assets/wsapi.json` align with REST collections:
   `cdp`, `calc_dp`, `paramsets`, `programs`, `sysvars`, `system`,
   `central`, …

### MQTT topic conventions

1. **No `hub/` namespace** under `{base}/{central}/` — hub-entities
   sit directly under the central:
   `{base}/{central}/install_mode`, `{base}/{central}/sysvars/{name}`.
   The dropped `hub/` was a stylistic vestige — there was no other
   namespace to disambiguate against.
2. **`devices/` namespace** for device-scoped paths to avoid colliding
   with the future `groups/` namespace and with the hub-entities:
   - `{base}/{central}/devices/{addr}/availability`
   - `{base}/{central}/devices/{addr}/cdps/{name}/{operation}/invoke`
   - The raw paramset path stays under the canonical
     `{base}/{central}/{iface}/{addr}/{ch}/{param}` (no `devices/`),
     because that path is keyed by interface-id which is not
     `devices` — it's the wire transport.
3. **Snake_case** for path segments (matches MQTT convention
   `homeassistant/binary_sensor/...`).
4. **HA Discovery node** derives from `BridgeConfig.Base`, not a
   hardcoded literal — `homeassistant/{component}/{base}/{objectID}/config`.
   Default `Base="openccu-loom"` reproduces the legacy form;
   non-default Bases give multi-daemon installations distinct
   discovery roots.
5. **Inbound command topics** end in a verb suffix:
   - `/set` for parameter-value writes (raw paramset)
   - `/invoke` for CDP-operation invocation
   - `/trigger` for program triggering
   - `/restart`, `/refresh`, etc. as needed
   The verb makes intent explicit and prevents accidental writes
   from ambiguous wildcards.

### Migration

1. The `hub/` topology drop ships with a `LegacyAliasConfig.HubTopics`
   opt-in (default `false`). Operators flip it to `true` to mirror
   onto both old and new topics during their MQTT-subscriber rollout,
   then disable.
2. REST URL renames are pre-release; no backwards-compat aliases.
3. `data_points` → `data-points` (kebab fix) is pre-release;
   no compat alias.
4. WS command renames (`custom_data_point.set` → `cdp.invoke`) are
   pre-release; no compat alias.

## Consequences

**Positive**

- URL surface is consistent and predictable.
- `cdp` / `calc-dp` shorten common audit-log entries by ~30 %.
- Method semantics are correct: clients can rely on PUT idempotency
  and POST non-idempotency without reading code.
- MQTT topic tree is shallower; `hub/` redundancy gone.
- Multi-daemon HA Discovery setups don't collide.
- ADR pins the conventions so the next contributor doesn't reintroduce
  drift.

**Negative**

- One-time pre-release breaking change for REST + WS clients. Easy
  before any release; would be expensive after.
- Operators with running MQTT consumers must opt-in to LegacyAlias
  during the migration window or update subscriptions atomically.

**Neutral**

- The kebab-vs-snake-case split between REST URLs and WS commands +
  MQTT topics is pragmatic, not principled. Each surface has its
  own ecosystem convention; we follow the local one.

## Implementation references

- REST routes: `internal/north/rest/router.go`
- REST handler renames: `internal/north/rest/handlers/custom_data_points.go`
  (`PutCustomDataPointState` → `InvokeCustomDataPoint`)
- Audit-filter params: `internal/north/rest/handlers/audit.go`
- WS command catalogue: `assets/wsapi.json`, `tests/contract/wsapi_schema_test.go`
- OpenAPI spec: `assets/openapi.yaml`
- MQTT topic builder: `internal/north/mqtt/topics.go`
- Legacy hub-topic mirror: `internal/north/mqtt/legacy_alias.go::HubTopicBuilder`
- CDP invoke topic: `TopicBuilder.CustomDPInvoke` +
  `CommandSubscriber.handleCDPInvoke` + `CDPInvocationSink`
- HA Discovery node: `TopicBuilder.DiscoveryConfig` (uses `b.Base`)
