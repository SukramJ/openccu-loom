# Behavior scenarios (Phase A)

End-to-end behavior tests for the Matter bridge. Each `*.json` in this
directory is a self-contained scenario the Go harness replays against
a real `*Bridge` instance with a real CASE session pair on UDP
loopback. New scenarios drop in as data files — no Go code change
required.

Scenarios live alongside the matter.js parity fixtures
(`im-wire-fixtures.json`) so the same audit walk covers both single-
message wire shape **and** multi-message conversation behavior.

## Test entrypoint

```
make scenarios
# or: go test ./tests/scenario/ -run TestScenarios -count=1 -race
```

The harness implementation: `tests/scenario/scenario_*_test.go`.

The Matter stack itself now lives in `github.com/SukramJ/go-fabric`; the
corpus stayed here because the coverage gate
(`tests/contract/matter_scenario_gate_test.go`) derives the required set
from `internal/model/custom`. The harness therefore drives the bridge
from *outside* the module, through its exported API only — which makes
this suite the first real consumer of that API as well as a behaviour
regression net.

One consequence is worth knowing before writing a scenario: the bridge
learns where to ship a subscription's reports only from an actual
`SubscribeRequest`, so the harness establishes every declared
subscription over the wire during setup (unless the spec sets
`skip_auto_subscribe`, in which case a step drives the Subscribe
itself). The setup exchange and its initial ReportData burst are
consumed before the first step runs.

## File format

```json
{
  "name": "<id, must match the filename stem>",
  "description": "<one-paragraph regression rationale>",
  "tags": ["<categorise: switch, subscribe, f4, ...>"],
  "given": {
    "session_id": 1,
    "peer_subscribe_exchange_id": 7,
    "subscription": { "endpoint": 2, "cluster": 6, "attribute": 0 }
  },
  "steps": [
    { "actor": "ccu",    "kind": "fire_attribute_change", "value": true },
    { "actor": "bridge", "kind": "expect_tx",
      "opcode": "ReportData",
      "initiator": true,
      "exchange_id_fresh": true,
      "exchange_id_neq_subscribe": true,
      "bind_exchange_id_to": "$fresh_exch",
      "bind_counter_to": "$counter" },
    { "actor": "peer",   "kind": "send_status_response",
      "exchange": "$fresh_exch", "status": "Success" },
    { "actor": "bridge", "kind": "expect_log",
      "msg": "matter.rx.im.status_ack", "match_exchange": "$fresh_exch" }
  ]
}
```

### `given` — pre-conditions

Two shapes are supported; the loader normalises both into the array form.

**Single-subscription** (legacy, used by most scenarios):

| Field | Meaning |
|---|---|
| `session_id` | The CASE session ID under which the conversation runs. The harness wires a complementary `bridgeSession` ↔ `peerSession` keyed pair, AES-CCM-128 with fixed keys per subscription index. |
| `peer_subscribe_exchange_id` | The exchange ID the commissioner used when it opened the Subscribe. Used to assert F4: ongoing reports MUST use a fresh exchange ≠ this one. |
| `subscription.{endpoint,cluster,attribute}` | The path the subscription is registered on. |

**Multi-subscription** (Phase E):

```json
"given": {
  "subscriptions": [
    { "session_id": 41, "peer_subscribe_exchange_id": 53,
      "subscription": { "endpoint": 6, "cluster": 6, "attribute": 0 } },
    { "session_id": 43, "peer_subscribe_exchange_id": 59,
      "subscription": { "endpoint": 7, "cluster": 6, "attribute": 0 } }
  ]
}
```

Each entry gets its own bridge↔peer session pair, distinct AES-CCM
keys, and its own subscription in the manager. Step kinds gain an
optional `subscription_idx` field (default 0) to target a specific
subscription — used by isolation scenarios that close one session
and verify another is unaffected.

### `steps` — sequence

Steps are dispatched by `actor` + `kind`. Phase-A supports four kinds:

