# ADR 0005 — Visibility as Outbound Filter (REST + MQTT)

- **Status**: Accepted
- **Date**: 2026-04-28
- **Related**: ADR 0002 (multi-CCU)

## Context

The CCU exposes a complete VALUES + MASTER paramset per channel. The
domain model materializes every parameter from that description (see
`internal/central/adapter/device_pipeline.go::hydrateParamset`). The
question this ADR answers is: which of those parameters should reach
each north-bound consumer (REST, WS, MQTT, future Matter) by default,
and how does the operator opt in to seeing the rest?

The architectural premise:

> The model is complete. Visibility is an outbound filter. Default =
> visible-set (`Rules.IsAllowed || Decider.IsUnIgnored`). Opt in to
> the complete model via `?include=all` or the WS attribute.

The model stays the single source of truth. Each outbound adapter
applies the visibility-set when listing/publishing. UnIgnore entries
extend the visible-set. A defense-in-depth write-gate on
`adapter.ParamsetsDomain` prevents writes to hidden parameters.

## Decision

Visibility is applied as an **outbound filter** on the following paths.

### 1. REST `GET .../data_points`

`handlers.ListDataPoints` accepts a `filter.VisibilitySet` parameter.
By default the handler skips parameters for which
`vis.Visible(model, channelType, paramset, parameter)` returns false.

Opt-in for the complete model: append `?include=all` to bypass the
filter. This is the operator escape hatch — useful for diagnostics and
for tooling that intentionally wants the full CCU parameter set.

### 2. MQTT `EventBridge.onValueChanged`

`adapter.EventBridge` carries a `vis filter.VisibilitySet` field wired
via `WithVisibility(vis)`. Before forwarding a
`DataPointValueChangedEvent` to `mqtt.Wiring.Publish`, the bridge
checks `vis.Visible(model, channelType, paramset, parameter)`. If the
check fails the publish is silently dropped.

### 3. WS `wsHub.PublishDataPointValueChanged` — intentionally unfiltered

The WebSocket stream is the operator-tooling channel. Operators may
want all events for diagnostics and for WS-based tooling. The WS path
is left unfiltered. A per-subscription opt-in filter can be added in a
follow-up if needed.

### 4. Defense-in-depth write gate

`adapter.ParamsetsDomain.SetVisibilityGate` is retained. Writes that
slip through a broken or un-wired outbound filter are still rejected
before reaching the CCU with `pkg/hmerr.ErrParameterHidden` →
HTTP 403 (REST) / `"forbidden"` error code (WS). The contract test
`TestVisibilityGateIsWiredIntoWritePath` pins this invariant.

### 5. `internal/north/filter` package

`filter.VisibilitySet` is the interface adapters inject. `filter.Adapter`
wraps `*visibility.Registry` and delegates to `Registry.IsAllowed`.
Nil-safe: a nil `*Adapter` or nil registry returns `true` (everything
visible) — preserves backward compatibility for tests and bare wiring
paths.

### 6. Daemon wiring

`daemonServe` constructs one `*visibility.Registry` and one
`*filter.Adapter` after the central registry is built. The same
adapter is injected into:

- `adapter.EventBridge.WithVisibility(visFilter)` (MQTT gate)
- `rest.Deps.DataPointVis` (REST list gate)
- `adapter.ParamsetsDomain.SetVisibilityGate(visReg)` (write-path gate)

## Consequences

**Positive**

- MQTT is noise-free out-of-the-box: globally hidden parameters (e.g.
  `ON_TIME_LIST_1`, internal CCU bookkeeping values) are never
  published to the broker, reducing topic chatter for MQTT consumers
  and Home-Assistant automations.
- REST `GET .../data_points` returns a clean, user-facing list by
  default. Operators who need the full model opt in with `?include=all`.
- UnIgnore entries (loaded from the aiohomematic-compatible un-ignore
  file) visibly affect the MQTT topic plane, the REST listing, and the
  write gate uniformly.
- Single source of truth: `visibility.Registry.IsAllowed` answers the
  visibility question for all three paths (write gate, MQTT, REST).

**Negative**

- The WS stream is unfiltered. Operators building tooling on the WS
  API see all events. This is intentional (see §3) but may surprise
  users who expected WS to match the REST list.
- Audit log does not record visibility denials. Writes rejected by the
  defense-in-depth gate are visible in the request log but not in the
  audit trail. Acceptable for v1.0.

**Neutral**

- Hot-reload of visibility rules is out of scope for v1.0.

## Implementation references

- Filter package: `internal/north/filter/visibility.go` + `visibility_test.go`
- Filter interface: `filter.VisibilitySet`
- Nil-safe adapter: `filter.Adapter.Visible`
- REST gate: `internal/north/rest/handlers/devices.go::ListDataPoints`
- REST router field: `internal/north/rest/router.go::Deps.DataPointVis`
- MQTT gate: `internal/central/adapter/eventbridge.go::EventBridge.WithVisibility`
- Daemon wiring: `cmd/openccu-loom/daemon.go` (`visReg`, `visFilter`)
- Write-gate: `internal/central/adapter/paramsets.go::SetVisibilityGate`
- Sentinel: `pkg/hmerr.ErrParameterHidden`
- Contract tests:
  - `internal/north/rest/handlers/devices_test.go::TestVisibilityFilterAppliedAtRESTListDPs`
  - `internal/central/adapter/eventbridge_test.go::TestVisibilityFilterAppliedAtMQTTOutbound`
  - `internal/store/visibility/cross_check_test.go::TestVisibilityFilterAppliedAtMQTTOutbound`
  - `internal/store/visibility/cross_check_test.go::TestVisibilityFilterAppliedAtRESTListDPs`
  - `internal/store/visibility/cross_check_test.go::TestVisibilityGateIsWiredIntoWritePath`
  - `internal/north/filter/visibility_test.go` (4 unit tests)
