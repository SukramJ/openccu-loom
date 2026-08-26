# Implementation plan — A1: Matter IM protocol depth

**Status:** partially executed. Step 2 (timed-required enforcement coverage)
shipped; Step 4 (OTA Software Update Provider) is deferred by design (see
Step 4 below); Step 3 (GroupKeyManagement group table) is a documented,
low-value gap. **Step 1 (persistent-subscription save/restore) is still
genuinely open** — no production caller invokes
`SavePersistentSubscription`/`LoadPersistentSubscriptions`.
**Audience:** a fresh Claude-Opus environment with no access to the
review conversation. Everything needed is inline. Read
[`CLAUDE.md`](../../CLAUDE.md) §"matter.js as the Matter Gold Standard"
and [`docs/matter-parity-contract.md`](../../docs/matter-parity-contract.md)
first; the hard rule there governs every step below.

## Summary

A1 hardens the Matter Interaction Model (IM). **Before doing anything,
note that this scope was re-verified against the tree and the original
framing was largely wrong** — the Subscribe state machine (quotas,
cadence, session/fabric teardown) and the Timed-interaction gate are
**already implemented and wired in production**. The genuine remaining
work is four narrower items, in tractability order:

1. **Persistent-subscription save/restore** — the SQLite store and
   schema exist but have **no production caller** (subscriptions are
   lost on daemon restart). Plus verify event-replay buffering.
2. **Timed-required enforcement coverage** — the gate works; confirm
   every attribute/command that the spec marks "timed required" (e.g.
   DoorLock operations) actually returns `NeedsTimedInteraction`.
3. **GroupKeyManagement group table** — returns empty; narrow, and
   arguably correctly deferred (HomeMatic has no group concept).
4. **OTA Software Update Provider cluster** — genuinely missing
   (only the Requestor exists). Largest net-new piece.

Gold standard for every change: **matter.js HEAD** at `../matter.js/`
(Apache-2.0). Read the cited matter.js source, mirror its behaviour,
cite `path:function` in the Go comment, and add a parity test.

---

## 1. Current state (verified)

All paths below were confirmed against the working tree. Line numbers
are approximate anchors — re-grep before editing.

### 1a. Subscribe state machine — MOSTLY DONE & WIRED (not a green field)

- **Manager** (`internal/north/matter/im/subscription/manager.go`):
  - `Config{MaxSubscriptionsPerFabric, MaxIntervalCeilingSeconds, TickInterval}`;
    defaults `MaxSubscriptionsPerFabric=16` (≈line 105),
    `MaxIntervalCeilingSeconds=3600` (≈line 111).
  - Per-fabric quota **enforced**: `m.perFabric[req.FabricIndex] >= cfg.MaxSubscriptionsPerFabric`
    → `ErrFabricQuotaExceeded` (≈line 188).
  - `validateCadence(min, max)` (≈line 175) → `ErrCadenceOutOfRange`.
  - `Subscribe`, `ClosePeer`, `CloseSession`, `CloseFabric`,
    `CloseFabricExcept` for teardown.
- **Engine** (`internal/north/matter/im/subscription/engine.go`):
  shared 250 ms ticker (`run` ≈line 36, `tick` ≈line 55) iterating all
  subscriptions; `drainEventsIfElapsed` enforces cadence. The shared-ticker
  vs. matter.js per-subscription timer divergence is documented by-design
  (engine.go header + `by_design.md` L6-PFAD-1).
- **Bridge wiring** (`internal/north/matter/bridge/`):
  `bridge.go:184 subManager *subscription.Manager`; `subscribe_dispatch.go:197`
  calls `m.Subscribe(...)`; KeepSubscriptions teardown (`registerSubscription`
  ≈line 153) mirrors matter.js `InteractionServer.ts:549-566`.
- **Production wiring** (`cmd/openccu-loom/daemon_matter.go`):
  `subMgr := subscription.NewManager(...)` (line 482), `subMgr.Start(ctx)`
  (483), `bridge.AttachSubscriptionManager(subMgr)` (484),
  `subMgr.SetEventReporter(bridge.SubscriptionEventReporter())` (512),
  session/fabric close hooks (495/545/582), `subMgr.Stop()` (848).
  → **The report pump IS wired in production.** The comment
  "when the report-pump is not yet wired" at `subscribe_dispatch.go:161`
  is defensive/stale; correct it as part of this work.