| Actor | Kind | Effect |
|---|---|---|
| `ccu`    | `fire_attribute_change` | Drive the bridge as if the CCU just echoed a value change on `given.subscription`. Bypasses the engine: invokes `reportSubscription` directly with the subscription's path set. |
| `ccu`    | `fire_via_engine`       | Same intent, but routes through the production dirty-mark path: calls `mgr.OnAttributeChanged` and waits for the next engine tick (bounded by `MinIntervalFloor=1s`) to drain. Use this when the scenario must validate the engine-tick pipeline itself, not just the wire-shape boundary. |
| `ccu`    | `fire_notifier_source`  | Looks up the topology recipe's fake notifier (`given.fire_source_key` selects when ≥2; default the only one) and fires it. Drives the full production wireMeasurementListeners callback chain — value-change → notifier.OnMatterValueChanged → mgr.OnAttributeChanged → engine drain → reportSubscription. Requires `given.topology` to be set. |
| `bridge` | `expect_tx`             | Pop the next outbound datagram on the peer socket, decrypt it, decode the protocol header, assert the constraints (`opcode`, `initiator`, `exchange_id_fresh`, `exchange_id_neq_subscribe`, `tlv_tags_present`, `tlv_tags_absent`). Bind `proto.ExchangeID` / `hdr.MessageCounter` for later steps via `bind_*_to`. |
| `peer`   | `send_status_response`  | Ship an IM:StatusResponse from the peer to the bridge on the given exchange. Used to drive the bridge through its post-report ACK path. |
| `bridge` | `expect_log`            | Scan captured `slog` records for one matching `msg` (and optionally `match_exchange`). Polls for ~2 s to absorb async receive-pipeline dispatch. |
| `bridge` | `expect_no_tx`          | Assert the peer socket receives nothing for `timeout_millis` (default 500). Post-condition after fault-injection steps that should leave the bridge silent (closed session, evicted subscription). |
| `bridge` | `close_session`         | Drives `subMgr.CloseSession(given.session_id)` — mirrors the F1 cascade the daemon wires via `opMgr.SetOnSessionClose(subMgr.CloseSession)`. |
| `peer`   | `drop_next_tx`          | Read the next outbound datagram and discard it (no StatusResponse). The bridge's outbound-reliable tracker keeps the retransmit entry; `tick_retransmit` drives the re-ship. |
| `bridge` | `tick_retransmit`       | Yield for one MRP backoff so the bridge's own ack pump re-ships the dropped datagram. go-fabric exports a single-shot tick for the *inbound* ack half (`Bridge.RunAckPumpOnce`) but none for the outbound retransmit half, so the harness attaches the tracker before `Start` — the daemon's own order — and lets the pump run for real. |
| any      | `wait`                  | `time.Sleep(timeout_millis)` — used only when the scenario needs to span a deadline the harness can't otherwise observe. Prefer `tick_retransmit` and per-step polling. |

### TLV-body assertions in `expect_tx`

`tlv_tags_present` and `tlv_tags_absent` accept arrays of context-tag
numbers (uint8) to assert at the top level of the decoded IM payload.
Example: `"tlv_tags_present": [0, 4]` requires `tagReportSubscriptionID`
(0) and `tagReportSuppressResponse` (4) — the latter locks the F3 fix
(always-emit SuppressResponse). Phase-D may extend with path-keyed
value assertions; for now, structural tag presence is the floor.

### `$variables`

Steps can capture values via `bind_*_to` and later reference them via
the leading-`$` form. The loader validates that every `$ref` was bound
earlier in the same scenario.

## Coverage gate

`tests/contract/scenario_coverage_test.go::TestScenarioCoverage`
walks `internal/model/custom/<type>/`, picks the directories
containing a `matter.go`, and asserts every such type appears as a
tag on ≥1 scenario JSON. The gate fails CI on a new Matter-mappable
custom-DP type that lands without scenario coverage; the error
message lists the missing types and includes a copyable skeleton.

To add coverage for a new type, drop a scenario tagged with the
type name (e.g. `["matter", "subscribe", "f4", "...", "<type>"]`).
The harness picks the file up automatically — no Go code change.

## Adding a scenario

1. Pick a stable name following the convention
   `<custom_dp_type>__<change_source>.json`
   (e.g. `light__brightness_apple_write.json`,
   `cover__level_ccu_echo.json`).
2. Author the JSON. Smallest-possible step list — one regression
   guard per scenario, not three.
