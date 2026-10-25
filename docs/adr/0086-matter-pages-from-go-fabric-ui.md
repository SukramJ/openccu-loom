# ADR 0086 — The Matter pages and their API come from go-fabric-ui

- **Status**: accepted (2026-10-25)
- **Date**: 2026-10-25
- **Related**: [ADR 0084 — the Matter profiles on go-fabric's device layer](./0084-matter-device-layer-and-node-facade.md),
  [ADR 0049 — Matter one endpoint per device](./0049-matter-one-endpoint-per-device.md),
  [ADR 0020 — External client wire contract](./0020-external-client-wire-contract.md),
  go-fabric [concept v9 — fabric-ui](https://github.com/SukramJ/go-fabric/blob/main/docs/concept-v9-fabric-ui.md) (wave L1),
  [go-fabric-ui](https://github.com/SukramJ/go-fabric-ui) `v0.1.1`

## Context

Until 0.93.0 this repository carried its own Matter operator surface:
five Svelte pages under `assets/ui/src/routes/matter/`, seventeen REST
routes under `/api/v1/matter/*` with their handlers and seventy-two
handler tests, six WebSocket topics `matter.*`, the design tokens the
pages are themed with and the Home Assistant theme bridge. None of it
was about Homematic. The pages showed what every go-fabric node shows —
its health, its fabrics, a commissioning window, its endpoints, its
sessions and its DNS-SD record — and the handlers read it from
go-fabric's node facade. go-ha2fabric, the Home Assistant bridge of the
same family, needed the same pages. Keeping two copies meant fixing the
same pairing-window edge twice and drifting on the second fix.

go-fabric-ui extracted the pages and the routes from this repository at
commit `b40b289a` (its report `docs/reports/U1.md` cites the loom
`file:line` each port came from) and ships twice per version: the Go
module `github.com/SukramJ/go-fabric-ui` with the handlers
(`fabricui.Mount`) and the npm package `@sukramj/fabric-ui` with the
Svelte pages, the kit, the tokens and the theme bridge. What a host
contributes is an `ExposureProvider`: which of its items are exposable,
how they are shown, what a change would do, and the change itself.

## Decision

1. **The pages are the package's.** The SPA's Matter view mounts the six
   node pages of `@sukramj/fabric-ui` — Status, Pair, Fabrics,
   Selection, Endpoints, Diagnostics — under `#/matter/…`, with loom's
   translator, toast, confirm dialog and fetch (session cookie and CSRF
   header). loom's own copies of the pages, the Matter store, the Matter
   API types, the QR renderer, the `--ha-*` token block and the theme
   bridge are deleted; the tokens and the bridge are imported from the
   package. What loom keeps of the theme is the detection of the
   openccu-lite shell frame, which is loom's own.

2. **The API is the module's.** The router mounts `fabricui.Mount` under
   `/api/v1/node`; the `/api/v1/matter/*` routes are removed without
   aliases, because only loom's own SPA, its chip-tool harness and its
   e2e tests called them (the Python client carries the generated models,
   no calls). The REST API moves to 14.0.0. The module's contract
   (`contract/openapi.yaml`) is copied into `assets/openapi.yaml` so the
   request validator and the generated clients know the routes, with
   loom's scope block under every `x-admin` operation and the five
   inline enums named (`NodePairingSource`, …);
   `TestNodeContractMatchesPinnedGoFabricUI` holds the copy to the
   module version `go.mod` pins. Admin rights are the host's:
   `Deps.IsAdmin` answers from the request's identity, so a viewer or an
   operator gets 403 on every write and on the pairing codes, as before.
   One loom rule sits in front of the window route (`rest.NodeAPI`): the
   node's own boot window is replaced by the operator's request, as the
   daemon's own opener did — go-fabric-ui answers 409 while any window is
   open, which would refuse the first pairing after a boot and, without a
   configured passcode, every pairing until the boot window times out. A
   window the operator opened and that is still running keeps the 409.

3. **loom's own half is the exposure provider.**
   `matteradapter.ExposureProvider` lists the candidate walk's rows
   (`CollectCandidates`) merged with the allowlist
   (`matterendpoint.Store`): six columns (CCU, device address, channel,
   data-point kind, parameter, parameter label), grouped by device,
   items keyed by loom's source key `central|address|channel|kind|key`,
   the verdict from the classifier, `source` `rule` or `override`.
   Preview returns the touched rows with the proposed state; Apply
   writes one allowlist row per update after the whole batch passed, and
   records `matter_exposure_bulk` with the request's principal. Verdicts
   are independent of each other, so a preview is never wider than the
   update.

4. **Events ride the WebSocket hub, not a second stream.** go-fabric-ui's
   `Events` is wired into `node.Config.Observers` before `node.New` and
   handed to the handler; the daemon subscribes once and publishes every
   event onto the hub as `node.<type>` with the stream's payload
   (`FabricEvent`, `WindowEvent`, `{}`, `ExposureEvent`). The SPA feeds
   the pages from the hub (`subscribe` on the context) rather than
   opening the handler's SSE stream: one authenticated connection
   carries every server push, Ingress and reconnect behave as for every
   other topic, and the REST request timeout (30 s) would end an SSE
   stream after one period. The `matter.*` topics are removed;
   `wsapi.json` moves to 2.0. The handler's own `GET /node/stream` stays
   mounted because it is part of the contract, with the timeout
   documented.

5. **The assistant keeps its own status seam.** `get_matter_status`
   reads `mcp.MatterStatusReader`, implemented in the composition root
   over the node and the allowlist, because it folds in what the node
   page family does not serve (the configuration flag, the enabled
   count); the health probe reads the same adapter.

## Consequences

- One Matter operator surface for the family; a fix in go-fabric-ui
  reaches loom with a version bump, and the contract guard says what
  the bump changed.
- Two breaking contract moves in one release (REST 14.0.0, WebSocket
  2.0). The Python client regenerates its models from the new
  `openapi.yaml` and follows the `node.*` topics; its `matter.*` event
  types are dead until then.
- The device-type name in the selection list is go-fabric's
  (`contract.DeviceTypeName`), not loom's richer table
  (`interfaces.MatterDeviceTypeName`); the list shows the standard's
  names.
- The audit ledger loses the principal of a fabric removal: go-fabric-ui's
  `DELETE /node/fabrics/{index}` and factory reset reach the host only
  as the node's observer event, the same event a controller's own
  `RemoveFabric` produces, so the entry is recorded with the actor
  `node`. A host hook with the request's principal is a go-fabric-ui
  follow-up. The reassembly after an exposure change is recorded as
  `matter_force_sync` with the principal, beside the exposure entry.
- The guards that walk the chi router cannot see inside the mounted
  handler; `TestRESTRouterMatchesOpenAPISpec` treats `/node/` as a
  mounted subtree and defers to the contract guard, and the authz-scope
  guard sees the mount's own tier (the auth chain) while the admin gate
  runs inside the handler — `TestRouter_NodePairing_RequiresAdmin`
  measures that gate with the viewer and the operator as negative
  controls.
- The Playwright baselines of the Matter screenshots are rebuilt in
  the CI container; the pages' own tests live in the package.
