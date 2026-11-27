# #1119 — central binding, audit "on behalf of", method+prefix routes

Plan for the daemon-side slice of #1119, cut on 2026-11-26 at `e1a67735`
(0.108.0, API 18.0.0) on branch `claude/retry-readonly-and-editor-central`,
which already carries the #1157 commit (`59562279`). This slice is commit 2
of that pull request. Source: a read-only planning pass. The coordinator
spot-checked the points marked ✓ against the code.

## Owner decisions

From the issue comment of 2026-11-20 (issuecomment-7050653824) and the
wizard of 2026-11-26:

1. **Central binding.** Device-addressed routes honour `?central=`. A central
   that does not hold the address answers 404. Without the parameter the
   behaviour is unchanged. **Every route the editor calls is bound in this
   slice.**
2. **Audit.**
   - The authenticated principal stays the entry's credential.
   - `X-On-Behalf-Of` is an opaque string of at most 128 bytes (the relay
     sends e.g. `Name (ha:<id>)`). It is recorded only as asserted, never
     used for authorization.
   - It is accepted only from the entry credentials (`bearer`,
     `occulite-token`). From any other principal the header is ignored and
     logged at debug level.
3. **Route list.** `routes` becomes `[{method, prefix}]`, retyped in place.
   This is **API 19.0.0** (major).
4. **Audit read in the editor is in scope.** GET `/audit` is admin-only
   (`router.go:1024`), and paired credentials are never admin. Measured on
   2026-11-26:
   - a paired token carries the role it asked for; approval does not change
     it (`internal/pairing/manager.go:591-595`), and the integration asks
     for `operator` (homematicip_local `config_flow.py:1599`);
   - a pasted daemon token carries whatever role the admin minted
     (`handlers/auth.go:152-156`);
   - a box token is admin with the full scope and operator with the
     add-on's scope (`internal/auth/occulite.go:347-357`); which scope the
     box pairing grants is unverified here.

   The `device` filter of GET `/audit` matches a prefix
   (`internal/store/sqlite/audit.go:151-153`), so a scope check on
   `?device=` would leak devices sharing the prefix. Wizard 2026-11-26:
   a new route **GET `/devices/{addr}/audit`**, exact address (the device
   and its channels), open to **every principal with role ≥ operator**
   regardless of scheme. It takes the central binding like every device
   route; GET `/audit` leaves the editor manifest and stays admin-only.
5. The edit-lock key `channel:{addr}:{key}` (`handlers/paramsets.go:98`)
   stays without the central: it may lock too much, which is safe.

## Findings

