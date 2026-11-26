# Feature adoption plan: go-fabric v0.22.2 → v0.25.0, go-fabric-ui v0.9.2 → v0.13.0

Read-only planning result, 2026-11-23. Nothing was edited, built or run.

Abbreviations for sources:

- `GF` = `/Users/markus/go/pkg/mod/thingfabric.dev/go-fabric@v0.25.0`
- `GU` = `/Users/markus/go/pkg/mod/github.com/!sukram!j/go-fabric-ui@v0.13.0`
- `loom` = the worktree `fix-1127-fixed-color-effects`, branch `claude/go-fabric-0.25.0`

Every statement below carries its source. Anything I did not read is marked **UNVERIFIED**.
Per the repo's model table, every reference statement here is a map for the coordinator;
the lines marked "Fable read" must be confirmed before they enter an ADR, `by_design.md`
or a comment.

---

## 1. What the two releases are, for a bridge host

- go-fabric 0.25.0 and 0.24.1 carry no behaviour: module path move only (`GF/CHANGELOG.md:13-42`).
- go-fabric 0.24.0 (`GF/CHANGELOG.md:44-243`) is the matter.js pin lift. Its Added section
  (`:60-102`) is almost entirely controller, OTA-provider, thermostat-preset and session
  internals. Host-usable: `node.Config.TimeFormat`, thermostat `SuggestionStore` /
  `ActivatePreset` / `LocalTemperatureChangeThreshold`.
- go-fabric 0.23.0 (`GF/CHANGELOG.md:245-1136`) is where the host-facing additions are
  (Added `:300-478`).
- go-fabric-ui 0.10.0–0.13.0 (`GU/CHANGELOG.md:8-584`): 0.12 and 0.13 are controller-family
  only (Inventory tab, `CommissionTimeout`, ICD multi-admin). Loom serves no controller
  family (`fabricui.Options` literal at `cmd/openccu-loom/daemon_matter.go:1004-1024` sets no
  `Controller`). Host-usable for the node family: `Options.MaxStreams`, `Options.Done`,
  the `subscribe(onEvent, onResync?)` second argument, `NodeStore.reassemble()`, and the
  0.8.0 options #1056 lists.

### State of loom's adoption today (measured)

`node.Config` literal, `cmd/openccu-loom/daemon_matter.go:406-449`: sets `Network`,
`Commissioning{Passcode, Discriminator, PBKDFIterations, Salt, LocationCapability}`,
`ProductDescription`, `BasicInformation`, `Store{Credentials, Endpoints}`, `Attestation`,
`Health`, `Locales`, `Limits{SupportedFabrics}`, `TimeSynchronization`, `Surface`, `Logger`.
Not set: `BootReason`, `Store.Sealer`, `Commissioning.WindowOnDemand` / `RotatingDeviceID` /
`Flow` / `PairingHints`, `TimeFormat`, `Faults`, `Metrics`, `Diagnostics`, `ICD`, `OTA`.

`fabricui.Options` literal, `daemon_matter.go:1004-1024`, plus the router's additions at
`internal/north/rest/router.go:1627-1635` (`Prefix`, `Admin`, `AllowedHosts`, `AllowHost`).
Not set: `Erase`, `Operations`, `Warnings`, `Counters`, `MaxStreams`, `Done`, `Registry`, `Nodes`.

SPA: `assets/ui/src/routes/matter/Matter.svelte:6-17` imports nine node pages including
`NodeTopology`; `assets/ui/src/lib/stores/fabric-node.svelte.ts:58-74` passes
`subscribe: (onEvent) => …` with one argument.

A grep over `cmd/` and `internal/` (non-test) for `SetNodeLabel`, `NodeSwitch`,
`SetReachable`, `BootReason` (as config), `Sealer`, `WindowOnDemand`,
`bridgeddevicebasicinformation`, `BridgedBasicInformation` returned no production use.

---

## 2. Issue status

### #1056 (go-fabric-ui 0.8.0 options)

