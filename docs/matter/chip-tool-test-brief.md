# chip-tool Test Brief — OpenCCU-Loom Matter Bridge

**Audience:** Anyone (human or AI agent) driving a real
[`chip-tool`](https://github.com/project-chip/connectedhomeip) commissioner
against the OpenCCU-Loom Matter bridge to verify commissioning, read/write,
invoke, and subscription behaviour **independently of Apple Home**.

chip-tool is the Apple-independent verifier: unlike Apple Home (a black-box
commissioner), chip-tool reports the specific IM status codes and MRP
transport events that let you attribute a failure to a concrete daemon-side
gap. Run this brief first, fix daemon-side gaps, then bring Apple into the
loop via [`apple-pair-test-guide.md`](./apple-pair-test-guide.md).

This brief is the operational companion to the per-DP-type coverage matrix in
[`chiptool-send-receive-matrix.md`](./chiptool-send-receive-matrix.md) and to
[ADR 0048](../adr/0048-chiptool-godevccu-send-receive-matrix.md) (send/receive
matrix decision). The hermetic Go implementation lives under `tests/chiptool/`
(`//go:build chiptool`) and runs only in the arm64 `chiptool` CI workflow;
the sections below describe the manual/live-CCU counterpart.

---

## Environment

- **Commissioner:** a Linux host (arm64 or amd64) with a built `chip-tool`.
  macOS cannot run the suite (no Linux mDNS + Thread/BLE stack); this is a
  hard constraint — the `chiptool` CI job is the only real-commissioner
  guard.
- **Bridge:** an OpenCCU-Loom daemon with `north.matter.enabled: true`,
  reachable on the same L2 segment as the commissioner (mDNS must not be
  isolated; see the mDNS diagnosis recipe in
  [`../contributor/matter-mdns-test-setup.md`](../contributor/matter-mdns-test-setup.md)).
- **South side:** either the live CCU (writes gated — see below) or the
  in-process `godevccu` simulator for hermetic runs.

Pair the bridge once per sweep, run the test roster, then unpair:

```sh
chip-tool pairing onnetwork <node-id> <setup-pin>
# ... run T1–T8 ...
chip-tool pairing unpair <node-id>
```

---

## Live-CCU write safety (read this before any SEND / write test)

The developer's CCU runs real, in-use devices. **Reads are free**
(`read`, `subscribe`, `read-event`, and all OpenCCU-Loom REST `GET`).
**Writes need explicit user approval AND the user must name the specific
target device** — this brief authorizes the *test type*, never the *device*.
See `CLAUDE.md` §"Live-CCU writes need explicit user approval". Self-chosen
targets are unsafe: what looks like an interchangeable `HMIP-PS` can be a
`Weinkühlschrank` that must not be power-cycled.

- The current sanctioned write-target slot is `00021BE9957782:4`
  ("Bücherregal", an HMIP-PS bookshelf lamp). Use it as the default and
  propose an alternative with reasoning if it does not fit.
- After the sweep, leave the switch in a deterministic **OFF** state with
  one final explicit `chip-tool onoff off` before unpair — do not trust a
  toggle to land you OFF.

For any test path that does not require real wire-CCU validation, run against
`godevccu` instead (`tests/integration/`, `-tags=integration`, or the
`tests/chiptool/` harness) — a parallel path, not a substitute for the
Apple-independence goal of T6.

---

## Test roster

T1–T5 are read-only and need no device approval. T6 involves a live write.
T7–T8 exercise subscription establishment, priming, and resume.

### T1 — Commissioning round-trip

`pairing onnetwork` completes PASE → CASE → AddNOC → CommissioningComplete
without error. Confirm the bridge advertises Endpoint 0 with a populated
`BasicInformation` cluster and an Aggregator (device type `0x000E`) carrying
≥1 Bridged-Device endpoint.

### T2 — Descriptor / topology read

`descriptor read parts-list 0x1 <node>` returns the full bridged-endpoint
list; each bridged endpoint exposes `BridgedDeviceBasicInformation` (0x0039)
with a non-nil `NodeLabel` (0x0005) and a non-empty, **distinct** `UniqueID`
(0x000F). Duplicate UniqueIDs cause an Apple Home pair-abort (catalogued in
`docs/parity/by_design.md`, matter.js section).

### T3 — Attribute read (per-cluster)

For each device-type row in `chiptool-send-receive-matrix.md`, `read` the
representative attribute and confirm it reflects the seeded south-side value
(e.g. `onoff read on-off`, `levelcontrol read current-level`,
`temperaturemeasurement read measured-value`). Nullable telemetry reads back
TLV-null when unobserved; non-nullable state (OnOff, BooleanState) reads its
default, never TLV-null (a stray null trips CHIP `0x26`).

### T4 — Event read

`read-event` on the event-bearing clusters (e.g. `GenericSwitch` 0x003B,
`BasicInformation.StartUp`) returns the recorded event log without error.

### T5 — Negative writes (read-only-cluster gate)

Writing a read-only attribute or invoking an unmapped command returns the
correct IM failure status rather than silently succeeding — e.g. a
`thermostatuserinterfaceconfiguration write temperature-display-mode`
returns `UNSUPPORTED_WRITE (0x88)`, a `groups add-group` returns the stub
rejection. The dispatcher's `schema.AttributeWritable` gate is the guard.

### T6 — Live write cycle (on / off / toggle) — **write authorization**

A real on/off/toggle cycle through the full **bridge → CCU → device** chain,
the core Apple-independence check that the daemon actually lands writes on a
physical device:

```sh
chip-tool onoff on     <node> <endpoint>
chip-tool onoff off    <node> <endpoint>
chip-tool onoff toggle <node> <endpoint>
chip-tool onoff off    <node> <endpoint>   # deterministic OFF before unpair
```

Each command drives `im.Dispatcher.Invoke` → the cluster server's
`MatterInvoke` → `generic.Switch.Set` → `writer.SetValue(STATE, bool)` on the
CCU. Assert CCU ground truth (REST `GET .../STATE/value`, or
`MockCCU.GetDPValue` in the hermetic harness) after each command — not just a
re-read of the Matter attribute, which can echo an optimistic value.

**Authorization scope:** this section authorizes the *on/off/toggle test
type*. The *device* is a separate decision the user owns; confirm the target
address + channel before running (default slot `00021BE9957782:4`
"Bücherregal"). End OFF.

### T7 — Subscription establishment + initial priming

`subscribe on-off <min> <max> <node> <endpoint>` must:

1. return a `SubscribeResponse` with the negotiated min/max interval
   (loom clamps to `MaxIntervalCeilingSeconds`, default 3600 s, and a
   `MIN_INTERVAL` floor of 2 s), and
2. deliver the **initial priming report** (the current value of every
   subscribed path) before the response, so the controller's attribute cache
   is populated immediately.

**Priming race to watch for:** the subscription manager stamps
`lastReport = now` at admission (`im/subscription/manager.go`) so the 250 ms
engine tick cannot fire an empty keep-alive `ReportData` in the window
between `Subscribe()` and the bridge's follow-up `TouchLastReport`. Without
that stamp, chip-tool's MRP layer drops the premature keep-alive
("Dropping message without piggyback ack when we are waiting for an ack")
and the whole subscription eventually times out. T7 passes only when the
subscription stays alive and the first *value-driven* report (see T8)
arrives on the negotiated cadence.

### T8 — Proactive report + resume across restart

With a live subscription from T7:

- **Proactive report:** drive a south-side change (a real device event, or
  `MockCCU.FireDeviceEvent(addr, key, value)` in the harness — read-only
  telemetry params need `force=true`). The controller must receive a
  `ReportData` carrying the new value within the max interval. Cluster
  servers that implement `MatterClusterServer` (e.g. `switch.Switch`) narrow
  the dirty set to their own cluster; non-server DPs (Light, Cover) dirty the
  full endpoint and co-report sibling attributes — assert or tolerate the
  co-report per the matrix row.
- **Resume:** restart the daemon against the same store. A persisted CASE
  subscription resumes its cadence and continues emitting `ReportData`
  without a fresh `SubscribeRequest` from the controller (subscriptions
  survive a daemon bounce; chip-tool `subscribe` keeps reporting).

---

## After the sweep

- Live target left in a deterministic **OFF** state (final explicit
  `chip-tool onoff off`).
- `chip-tool pairing unpair <node>` to release the fabric.
- Any daemon-side gap found here that is a deliberate divergence goes into
  `docs/parity/by_design.md`; any behavioural fix adds or extends a parity
  test under `internal/north/matter/.../parity_matterjs_test.go` and/or a
  `tests/chiptool/` case (see `chiptool-send-receive-matrix.md`).