- **Persistence store EXISTS but is UNWIRED**
  (`internal/north/matter/store/subscriptions.go`):
  `PersistentSubscriptionRecord` (line 18), `SavePersistentSubscription`
  (40), `LoadPersistentSubscriptions` (65), `DeletePersistentSubscription`
  (105), `DeletePersistentSubscriptionsByFabric` (117),
  `GetPersistentSubscription` (128); intervals codec (153+). Table created
  by `internal/store/sqlite/migrations/006_matter_persistence.sql`.
  **A repo-wide grep for `SavePersistentSubscription` /
  `LoadPersistentSubscriptions` outside the store + tests returns
  nothing** → no production code saves on subscribe or restores at boot.
  **This is the real subscribe gap.**

### 1b. Timed interaction — DONE & WIRED (doc comment is stale)

- `internal/north/matter/im/timed.go`: `TimedRequest`,
  `UnmarshalTimedRequestTLV`, `StatusResponse` (Matter §10.6.10 /
  §8.7).
- `internal/north/matter/bridge/bridge.go`: `timedDeadlines sync.Map`
  keyed by `timedKey{sessionID, exchangeID}` (≈line 208-223), with the
  session-binding rationale to defeat cross-session exchange-ID attacks.
- `internal/north/matter/bridge/receive_dispatch.go`: `checkTimedGate(...)`
  is invoked before **Write** (line 240) and before **Invoke** (line 311);
  `dispatchTimedRequest` (line 370).
- **`StatusNeedsTimedInteraction` (0xc6)** is defined
  (`im/status.go:86`) and **already returned** from
  `bridge/receive.go:388`.
- ⇒ The `im/doc.go` line "Timed Request / Timed Action — not yet
  implemented" is **stale**. Fix it. The only open question is
  **coverage**: does every spec-"timed required" command/attribute reach
  the `receive.go:388` path? (See step 2.)

### 1c. GroupKeyManagement — store DONE, group table empty

- `internal/north/matter/store/groupkeys.go`: full persistence for
  `GroupKeySet` (UpsertGroupKeySet/Get/List/Remove + by-fabric) and
  `GroupKeyMapping` (Set/Remove/List). Table from migration 006.
- `internal/north/matter/cluster/core/group_key_management.go`
  (≈line 204): `groupKeyMgmtAttrGroupTable` returns `[]GroupInfoMapStruct{}`
  with the comment "Endpoints + GroupName persistence is not yet wired …
  v1.1 ships with empty group table." This is the only gap — and it is
  tied to the **Groups cluster (0x0004)**, which is a deferred stub
  because HomeMatic has no group/scene concept (`by_design.md`
  BD-Matter-P2-D19).

### 1d. OTA Software Update Provider — MISSING (genuine net-new)

- `internal/north/matter/cluster/core/` contains
  `ota_software_update_requestor.go` only — **no provider**.

---

## 2. matter.js gold-standard references

Cite these `path:function` anchors in the Go comments you add.

### Subscribe (for the persistence + replay work)

- `../matter.js/packages/protocol/src/interaction/SubscriptionOptions.ts`
  — `MIN_INTERVAL_S = 2`, `INTERNAL_INTERVAL_PUBLISHER_LIMIT_S = 180`,
  `DEFAULT_RANDOMIZATION_WINDOW_S = 10`, `ServerSubscriptionConfig.of(...)`.
  Note loom's `MaxIntervalCeilingSeconds=3600` default is a deliberate
  divergence from matter.js's 180 s publisher limit (chip-aligned; keep,
  but record it in `by_design.md`).
- `../matter.js/packages/protocol/src/interaction/Subscription.ts` and
  `InteractionMessenger.ts` — server subscription lifecycle, the
  ReportData send loop, KeepSubscriptions teardown
  (`InteractionServer.ts:549-566`, already mirrored).
- matter.js persists subscriptions via its node-state store; for the
  bridge we mirror the *intent* (restore after restart), not matter.js's
  exact store shape — loom already has its own SQLite store.