3. Run the harness; it picks up the new file automatically.
4. To prove the scenario locks the invariant you claim, briefly
   revert the production fix it covers and verify the scenario fails
   with an attributable error. Restore the fix; commit the scenario.

## matter.js reference sidecars

```sh
make scenarios-regen-sidecars
```

The recorder walks every scenario JSON in this directory, finds the
`bridge.expect_tx` steps that ship `ReportData`, and emits a
`<scenario>__matter_js_reference.json` sidecar capturing the wire
envelope matter.js HEAD would produce for the same logical message
(suppressResponse, interactionModelRevision, the
presence-or-absence of attributeReports, plus a placeholder
`subscriptionId=0xFFFFFFFF` that the bridge replaces at runtime).
Each sidecar pins the matter.js git revision at capture time
(`matter_js_pinned_at`), so a later byte-exact comparison run
against the bridge's actual outbound can detect drift the moment
the matter.js pin moves.

When to regen:
* New scenario lands with one or more `expect_tx` / `ReportData` steps.
* The local `../matter.js` checkout updates (e.g. HEAD bumps).
* The IM ReportData wire-shape changes in matter.js (revision bump,
  new TLV tag, structural change).

The harness does not currently compare sidecar bytes to bridge
output. That layer drops in once cluster-specific AttributeData
pre-encoding lands (the inner data bytes are a TlvStream matter.js's
own encoder cannot directly synthesise from a JS-typed value). The
sidecars exist today so the comparison layer can drop in without
re-recording history.

## Phase scope summary

