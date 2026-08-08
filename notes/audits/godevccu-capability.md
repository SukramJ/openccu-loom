# godevccu XML-RPC / JSON-RPC capability matrix

Source of truth for the capability tripwire in
`tests/integration/godevccu_capability_test.go`. That test probes every
method below against the in-process **godevccu** simulator through
OpenCCU-Loom's own XML-RPC / JSON-RPC clients. A method marked ✓ asserts a
non-error response (and, where noted, a well-typed shape); a method marked ✗
is a known simulator gap and the corresponding test case carries a `t.Skip`
so the suite stays green while the gap stays visible in the run output.

The `pydevccu` column records whether the upstream Python simulator
(from which godevccu is ported) implements the same method — it explains why
a handful of godevccu-only extensions (device lifecycle, BidCos interface
listing) have no pydevccu counterpart. It is documentary, not asserted by
the test.

The columns are:

- **pydevccu** — implemented in the upstream Python simulator.
- **godevccu** — implemented in the Go simulator and asserted non-error by
  the tripwire test.

## XML-RPC methods

| Method | pydevccu | godevccu | Test | Notes |
|---|---|---|---|---|
| `ping` | ✓ | ✓ | `TestCapability_ping` | Echoes the caller id back as a `pong` event. |
| `getVersion` | ✓ | ✓ | `TestCapability_getVersion` | |
| `listDevices` | ✓ | ✓ | `TestCapability_listDevices` | Seeds the device/channel addresses the other cases reuse. |
| `getDeviceDescription` | ✓ | ✓ | `TestCapability_getDeviceDescription` | Skips only when `listDevices` yielded no address. |
| `getParamsetDescription` | ✓ | ✓ | `TestCapability_getParamsetDescription` | `VALUES` paramset. |
| `getParamset` (`VALUES`) | ✓ | ✓ | `TestCapability_getParamset_VALUES` | |
| `getParamset` (`MASTER`) | ✓ | ✓ | `TestCapability_getParamset_MASTER` | Empty MASTER on some channels is not a gap. |
| `getParamset` (`LINK`) | ✓ | ✓ | `TestCapability_getParamset_LINK` | godevccu returns an empty link paramset rather than erroring. |
| `getValue` | ✓ | ✓ | `TestCapability_getValue` | Skips when the channel exposes no `VALUES` parameter. |
| `getServiceMessages` | ✓ | ✓ | `TestCapability_getServiceMessages` | |
| `addLink` | ✓ | ✓ | `TestCapability_addLink` | No-op stub. |
| `removeLink` | ✓ | ✓ | `TestCapability_removeLink` | No-op stub. |
| `getLinkPeers` | ✓ | ✓ | `TestCapability_getLinkPeers` | Returns an empty list. |
| `getLinks` | ✓ | ✓ | `TestCapability_getLinks` | Returns an empty list. |
| `reportValueUsage` | ✓ | ✓ | `TestCapability_reportValueUsage` | No-op stub. |
| `getInstallMode` | ✓ | ✓ | `TestCapability_getInstallMode` | |
| `setInstallMode` | ✓ | ✓ | `TestCapability_setInstallMode` | |
| `clientServerInitialized` | ✓ | ✓ | `TestCapability_clientServerInitialized` | Homegear ping-path stub. |
| `getMetadata` | ✓ | ✓ | `TestCapability_getMetadata` | |
| `system.listMethods` | ✓ | ✓ | `TestCapability_systemListMethods` | Must advertise the core method set (`listDevices`, `ping`, `getVersion`, `getDeviceDescription`, `getParamsetDescription`, `getParamset`, `putParamset`, `setValue`, `getValue`, `init`, `getServiceMessages`). |
| `deleteDevice` | ✗ | ✓ | `TestCapability_deleteDevice` | godevccu-only device-lifecycle extension. |
| `listBidcosInterfaces` | ✗ | ✓ | `TestCapability_listBidcosInterfaces` | godevccu-only. |
| `replaceDevice` | ✗ | ✓ | `TestCapability_replaceDevice` | godevccu-only; paired with `TestCapability_readdedDevice`. |

## JSON-RPC methods

Probed via `TestOpenCCUCapability_*`; each case skips cleanly when the
godevccu JSON-RPC listener is not reachable (`JSONRPCURL` empty).

| Method | godevccu | Test |
|---|---|---|
| `system.listMethods` | ✓ | `TestOpenCCUCapability_systemListMethods` |
| `Session.login` / `Session.logout` | ✓ | `TestOpenCCUCapability_sessionLoginLogout` |
| `CCU.getAuthEnabled` | ✓ | `TestOpenCCUCapability_getAuthEnabled` |
| `Interface.listInterfaces` / `Interface.listDevices` | ✓ | `TestOpenCCUCapability_interfaceListDevices` |
| `Program.getAll` | ✓ | advertised via `system.listMethods` |
| `SysVar.getAll` | ✓ | advertised via `system.listMethods` |
| `ReGa.runScript` | ✓ | advertised via `system.listMethods` |

## Maintenance

When you add or remove a probed method in
`tests/integration/godevccu_capability_test.go`, update the corresponding
row here so the ✓/✗ classification stays reviewable outside the test source.