### Timed (for coverage)

- `../matter.js/packages/protocol/src/interaction/` — search
  `TimedInteraction` / the `Status.NeedsTimedInteraction` path in
  `InteractionServer.ts`. matter.js derives "timed required" from the
  cluster model's command/attribute `timed` flag; mirror by reading the
  schema's `timed` conformance rather than hand-listing commands.

### GroupKeyManagement / Groups

- `../matter.js/packages/node/src/behaviors/group-key-management/`
  (`GroupKeyManagementServer.ts`) — `GroupTable` derivation from the
  Groups cluster membership.
- `../matter.js/packages/node/src/behaviors/groups/` — group membership
  source that populates the table.

### OTA Provider (the big one)

- `../matter.js/packages/node/src/behaviors/ota-software-update-provider/OtaSoftwareUpdateProviderServer.ts`:
  - `class OtaSoftwareUpdateProviderServer` (line 83)
  - `queryImage(request)` (line 152-156) → `QueryImageResponse`
  - `applyUpdateRequest({...})` (line 395) → `ApplyUpdateResponse`
  - `notifyUpdateApplied({...})` (line 434-439)
- Cluster definition (IDs, command/response TLV shape):
  `../matter.js/packages/types/src/clusters/ota-software-update-provider.ts`.
- Device-type / endpoint composition: search
  `../matter.js/packages/node/src/devices/` and
  `../matter.js/packages/node/src/endpoints/` for how the provider
  cluster attaches to the Aggregator/RootNode.

---

## 3. Design decisions

- **Translation idioms (TS→Go):** matter.js mixins/`Behavior.with(...)`
  → struct-with-methods; `Promise<T>` → synchronous return + `error`;
  per-subscription `Time.getTimer` → loom's shared ticker (keep — it is
  the established by-design choice). `context.Context` is the first arg
  on every new I/O method.
- **Persistence lives in the existing Matter store**
  (`internal/north/matter/store/`, SQLite via `modernc.org/sqlite`, pure
  Go — no CGo). Reuse `subscriptions.go` / `groupkeys.go`; add a
  migration under `internal/store/sqlite/migrations/` (goose
  `NNNN_*.sql` with Up/Down) only if a new table/column is needed.
- **Multi-CCU / multi-fabric safety:** the Matter bridge is per-daemon
  but fabric-scoped; every persistent record already carries
  `FabricIndex`. Never assume a single fabric.
- **Threading:** keep the single shared ticker. Restore-at-boot must run
  on the daemon bring-up goroutine before the UDP transport accepts
  traffic, so restored subscriptions exist before the first controller
  ReportData poll.
- **No silent feature stubs** (matter-parity rule): do not advertise a
  cluster attribute/command unless its FeatureMap bit is set. The OTA
  Provider must be fully functional or not present.

---

## 4. Implementation steps

### Step 1 — Persistent-subscription save/restore (Effort: M)

1. In `bridge/subscribe_dispatch.go` `registerSubscription` (after a
   successful `m.Subscribe`), persist the subscription via
   `store.SavePersistentSubscription` for **operational (CASE)**
   subscriptions only (skip PASE/fabricIndex==0 — they are pre-commission
   and must not survive). Store: fabric, peer node, negotiated
   min/max intervals, attribute + event paths, `KeepSubscriptions`.
   Wire the `*store.Store` into the bridge (it already holds other store
   handles; follow the `aclLister`/`AttachACLLister` wiring style in
   `bridge.go` + `daemon_matter.go`).
2. On teardown (`ClosePeer`/`CloseSession`/`CloseFabric`), delete the
   matching persistent rows (`DeletePersistentSubscription[ByFabric]`).
3. At daemon bring-up in `cmd/openccu-loom/daemon_matter.go` (before the
   UDP transport starts serving), call `LoadPersistentSubscriptions` and
   re-register each into the Manager so cadence resumes. A restored
   subscription whose fabric no longer exists must be dropped (and its
   row deleted).
4. **Verify event-replay buffering** end-to-end:
   `im/event_filter.go` + `im/eventlog.go` already implement `EventMin`
   filtering (Matter §10.6.9). Confirm a controller reconnecting with an
   `EventFilterIB` `EventMin` only receives `Number > EventMin`. If the
   event log is not durable across restart, decide (and document in
   `by_design.md`) whether replay-after-restart is in scope or a
   deliberate divergence.