| Phase | Status | Adds |
|---|---|---|
| A | landed | harness skeleton, 4 step kinds, switch__ccu_echo |
| B | landed | TLV-tag assertions, fire_via_engine, matter.js sidecar scaffold |
| C | landed | scenario library: ~12 base scenarios across Switch / Cover / Light / Climate / Lock / Siren |
| D | landed | fault-injection step kinds (`close_session`, `drop_next_tx`, `tick_retransmit`, `wait`, `expect_no_tx`); F1 + MRP-retransmit scenarios |
| E | landed | multi-subscription `given.subscriptions[]` + `subscription_idx` on every step kind; multi_session__close_one_does_not_evict_other |
| F | landed | coverage-gate test (TestScenarioCoverage); every custom-DP type with matter.go needs ≥1 scenario tagged with its name |
| G | landed | matter.js HEAD regen pipeline (make scenarios-regen-sidecars); one envelope sidecar per ReportData-shipping scenario, pinned to matter.js git rev. Byte-exact comparison stays queued for the next iteration once cluster-specific AttributeData pre-encoding is in. |
| H | landed | topology recipes via given.topology (single_temp_sensor); fire_notifier_source step kind exercises the production measurement-listener callback chain end-to-end; attribute_reports_count assertion in expect_tx. F2 cluster-narrowing for custom-DP-as-cluster-server stays unit-level (filterPathsByNotifierCluster). |
| I | landed | peer.send_write_request step kind + WriteResponse opcode recognition in expect_tx. switch__apple_write_unsupported locks "WriteRequest always elicits a WriteResponse" regardless of dispatcher path-resolution outcome. |
| J | landed | peer.send_subscribe_request step kind + SubscribeResponse opcode recognition. scenarioSubSpec.skip_auto_subscribe leaves the manager empty so the peer-driven SubscribeRequest registers the subscription via the production handler. subscribe__negotiation_request_response locks the SubscribeRequest → initial ReportData → StatusResponse → SubscribeResponse round-trip (F5/F6). |
| K | landed | min_interval_floor_seconds + max_interval_ceiling_seconds overrides on scenarioSubSpec; subscribe__min_interval_gates_initial_drain locks the §10.6.5 MinIntervalFloor gate at scenario level. |
| L | landed | peer.send_read_request step kind with optional fabric_filtered flag; read__simple_request_response locks "ReadRequest always elicits a ReportData reply" via the empty-topology path. Fabric-scoped variants (FabricScopedReader topology) queued for a later phase. |
| M | landed | single_onoff_endpoint_source topology (fake source = MatterEndpointSource + MatterClusterServer + MatterChangeNotifier + MatterClusterDataVersion). f2__cluster_server_narrows_dirty_paths locks F2 at scenario level (attribute_reports_count=1 on a 4-path subscription). bind_data_version_to extractor + assert_gt cross-step assertion. dataversion__monotonic_per_cluster locks the §10.6.5 DataVersion advancement contract. |
| N | landed | switch__apple_write_success — Apple writes OnOff=true through the writable single_onoff_endpoint_source topology, bridge replies WriteResponse, a follow-up notifier fire ships ReportData carrying the post-write DataVersion. |
| O | landed | wildcard flag on send_subscribe_request + drain_subscribe_chunks step kind (consumes N ReportData chunks, ACKs each, stops on SubscribeResponse). subscribe__wildcard_against_real_topology drives the F5/F6 per-chunk handshake loop end-to-end on whatever chunk count the dispatcher's wildcard expansion produces. |
| P | landed | subscribe__max_interval_keepalive — MaxIntervalCeiling=2 forces the engine's heartbeat (min(MaxInterval/2, 30s)) to fire within ~1.5 s. The scenario asserts an empty-payload ReportData (attribute_reports_count=0) lands on the peer socket without any prior dirty mark. Locks the matter.js-aligned heartbeat cadence Apple Home and chip-tool both require to keep the subscription alive. |
| Q | landed | read__fabric_filtered_request — peer ships ReadRequest with FabricFiltered=true (Matter §10.6.3); bridge MUST reply with a ReportData rather than drop or panic. Per-fabric content projection verification (FabricScopedReader returning different payloads per fabric index) queued behind a fabric-scoped fake cluster server topology. |
| R | landed | many_temp_sensors topology (30 bridged Temperature endpoints) + min_chunks assertion on drain_subscribe_chunks. subscribe__multi_chunk_burst_guaranteed locks the F5/F6 per-chunk handshake against a guaranteed multi-chunk initial burst. |
| S | landed | fabric_scoped_reader topology (FabricScopedReader fake cluster server returns the dispatch FabricIndex as the attribute value) + scenarioFabricResolver wires per-session fabric_index through the bridge's SessionFabricResolver hook + bind_attribute_value_to / expect_attribute_value extractor on expect_tx. read__fabric_scoped_projection drives two sessions with FabricIndex=3 and FabricIndex=7, asserts each ReadRequest with FabricFiltered=true returns the session-specific fabric ID. |
| T | landed | tlvSubscribeResponseMaxInterval walker + bind_max_interval_to / expect_max_interval extractor on expect_tx. subscribe__max_interval_in_response asserts the bridge's SubscribeResponse advertises the negotiated MaxInterval the peer requested (60s), locking the §10.6.5 cadence-clamp contract. |
| U | landed | two_centrals topology (two distinct CCUs each carrying one OnOff endpoint source) + multi_ccu__cross_central_isolation. Firing CCU A's notifier MUST emit exactly one AttributeReport (for CCU A's endpoint); CCU B's path stays clean. Locks cross-central isolation at the dirty-mark callback layer. |
| V | landed | engine_manual_only on scenarioSubSpec + engine_tick_at step kind. The harness anchors a t0 wall-clock just before Subscribe() and the step pumps subMgr.Tick at t0+at_millis. subscribe__keepalive_deterministic completes in <50 ms instead of 1.5 s by driving the heartbeat path synthetically. |
| W | landed | reconnect__subscribe_after_session_evict — closes session A (cascading subscription eviction), then drives a peer-side SubscribeRequest on session B. drain_subscribe_chunks asserts the SubscribeResponse lands on session B's exchange, locking the recovery-from-reconnect contract: post-evict subscribe negotiations on a fresh session must succeed without state bleed from the closed session. |

## Why scenarios, not more unit tests

The F4 bug (external CCU state changes invisible in Apple Home) lived
across nine layers — CCU callback, DataPoint update, custom-DP
wrapper, bridge notifier, engine tick, cluster-server read, IM
encode, MRP/wire, Apple's HMOutlet projection. Each individual layer
had a unit test. None caught the bug, because none exercised the
*conversation* across all nine. Scenarios fill that gap: one JSON,
one assertion at the outermost behavior, one regression sealed.
