# ADR 0084 — Matter: the profiles move onto go-fabric's device layer, the bring-up onto its node facade

- **Status**: accepted (2026-10-12); phases 1 and 2 are this repository's
  work, phase 3 done on go-fabric main b11840f (#87, #89 closed), pinned
  on the go-fabric v0.4.0 release since 2026-10-14; amended for go-fabric
  v0.5.0 (see [Amendment](#amendment--go-fabric-v050))
- **Related**: [ADR 0012](./0012-matter-pure-go-implementation.md) (pure-Go
  Matter), [ADR 0049](./0049-matter-one-endpoint-per-device.md) (one endpoint
  per device), [ADR 0063](./0063-self-maintained-device-profiles.md) (the
  profiles are hand-maintained),
  go-fabric [ADR 0016](https://github.com/SukramJ/go-fabric/blob/main/docs/adr/0016-device-type-validation.md),
  [ADR 0017](https://github.com/SukramJ/go-fabric/blob/main/docs/adr/0017-generated-default-cluster-server.md) and
  [ADR 0018](https://github.com/SukramJ/go-fabric/blob/main/docs/adr/0018-device-layer-and-node-facade.md)
  (the device layer and the node facade this ADR adopts)
- **Supersedes**: nothing; it binds the migration go-fabric ADR 0018 names
  ("openccu-loom migrates at least its lights and covers before v1.0.0")

## Context

Since go-fabric v0.3.0 (#951) the Matter stack offers three things this
daemon does not use:

- **`device/`** — one generated Go type per Matter device type, carrying what
  matter.js's `devices/*.ts` state: the mandatory and optional clusters, the
  mandated features, the attribute constraints and defaults. A host calls
  `device.NewDimmableLight(opts, …)` and gets an `endpoint.Spec`; a cluster
  the host must drive itself is supplied with `device.WithServer`.
- **`node/`** — the facade that performs the forty-call bridge bring-up in
  the documented order with every port wired (`node.New`, `Start`, `Stop`,
  an `Aggregator` with `Add` / `Remove` / `MarkComplete`).
- **`cluster/onoff.Server`** — matter.js's `OnOffServer` as a self-held
  server: the six commands, the 100 ms timed-on and delayed-off countdowns,
  StartUpOnOff, GlobalSceneControl and the scene paths, with a host `Sink`
  that is asked before a change is stored.

The daemon reaches the stack through `contract.EndpointSource` and a
hand-built `endpoint.Spec` instead. Measured on main after #951:

- `cmd/openccu-loom/daemon_matter.go` is 4 442 lines. Of the 37
  responsibilities of `startMatterBridge`, 29 are stack-generic (sessions,
  PASE / CASE adapters, ACL, root clusters, fabric hooks, diagnostics
  counters, event-number persistence); 4 are this daemon's (the enable and
  database gate, the endpoint-identity and exposure store, the per-central
  readiness latch, the i18n channel word); 4 mix the two (health probe and
  RTT gauges, the stop closure, the bundle).
- The profiles carry **14 hand-written cluster servers** (OnOff five times:
  custom switch, generic switch, light, siren, sound player; LevelControl
  and three ColorControl variants for the lights; WindowCovering twice;
  Thermostat and its UI configuration; SmokeCOAlarm) and already delegate
  **4** to go-fabric servers behind a host port (DoorLock, ClosureControl,
  LevelControl for the sound player, ModeSelect). Every one of the five
  OnOff servers re-implements the same six commands; the light alone keeps
  its own 100 ms timed-off engine (`matter_timed_onoff.go`).
- `internal/north/matteradapter` knows which clusters a device type carries
  only implicitly, through what each profile returns; nothing validates a
  profile's cluster set against the device type it advertises except the
  per-package parity tests, which pin what the profile does, not what the
  type mandates.
- A controller-written bridged-endpoint NodeLabel is not persisted: the
  assembler never sets `endpoint.Config.OnNodeLabelWritten` or
  `Spec.NodeLabel`. Only the root NodeLabel and Location persist.

Three facts about go-fabric v0.3.0 decide the shape of the migration (all
measured in the module, not in its changelog):

1. **The assembler path is one path.** `endpoint.ClusterServers` takes
   `Source.MatterClusterServers()` for a `device.Device` exactly as for a
   hand-built Spec, mounts Groups and ScenesManagement from the node's group
   state in both cases, and the stack owns Identify, Descriptor and
   BridgedDeviceBasicInformation in both cases. A `device.Device` differs
   only by what its Spec carries in addition: Parts, OptIn, ClientClusters,
   DeviceConditions, NodeLabel, Measurement, and device-type validation at
   assembly (go-fabric ADR 0016). `device.WithServer` accepts any
   `contract.ClusterServer`, so every one of the 18 servers above plugs in
   unchanged.
2. **`node.Config` has no host callbacks.** It exposes the configuration a
   reference daemon needs and accessors to the ports (`Bridge()`, `Root()`,
   `CommissioningWindow()`, `Store()`, …), but the hooks this daemon wires
   for its REST and WebSocket surfaces are either not wired by the facade
   (`Bridge.SetOnFabricAdded` is never fired by it) or wired by the facade
   itself and overwritten by a host that sets them
   (`OperationalCredentials.SetOnFabricRemoved` / `SetOnFabricUpdated`,
   `GeneralCommissioning.SetOnCommissioningComplete`,
   `CommissioningWindow.SetTransitionHook`). The facade also fixes product
   choices this daemon exposes as configuration: TimeSynchronization is
   always mounted (`north.matter.enable_time_sync` is an opt-in here), the
   node opens its own 48-hour window at first boot and announces CM=1 only
   while a window is open (this daemon announces only operator-opened
   windows), and a window with per-window random credentials
   (`north.matter.commissioning.ephemeral_window`) has no facade API.
3. **The default `onoff.Server` has no Sink.** `device.NewOnOffLight`
   mounts `onoff.Server` with `Bridged: true` and nothing else; a host whose
   device must act on a command builds `onoff.New(onoff.Config{Sink: …})`
   itself and supplies it with `WithServer`. `Server.Set(on)` is the host's
   own change (stored and reported, Sink not called), so the CCU's
   confirmation never echoes into a second CCU write.

## Decision

**The migration happens in three phases, each its own pull request series,
each live-verified against Apple Home before the next starts. The profiles
move first, the bring-up last, because the first two phases need nothing
from go-fabric and the third does.**

### Phase 1 — the assembler constructs devices through `device/`

`internal/north/matteradapter` stops building `endpoint.Spec` by hand for
a source that is a `contract.EndpointSource`. It maps the source's
`MatterDeviceType()` onto the generated constructor (one table:
`0x0100 → device.NewOnOffLight`, `0x0101 → NewDimmableLight`,
`0x010C → NewColorTemperatureLight`, `0x010D → NewExtendedColorLight`,
`0x010A → NewOnOffPlugInUnit`, `0x0202 → NewWindowCovering`,
`0x0230 → NewClosure`, `0x000A → NewDoorLock`, `0x0301 → NewThermostat`,
`0x0076 → NewSmokeCoAlarm`, `0x0022 → NewSpeaker`, `0x0027 → NewModeSelect`,
`0x000F → NewGenericSwitch`, `0x0510 → NewElectricalSensor`, and the sensor
types by measurement class), passes `device.Options{StableKey, Name,
Vendor, DeviceAddress, ChannelAddress, Reachable, NodeLabel}`, every server
the source returns as `device.WithServer`, the source as
`device.WithChangeNotifier`, a measurement source as
`device.WithMeasurement`, the features a type refuses to default
(`WithFeatures` for WindowCovering, Thermostat, SmokeCoAlarm,
GenericSwitch, ElectricalSensor — read from the profile's own FeatureMap
answer, never from a constant here), and hands `dev.Spec()` to the inner
assembler. The walk, the exposure allowlist, the per-central snapshots, the
secondary-channel policy, the button and electrical consolidation, the
battery placement, the StableKey and the labels are unchanged: the
`matterendpoint.SourceKey` string stays the UniqueID hash input.

What phase 1 adds, and what it must not change:

- **Device-type validation** runs at assembly (go-fabric ADR 0016). Mode
  `warn` in production for one release, `error` in every test that
  assembles a profile, so a profile whose cluster set contradicts its type
  fails a test instead of a pairing. A profile the validator refuses is a
  finding, not a reason to relax the mode.
- **Bridged NodeLabel persistence**: `endpoint.Config.OnNodeLabelWritten`
  stores the label under the endpoint's key; `device.Options.NodeLabel`
  hands it back on the next assembly. The gap closes in this phase because
  the option is the carrier.
- **Wire shape is unchanged** except where the type order of
  `device.Device.MatterClusterServers` differs from a profile's own list
  order — the Descriptor ServerList is the only attribute that can move.
  The topology golden decides; a re-record needs the diff to be an order
  change only. UniqueIDs, endpoint numbers, cluster sets, revisions,
  FeatureMaps and attribute values do not change.
- **Sources keep their shape.** A profile still implements
  `contract.EndpointSource`; the assembler, not the profile, calls
  `device.New`. The profiles' knowledge of *which* clusters they serve
  stays theirs; the knowledge of what the type mandates becomes go-fabric's.

### Phase 2 — the OnOff family runs on `cluster/onoff.Server`

The five hand-written OnOff servers (custom switch, generic switch, light,
siren, sound player) are replaced by `onoff.New(onoff.Config{Features,
Sink, Bridged: true, DataVersion})`, built **once per source** and reused
across reassemblies (a reassembly re-reads `MatterClusterServers`; a server
rebuilt each time loses its timers and its data version). The Sink is the
profile's CCU write at `CommandPriorityHigh` (`TurnOn` / `TurnOff`); a
failed write refuses the change, so the command answers FAILURE and the
attribute keeps the CCU's truth. The CCU's confirmed state reaches the
server through `Server.Set(on)` from the data point's confirmed-update
hook — the one path that stores without asking the Sink.

Consequences inside the profiles:

- The light's `matter_timed_onoff.go` engine, the LT attribute atomics of
  every OnOff server and the five copies of the six-command switch are
  deleted; OnWithTimedOff, OffWithEffect, OnWithRecallGlobalScene,
  StartUpOnOff and GlobalSceneControl are the stack's, including the scene
  paths the assembler injects (`SetScenes`).
- The light's LevelControl server, when it switches the device through a
  `…WithOnOff` command, calls `onOff.Set(true/false)` so the OnOff
  attribute follows, as the reference daemon's `publishOnOff` does; it
  must not call the Sink.
- The siren keeps collapsing On / Off / Toggle onto `TurnOn(OnConfig{})` /
  `TurnOff` in its Sink; a timed-off now ends the alarm after OnTime
  through the stack's countdown, which it did not before. The sound player
  keeps an empty FeatureMap (no LT).
- `onoff.Server` reports through `contract.AttributeChangeNotifier`; the
  profile's `OnMatterValueChanged` stays for its other clusters.

Two follow-ups are named here and decided per profile later, each with its
own `by_design.md` review of the Homematic ramp and transition semantics:
the light's LevelControl onto `levelcontrol.NewServer` with a `LevelSource`
port (the transition engine, `Move` / `Step` with a rate and `RemainingTime`,
which the hand-written server answers as no-ops today), and the three
ColorControl variants onto `light.NewColorControlServer` with the
`ColorWriter` / `ColorTemperatureWriter` ports. The WindowCovering servers
stay hand-written: their two-phase GoTo debounce and target inference are
this daemon's accepted-before-written contract with the CCU
(`BD-Matter-WindowCovering-SliderDebounce`), which go-fabric's
`cover.WindowCoveringServer` does not model. The Thermostat server stays
hand-written until the SystemMode → `CONTROL_MODE` mapping has a port in
`cluster/thermo`.

### Phase 3 — the bring-up runs on `node.New`

`startMatterBridge`'s stack-generic responsibilities became
`node.New(node.Config{…})` plus `Start` / `Stop`; what stays in
`cmd/openccu-loom` is this daemon's: the enable and database gate, the
endpoint-identity and exposure store, the per-central readiness latch,
the i18n channel word, the topology assembler handed to the facade as
`node.Config.Topology` (one snapshot per central with its own
completeness — the facade's Aggregator is not used), the shared group
manager (`node.Config.Groups`, so the endpoints' Groups servers and the
root GroupKeyManagement run on one state), the attestation material, the health probe and gauges, the REST / WebSocket
adapters over `n.Bridge()`, `n.Root()`, `n.CommissioningWindow()`,
`n.Store()`, `n.Sessions()` and `n.Subscriptions()`, the REST window over
`CommissioningWindow().OpenOwnWindow` (configured credentials) and
`n.OpenBasicWindow` (the ephemeral window), the REST revoke and factory
reset over `n.RemoveFabric`, the WebSocket fabric events over
`node.Config.Observers`, and the live-adopt hook. The facade is handed the
two stores, never the database handle: `node.Store.DB` would apply
go-fabric's schema, which the daemon carries in its own migrations.

The phase was blocked twice on seams the facade lacked. First the host
hooks of [go-fabric #58](https://github.com/SukramJ/go-fabric/issues/58)
(fabric observers, a commissioning-complete and a window-transition
observer, per-window PASE credentials, TimeSynchronization as an opt-in,
`SupportedFabrics`), closed in go-fabric 0.3.1. Then, read against main at
c7e67ac, four daemon responsibilities with no seam, filed as
[issue #87](https://github.com/SukramJ/go-fabric/issues/87) and closed by
#88: a host-assembled topology (`Config.Topology`; the facade's label /
configuration-version / scene persistence is keyed by `SourceKey.String()`
since, so the daemon's key type is persisted too), the pinned
`BasicInformation.UniqueID`, the session and subscription managers for
the diagnostics endpoint, and host-initiated fabric removal. And on the
way onto the facade two more, [issue #89](https://github.com/SukramJ/go-fabric/issues/89),
closed by #92: `Config.Groups` / `Node.Groups()` for the shared group
manager, and responder session parameters that advertise `Network.TCP`'s
roles (the daemon had set them on every PASE and CASE adapter itself).

What the facade fixes is **adopted**, as this ADR announced — it is
matter.js's and chip's:

- commissionable only while a window is open, and the node's own window
  at first boot when no fabric exists (48 h, the configured passcode); the
  operator's window request restarts that window with the requested
  duration, and a commissioned daemon stops being commissionable with its
  configured passcode for its whole lifetime, which is a security gain;
- per-exchange PASE always, so `north.matter.commissioning.concurrent_pairings`
  has no effect; CASE answers with the identity each commissioner
  installed, so `north.matter.case.*` has no effect — both stay accepted
  for existing configurations and say so in their help texts;
- the facade refuses to build without a valid passcode and a salt of
  spake2's size, whereas the daemon runs without a configured passcode
  (the operator pairs through the ephemeral window, or not at all): a
  missing or invalid passcode becomes a random one for the process, the
  REST opener keeps refusing the configured-credentials window, and a
  salt outside the bounds becomes a random one — which a commissioner
  cannot tell, the salt travels in PBKDFParamResponse;
- the root endpoint carries the facade's full server set
  (LocalizationConfiguration with the daemon's locale, TimeFormat- and
  UnitLocalization, SoftwareDiagnostics, EthernetNetworkDiagnostics,
  DiagnosticLogs, PowerSourceConfiguration, FixedLabel, UserLabel), the
  Aggregator FixedLabel and UserLabel; TimeSynchronization stays the
  operator's opt-in;
- RebootCount counts as matter.js does (0 on the first boot of a store,
  +1 per later boot) under the facade's settings key, so it reads 0 once
  after the update; the operational time keeps its key and carries over;
  the
  development self-signed attestation fallback is gone; the CSA test chain
  is served when no attestation file is configured (since the amendment
  below, a configured chain that does not load shuts new commissioning).

The 50-odd test call sites of `startMatterBridge` kept their helper: it
boots the facade the same way and hands back the same bundle (bridge,
stores, assembler, the facade's managers), so the effect pins of #951
(`bridgetest.Commissioner` against the real boot) keep their meaning. The
tests of the bring-up the daemon no longer performs — CASE identity
loading and resolution, PASE adapter construction, the root cluster set,
the fabric teardown, the verifier installer, the ephemeral provider — went
with it; go-fabric's node package carries theirs.

### Live verification

Every phase ends with the same run against the developer's CCU and Apple
Home, under the live-write rule of `CLAUDE.md`: writes only to the
sanctioned slot (`00021BE9957782:4 Bücherregal`, HMIP-PS), left OFF by one
final explicit command before unpairing; every other device read-only.

1. Topology golden and the chip-tool suite green in CI (the suite's
   positive Groups / Scenes tests are added in phase 1, since the
   stub-rejection tests left with #951).
2. Pair with Apple Home from a fresh fabric; confirm the bookshelf lamp
   appears as the device type the profile advertises and that every
   exposed accessory of the fleet keeps its name and room.
3. Phase 1: switch the lamp on and off from Apple Home and from the CCU;
   read a thermostat, a cover and a sensor. Phase 2: the same, plus
   OnWithTimedOff via `chip-tool onoff on-with-timed-off` (lamp returns to
   OFF after OnTime without a CCU-side timer), a scene stored and recalled
   from Apple Home. Phase 3: unpair, restart, re-pair; the fabric list,
   the subscriptions after a restart and the WebSocket fabric events are
   what they were.
4. Unpair, confirm the lamp is OFF.

A phase whose live run is not green does not merge; a finding goes into
`by_design.md` or back into go-fabric, never into a relaxed test.

**Result (2026-10-15, OpenCCU-Loom 0.91.0, Apple Home, phases 1–3
together):** the existing accessories (switches, thermostats, the lock,
lights) stayed available and respond with no perceptible delay; newly
added devices appear in Home after a while; unpairing and re-pairing
worked without a hitch. One finding: an HmIP-BSL offers brightness in
Home but no colour picker — the fixed-colour light is bridged as a
DimmableLight (OnOff + LevelControl) while ADR 0012's mapping table
promises ExtendedColorLight with a palette-quantised ColorControl. That
is a gap against ADR 0012, not a phase-3 regression (the projection
predates the migration); closed by the palette-quantised ColorControl of
`FixedColorLight` (`matter_fixedcolor.go`, the chip-tool test
`TestSendReceive_FixedColor`), released as 0.91.1 and confirmed live at
the owner's HmIP-BSL on 2026-10-15: the colour picker is there and a
colour change reaches the device.

## Amendment — go-fabric v0.5.0

A review of the glue on 2026-10-15 found library code on this side of the
boundary: copies of what the facade already did, and generic mechanics
the facade did not yet expose. Six go-fabric issues (#117–#122) moved it;
go-fabric v0.5.0 ships them, and this daemon now uses them:

- **UniqueID**: the daemon's own pinning is gone; `BasicInformation.UniqueID`
  stays empty and the facade pins it under the same settings key, so the
  identity a paired controller knows carries over (#122).
- **Scenes and bridged NodeLabels**: `node.NewEndpointPersistence` serves
  both the daemon's assembler and the facade (`node.Config.EndpointPersistence`);
  the `node_label` column and the daemon's scene store are gone (#117).
- **Endpoint identity**: `matterendpoint.Store` is go-fabric's
  `endpoint/sqlitestore` under this daemon's key type
  (`sqlitestore.WithKeyDecoder(matterendpoint.DecodeSourceKey)`); migration
  049 rebuilds `matter_endpoints` to go-fabric's schema row for row, keeping
  every endpoint number, moving the NodeLabels into the settings table and
  the counter into `matter_endpoint_allocation`. The store refuses a key
  whose rendering does not decode back to itself, so no row can come back
  as a foreign key and be garbage-collected (#121).
- **DNS-SD re-announce**: the facade runs the loop; the daemon no longer
  type-asserts the advertiser (#119).
- **Attestation**: `attestation.LoadChainFiles` replaces the daemon's
  loader (#118). A configured chain that does not load no longer falls back
  silently to the CSA test chain: pairing as the test vendor while the
  operator believes a production chain is served would only surface as a
  controller rejecting the device in the field. Stopping the bridge was
  rejected, because attestation is checked only while commissioning — the
  controllers already paired would lose every accessory for a fault that
  cannot affect them. The bridge therefore keeps serving them and shuts new
  commissioning through the facade before `Start`
  (`Node.SetCommissioningBlocked`, go-fabric v0.5.4, #133): the node's own
  window, the REST opener (409) and a paired controller's
  AdministratorCommissioning (multi-admin sharing) are all refused, and the
  `matter.bridge` health component reports the cause (degraded, not 503).
- **SmokeCOAlarm**: the last hand-written cluster server of the profiles is
  go-fabric's `cluster/alarm.Server`; the smoke siren gains the SmokeAlarm
  and AllClear events and stops reporting a null ExpressedState.
- **LevelControl (#120, #127)**: the light runs on
  `levelcontrol.NewNativeSource` since go-fabric v0.5.1, which fixed the two
  places where v0.5.0 departed from matter.js (#127: a rated Move on an
  unknown level, the switch-off at or below MinLevel). The light implements
  only `NativeRamp` (one atomic LEVEL / RAMP_TIME write per command) and an
  attribute-only `OnOff`; `BD-Matter-LevelControl-NativeRamp` carries the
  details. Two behaviours change towards matter.js: a Step cropped at a
  bound runs for the shorter time its rate gives (`stepLogic`), and a Step
  on an unknown level answers Failure (`currentLevel` getter). The light
  also uses go-fabric's `LevelFromUnit` / `UnitFromLevel` and the saturation
  and mireds scalers, each equal to the daemon's on every input; the hue
  scaler stays (the daemon wraps 360° to 0, go-fabric crops).
  The dimmer keeps switching its OnOff attribute off itself on a "with
  On/Off" command to the minimum: the source does so only while OnOff still
  reads on, and the dimmer's OnOff reads its optimistic level, which the
  accepted LEVEL 0 write has already turned off. The sound player runs on
  `NativeSource` since go-fabric v0.5.3: it declines Move through
  `NativeMover` (#129) — on a speaker a jump to the bound is a jump to full
  volume — and takes the "with On/Off" commands through `NativeSwitcher`
  (#131), so "play at this volume" and "stop" stay one device write each.

## Amendment — go-fabric v0.10

go-fabric v0.10.0 removes the device kit's per-cluster options and the
facade's deprecated surface instead of deprecating them; its migration
guide ("Migrating to the facade") names a route for every removed symbol
and is what this daemon follows. Where the guide says a package stays
below the facade for a host with its own assembler, it stays here too.
v0.10.1 added the two routes this daemon's migration found missing
(go-fabric #220, #221), and the daemon is pinned to it.

- **Profile servers are declarations.** `device.WithServer` takes only a
  cluster without a generated package now; the assembler declares each
  profile server with `device.HostServer`, which resolves the cluster's
  generated `<cluster>def.Declare{HostServer}`.
- **Sensor endpoints stay on the device layer.** Phase 1 built a
  measurement endpoint with `device.WithMeasurement`, which v0.10.0
  removed; v0.10.1's `device.Measurement` is its successor for a host
  whose sensors are classified data, and the endpoint assembler
  materialises the measurement once the endpoint number is known (the
  momentary switch and the battery need it). Every bridged endpoint is a
  device-layer device, as phase 1 decided; the golden topology is
  unchanged, and an in-process controller (`fabrictest`) reads both
  shapes over CASE.
- **Facade routes.** The health probe is `node.Config.Health`; the
  attestation files are `node.Attestation.Files` with
  `BlockCommissioningOnError`, so the block above is the node's
  (`Node.AttestationError`); DNS-SD is `node.Network.MDNS`, the node owns
  the subtype responder; fabrics are `Node.FabricInfos`; credentials are
  `node.NewCommissioningCredentials`, `Commissioning.WindowCredentials`
  and `Node.PairingCodesFor`; the status and window adapters read
  `Node.LocalAddr` and `Node.Topology`.
- **What stays below the facade:** the bridge (endpoint inspector,
  reassembly, reachability forward, controller RTT), the shared store
  handle, the group manager the assembler is built over, the endpoint
  store and assembler, the profiles' `cluster/…` servers, and
  `secure/setup` / `secure/spake2` where a payload is encoded or a value
  validated without a node (the REST handler, the configuration
  validator).

## Consequences

- `internal/north/matteradapter` loses the implicit "which clusters does a
  type carry" knowledge and gains device-type validation as a guard; a new
  profile that advertises a type it does not satisfy fails
  `go test ./internal/model/...`.
- The profiles shrink by the OnOff family (about 1 000 lines across five
  packages plus the timed-off engine) and gain the stack's OnOff behaviour
  wholesale; their wire-shape pins (`parity_matterjs_test.go`, the schema
  snapshot, the topology golden) stay and are the gate for each phase.
- `cmd/openccu-loom/daemon_matter.go` shrinks to the daemon's own
  responsibilities in phase 3; the 47 test call sites of
  `startMatterBridge` move to a helper that boots the node facade the same
  way, so the effect pins of #951 (`bridgetest.Commissioner` against the
  real boot) keep their meaning.
- Two go-fabric requests leave this ADR: the facade host hooks above, and
  the SoilSensor label already filed as go-fabric issue #57.
- Rejected: a loom-local facade duplicating `node.New` with hooks (it would
  be the fourth copy of the bring-up order and age with every go-fabric
  release); migrating the profiles to `device.New` calls inside the
  profiles themselves (the profiles would then depend on the assembler's
  key and label rules); skipping phase 2 for the siren and sound player
  because they are "simple" (they are the two OnOff servers whose
  OnWithTimedOff behaviour is wrong today).
