# The Matter boundary

The Matter stack is not part of this repository. It is the external Go module [go-fabric](https://github.com/SukramJ/go-fabric), and this page describes where the line between the two runs, what stays on this side and why, and how a go-fabric release reaches the daemon.

!!! info "Who this page is for"
    Contributors who change anything Matter-related — a device profile's projection, the bridge bring-up, the exposure allowlist — or who move the daemon onto a newer go-fabric. The user-facing view is [Matter](../user/matter.md); the binding parity rules are the [Matter parity contract](../matter-parity-contract.md).

## What go-fabric owns

go-fabric is the wire stack: a semantic port of matter.js HEAD that the daemon embeds as a library. The module version is the `require` line in [`go.mod`](https://github.com/SukramJ/openccu-loom/blob/main/go.mod) and nothing else in this repository states it.

Since [ADR 0084](../adr/0084-matter-device-layer-and-node-facade.md) the daemon brings the bridge up through go-fabric's node facade (`node.New`, called in `startMatterBridge` in [`cmd/openccu-loom/daemon_matter.go`](https://github.com/SukramJ/openccu-loom/blob/main/cmd/openccu-loom/daemon_matter.go)). The facade's `node.Config` receives what only the host can know — the topology, the credential and endpoint stores, the attestation files, whether to advertise over DNS-SD — and performs the bring-up itself. Its [Amendment for go-fabric v0.5.0](../adr/0084-matter-device-layer-and-node-facade.md#amendment-go-fabric-v050) moved the remaining generic mechanics out of the daemon: the bridge's UniqueID pinning, scene and bridged-NodeLabel persistence (`node.NewEndpointPersistence`), the DNS-SD re-announce loop and the attestation-chain loader. Its [Amendment for go-fabric v0.10](../adr/0084-matter-device-layer-and-node-facade.md#amendment-go-fabric-v010) moved the daemon onto the facade's routes for the rest — health probe, attestation files, DNS-SD, fabrics, commissioning credentials — and onto the device kit's `HostServer` and `Measurement` declarations.

The port contracts a device profile implements live in go-fabric's `contract` package. [`pkg/interfaces/matter.go`](https://github.com/SukramJ/openccu-loom/blob/main/pkg/interfaces/matter.go) re-exports them as compatibility aliases (`MatterEndpointSource = contract.EndpointSource`, …); new code names the `contract` symbol directly.

## What stays in this repository, and why

Everything that needs the daemon's own vocabulary — devices, channels, data points, Homematic addresses, the operator's choices — stays here. A channel is not a Matter concept, so the module cannot hold it.

### The topology assembler

[`internal/north/matteradapter/`](https://github.com/SukramJ/openccu-loom/tree/main/internal/north/matteradapter) is the only place where the two vocabularies meet. It walks the device model, decides what deserves an endpoint, applies the exposure allowlist and visibility rules, resolves the operator-facing labels through the model's naming authority, and hands go-fabric flat endpoint specs. The facade consumes a topology through `node.Config.Topology` and never builds one; `matterTopologySnapshotter` in `daemon_matter.go` connects the two. The walk is non-reactive: re-assembly on device add, remove or readiness change is triggered by the daemon (`wireMatterReassembleOnReady`, `subscribeMatterDeviceLifecycleTrigger`).

### Endpoint identity and the exposure allowlist

[`internal/store/matterendpoint/`](https://github.com/SukramJ/openccu-loom/tree/main/internal/store/matterendpoint) owns the two tables keyed by a Homematic source identity, the 5-tuple (central name, device address, channel number, data-point kind, data-point key):

- `matter_endpoints` — the persisted source → endpoint-id mapping. Its schema is go-fabric's `endpoint/sqlitestore`; `matterendpoint.Store` runs that store under this daemon's key type (`sqlitestore.WithKeyDecoder(DecodeSourceKey)`). Migration [`049_matter_endpoints_sqlitestore.sql`](https://github.com/SukramJ/openccu-loom/blob/main/internal/store/sqlite/migrations/049_matter_endpoints_sqlitestore.sql) rebuilt the table onto that schema row for row, keeping every endpoint number, because a paired controller keys its accessory cache on them.
- `matter_exposures` (migration `009_matter_exposures.sql`) — the operator's allowlist, which keeps the 5-tuple as its primary key.

The rendering of `SourceKey.String()` is the sole input to every bridged endpoint's Matter UniqueID. Changing one byte of it re-identifies the whole fleet under every commissioned controller; `TestSourceKeyRendering` pins it.

### Profile-side cluster ports

Each device profile projects itself onto Matter in its own package, in `internal/model/custom/<profile>/matter*.go` (`climate`, `cover`, `light`, `lock`, `siren`, `switch`) and, for the generic switch and select data points, in `internal/model/generic/switch_matter.go` and `select_matter.go`. A profile is one bridged device it builds itself from cluster declarations (`interfaces.MatterDeviceSource`): it asserts `MatterDeviceSource` and `MatterChangeNotifier`, declares each cluster as its generated `<cluster>def.Declare` with the host port the cluster takes (the switch profile's `onoffdef.Declare{Sink, Initial}`, the lock's `doorlockdef.Declare{StateSource}`, the light's `levelcontroldef.Declare{Ramp}`), builds the device once through `device.FromSpec` (`generic.NewMatterDevice`, held by `generic.MatterDeviceHolder`) and feeds the CCU's confirmed state into the kit's servers through their typed `Set` or inbound setters. The profile decides *which* Homematic parameter a Matter attribute or command maps to; the cluster semantics, the servers and their DataVersions come from go-fabric. A server a profile must still build itself goes through `device.HostServer`; none does today.

### Commissioning block on a broken attestation chain

When `north.matter.attestation` names a chain that does not load, the daemon does not fall back to the CSA test chain and does not stop the bridge. `matterAttestation` returns the error, `startMatterBridge` calls `Node.SetCommissioningBlocked` before `Start`, and:

- controllers already paired keep working — attestation is checked only while commissioning;
- every new commissioning window is refused, including the REST opener, which answers `409 Conflict` with `ErrCommissioningBlocked` ([`internal/north/rest/handlers/matter.go`](https://github.com/SukramJ/openccu-loom/blob/main/internal/north/rest/handlers/matter.go));
- the health component `matter.bridge` reports the cause as unhealthy; the component is not critical, so `/health` degrades and stays `200` ([`cmd/openccu-loom/matter_health.go`](https://github.com/SukramJ/openccu-loom/blob/main/cmd/openccu-loom/matter_health.go)).

The reasoning is in the Amendment of ADR 0084.

## matter.js is the gold standard

The Matter side is a semantic port of matter.js HEAD ([ADR 0012](../adr/0012-matter-pure-go-implementation.md), [ADR 0084](../adr/0084-matter-device-layer-and-node-facade.md)). Cluster IDs, revisions, attribute IDs, constraints, defaults and wire shape are taken from matter.js, never hand-coded from the specification. The wire-level parity tests run in go-fabric; this repository holds the host half — the projection of its model onto the contract ports. The [Matter parity contract](../matter-parity-contract.md) lists the guards on this side, among them:

- `TestMatterSchemaSnapshotInSync` (`tests/contract/matter_schema_sync_test.go`) — this repository's pinned copy of the schema snapshot, [`notes/parity/matter/matter-schema-snapshot.json`](https://github.com/SukramJ/openccu-loom/blob/main/notes/parity/matter/matter-schema-snapshot.json), must equal the one the go-fabric module embeds;
- the scenario corpus under `tests/scenario/`, gated by `tests/contract/matter_scenario_gate_test.go`;
- `internal/north/matteradapter/parity_matterjs_test.go` and the topology golden (`topology_golden_test.go`).

Deliberate projection divergences are recorded in [`notes/parity/by_design.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/parity/by_design.md).

## Taking a go-fabric release

go-fabric has its own SemVer lane — the arrangement [ADR 0050](../adr/0050-mqtt-transport-shared-module.md) set up for the MQTT transport module. It is **not** a daemon version carrier: no config file, changelog or workflow guard is tied to it, and bumping it does not by itself oblige a daemon release.

1. Land the change in go-fabric and let its own CI go green.
2. Move the require and tidy:

    ```sh
    go get github.com/SukramJ/go-fabric@<tag>   # a tag; a commit SHA also works and records a pseudo-version
    go mod tidy
    ```

    CI runs `make tidy-check`, which fails on an untidy `go.mod` / `go.sum`.

3. If `TestMatterSchemaSnapshotInSync` fails, the module ships a different matter.js extract than the one pinned here. Refresh the pin and review what moved:

    ```sh
    make sync-matter-schema
    git diff notes/parity/matter/matter-schema-snapshot.json
    ```

    Never hand-edit either JSON file.

4. Run the gate: `make lint && make test && make contract`.
5. If the bump changes user-visible behaviour, add a `CHANGELOG.md` entry that describes the behaviour, not the module version.

There is no `go.work` and no filesystem `replace` in the committed tree; CI resolves the module from the proxy. A local `go.work` is fine for development and must never be committed.

## Related

- [Architecture](architecture.md) — where the Matter glue sits among the other adapters.
- [Composition root and wiring](composition-root.md) — how `daemon_matter.go`'s seams are pinned.
- [ADR 0049](../adr/0049-matter-one-endpoint-per-device.md) — one endpoint per device; [ADR 0063](../adr/0063-self-maintained-device-profiles.md) — the profiles are hand-maintained.