- ✓ **Theme tokens are already done on main.** go-fabric-ui#72 shipped in
  v0.10.0. Both pins are on v0.14.1 (`go.mod:67`,
  `assets/ui/package.json:46`, npm `@thingfabric/fabric-ui`). 5b5b976e (#1164)
  removed the `test.fail`. Only ADR 0089:213-215 and
  `notes/plans/editor-component-2b.md:11,42` still read as pending.
- ✓ **Retyping `routes` requires a major bump**
  (`tests/contract/api_surface_bump_test.go:209-212`).
- ✓ **Audit rows written in the adapter carry no `User`.** Affected:
  - `adapter/paramsets.go:425`, `:654`
  - `links.go:288/317/341/373`
  - `schedules.go:1182/1244`
  - `schedule_io.go:300/460/567`
  - `config_repair.go:234`
  - `custom_dp_dispatcher.go:908`

  `Record(Entry)` takes no context (`internal/audit/audit.go:340-343`), so
  ADR 0089:177 is wrong for the editor's writes. `hmreqctx.FromContext(ctx)`
  is already in reach at `paramsets.go:418`. Weigh carrying the principal
  there against a new `audit.Actor` context carrier.
- **Gap, read but not observed at runtime:** `/measurements/recording`
  (`client.ts:2579/:2602`, `RecordToggle.svelte:41`) is missing from
  `editor-routes.json`.
- **`?central=` today** is read only by GET `/devices`
  (`handlers/devices.go:680`) and by replace-candidates
  (`device_replace.go:54`). There are 65 `/devices/{addr}` routes: router.go
  880-1110, plus `:891`, `:1291`, `:1335-1340`. `GetParamsetOn` with a
  foreign central ends in `ErrNoParamsetBackend` (`paramsets.go:841`), which
  the handler answers with 502. The 404 therefore needs a guard in front.

## Design

1. **Central guard.** New file `handlers/device_central.go`.
   - Port `DeviceCentralLocator{HasCentral, CentralsHolding}`, implemented on
     `DevicesAdapter`.
   - Middleware `RequireDeviceCentral(loc, pinned)`, attached per route with
     `pr.With`:

     | Case | Answer |
     | --- | --- |
     | unknown central | 404 |
     | central does not hold the address | 404 |
     | route not pinned, address on more than one central | 409 |
     | pinned | central into ctx, `middleware.SetCentralName` |

   - Contract `TestEveryDeviceRouteHonoursCentral`: `chi.Walk` over every
     route with `?central=nope` must answer 404.
2. **Pin the editor's routes.**
   - `ParamsetService` gains `GetParamsetOn`/`PutParamsetOn`
     (`pkg/interfaces/rest_ports.go:288`).
   - Also needed: link-ps `…On`, `DeviceOn`, apply-to/targets, determine,
     ui-schema, data-points, value, flags, team, links, central-links,
     schedules, PATCH device/channel.
   - Contract test: two centrals with a colliding address; every route the
     manifest covers lands on the central it names.
3. **Manifest.** Shape `{"routes":[{"method","prefix"}]}`; `*` matches
   exactly one segment.

   | Method | Prefixes |
   | --- | --- |
   | GET | `/audit` `/auth/me` `/info` `/system/ccu` `/taxonomy` `/rooms` `/functions` `/schedules` `/devices` `/measurements/recording` |
   | PATCH | `/devices/*` |
   | PUT | `/devices/*` `/measurements/recording` |
   | POST | `/devices/*/channels/*/paramsets` `/devices/*/paramsets` `/devices/*/links/test` `/devices/*/central-links` `/devices/*/schedule/active-profile` `/devices/*/channels/*/schedule/active-profile` `/rooms` `/functions` `/sessions/edit` |
   | DELETE | `/devices/*/links` `/devices/*/central-links` `/sessions/edit` |

   Files:
   - `pkg/hmapi/editor_component.go:18-20` (`EditorRoute`)
   - `internal/north/ui/editor_component.go:63-78` (loader validation)
   - `assets/ui/src/component/routes.ts` (`routeAllowed(method, path)`)
   - `install-host.ts:74`
   - `routes.test.ts`

   Tests: contract `TestEditorRoutesExcludeLifecycle`, and a Playwright walk
   with `measurements.v1` on.
4. **Audit.**
   - Header `X-On-Behalf-Of` (rules in decision 2); malformed (too long,
     control characters, invalid UTF-8) → 400.
   - `Entry.OnBehalfOf` (`on_behalf_of`).
   - Migration `059_audit_on_behalf_of.sql`.
   - Store: `sqlite/audit.go:84/:164/:232`.
   - CSV: column appended at the end (`handlers/audit.go:234`).
   - SPA: `AuditLog.svelte:206/:341`, plus i18n in en and de.
   - Producers move to `RecordFrom(ctx, …)` (76 sites).
   - AST guards: no direct `.Record(audit.Entry{` outside `internal/audit`;
     `OnBehalfOf` never in `internal/auth`.
   - Audit read for the entry token, scoped to the device (decision 4).
5. **Contract assets.**
   - OpenAPI:
     - `EditorRoute` schema (`:13608`)
     - `CentralScope` query parameter next to `Address` (`:8540`), on every
       `/devices/{addr}` path, with 404/409
     - `OnBehalfOf` header (like `IdempotencyKey`, `:8563`)
     - `AuditEntry.on_behalf_of` (`:11523`)
   - APIVersion 19.0.0, `valueSemanticsChanges` entries, refreshed baseline.
   - `make export-schemas ui-types`.
   - MCP `list_audit` (`mcp/tools.go:81-88`) gains `on_behalf_of`.
   - wsapi unchanged.
   - Client regeneration after release.
6. **ADR 0089 amendment** "Settled by #1119": central binding, audit, route
   list, theme tokens (supersedes :213-215). Replace "Still open" (`:228`).
   Add the CHANGELOG entries.

## As built (2026-11-26)

The wizard of 2026-11-26 replaced design points 1 and 2: the adapters resolve
an address in some 80 loops of their own over 37 files ("first central by
name"), so a `…On` method per port would have doubled the port surface. The
owner chose a request scope with one resolver instead:

- `hmreqctx.WithCentralScope` / `CentralScope` carry the binding, a context
  value of its own, apart from the log enrichment's `CentralName`.
- `central.Registry.Scoped(ctx)` is the walk every request-path lookup uses:
  all units unscoped, the bound one scoped, none for an unknown central.
- `middleware.DeviceCentral` (404 unknown / not holding) is attached to all 65
  `/devices/{addr}` routes plus the new audit route; the 409 case of the
  original design does not arise, because every route honours the scope.
- `Registry.HoldsDevice` backs the guard (`DeviceCentrals` in `rest.Deps`).
- The audit actor travels the same way: `middleware.AuditActor` after the
  global identity resolver, `audit.RecordFrom(ctx, rec, entry)` for every
  producer.

Guards: `TestEveryDeviceRouteHonoursCentral`, `TestEditorRoutesExcludeLifecycle`,
`TestAuditProducersRecordThroughRecordFrom`, `TestAuthNeverReadsOnBehalfOf`.

Known gap, recorded in ADR 0089: an audit row carries no central, so the
history of an address present on two CCUs lists both CCUs' rows. Follow-ups
outside this slice: the MCP device tools' `central_name` checks against the
first central holding the address instead of binding the scope; the relay in
homematicip_local sends `?central=`, `X-On-Behalf-Of` and matches
`{method, prefix}`; the Python client is regenerated after the release.

## Architecture check

- **Binding in the daemon.** Only the daemon knows which CCU holds an
  address; a relay check on the entry name is unsound.
- **Alternatives rejected.**
  - Ambient scope via the request context: implicit, and it reaches every
    caller.
  - `/centrals/{c}/devices/{a}`: duplicates 65 routes.
- **Wildcard prefix rather than full templates.** It is the minimum that
  separates lifecycle POSTs from editor POSTs.
- **Split.** The daemon owns binding, authorization and audit. The relay owns
  entry → central (by serial), the allow-list and the HA user. The component
  owns no network access.