5. Update the stale comment at `subscribe_dispatch.go:161`.

### Step 2 — Timed-required enforcement coverage (Effort: S) — DONE

**Status: shipped.** The bridge now enforces server-side timed conformance:
`internal/north/matter/schema/timed.go` (`IsTimedInvoke`, matter.js-derived
"T" access quality, pinned by `TestTimedInvokeParity` against the
`administrator-commissioning.element.ts` model file — skips when the matter.js
checkout is absent) + `bridge/receive_dispatch.go` folds it into the invoke
gate (`req.TimedRequest || anyTimedRequiredInvoke(req)`), mirroring matter.js
`CommandInvokeResponse.ts:266`. Across loom's exposed root/utility clusters
AdministratorCommissioning (0x003C, all three commands `A T`) is the only
cluster with timed commands; the parity test fails if matter.js adds/removes
one. The stale `im/doc.go` line was corrected separately (PR #247). Original
step list kept below for reference.

1. Find where `receive.go:388` returns `StatusNeedsTimedInteraction` and
   determine what predicate gates it.
2. Mirror matter.js: derive "timed required" from the cluster model's
   `timed` conformance flag (see `internal/north/matter/schema/`), not a
   hand-maintained switch. If the schema does not yet carry the `timed`
   flag, add it to the generator
   (`script/generate_matter_schema.go` → `schema/clusters.go`) so the
   next `make generate-matter-schema` keeps it current.
3. Confirm DoorLock lock/unlock and any AdministratorCommissioning
   command that the spec marks timed are rejected when sent without a
   preceding `TimedRequest`.
4. Fix the stale `im/doc.go` "not yet implemented" line.

### Step 3 — GroupKeyManagement group table (Effort: S, low value)

**Recommendation: confirm-and-document rather than build**, unless the
Groups cluster (0x0004) gains real membership. Loom exposes Groups as a
deferred stub because HomeMatic has no group concept
(`by_design.md` BD-Matter-P2-D19), so the group table has nothing to
populate.

If you do build it: source membership from the Groups cluster server,
join with `store.ListGroupKeyMappings`, and return `GroupInfoMapStruct`
rows from `groupKeyMgmtAttrGroupTable` in
`cluster/core/group_key_management.go`. Otherwise: keep the empty list
and move the rationale from the code comment into a `by_design.md` entry
(code comments must not read like deferral TODOs — see
`CLAUDE.md` §"Comments in code").

### Step 4 — OTA Software Update Provider cluster (Effort: M) — DEFERRED

> **SCOPE CORRECTION (verified against matter.js, 2026-07): the locked
> "NotAvailable responder mounted on RootNode/Aggregator" premise is
> unsafe, so this step is DEFERRED — see
> `notes/parity/by_design.md` BD-Matter-OTAProvider-NotExposed.** matter.js
> composes the Provider cluster (0x0029) on **no** endpoint: it is
> mandatory on the `OtaProvider` device type (id 0x14,
> `ota-provider.element.ts:13`), NOT on RootNode (0x0016) or Aggregator
> (0x000E). Mounting 0x0029 on the RootNode makes Apple Home reject the
> node (the exact reason the existing Requestor 0x002A stays unmounted —
> `daemon_matter.go` buildRootClusters). A bridge also has no Matter-OTA
> consumer (HomeMatic devices update via the CCU; the daemon via
> Docker/GoReleaser). So the only safe exposure is a dedicated
> device-type-0x14 endpoint — a large composition matter.js itself does
> not ship and out of scope here. Implementing an unmounted responder
> would be unreachable dead code (silent stub). The command IDs / TLV
> shapes below stay accurate for that future device-type work.

**Design decision (originally locked, now superseded by the correction
above): schema-correct "NotAvailable" responder.**
The provider cluster is a minimal, fully schema-conformant responder —
it answers `QueryImage` with `status = NotAvailable` and never offers an
image. There is **NO BDX hosting and no real firmware transfer**; the
bridge does not distribute CCU/device firmware over Matter. This lets any
controller (Apple / Google / chip-tool) probe the bridge for updates
without a cluster-not-found or decode error, while keeping the surface
small and certifiable. Full image hosting / BDX is explicitly out of
scope and is recorded as a deliberate boundary in
`notes/parity/by_design.md`. Do not reopen this decision while
implementing.

Steps:

1. Add `internal/north/matter/cluster/core/ota_software_update_provider.go`
   mirroring `OtaSoftwareUpdateProviderServer.ts`: cluster ID `0x0029`,
   commands `QueryImage` (0x00) → `QueryImageResponse` (0x01),
   `ApplyUpdateRequest` (0x02) → `ApplyUpdateResponse` (0x03),
   `NotifyUpdateApplied` (0x04). Take every command/response ID, TLV
   field tag and enum value verbatim from
   `../matter.js/packages/types/src/clusters/ota-software-update-provider.ts`.
2. Implement the three commands as the NotAvailable responder, mirroring
   the handler shapes in `OtaSoftwareUpdateProviderServer.ts`:
   - `QueryImage` (mirror `queryImage`, ll. 152-156): always return a
     `QueryImageResponse` with `status = NotAvailable` (StatusEnum value
     2) and none of the image fields set (`imageUri`, `softwareVersion`,
     `softwareVersionString`, `updateToken`, `delayedActionTime`,
     `userConsentNeeded`, `metadataForRequestor`). Do not consult any
     image source.
   - `ApplyUpdateRequest` (mirror `applyUpdateRequest`, l. 395): return
     an `ApplyUpdateResponse` with `action = Discontinue`
     (ApplyUpdateActionEnum value 2) and `delayedActionTime = 0` — by the
     locked policy there is never a pending image to apply.
   - `NotifyUpdateApplied` (mirror `notifyUpdateApplied`, ll. 434-439):
     accept and no-op (status success); nothing to record.
   Cite each mirrored `OtaSoftwareUpdateProviderServer.ts:func` in the Go
   comment. Set the FeatureMap to the cluster's mandatory-only baseline
   (no optional features) — never advertise a capability the responder
   does not back.
3. Wire the cluster onto the appropriate endpoint (RootNode/Aggregator)
   following the existing core-cluster registration pattern; attach the
   server in `daemon_matter.go` next to the other core clusters.
4. Regenerate schema constants if needed (`make generate-matter-schema`),
   then reconcile `schema/clusters.go` per
   `CLAUDE.md` §"Regenerate Matter schema from matter.js HEAD".
5. Add a `notes/parity/by_design.md` entry recording the NotAvailable
   responder as the deliberate scope boundary (no BDX, no firmware
   transfer), citing the mirrored matter.js server.

---

## 5. Tests

Every Matter change **must** add or extend a parity test — PRs without
one are rejected (`CLAUDE.md`, `matter-parity-contract.md`).

- **Subscribe:** extend `im/subscription/manager_test.go` and add a
  bridge-level test that a CASE subscription is persisted, the daemon is
  "restarted" (re-run bring-up against the same store), and the
  subscription resumes its cadence. Event-replay: a focused test on
  `im/event_filter.go` proving `EventMin` exclusivity.
- **Timed:** a parity case (cluster/core or bridge) that a timed-required
  command without a preceding `TimedRequest` returns
  `0xc6 NeedsTimedInteraction`, and succeeds with one. Cross-check the
  `timed` flag against the matter.js cluster model.
- **GroupKeyManagement:** if built, a parity test for `GroupTable`
  shape vs. `GroupKeyManagementServer.ts`; if documented, no code test —
  add the `by_design.md` entry instead.
- **OTA Provider:** add `cluster/core/parity_matterjs_test.go` cases for
  the three command/response TLV shapes (verify against
  `notes/parity/matter/tlv-wire-fixtures.json`; regen the schema snapshot
  `notes/parity/matter/matter-schema-snapshot.json` if the cluster was
  not previously enumerated). A `QueryImage` → `NotAvailable` round-trip
  test through the bridge.
- **Integration (optional, hermetic):** `tests/integration/` with
  `-tags=integration` against `godevccu`. Live chip-tool sweeps follow
  `notes/contributor/chip-tool-test-brief.md` and need explicit user approval
  for any **write** to the live CCU (`CLAUDE.md` §"Live-CCU writes").

---

## 6. Project-rule checklist (per file / per PR)

- [ ] SPDX header on every new `.go` file:
      `// SPDX-License-Identifier: MIT` + `// Copyright (C) 2026 SukramJ.`
- [ ] `CGO_ENABLED=0` preserved; no new CGo, no new GPL/LGPL/MPL/AGPL deps.
- [ ] Pure-Go SQLite only (`modernc.org/sqlite`); migrations via goose.
- [ ] Multi-CCU / multi-fabric safe; no hard-coded single fabric.
- [ ] Every Matter change cites the mirrored matter.js `path:function`
      in a Go comment; deliberate divergences land in
      `notes/parity/by_design.md` (no audit-tracking tokens in comments —
      `TestDocPurity`).
- [ ] Parity test added/updated for each cluster/IM change.
- [ ] `CHANGELOG.md` entry for user-visible changes (and the HA add-on
      changelog on a release).
- [ ] `make lint && make test` green (incl. `TestDocPurity`,
      `TestMarkdownLinksValid`); Matter parity tests green.

## 7. Acceptance criteria

- A CASE subscription survives a daemon restart: after restart the bridge
  resumes ReportData on the negotiated cadence without the controller
  re-subscribing (observable: Apple Home / Google Home keep the bridged
  devices live across a daemon bounce; chip-tool `subscribe` resumes).
- A timed-required command (e.g. DoorLock unlock) is rejected with
  `0xc6` when sent without a `TimedRequest`, accepted with one
  (chip-tool: `doorlock unlock-door ...` with/without `--timedInteractionTimeoutMs`).
- `OtaSoftwareUpdateProvider.QueryImage` returns a schema-correct
  response (chip-tool / matter.js controller probe succeeds, no
  cluster-not-found / decode error).
- Group table: either populated correctly from Groups membership, or an
  empty-by-design entry exists in `by_design.md`.

## 8. Sequencing & effort

Recommended order: **Step 2 (timed coverage, S) → Step 1 (subscribe
persistence, M) → Step 4 (OTA Provider, M) → Step 3 (group table, S /
likely document-only).**

Rationale: Step 2 is a small, high-certainty correctness + doc fix on an
already-wired gate. Step 1 is the highest interop value (subscriptions
surviving restart) and is mostly wiring an existing store. Step 4 is the
largest net-new surface. Step 3 is lowest value and probably resolves to
a `by_design.md` entry.

## 9. References

- `CLAUDE.md` §"matter.js as the Matter Gold Standard", §"Live-CCU
  writes need explicit user approval", §"Comments in code".
- [`docs/matter-parity-contract.md`](../../docs/matter-parity-contract.md) — the
  standing parity guards this work must extend.
- `notes/parity/by_design.md`: L6-PFAD-1/L6-PFAD-3 (shared-ticker /
  burst-send divergences), BD-Matter-TimedAndQuotaDeferred,
  BD-Matter-P2-D18 (ScenesManagement), BD-Matter-P2-D19 (Groups).
- matter.js: `protocol/src/interaction/SubscriptionOptions.ts`,
  `Subscription.ts`, `InteractionMessenger.ts`,
  `InteractionServer.ts:549-566`;
  `node/src/behaviors/ota-software-update-provider/OtaSoftwareUpdateProviderServer.ts:83,152,395,434`;
  `types/src/clusters/ota-software-update-provider.ts`;
  `node/src/behaviors/group-key-management/`.
- Loom anchors: `internal/north/matter/im/{subscribe,timed,doc,status,event_filter,eventlog}.go`,
  `internal/north/matter/im/subscription/{manager,engine,subscription}.go`,
  `internal/north/matter/bridge/{bridge,subscribe_dispatch,receive_dispatch,receive}.go`,
  `internal/north/matter/store/{subscriptions,groupkeys}.go`,
  `internal/north/matter/cluster/core/{group_key_management,ota_software_update_requestor}.go`,
  `internal/store/sqlite/migrations/006_matter_persistence.sql`,
  `cmd/openccu-loom/daemon_matter.go:482-512`.