| Item | State | Evidence |
| --- | --- | --- |
| Topology tab | done | `Matter.svelte:15,44`; issue comment (PR #1058) |
| `DiagnoseMDNS` at start | done | `daemon_matter.go:521` `logMatterMDNSInterfaces(n.DiagnoseMDNS, …)` |
| Live node label | open | `north.matter.node_label` is still a restart rule, `internal/config/restart.go:329`; no `SetNodeLabel` caller |
| `Options.Erase` + `NodeSwitch` | open | no `Erase` in the Options literal; `matterNodeSource{bundle.node}` is a fixed source (`daemon_matter.go:1005`) |
| `Options.Operations` | open, product decision | loom has `pkg/hmlog/livelog.go`, `levels.go` and its own handlers `internal/north/rest/handlers/diagnostics_logs.go`, `diagnostics_loglevels.go` |
| `Options.Warnings` / `Counters` | open | not in the Options literal; the premise "no list exists" has moved, see F3 |

### #1027 acceptance 2

Open. `spec.Availability = in.dev.Available` at
`internal/north/matteradapter/assembler.go:996, 1070, 1114`; three forwards to
`mb.NotifyDeviceReachable` at `daemon_matter.go:943` (boot loop) and `:969` (central hook),
through `wireMatterDeviceReachableForward` (`:841-852`). Verdict and migration in section 4.

### #1092

Open. `harness.Options.EnableMatter` is a no-op (`tests/e2e/harness/daemon.go:71-72`);
`tests/e2e/matter_boot_test.go:17-32` tests only the disabled case. Both "not established"
points of the issue are answered in section 5.

---

## 3. Candidate features

Effort S/M/L. Risk is the risk to paired fabrics and to the wiring, not to the build.

### A. Node facade (go-fabric)

| # | Feature (source) | What loom does | User value | Eff. | Risk | Recommendation |
| --- | --- | --- | --- | --- | --- | --- |
| F1 | **Bridged endpoint identity is the host's to state.** Breaking change: a bridged endpoint serves SerialNumber and NodeLabel only when the host states them; no SerialNumber is derived from the device address (`GF/CHANGELOG.md:723`; `GF/endpoint/materialize.go:146-150`). Route: `bridgeddevicebasicinformationdef.Declare{Initial}` on `device.New` (`GF/CHANGELOG.md:442`; `GF/device/bridged_basic_information.go:27-56`). Without it ProductName falls back to the device address (`materialize.go:151-158`). | Add one declaration in the two `device.New` paths (`assembler.go:850` `buildDevice`, `:1102`): SerialNumber = CCU device address, ProductName = CCU model, SoftwareVersionString = `Device.Firmware()` current (`internal/model/device/device.go:343`), ProductLabel. All values come from the CCU at run time (no static DB). Contract test on the served attributes; chip-tool run (composition changes). | High. Since the bump every bridged endpoint has lost its SerialNumber and shows the address as product name; controllers show model, serial and firmware per accessory. | M | Medium: fixed-quality attributes change once on paired fabrics. Whether `IncreaseConfigurationVersions` is owed: **UNVERIFIED**, Fable read of matter.js. Constraint lengths are enforced by `device.New` (`materialize.go:214-216`), so an over-long model string refuses the device. | **Adopt now.** It restores what the bump removed and replaces a fallback with the host's statement. |
| F2 | **`Device.SetReachable` as the one reachability input** (`GF/device/lifecycle.go:217-236`; `GF/docs/guides/migrating-to-the-facade.md:35,38,180`; `GF/docs/guides/advanced-seams.md:89-93`; `GF/CHANGELOG.md:1603`). | Section 4. | Correctness: attribute and event from one state, in order. | L | High (wiring, reconciler, three forwards). | **Adopt now**, as its own slice; closes #1027. |
| F3 | **`Node.SetNodeLabel(ctx, label)`** (`GF/node/node_label.go:15-41`). The stored label is restored over `Config.BasicInformation.NodeLabel` at the next `New` (same godoc). | Drop the restart rule `north.matter.node_label` (`restart.go:329`); apply the label through the existing reload seam (`cmd/openccu-loom/reload.go:87` `hotReloadHandler`, `reloadDeps`) and the config-save path; wiring pin under `tests/contract/wiring_pins/` asserting the served NodeLabel changes. | Label edits take effect at once. More important: after any stored label (a controller's write, or one `SetNodeLabel`), a config edit plus restart no longer changes the label at all, because the store wins. Today's "restart required" hint is then false. That a controller has written the root label on a real install is **UNVERIFIED**. | S–M | Low. Open point: `ProductDescription.Name` (the DNS-SD device name) is also built from `mc.NodeLabel` (`daemon_matter.go:422`); whether `SetNodeLabel` moves it: **UNVERIFIED**. If not, the mDNS name stays restart-bound and the help text says so. | **Adopt now.** Closes the #1056 item. |
| F4 | **`Commissioning.WindowOnDemand`** (`GF/node/config.go:524-535`, `GF/CHANGELOG.md:411`): the node opens no window by itself; no PASE and no commissionable record until the host opens one. go-fabric-ui names it as the setting for "a host that wants no window it did not open" (`GU/fabricui/options.go:278-280`). | New `north.matter.commissioning.window_on_demand` (`cfg:"basic"`), label + help in en/de, restart rule, `example.config.yaml`, `docs/reference/configuration.md`; the Pair page already opens windows. One e2e per value (needs slice 0). | Security posture: an unpaired bridge without a configured passcode opens a window at start on credentials nobody was shown (`daemon_matter.go:251-262` falls back to random ones). With the option nothing is advertised until the operator presses "open". | M | Low with default false. A changed default is a behaviour change for first-run pairing with a printed code. | **Adopt now**, default false. The default is the owner's call (D4). |
| F5 | **`Config.BootReason`** (`GF/node/config.go:92-102`, `GF/CHANGELOG.md:360`). The facade now reports Unspecified where it reported PowerOnReboot (`GF/CHANGELOG.md:570`). go-fabric-ui shows `Status.boot_reason` (`GU/CHANGELOG.md:770`). | State only what loom knows: SoftwareUpdateCompleted when `build.Version` differs from a persisted last-run version; SoftwareReset when the previous stop was an operator restart (`internal/north/rest/handlers/system_admin.go:138` `claimRestart`), which needs a persisted marker; otherwise leave Unspecified. Constants from `cluster/core`, never numbers. | Low: one honest field on the status page and in controllers' diagnostics. | S–M | Low. The persisted marker is new state; a crash between marker and stop must not report SoftwareReset forever (clear at boot). | **Adopt now, last in its slice**; drop it if the marker turns out to need a migration of its own. |
| F6 | **Sealing at rest**: `store.NewSealed`, `store.NewAESGCMSealer`, `store.Reseal`, `node.Store.Sealer` (`GF/CHANGELOG.md:398,416,432`; `GF/node/config.go:799-813`; `GF/docs/security.md:96-124`). `Store.Sealer` excludes `Store.Credentials`; loom passes `Credentials: matterstore.New(db)` (`daemon_matter.go:348`), so loom would call `store.NewSealed(ctx, db, sealer)` itself. | Seal the Matter secrets (operational keys, IPKs, group keys, resumption secrets) in `openccu-loom.db`. Key source: `internal/secret` already holds an AES key outside the DB (`internal/secret/secret.go:55,132`, ADR 0027). Needs an ADR: key reuse or a second key, backup/restore carrying the key, rotation (`Reseal`), downgrade (a sealed DB is unreadable by an older daemon, `store.ErrNoSealer`). | Real: a copied DB or backup no longer contains the fabric's private keys. | L | High and one-way: every later start needs the same key (`config.go:807-810`); a lost key file loses every pairing. Whether loom's backup archive includes the key file: **UNVERIFIED**. | **Adopt, but as its own ADR-gated slice after the others** (D5). Not something to slip into a bump. |
| F7 | `Config.TimeFormat` (`GF/CHANGELOG.md` 0.24.0 Added, `node`; `GF/node/config.go:149,633-643`) | none | A bridge has no display; hour format would have to be guessed from the locale. | S | – | **Not for loom**: no source for the value, and a guessed convention is worse than the default. |
| F8 | `Commissioning.RotatingDeviceID` (`GF/node/config.go:536-549`) | none | – | S | – | **Not for loom**: off in connectedhomeip's default and absent in matter.js per the godoc; no commissioner need known. |
| F9 | `Commissioning.Flow`, `PairingHints`, `PairingInstruction` (`GF/node/config.go:550-572`) | Could state a truthful hint (the window is opened in the web UI, not by power cycle). Which bit fits: **UNVERIFIED**. | Low; chip-based commissioners read bits 16–22 as no hint (same godoc). | S | Low | **Adopt later**, together with F4 if the owner makes on-demand the default (then `FlowUserIntent` becomes the truthful flow; Fable read first). |
| F10 | `Config.EndpointState` (`GF/CHANGELOG.md:394`), `Limits.*` beyond `SupportedFabrics`, `Network.InterfaceCheck`, `Sessions`, `CapabilityMinima` | none | – | – | – | **Not for loom**: loom uses the facade's own persistence and defaults; no deprecated twin is in use (grep: no `EndpointPersistence`). |
| F11 | ICD (`Config.ICD`), OTA requestor/provider (`Config.OTA`, `OTAProvider`), TLS, WebRTC, CommissionerControl, GATT / Wi-Fi | none | – | – | – | **Not for loom**: mains-powered bridge, updated as container or add-on, bridges no Matter devices. |
| F12 | `contract.BatteryReplaceability` / `PowerSourceSelection.BatteryReplaceability` (`GF/CHANGELOG.md:444`). The zero value is `BatteryUserReplaceable`. | State `BatteryReplaceabilityUnspecified`: loom has no source for replaceability (no static device DB). Where the selection interface is implemented on loom's battery source: **UNVERIFIED** (`assembler.go:276-307` attaches a `contract.MeasurementSource`). | Honesty of one attribute. | S | Low; composition-visible, chip-tool run. | **Adopt now inside the F1 slice** if the Fable read confirms the zero value is served today; otherwise drop. |
| F13 | `cover.Config.InitialPositionUnknown` (`GF/CHANGELOG.md:366`) | Start a cover whose LEVEL has not arrived with null positions instead of a number. How loom's covers are built (`cluster/spec/windowcovering` declarations): **UNVERIFIED**. | Covers do not report 0 % before the CCU's first value (relevant at a not-ready boot). | M | Medium | **Adopt later**: needs its own read of loom's cover source and matter.js. |
| F14 | Thermostat `SuggestionStore`, `ActivatePreset`, `SetLocalTemperatureChangeThreshold` (`GF/CHANGELOG.md` 0.24.0 Added) | Only if loom serves presets. **UNVERIFIED**. | – | M | – | **Adopt later / likely not**: check against `north.matter.thermostat_*` first. |
| F15 | `contract.ElectricalCurrentSource` (DC meters, `GF/CHANGELOG.md:444`) | none | – | – | – | **Not for loom**: no DC meter in the device model known to me (**UNVERIFIED**). |

### B. go-fabric-ui options and pages

| # | Feature (source) | What loom does | User value | Eff. | Risk | Recommendation |
| --- | --- | --- | --- | --- | --- | --- |
| U1 | **`Options.Warnings`** (`GU/fabricui/options.go:301-306`); the pages render them (`GU/ui/src/lib/pages/node/HostWarnings.svelte`) | Feed Matter-side findings loom today only logs once per key: devices the kit refused or the spec level withheld (`assembler.go:975-984`, `:1103-1111`, `refusedDevices`), hand-built fallbacks (`logFallback`, `:1129`), mDNS interface findings. Small findings registry in `matteradapter` with stable codes; `Warnings: registry.List`. Non-admins get the code only (`GU/CHANGELOG.md:330-333`), so messages may name devices. | The operator sees on the Matter status page why a selected device is not on the bridge, instead of reading the log. | M | Low | **Adopt now.** The #1056 premise ("no list exists") no longer holds: the findings exist, as log lines. |
| U2 | **`Options.Counters`** (`options.go:337-342`) | Reassembly runs / failures, refused devices, fallbacks. No such counters exist yet (the reconciler only returns errors). | Low: numbers beside the findings. | S on top of U1 | Low | **Adopt with U1 only if the counters fall out of the registry**; otherwise close the item as "no counter worth showing" (D7). |
| U3 | **`Options.Done`, `Options.MaxStreams`** (`options.go:330-336`, `GU/CHANGELOG.md:376-380`) | Hand the daemon's shutdown channel as `Done`; keep `MaxStreams` at its default. The SSE routes are mounted even though the SPA uses the WebSocket hub. | Clean shutdown: open node streams end and their observers are removed. | S | Low | **Adopt now.** |
| U4 | **`subscribe(onEvent, onResync?)`** (`GU/ui/src/lib/store.svelte.ts:49-53`, `GU/CHANGELOG.md:416-418`) | Pass loom's `onResync` (`assets/ui/src/lib/stores/events.svelte.ts:108-114`) as the second argument in `fabric-node.svelte.ts:68-73`; vitest case. Whether loom's resync also fires on a plain socket reconnect: **UNVERIFIED** (`events.svelte.ts:84`). | The Matter pages reload after missed events instead of showing stale fabrics or windows. | S | Low | **Adopt now.** |
| U5 | **`Options.Erase` + `fabricui.NodeSwitch`** (`options.go:236-251`; `GU/fabricui/nodeswitch.go:17-42`; `GF/node/factory_reset.go:20-65`) | Section 6. | A real "start over": endpoint numbers, UniqueID and store erased, not only the fabrics. | L (A) / M (B) | High (A) | **Adopt in its own slice; variant is the owner's call** (D1). |
| U6 | **`Options.Operations`** + the `Operation` page (`GU/fabricui/operations.go:18-41`) | none | None beyond loom's own Logs view and log-level editor. | M | – | **Not for loom**: a second place for the same action; the daemon's log is not a Matter concern. Record the decision and close the item (D6). |
| U7 | `Options.Registry` / `Nodes` (several nodes), `FrameAncestors`, `NewHandler` | none | – | – | – | **Not for loom**: one node per daemon (ADR 0049); loom uses `Mount`, which serves no page. |
| U8 | Controller family: Inventory tab, `ControllerDiagnosticsPage`, `CommissionTimeout`, `icd_multi_admin` | none | – | – | – | **Not for loom**: no controller. |
| U9 | `NodeEcosystemHints` (exported from `GU/ui/src/lib/pages/index.ts`, "wave U5a") | Not imported by `Matter.svelte`. Whether `NodePair` already renders it: **UNVERIFIED**. | Per-ecosystem pairing hints under the pairing flow. | S | Low | **Check in slice 1**; adopt if `NodePair` does not include it. It predates 0.10, so it is a leftover, not a new feature. |

### C. Older, still unadopted (not part of these releases; listed so they are not lost)

`node.Config.Metrics` (named counters such as MRP retransmits and recovered panics,
`GF/node/config.go:272-275`) into loom's metrics, and `node.Config.Faults`
(`config.go:228-236`). Both predate 0.23.0 (`GF/CHANGELOG.md:1227`, `:1314`). Not in the
slices below; a separate issue if wanted.

---

## 4. #1027: does v0.25.0 want hosts on `Device.SetReachable`? Yes.

Measured:

1. `Device.SetReachable` godoc: "It is the one reachability input" (`GF/device/lifecycle.go:217-236`).
   A device starts reachable; before it is added only the value changes.
2. A bound device reaches the wire through the node: `Device.Spec()` sets
   `Availability: d.available` (`GF/device/device.go:918`, `lifecycle.go:279`), and a change
   calls `nodeHost.NotifyReachable` → `Bridge.NotifyEndpointReachable` by endpoint id
   (`GF/node/lifecycle.go:66-70`). Binding happens when the Aggregator is handed the device
   itself (`GF/node/aggregator.go:571-582`, `deviceseam.Attach`).
3. The migration guide routes loom by name: "a source's availability (`assembler.go:861`) is
   `Device.SetReachable`" (`migrating-to-the-facade.md:35`) and "reachability is
   `Device.SetReachable`" for loom's replace-under-the-same-key step (`:38`).
4. `NotifyDeviceReachable` stays "for a host that assembles its own endpoints behind
   `Config.Snapshotter`" (`advanced-seams.md:89-93`). Loom does not: it uses the Aggregator's
   partitions (`reconciler.go:327-328`).
5. Neither `endpoint.Spec.Availability` nor `Bridge.NotifyDeviceReachable` carries a
   Deprecated marker in v0.25.0 (grep of `GF/endpoint/spec.go`, `types.go`,
   `GF/bridge/receive.go`; the two Deprecated sections `GF/CHANGELOG.md:180-192`, `:836-879`
   do not name them). So nothing breaks at the next tag; this is a design migration, not a
   forced one.

Why loom is not on it: the reconciler adds `&specEndpoint{spec}` (`reconciler.go:82-89`,
`:341-354`), a type with only `Spec()`. The `*device.Device` built in `deviceSpec`
(`assembler.go:1102`) and `declaredDeviceSpec` (`:964`) is dropped after `d.Spec()`, so no
device is ever bound and the spec's `Availability` is overwritten with loom's probe.

### The migration

1. `Described` carries, per endpoint, the go-fabric device beside (or instead of) its spec.
   The reconciler holds the device per key and hands the Aggregator the device
   (`b.Add(dev)` / `b.Replace(key, dev)`), so the node binds it.
2. Before `Add`/`Replace`: `dev.SetReachable(ctx, loomDev.Available())`, because a rebuilt
   device starts reachable.
3. `Reconciler.SetReachable(ctx, central, address, ok)` looks up the held devices of that
   CCU device and calls `SetReachable` on each. The three `cmd` forwards call it instead of
   `mb.NotifyDeviceReachable`. `ErrNotBridged` on a part is expected and ignored by key
   (`lifecycle.go:230-234`).
4. Remove the three `spec.Availability = …` assignments and the comment block at
   `daemon_matter.go:929-941`.
5. A name change becomes `Device.Update` on the held device instead of a `Replace`
   (`migrating-to-the-facade.md:38`), which removes `name` from `endpointIdentity`.
6. Record in `notes/parity/by_design.md` the divergence the issue comment names:
   `matterNodeSource` keeps `RevokeWindow` instead of `node.CloseWindow`.

Open points the slice must settle before code (each a stop condition):

- **Post-hoc spec edits.** Loom changes the spec after `d.Spec()`: `attachPowerSource` sets
  `specs[target].PowerSource` (`assembler.go:276-307`), `declaredDeviceSpec` overwrites
  `StableKey`, `FriendlyName`, `VendorName`, addresses (`:990-995`). Once the node calls
  `Spec()` on the device itself, these edits are gone. Each must become a declaration or an
  option of `device.New`. Whether the device kit takes loom's battery `MeasurementSource` as
  a declaration: **UNVERIFIED**; if not, it is a go-fabric issue, not a workaround.
- **Hand-built fallbacks** (`assembler.go:1063-1077`, `:1104-1110`): a device type not in the
  device layer, or a composition the kit refuses, yields a plain `endpoint.Spec` with no
  device. How many of loom's endpoints take that path today is **not measured** (it would
  need a run over the golden fleet counting `logFallback`). Owner's call D2.
- **Wiring rule 2.** `wireMatterRuntime` walks `reg.List()` three times at boot
  (`daemon_matter.go:942`, `:952`, `:963`) and repeats the same wiring in the central hook
  (`:969`). The slice should leave one seam for the reachability forward, not a fourth copy.

Tests the slice owes: a flip on the bus reaches the served `Reachable` and fires
ReachableChanged once, through the composition root with the CCU booted not ready (slice 0
harness); a repeated value fires nothing; a rebuilt device starts with the current
availability; bite proof on the forward.

---

## 5. #1092: both open questions are answerable

- **Which route lists endpoints with their source keys?** `GET /api/v1/node/endpoints`: each
  entry carries `source.key`, the stable key's `String()`, plus `endpoint_id` and
  `reachable` (`GU/fabricui/handlers_diagnostics.go:191-199`, `:270-288`). The projection
  calls a published entry by `identity["source_key"] = key.String()`
  (`internal/north/matteradapter/projection_plane.go:19-22`, `:90-91`). Same rendering of the
  same key on both sides, so the comparison needs no mapping.
- **Can the daemon run the bridge without a commissioner?** `node.Start` needs none; loom
  already has `north.matter.mdns_advertise: noop` (`internal/config/config.go:962-966`,
  `daemon_matter.go:58-68`). That a hermetic CI run binds the UDP port is **UNVERIFIED**
  until tried; the chip-tool suite boots the same stack.

Recommended: candidate 1 of the issue. `harness.Options.EnableMatter` writes a real
`north.matter` block (enabled, a free UDP port on loopback, `mdns_advertise: noop`), and a
test with `StartCCUNotReady: true` exposes a set of sources, flips the CCU ready, then
compares both directions: every `published` entry of
`GET /devices/{addr}/projection?plane=matter` is a `source.key` of `/node/endpoints`, and
every keyed endpoint is a published entry. Two bite proofs. Candidate 2 (chip-tool
PartsList) proves the wire but costs a commissioner per run and cannot run in the hermetic
suite; it stays as an optional second leg.

---

## 6. Erase: the two shapes

`Node.FactoryReset` removes the fabrics, stops the node and erases its stores; the node is
single-use and the host brings a new one or exits for its supervisor
(`GF/node/factory_reset.go:40-48`; `GU/fabricui/options.go:236-251`).

- **A. Rebuildable Matter runtime + `NodeSwitch`.** The bundle `startMatterBridge` returns
  becomes a unit that can be built twice on the same store. Everything that today captures
  `bundle.node` or `mb := bundle.node.Bridge()` in a closure reads through one holder: the
  reconciler (`n.Aggregator()`, `daemon_matter.go:479`), the RTT gauges (`:537-543`),
  `SetOnReassembled` (`:976`), the node-event bridge (`:979`), the status adapter (`:981-`),
  the reachability forward. `Options.Node` is a `fabricui.NewNodeSwitch(...)`, `Set` after
  the new node started. Works in every deployment. L, and every moved closure is a wiring
  seam that needs its pin.
- **B. Erase, then restart the process.** `Erase` runs `FactoryReset` and triggers the
  existing supervised restart; where there is no supervisor (`HasSupervisedRestart`,
  `cmd/openccu-loom/daemon_north.go:1001`) `Erase` stays nil and the page offers none
  (`Status.erase` false). M.

A is the better architecture: one behaviour everywhere, and the same holder is what would
later let Matter be enabled without a restart. B is a convenience seam whose availability
depends on how the daemon was installed. A after slice 3, because both restructure who
holds the node's parts.

---

## 7. Proposed PR slices

Order matters: slice 0 gives the others their through-the-root test.

| Slice | Content | Closes | Size | Chip-tool run | Best design? |
| --- | --- | --- | --- | --- | --- |
| **0. Matter in the e2e harness** | `EnableMatter` real; projection ↔ `/node/endpoints` round trip both ways, CCU booted not ready; two bite proofs | #1092 | M | no (composition unchanged) | Yes. It is the missing fourth plane of wiring rule 4, and every later slice's first test crosses this seam instead of constructing the collaboration itself. |
| **1. Node page options** | F3 live node label; U1 warnings (+U2 if real); U3 `Done`; U4 resync; U9 check; U6 recorded as declined | #1056 except Erase | M | no | Yes for label, `Done`, resync. Warnings: yes, provided the findings registry is the one source for both the log line and the page, not a second list. |
| **2. Bridged device identity** | F1 declaration (serial, model, firmware); F12 if confirmed | new issue | M | **yes** | Yes: the host states what it knows from the CCU, the fallback (address as product name) disappears. Needs the Fable read on ConfigurationVersion first. |
| **3. Reachability on the device** | F2 as in section 4; `by_design.md` row for `RevokeWindow` | #1027 | L | **yes** | Yes, and the only one of the three options that is: one state, as go-fabric and matter.js have it. Keeping probe + push is the shape go-fabric removed from its own device layer because a probe change reached no subscription (`GF/CHANGELOG.md:1603`). |
| **4. Commissioning posture** | F4 `window_on_demand`; F5 boot reason | new issue | M | yes for F4 (commissioning path) | Yes with default false. Making on-demand the default is arguably the better product, but it changes first-run pairing; say so and let the owner choose. |
| **5. Erase** | U5, variant A or B | last item of #1056 | L / M | yes | A yes, B no (section 6). |
| **6. Sealing at rest** | F6 behind an ADR | new issue | L | no | Only with the ADR's three-question section answered (key custody, backup, rotation). Without it, not finished by the repo's own standard. |

#1056 closes after slices 1 and 5, or after slice 1 if the owner moves Erase to its own issue.

Slices 2 and 3 both write in `internal/north/matteradapter`: sequential, never parallel.
Paths the delegation table keeps out of an agent's hands appear in every slice
(`cmd/openccu-loom/daemon_matter.go` is the composition root; F4 adds a `cfg:` field).

---

## 8. Decisions for the owner

- **D1 Erase**: A (rebuildable runtime + `NodeSwitch`), B (erase + supervised restart), or
  leave 501. Recommendation A, after slice 3.
- **D2 Hand-built fallback endpoints** in slice 3: keep as a documented exception on
  `NotifyDeviceReachable`, drop them, or ask go-fabric for the missing types. Needs the
  fallback count over the golden fleet first; recommendation is to measure, then decide.
- **D3 Bridged identity values** (slice 2): SerialNumber = CCU address, ProductName = CCU
  model, firmware as SoftwareVersionString; and accepting a one-time attribute change on
  paired fabrics.
- **D4 `window_on_demand` default**: false (no behaviour change) or true when no passcode is
  configured.
- **D5 Sealing**: now with an ADR, or deferred. Recommendation: deferred to its own slice,
  not dropped.
- **D6 Operation tab**: confirm "not for loom".
- **D7 Counters**: adopt only if slice 1's registry yields real ones; otherwise close.

---

## 9. What was not verified

- Whether `IncreaseConfigurationVersions` is owed when F1 changes fixed attributes.
- Whether `SetNodeLabel` also moves the DNS-SD device name.
- Whether the device kit can take loom's battery source as a declaration (blocks part of slice 3).
- How many endpoints take the hand-built fallback today.
- Whether loom's backup contains the `internal/secret` key file.
- Whether loom's hub resync fires on a plain reconnect.
- Whether `NodePair` already renders `NodeEcosystemHints`.
- F13, F14, F15: how loom builds covers and thermostats against the new cluster options.
- The go-fabric 0.23.0 Changed section (`GF/CHANGELOG.md:479-835`, about 350 entries) was
  read only for the lines cited here; a behaviour change relevant to loom may sit in the rest.
- The go-fabric-ui Fixed sections of 0.10–0.12 were not read.
- No test, build or chip-tool run was performed; every "loom does not use X" rests on grep
  over `cmd/` and `internal/` non-test Go files.

---

## 10. Owner decisions (2026-11-23)

- **D1 Erase:** A — rebuildable Matter runtime with `fabricui.NodeSwitch`, after slice 3.
- **D3 Identity:** SerialNumber = CCU address (shipped with the bump PR), ProductName = CCU
  model, SoftwareVersionString = firmware; the one-time attribute change is accepted.
- **D4 `window_on_demand`:** the setting is added with default false.
- **D5 Sealing:** now, behind an ADR that answers key custody, backup and rotation.
- **D6 Operation tab:** not for loom (recommendation taken).
- **D7 Counters:** only if slice 1's findings registry yields real ones (recommendation taken).
- **D2 Hand-built fallbacks:** measure the count over the golden fleet first, then decide.
- No daemon release before the issues of this plan are closed.

## 11. Slice 1 as built (2026-11-23)

Branch `claude/matter-node-page-options-1056`, from `main` at 1a41379e (slice 0, #1165, still open).

- **F3 live node label.** Owner decisions (wizard): a loom-owned marker, and the root serial pinned.
  Migration 056 `matter_host_identity` (single row: `root_serial`, `applied_node_label`), seeded on the
  first Matter start with the label-derived serial and the configured label, so an upgrade changes
  neither. `matterNodeLabeler` applies `north.matter.node_label` only when it differs from the marker:
  live through the config save (`sectionApplier`, `north.matter`) and the file watcher
  (`hotReloadHandler`), and before `Start` for a change made while the bridge was off. A controller's
  rename stands until the setting changes. The restart rule is gone. Fable note (verified): the stored
  label is restored at `New` (GF `node/persist.go:300-306`); `SetNodeLabel` before `Start` is allowed
  (GF `cluster/core/basic_information.go:856-875`, no started guard); the DNS-SD name is built once in
  `New` (GF `node/node.go:448,455`, `node/commissioning.go:336-349`), so it follows at the next restart.
- **Gap left to go-fabric-ui.** `NodeConfigView.Label` on the status page is stored once under `Mount`
  (GU `fabricui/handler.go:335`, no setter); after a live change it shows the old label until the
  restart. Handover issue to go-fabric-ui: a `NodeConfig` func or setter.
- **U1 warnings.** `matteradapter` findings registry (`findings.go`), the one record behind the
  `device_refused` / `device_withheld` log lines and `Options.Warnings`; pruned per completed central
  walk and on `Retire`. mDNS interface findings are NOT warnings: the Diagnostics tab already shows the
  whole diagnosis. `device_layer_fallback` stays log-only (a loom gap, not operator configuration).
- **U2 counters (D7): closed.** The registry yields no counter "since start" worth showing.
- **U3 `Done`:** closed from the REST server's shutdown hook (`Server.RegisterOnShutdown`); the SSE
  route is `GET /node/stream` (GU `fabricui/stream.go:27,135-137`).
- **U4 resync:** `nodeSubscription` passes the hub's `replay_lost` resync to the node store.
- **U6 Operation tab: declined** (D6). **U9 `NodeEcosystemHints`: nothing to do**, `Pair.svelte` renders
  it (GU `ui/src/lib/pages/node/Pair.svelte:20,244-246`).
- **e2e round trip (after #1165 merged):** `TestE2EMatterNodeLabelSaveReachesTheRunningBridge` saves
  `north.matter` through the REST API of the built daemon and reads the served root NodeLabel /
  SerialNumber over `GET /api/v1/node/endpoints/0`, across a restart. Bite proofs: dropping
  `deps.SetMatterNodeLabeler` or the section applier's `matterLabelSection` turns the save's `applied`
  false.

## 12. Slice 2: decisions and verified facts (2026-11-23)

Branch `claude/matter-bridged-identity`, from `main` at 3dc2682f (#1166 merged).

**Fable note, spot-checked by the coordinator.** GF = go-fabric v0.25.0, MJ = matter.js HEAD 61b1a41a
(equality with GF's pins b25ed960 / db021da0 not checked).

- GF serves stated ProductName / ProductLabel / SoftwareVersionString (`endpoint/materialize.go:146-260`,
  `applyStatedBridgedInfo` `:590-621`); unstated ProductName and ProductLabel both fall back to the device
  address. Constraints: ProductName max 32, SoftwareVersionString 1 to 64, ProductLabel max 64, all quality
  Fixed (`cluster/spec/bridgeddevicebasicinformation/definition_gen.go:460-555`).
- ConfigurationVersion: MJ raises only through `increaseConfigurationVersion`
  (`packages/node/src/behaviors/bridged-device-basic-information/BridgedDeviceBasicInformationServer.ts:61-91`);
  what counts as a configuration change is the bridge's call (`bridged-device-basic-information.resource.ts:100-110`).
  GF raises by itself on surface drift (AttributeList etc., no values) at Start and after a reassemble
  (`node/surface_stability.go:163-228`); a string attribute is listed only when non-empty
  (`cluster/core/bridged_device_basic_information.go:285-300`). A value-only change needs
  `Aggregator.IncreaseConfigurationVersions` (`node/aggregator.go:620-659`).
- No live firmware path existed: `applyFirmwareFromDescriptions` (`internal/central/adapter/firmware_domain.go:135`)
  reaches only per-device `Firmware.OnChange` handlers (MQTT); the Matter triggers are Created, Removed,
  MetadataChanged, Released (`cmd/openccu-loom/daemon_matter.go:758-775`); `identityOf` compared no
  BridgedBasicInformation (`internal/north/matteradapter/reconciler.go:98-126`).
- F12: GF takes the battery replaceability from the optional `contract.PowerSourceSelectionSource`, else
  the zero value `BatteryUserReplaceable` (`cluster/measurement/materializers.go:146-164`,
  `contract/matter.go:818-852`); MJ serves Unspecified when undefined
  (`packages/node/src/behaviors/power-source/PowerSourceServer.ts:48-54`).

**Owner decisions (wizard 2026-11-23).**

- ProductLabel = CCU model, same as ProductName (the MJ bridge example states one value for both,
  `examples/device-bridge-onoff/src/BridgedDevicesNode.ts:94-96`).
- Firmware live, with a version raise: a new bus event `hmevent.DeviceFirmwareChangedEvent`, published by
  `applyFirmwareFromDescriptions` when the current version changes; the Matter reassembly debounce
  subscribes; the stated strings join `endpointIdentity`; a value-only change of a stated string raises
  the device's ConfigurationVersion. Empty ↔ non-empty is surface drift that GF raises itself.
- F12: the battery source states `BatteryReplaceabilityUnspecified`.
- Known limit: a firmware change while the daemon is stopped raises no version at the next start (no
  stored values to compare; MJ does not raise on its own either).

## 13. Slice 3: measurements, verified facts and decisions (2026-11-23)

Measured (`TestMatterFallbackCensus`, `-tags=integration`, whole embedded godevccu fleet,
`IncludeMeasurements`, no allow-list): 401 devices → 2021 bridged endpoints (2258 with
`ExposeSecondaryChannels`). Hand-built (not through the device layer): 59, every one a
calculated `OperatingVoltageLevelSensor` (class `MeasurementBattery`) served as its own
endpoint with device type 0x0000. Device type missing from the layer or composition refused: 0.

Verified facts (Fable note, load-bearing lines re-read at GF v0.25.0):

- Binding is by type: `deviceseam.Attach` binds whatever promotes `base() *Device`
  (`GF/device/lifecycle.go:49-64`); `&specEndpoint{spec}` is never bound.
- `Replace` with the same pointer is a no-op (`GF/node/aggregator.go:496-497`); a new device
  starts reachable, so the seed `SetReachable` precedes `Add`/`Replace`.
- `SetReachable` notifies only on a change (`GF/device/lifecycle.go:240-244`).
- `HostStatement` carries Name and addresses only (`GF/device/lifecycle.go:326-329`); stated
  BDBI strings and the battery are construction-time (`fabricdevice.Measurement`,
  `GF/device/measurement.go:48-52`; bdbidef `Initial`).
- `NotifyDeviceReachable` addresses every endpoint of the device without a duplicate check
  (`GF/bridge/receive.go:935-952`): beside `SetReachable` it would fire twice.
- Defect: the calculated branch of `channelSpecs` has no battery fold, and
  `attachPowerSource` walks generic data points only; a type-0 endpoint is served as
  `[BridgedNode]`, not refused. Whether production reaches it without an operator's
  exposure: UNVERIFIED.

Owner decisions (wizard):

- **Kept devices (first answer, superseded):** a go-fabric issue for a BDBI host statement.
  Re-decided on new facts (below): **a firmware change is a rebuild, as a reboot is.** The
  source builds a new go-fabric device with the new stated strings, the reconciler hands it
  over with `Aggregator.Replace` under the same key (the endpoint number stays), and the
  ConfigurationVersion is raised. No go-fabric change. Slice 3 splits: **3a** (battery fold
  fix, census as guard, one reachability seam, `by_design` row for `RevokeWindow`), **3b**
  (construction-time declarations, binding, `SetReachable`, rebuild on firmware change,
  specEndpoint gone). 3b measures first: does the physical device restart on a CCU firmware
  update (UNVERIFIED), and what `Replace` does to a running subscription (UNVERIFIED).
- **Facts behind the re-decision** (Fable note `.scratch/fable-bdbi-facts.md` of the slice-3
  worktree, re-read): SoftwareVersion/-String are quality F (MJ
  `model/src/standard/elements/basic-information.element.ts:51-57`); chip keeps them in
  `const FixedData mFixedData` (`src/app/clusters/bridged-device-basic-information-server/BridgedDeviceBasicInformationCluster.h:229`);
  ConfigurationVersion rises when the bridge deems a change a configuration change
  (MJ `types/dist/esm/clusters/bridged-device-basic-information.d.ts:177-186`); a controller
  using WildcardSkipFixedAttributes may never re-read an F attribute
  (MJ `node/src/node/integration/ProtocolService.ts:502-503`).
- **D2:** the hand-built fallback is removed; a type the layer lacks or a refused composition
  is withheld with a finding. The census becomes a guard: zero hand-built endpoints.
- **Battery fix:** fold only — the calculated level never becomes an endpoint; PowerSource
  keeps today's choice (generic). Feeding the percentage into PowerSource is a measured
  follow-up.
- **Base:** slice 3 is stacked on #1168 (`claude/matter-bridged-identity`), rebased onto main
  after its merge.

## 14. Slice 3b: measurements and decisions (2026-11-23)

Measured before code (Fable notes, load-bearing lines re-read by the coordinator):

- **Device restart on a CCU firmware update.** BidCos-RF: rfd starts the bootloader, flashes
  and reports `DEVICE_IN_BOOTLOADER` true then false (`OpenCCU-Base/src/rfd/RFDevice.cpp:1499-1527`,
  `:1561-1570`), then announces the device with `newDevices` (`:1619-1624`). BidCos-Wired: hs485d
  update mode, flash, bootloader message "g", `newDevices` (`src/hs485d/HS485Device.cpp:843-943`).
  HmIP-RF / HmIP-Wired: UNVERIFIED (no open source or fixture states a restart). Runtime state kept
  or reset: UNVERIFIED for every family. rfd writes the new FIRMWARE from the file without device
  confirmation (`RFDevice.cpp:1605-1613`). **Consequence:** the rebuild is justified by go-fabric's
  construction-time BDBI statement and the F quality, never by "the device restarted".
- **Same-key `Replace` under a running subscription** (v0.25.0, `fabrictest` experiment with
  controls): the subscription survives, the endpoint number and Descriptor stay; **no attribute of the
  replaced endpoint is reported and its DataVersions carry over** (`bridge/bridge.go:1774` skips known
  endpoint ids; state keyed by SourceKey, `endpoint/types.go:246-275`). A `DataVersionFilter` with the
  pre-replace version hides a changed cluster. BDBI is covered only by the ConfigurationVersion raise.
  matter.js re-announces a re-added endpoint (`Datasource.ts:763`, `ProtocolService.ts:290-296`).
  Handed over as **thingfabric/go-fabric#589**.
- Availability events are published after the model state changed
  (`internal/central/adapter/device_availability.go:46-53`, `eventbridge.go:1765-1793`), so a seed that
  reads the model at seed time loses no flip.

Owner decisions (wizard):

- go-fabric#589 filed; 3b proceeds (a firmware rebuild serves the same model values except the BDBI
  strings, which the ConfigurationVersion raise covers); the gap goes into `by_design.md` until the bump.
- Approach approved: (1) one source contract `MatterDevice(MatterDeviceRequest{Options, Build, Host})`;
  the holder rebuilds when the host facts (battery, stated BDBI strings) change; hooks register once
  and reach the device through `Current()`; (2) `Described` carries the go-fabric device, the reconciler
  hands the Aggregator the device, `specEndpoint` goes; (3) a name change is `Device.Update`, name leaves
  `endpointIdentity`; (4) `Reconciler.SetReachable` replaces `NotifyDeviceReachable` in the forward,
  seeded from the model under the lock that indexes the device; (5) every `spec.Availability` assignment
  goes.

As built (branch `claude/matter-reachability-3b`):

- One source contract `MatterDevice(MatterDeviceRequest{Options, Build, Host})`; `MatterHostFacts`
  (battery, stated identity) are the only facts a source rebuilds for. `MatterDeviceHolder.Device(req,
  MatterDeviceBuild{New, Bind, Settle})`: hooks register once and resolve `Current()`; `Settle` stores the
  current state after every swap.
- Every caller asks a source through `Assembler.deviceRequest` (walk, projection probe, eligibility
  listing — now `Assembler.CollectCandidates` — and `DecideDevice`), so no caller makes a source rebuild.
  The battery target is remembered per physical device (`powerTargets`); a walk that finds a new target
  describes the device once more.
- `Described.Endpoints` are `Endpoint{Device, Kept, Available}`; the reconciler binds the device,
  `specEndpoint` and every `spec.Availability` assignment are gone; a rename is `Device.Update`.
- `Reconciler.SetReachable` + seed: `reachMu` orders a flip against one central's seed → batch → reindex,
  because during a `Replace` both devices are bound (`node/aggregator.go:502-503`, detach `:404-407`) and
  `notifyReachable` emits without dedupe (`bridge/receive.go:1051-1059`). Found by the Fable text review;
  reasoned, not measured (the window is a race).
- Guards: `TestReconcilerSetReachableReportsEachFlipOnce` (event log: one per flip, none on repeat),
  `…StatesTheModelsAvailabilityBeforeBinding`, `…KeepsReachabilityAcrossAFirmwareRebuild`,
  `…StatesARenameOnTheHeldDevice`, e2e `TestE2EMatterReachableFollowsTheCCU` (flip and back; UNREACH
  before a not-ready restart), pin `TestPin_SetReachable_WiredInDaemon`, holder rebuild test in
  `internal/model/generic`. Bite proofs: seed removed → 2 unit + e2e phase 2 red; forward call removed →
  e2e phase 1 + pin red; hook capturing the first device → rebuild test red.
- Open: chip-tool run (composition binding changed → `needs-chiptool`), go-fabric#589 bump, push/PR on
  the owner's word.

## 15. Slice 4: measurements and decisions (2026-11-26)

State before the slice: #1170 and #1172 merged (279bac93); main carries go-fabric v0.26.0 and
go-fabric-ui v0.14.1. Branch `claude/matter-commissioning-posture` from main 7439a8ea.

Measured (go-fabric v0.26.0, go-fabric-ui v0.14.1, module cache):

- **F4.** `node.Commissioning.WindowOnDemand` (`node/config.go:523-535`): with it a node without a
  fabric opens no window at `Start` (`node/node.go:1528-1531`, log `commissioning.window_on_demand`),
  none after its last fabric goes (`node/commissioning.go:509-513`), none when a basic window closes
  and none when `SetCommissioningBlocked` lifts (`node/node.go:1815-1822`); the host opens it with
  `OpenOwnWindow` / `OpenBasicWindow` (`node/commissioning.go:741-756`). matter.js
  `commissioning.enabled: false` (`CommissioningServer.ts:266-273` per the godoc). The node page
  already opens windows through those two calls (`fabricui.Options.ConfiguredWindow`,
  `GU/fabricui/options.go:270-281`); `GET /node/status` reports `commissioning_window_open` and
  `window_kind` (`GU/fabricui/handlers_node.go`), which is what the e2e per value reads.
- **F5.** `node.Config.BootReason` (`node/config.go:91-102`), values `mattercore.BootReason*`
  (`cluster/core/general_diagnostics.go:223-229`); a value past SoftwareReset fails `Validate`
  (`node/config.go:1004-1005`); the node's own OTA record overrides it (`node/root.go:112-125`;
  loom configures no OTA). go-fabric-ui serves `Status.boot_reason`.
- loom persists neither the last version a node started with nor a restart marker;
  `matter_settings` is go-fabric's table (`055_matter_store_v8_to_v12.sql:61`), not a place for
  loom state. So F5 needs a migration of its own (the case the F5 row named for dropping it).
- Both restart paths (REST `Restart`, MQTT `RequestRestart`) claim through
  `handlers.claimRestart` (`internal/north/rest/handlers/system_admin.go:138`), so the shutdown
  path can ask whether a restart was requested without a new wiring setter.

Owner decisions (wizard):

- F4: `north.matter.commissioning.window_on_demand`, default false (D4), restart-required.
- F5 adopted with migration 057: a loom-owned single-row boot record with the version the Matter
  node last started with (a different running version → SoftwareUpdateCompleted) and a cause a
  graceful shutdown writes after an operator restart (→ SoftwareReset); read and cleared at boot, so
  a crash or kill reports Unspecified.
- Both present: SoftwareUpdateCompleted wins.
- Flow stays Standard; F9 (User-Intent flow, pairing hints) stays "later".

As built (branch `claude/matter-commissioning-posture`):

- F4: `Commissioning.WindowOnDemand` from the config in `startMatterBridge`. Tests per value through
  `startMatterBridge` (`matter_window_on_demand_test.go`: window at start; basic window closed without
  a reopen) and e2e `TestE2EMatterWindowOnDemandFollowsTheSetting` (section save → restart-required →
  restart → no window → pairing page opens one). Bite: root line removed → both unit "on" cases and the
  e2e red.
- F5: migration 057 `boot_record` (`stop_requested`, `last_stop_requested`, `matter_started_version`).
  Boot: `takeStopRecordAtBoot` right after the DB opens (every boot, Matter on or off). Shutdown: the
  DB teardown writes the marker when `handlers.RestartRequested()` (a claim in this process; REST and
  MQTT share `claimRestart`). Matter start: `loadMatterBootReason` → `node.Config.BootReason`;
  `SetMatterStartedVersion` after `Start` succeeded. No new wiring setter, `startMatterBridge`'s
  signature unchanged (it reads the record through its `db`).
- Tests: store (`boot_record_test.go`: the marker lives one boot), handlers (`RestartRequested` after a
  REST and an MQTT claim), derivation table + three cases through `startMatterBridge`, e2e
  `TestE2EMatterBootReasonStatesWhatTheDaemonKnows` (fresh 0, SIGTERM 0, `POST /system/restart` 6,
  SIGTERM after it 0, a second binary built with another `build.Version` 5). Harness:
  `RestartOnRequest`, `RestartWithBinary`, `BuildDaemonWithVersion`, `NodeStatus`, `OpenPairingWindow`.
- Bite proofs: `BootReason: bootReason * 0` → unit 5/6 cases red (a deleted line fails the build, not
  the test); started-version write removed → unit red; shutdown marker forced false → e2e step
  "operator restart" red; boot take removed → same step red.

## 16. Slice 5: measurements (2026-11-26)

State before the slice: #1176 merged (ce8581a2), slice 4 done. Branch `claude/matter-erase-1056`
from main ce8581a2. D1 = A (section 10).

Measured (go-fabric v0.26.0, go-fabric-ui v0.14.1, module cache; this repo at ce8581a2):

- **go-fabric-ui offers both halves.** `fabricui.NodeSwitch` (`GU/fabricui/nodeswitch.go:42-205`):
  `NewNodeSwitch(initial)`, `Set(n)` after the new node started, `Current()`; it delegates every
  `NodeSource` method and `Set` ends every node stream so the page reconnects onto the new node
  (`:25-32`, `stream.go:38-50,138`). `Options.Erase` (`GU/fabricui/options.go:236-251`): the host
  erases and brings a node back; run detached, bounded by two minutes; nil answers 501 and
  `Status.erase` false (`handlers_node.go:151,446-478`, confirm word `erase-node`).
- **`Node.FactoryReset`** (`GF/node/factory_reset.go:19-96`): removes every fabric, stops the node,
  erases the credential store and the endpoint records, pins a fresh UniqueID; the node is
  single-use, the host builds a new one on the same store (`fabrictest/device.go`
  `Device.FactoryReset`: reset, then `startLocked`). A stop that does not complete erases nothing.
  A host's own endpoint state is the host's to erase (`:48-49`).
- **The erase scrubs loom's whole database.** loom hands the node `matterstore.New(db)` on the shared
  `openccu-loom.db` handle (`daemon_matter.go:351`); `store.Store.Erase` ends with `Scrub`
  (`GF/store/erase.go:78-97`): `VACUUM` of the whole file and `wal_checkpoint(TRUNCATE)`. The pool is
  WAL with several connections (`internal/store/sqlite/dsn.go:48`, `store.go:57`), so a reader
  blocks the checkpoint and the reset returns `ErrScrubIncomplete` (rows gone, bytes may remain in
  the -wal) — every other step completes (`factory_reset.go:31-37`). The VACUUM duration scales
  with the whole file (history included); not measured.
- **loom's endpoint store** (`internal/store/matterendpoint/endpoints.go:93-95`) erases only the
  go-fabric endpoint records; the exposure allowlist (`matter_exposures`), `matter_host_identity`
  (root serial, applied label) and `boot_record` are loom's and survive the reset.
- **Who holds the node or its parts today** (`cmd/openccu-loom/daemon_matter.go` at ce8581a2): the
  reconciler on `n.Aggregator()` (`:498`) and through it `sync` / `retire` / `forget` / the
  reachability forward (`:957`, the ready and hot-plug triggers, the live-adopt hook); the RTT
  gauges over `bridge` (`:573-579`); `SetOnReassembled` (`:988`); `wireMatterNodeEvents`'
  `Observe` (`:991`, `matter_node_events.go:91`); the status adapter (`:993-1000`,
  `matter_status_adapter.go:28`) and the health probe on it; the node labeler
  (`matter_node_label.go:43`); the health-sample listener (`:473`); `Options.Node`
  (`matterNodeSource{bundle.node}`, `:1017`); the stop closure (`:581-592`). Eighteen test files
  construct through `startMatterBridge` / `wireMatterRuntime` and read `bundle.node`.

Owner decisions (wizard, 2026-11-26):

- **Scrub:** the erase counts as done when only the scrub is incomplete (`ErrScrubIncomplete`): the
  new node starts, the log and `Options.Warnings` say the bytes may remain, and a flag in
  `boot_record` (migration 058) makes the next boot run `store.Scrub` right after the database
  opens, before any reader exists, clearing the flag on success.
- **loom's own state survives:** exposure allowlist, `matter_host_identity` (root serial, applied
  label) and `boot_record` are operator configuration and product identity, not pairing data. The
  new node comes up with the same selection, unpaired, with a fresh UniqueID.
- **BootReason of the rebuilt node:** SoftwareReset (6), as slice 4 states for an operator restart.

Approach (to be approved):

- One holder, `matterRuntime` (`cmd/openccu-loom/matter_runtime.go`), owns the current generation
  (node + reconciler) under an RWMutex; `Sync` / `Retire` / `QueueForget` / `SetReachable` /
  `Node()` / `Bridge()` read through it, `Erase` and `Stop` swap or end it under the write lock.
  The node config is built by one function both the first start and the rebuild call, so the
  rebuilt node cannot drift from the first (only `BootReason` differs).
- Per-generation attachments re-run on every new node and detach from the old one: the node-event
  observer (WebSocket + audit), the health-sample listener, `SetOnReassembled`, the labeler's node.
  The RTT gauges, the status adapter and the stop closure read through the holder.
- `Options.Node` = `fabricui.NewNodeSwitch(matterNodeSource{first})`, `Set` after the new node
  started and the first sync ran; `Options.Erase` = `matterRuntime.Erase`. A new node that does
  not start leaves the switch on the stopped one (go-fabric-ui's contract) and the error reaches the
  page; a second erase retries.
- No new `Set*` / `Attach*` setter: everything is constructed inside `wireMatterRuntime`.
- Tests: unit through `wireMatterRuntime` per attachment (each a pin with a bite); store test for the
  scrub flag; e2e `TestE2EMatterEraseBringsAFreshNodeBack` (CCU booted not ready; erase → 204,
  status `erase: true`, window open, same endpoint count, new UniqueID, events stream reconnects);
  chip-tool: pair → erase → pair again (label `needs-chiptool`). REST contract: the route is
  go-fabric-ui's and already in `assets/openapi.yaml:6643`. MCP: not extended (destructive,
  operator-only on the node page) — recorded in the PR.

As built (branch `claude/matter-erase-1056`):

- `cmd/openccu-loom/matter_runtime.go`: `matterRuntime` holds the generation (node + reconciler) under
  an RWMutex held per call or per swap, never across the reset; `eraseMu` serialises Erase and Stop.
  While an erase runs, Sync / Retire / QueueForget / SetReachable are skipped (the bus never waits
  for an erase); the erase syncs the new node from the model after the swap. Attachments
  (`attach`): health-sample listener, `SetOnReassembled`, the node observer of
  `wireMatterNodeEvents`; `release` in the closers. Labeler `rebind` sets the last applied label on
  the new node before Start. A reset error other than `node.ErrScrubIncomplete` returns without a
  rebuild (the next erase finishes it, go-fabric `node/factory_reset.go:51-55`).
- `startMatterBridge`: one `nodeConfig(bootReason)` for the first node and the rebuild; gauges and
  the stop read through the runtime; `fabricui.NewNodeSwitch(matterNodeSource{n})` built with it.
  `wireMatterRuntime`: `Options.Node` = the switch, `Options.Erase` = `rt.Erase`, `Warnings` =
  findings + the runtime's `scrub_pending`. Status adapter reads `node func() *node.Node`.
- Migration 058 `boot_record.scrub_pending`; `sqlite.MarkScrubPending` / `ClearScrubPending`;
  `runPendingScrub` in `wireAuditOverlay` right after `takeStopRecordAtBoot`.
- Measured while testing: with a read transaction open the erase's `VACUUM` itself waits the
  busy timeout (5 s) and fails, so the scrub is deferred (`ErrScrubIncomplete: vacuum`), not only
  the checkpoint.
- Tests: `matter_erase_wiring_test.go` (pages, status, UniqueID, BootReason, selection, label,
  window, node observer, reassembly hook, reconciler; scrub with and without a reader; boot path
  through `wireAuditOverlay`), store `TestBootRecordScrubPendingLivesUntilCleared`, e2e
  `TestE2EMatterEraseBringsAFreshNodeBack` (harness `EraseNode`).

## 17. Slice 6: measurements (2026-11-26)

State before the slice: #1177 merged (2e5e3a23), slice 5 done, #1056 closed by it. Branch
`claude/matter-sealing-at-rest` from main 2e5e3a23. D5 = sealing now, behind an ADR.

Measured (go-fabric v0.26.0 module cache; this repo at 2e5e3a23):

- **go-fabric.** `store.NewSealed(ctx, db, s Sealer)` (`GF/store/seal.go:164`) seals every secret
  column on the way in, writes a seal canary, scrubs; strict while open (no clear secret written, an
  unmarked secret row refused with `ErrUnseal`); a store without the sealer reads sealed values as
  `ErrNoSealer`; "every Store of one database needs the sealer" (`:160-161`). `ScrubOwed`
  (`:656-664`): an unfinished scrub is no error, retried on every `NewSealed`. `Reseal(ctx, db, old,
  next)` (`:686-698`): one transaction, then a scrub; "there is no unseal" — back to clear only by
  restore or factory reset. `node.Store.Sealer` excludes `Store.Credentials`
  (`GF/node/config.go:823-824`), and loom passes `Credentials`, so loom opens the store with
  `store.NewSealed` itself. `Erase` drops the canary (`GF/CHANGELOG.md:864`). Protects
  confidentiality of a copy, not integrity (`GF/docs/security.md` "Sealing at rest").
- **loom opens the store once:** `matterstore.New(db)` at `cmd/openccu-loom/daemon_matter.go:354`
  (after `matterstore.Upgrade` at `:346`); no other production open.
- **Key custody today (ADR 0027, `internal/secret/secret.go:1-130`):** one AES-256 master key,
  `OPENCCU_LOOM_SECRET_KEY` (base64) or `<data_dir>/secret.key` (auto-created, 0600); without either
  the config secrets fall back to plaintext ("hardening, not a boot dependency"). `Cipher` keeps
  only the AEAD, not the raw key. ADR 0027 defers rotation (`:127-129`).
- **Backup (`cmd/openccu-loom/backup.go`):** the archive never contains `secret.key` (`:233-240`);
  it is sealed with the master key when one exists (`:271-285`, `secret.LoadExisting`, never minted
  by the CLI), else written in a loud plaintext mode; restore needs the master key for a sealed
  archive (`:756-762`). So a Matter key derived from the master key travels with the same custody
  the archive already requires.
- **Unverified:** whether a sealed `Store` keeps sealing (and re-marks) after its `Erase` dropped the
  canary — slice 5 rebuilds the node on the same `Store` instance; to be measured by a test first.

Owner decisions (wizard, 2026-11-26), written up as ADR 0092 (proposed,
`docs/adr/0092-matter-secrets-sealed-at-rest.md`):

- **Key:** HKDF-SHA256 from ADR 0027's master key, own label; go-fabric's AES-256-GCM sealer.
- **Activation:** automatic whenever a master key resolves (no switch); without one, clear as today
  unless the database was sealed before.
- **Rotation now:** offline `openccu-loom secret rotate` — config secrets re-sealed in one
  transaction + `store.Reseal`, scrub; both keys kept until every step committed (re-runnable);
  env-managed key: the command takes or prints the new value.
- **No/wrong key:** bridge off (`ErrNoSealer` / `ErrUnseal`), health `matter.bridge` names reason and fix;
  offline `openccu-loom matter reset` erases the Matter store + endpoint records (selection kept).
- Scrub owed after sealing: page warning while `ScrubOwed`; `NewSealed` retries each start.

Open measurements before code: (1) a sealed `Store` after its `Erase` (slice 5 rebuilds on the same
instance) — still sealing, canary rewritten?; (2) how a start without a key detects a sealed
database before `node.New` (canary probe vs. the error `store.New` returns); (3) whether `Erase` of
a sealed database works through a store opened without the sealer (for `matter reset`); (4) the
config-secret rows to re-seal (`config_sections` JSON leaves, `centrals.password_plain`) and whether
`internal/secret` can keep the raw key for HKDF with one derivation method as its only API growth.

ADR 0092 accepted by the owner (2026-11-26). Open measurements, answered:

1. A sealed `Store` seals every write through its `s.sealer` field whatever the canary
   (`GF/store/seal.go:230-231`); `Erase` drops the canary with every other `matter_settings` row
   (`:422-427`). The erase rebuild therefore opens a fresh `NewSealed` store, which re-marks at once.
2. go-fabric exports no "is sealed" probe; the mark is the reserved row `store.seal_canary` in
   `matter_settings` (`GF/store/seal.go:427`, `readCanary :434-443`). loom probes that row before
   `node.New` and pins the probe by behaviour (true after `NewSealed`, false after `Erase` and on a
   fresh database), so a rename upstream turns the pin red. A store without the sealer on a marked
   database refuses only secret writes (`refuseClearSecretWrite :476-490`), so without the probe the
   bridge would start and fail later at CASE or commissioning.
3. `Store.Erase` is DELETEs, seeds and `Scrub` — no sealer (`GF/store/erase.go:78-98`): `matter
   reset` erases through `store.New(db)` without the key; to be confirmed by its test.
4. Config secrets to re-seal on rotation: `config_sections` JSON leaves at secret paths
   (`configstore.TransformSectionJSON`, wired `cmd/openccu-loom/daemon_boot.go:224`,
   `configcli.go:35`) and `centrals.password_plain` (`internal/store/sqlite/centrals_store.go:67-89`).
   No other `enc:v1` user; `backup.go` archives use the separate stream format.
5. No existing guard detects a running daemon for offline commands (`backup.go` has none).

Approach approved (wizard, 2026-11-26): `internal/secret` keeps the raw key and gains one HKDF
derivation; the boot's cipher is passed explicitly to `startMatterBridge`; with a key the store is
opened with `NewSealed` (fresh per erase rebuild), an owed scrub is a page warning; without a key a
canary probe fails closed with a named health reason; offline `secret rotate` (progress through
`secret.key.next`, re-runnable) and `matter reset`. Running-daemon guard: the daemon holds an
exclusive lock on `<data_dir>/openccu-loom.lock` (flock, stdlib) while it runs; the offline
commands take it non-blocking and refuse otherwise.

As built (branch `claude/matter-sealing-at-rest`):

- `internal/secret`: `Cipher` keeps the raw key; `DeriveKey(label)` (HKDF-SHA256), `NewCipher`,
  `GenerateKey`, `EncodeKey`/`DecodeKey`, `WriteKeyFile` (temp + rename), `ReadKeyFile`,
  `PendingKeyFileName`.
- `internal/datalock`: `Acquire(dataDir)` / `Release`, `ErrHeld`; Unix `syscall.Flock`, Windows
  `syscall.CreateFile` with share mode 0 (no new dependency). `runDaemon` takes it before the serve.
- `cmd/openccu-loom/matter_seal.go`: label `openccu-loom matter-store seal v1`, canary probe
  (`store.seal_canary`), `openMatterStore` (sealed / clear / refused), `matterSealRefused` →
  `matter.bridge` unhealthy, sticky. `startMatterBridge(…, cipher, …)`; the boot cipher travels as
  `auditOverlay.cipher`. Erase rebuild opens the store anew. Page warning `seal_scrub_owed`.
- `cmd/openccu-loom/matter_seal_cli.go`: `secret rotate` (config secrets in one tx, then
  `store.Reseal`, then rename `secret.key.next` → `secret.key`; env mode prints the key) and
  `matter reset --yes` (endpoint records + `store.Erase` + fresh UniqueID pin, mirroring
  `node.FactoryReset`), both behind `withOfflineDB` (lock first).
- e2e: the harness provisions no key, so the daemon creates `secret.key` and **every Matter e2e
  and chip-tool run now exercises the sealed store**, restarts included.
  `TestE2EMatterSealedStoreFailsClosedAndResetRecovers` (key removed → another key → bridge off,
  `matter.bridge` names it; `matter reset` refused while running; reset while down → bridge back
  with the selection). Harness: `RestartAfter`, `RestartAfterWithoutBridge`, `Offline`.
- Measured while testing: the health component is `matter.bridge`, not `matter` (ADR text
  corrected).
