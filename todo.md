# TODO — Codebase Sweep → 0.28.0

Tracking document for the codebase-improvement sweep (Matter excluded).
Source: multi-agent survey + adversarial verification (29 confirmed, 1 refuted).
Branch: `fix/codebase-sweep-0.28.0` — one branch, one commit per group, one PR.

**Implementation policy: test-first.** Every fix lands as a failing
reproducer/test FIRST, then the fix, then green. Every group commits with
`git commit -s` (no Co-Authored-By trailer), a conventional-commit subject,
checks off its items here, and appends a user-visible bullet to
`CHANGELOG.md` `## [Unreleased]`.

Legend: `[ ]` open · `[~]` in progress · `[x]` done · `[!]` blocked/partial (see note)

---

## Tier 1 — Real correctness bugs

- [x] **B1 · Central edit/disable is a silent no-op until restart** (M, high)
  `cmd/openccu-loom/central_adopt.go` — `liveCentralAdmin.Put` persists the row
  but, for an already-registered central, returns without touching the running
  `central.Unit`; the `enabled:false` branch does so with no log and no teardown,
  while the SPA shows a success toast. Fix: call `orch.removeCentral` on disable
  (mirror `Delete`), and surface a "restart required" signal for a live config
  edit. Also `assets/ui/.../CentralsAdmin.svelte` toast wording.
  Implemented: `Put` now tears the live `central.Unit` down via
  `orch.removeCentral` (logging `central.disable.live`) when a currently-
  registered central is saved with `enabled:false`. A still-enabled edit of an
  already-live central is diffed against the previously persisted row via the
  new `centralConfigNeedsRestart` helper (host/ports/TLS/credentials/primary
  interface/interface set — the fields the running Unit only reads once, at
  adopt time) and logs `central.edit.restart_required` at Warn instead of the
  previous unconditional Info skip. `CentralsAdmin.svelte` mirrors the same
  diff client-side (`centralNeedsRestartSignal`) so `saveModal` shows the new
  `centrals.updated_restart_required` toast (EN+DE) instead of a bare success
  toast when a live edit cannot be hot-applied; `toggleEnabled`'s existing
  success toast is now honest for disable because the backend actually tears
  the connection down. New tests:
  `TestLiveCentralAdminPutDisablesRegisteredCentralTearsDownLive`,
  `TestLiveCentralAdminPutEditOfLiveCentralLogsRestartRequired`,
  `TestCentralConfigNeedsRestartDetectsSouthboundFieldChanges` (Go); a new
  `CentralsAdmin.edit.test.ts` (vitest) covering both toast paths.
- [x] **B2 · Manual backup/restore pinned to one central — breaks multi-CCU** (M, high)
  `internal/central/adapter/stubs.go`, `ccu_wiring.go` — `TriggerBackup` backs up
  only the first central; `HTTPBackupRestorer` uploads every `.sbk` to one fixed
  CCU regardless of the backup's owning central (restore-to-wrong-CCU). Expose
  the existing per-central `CreateBackupForCentral`/`TriggerBackupForCentral` +
  a central-scoped restore over REST/WS; SPA picker. Violates ADR 0002.
  Implemented: `BackupAdapter` now resolves a backup's owning central from its
  id (`ownerCentralName`, via the existing `<central>-<timestamp>` id shape)
  and holds one [`BackupRestorer`] per central (`SetRestorerForCentral` /
  `restorers map[string]BackupRestorer`); `Restore` picks strictly by
  resolved owner and never falls back to a different central's restorer —
  only to the legacy single-`SetRestorer` fallback when an id's owner can't
  be resolved at all (unknown shape / manually-imported archive), preserving
  single-CCU behaviour. `ccu_wiring.go`'s `bringUpCentral` now calls
  `SetRestorerForCentral(cc.Name, …)` for every central instead of wiring one
  global restorer for "whichever central came up first". `List` backfills
  `BackupEntry.Central` from the id via the same resolver so the SPA can
  render an owning-CCU column/picker. REST `POST /backups` accepts an
  optional `{"central_name": "..."}` body routed to
  `TriggerBackupForCentral` (omitted/empty body keeps the unscoped
  first-central default); openapi.yaml documents the new requestBody + 400
  response. WS gets a new `backups.trigger` admin command
  (`central_name` required) delegating to `TriggerBackupForCentral`,
  registered in `writeCommandRoles` alongside the legacy `backup.trigger`.
  SPA `BackupList.svelte` shows a central picker (mirroring `Energy.svelte`'s
  pattern) only when more than one central is registered; `api.triggerBackup`
  takes an optional `centralName`. `assets/openapi.yaml` / `APIVersion`
  bumped to 2.16.0; `assets/wsapi.json` documents `backups.trigger`;
  `internal/north/rest/handlers/schema_digest_gen.go` regenerated via
  `make export-schemas`. New tests:
  `TestBackupAdapterRestoreTargetsOwningCentralNotAnyOther`,
  `TestBackupAdapterRestoreUnknownOwnerNeverFallsBackToOtherCentral`,
  `TestBackupAdapterRestoreForCentralWithNoOwnerFallsBackToLegacyRestorer`,
  `TestBackupAdapterRestorerForCentralGetter`,
  `TestBackupAdapterListPopulatesCentralFromID` (Go, `internal/central/adapter`);
  `TestTriggerBackup_NoBody_CallsUnscopedTrigger`,
  `TestTriggerBackup_WithCentralName_CallsTriggerBackupForCentral`,
  `TestTriggerBackup_MalformedBody_Returns400` (REST handlers);
  `TestBackupsTriggerDelegatesToNamedCentral`,
  `TestBackupsTriggerMissingCentralNameIsBadRequest`,
  `TestBackupsTriggerPropagatesServiceError` (WS); `BackupList.test.ts`
  (Svelte/vitest) for the picker visibility + trigger routing.
- [x] **B3 · MQTT handlers run blocking I/O on the client read loop → deadlock** (M, high)
  `internal/north/mqtt/birth_sync.go`, `command_subscriber.go` — `go-mqtt`
  requires handlers to return fast; `BirthSync.handle` calls `RepublishDiscovery`
  (per-entity QoS1 publish, blocks on PUBACK processed by the same loop) → self-
  deadlock on every HA restart; command handlers block up to seconds on CCU stall.
  Fix: dispatch handler bodies onto a bounded worker queue.
  Implemented as a new `boundedDispatcher` primitive in `bridge.go` (fixed
  worker pool, per-key FIFO queues hashed by topic so same-datapoint writes
  never reorder, blocks + logs a bounded warning instead of dropping when a
  queue is full, `Close()` drains cleanly). `BirthSync` gets a dedicated
  single-worker instance (republish is idempotent); `CommandSubscriber` gets
  an 8-worker instance keyed by the inbound topic string across every
  handler (`handleDataPoint`, `handleScheduleSwitch`, `handleWeekProfile`,
  `handleCombinedDP`, `handleServiceMethod`, `handleSysvar`, `handleProgram`,
  `handleInstallMode`, `handleCDPInvoke`). Both types expose `Close()`; a
  new `CommandSubscriber.WaitIdle()` gives external test packages a
  deterministic barrier. Updated the tests that previously asserted
  synchronously right after delivering a message
  (`command_subscriber_test.go`, `command_subscriber_lifecycle_test.go`,
  `bridge_edge_cases_test.go`, `retained_filter_test.go`,
  `tests/contract/service_discovery_shape_test.go`,
  `tests/integration/svc_method_topic_test.go`) to wait on the dispatcher
  before asserting. New tests: `bridge_dispatcher_test.go` (the primitive:
  prompt-return, per-key order, Close-drains, post-Close no-op, flush
  barrier), `birth_sync_test.go` (slow-publisher deadlock reproducer +
  Close-drains), `command_subscriber_dispatch_test.go` (slow-sink
  reproducer, per-topic order, Close-drains). `go test -race` green on
  `internal/north/mqtt`, `tests/contract`, and `tests/integration`
  (`-tags=integration`).
- [x] **B4 · MQTT raw-plane state topics never retracted on device removal** (M, high)
  `internal/central/adapter/eventbridge.go` `onDeviceRemoved` only clears
  HA-Discovery `/config`; retained raw topics (`values`/`master`/`availability`/
  `info`/`diagnostics`) stay forever → non-HA consumers see removed devices as
  `available:true`. Reuse `Bridge.EvictState` per data point + availability topics.
  Implemented as `Bridge.RetractRawStateForDevice`: an address-scoped needle
  match over a new `rawTopics` index (mirrors `RetractDiscoveryForDevice`'s
  `declared`-map sweep) plus direct clears of the device-scoped availability/
  info/diagnostics topics; wired into `onDeviceRemoved` alongside the existing
  discovery retraction.

## Tier 2 — High-value UX & test gaps

- [ ] **U1 · ProgramList/SysvarList silently truncate at 50 items** (S, high)
  `assets/ui/src/lib/api/client.ts` — `listPrograms`/`listSysvars` (and
  `Favorites.svelte:36`) call paginated endpoints with no page params → only
  page 1. Loop pages like `devices.svelte.ts` or add a pager (`AuditLog` pattern).
- [ ] **T1 · MQTT combined-DP timer/sensor + schedule-entity discovery: 0% tests** (M, high)
  `internal/north/mqtt/discovery_combined.go`, `discovery_schedule.go` (~685 LoC,
  live-wired, errors discarded). Add table-driven discovery/state tests +
  assert the eventbridge discarded error is at least logged.
- [ ] **T2 · Largest live-control routes have no dedicated test** (M, high)
  `assets/ui/src/routes/DeviceDetail.svelte` (861) and `Diagnostics.svelte` (934)
  — no vitest/functional Playwright spec. Add `DeviceDetail.test.ts` +
  a `device-detail.spec.ts` covering a parameter write + toast feedback.

## Tier 3 — Medium

- [ ] **T3 · SSDP Discoverer lifecycle untested** (S, med)
  `internal/north/discovery/ssdp` at 30% — `New/Start/Stop/loop/scan/fetch/List`
  0%. `http`/`now` are injectable; add `discoverer_test.go` (httptest + fake clock).
- [ ] **T4 · CCU add-on `update_script` has no test** (S, med)
  `packaging/ccu-addon/ccu/update_script` — exit-code contract (0=no reboot,
  10=reboot) unguarded; exact subject of the 0.27.1 bug. Add a shell/bats test
  stubbing `uname`/`mount`/etc. and asserting exit code per `$1`.
- [ ] **U2 · Settings.svelte breaks the shared operating concept** (S, med)
  `assets/ui/src/routes/Settings.svelte` — inline MQTT-reload banner instead of
  toast; `schemaError` as ad-hoc Card instead of `ErrorState`. Only route with no
  Loading/Empty/ErrorState. Replace with `toastStore` + `<ErrorState onRetry>`.
- [ ] **U3 · ConfirmDialog has no focus trap** (S, med, a11y)
  `assets/ui/src/lib/components/ui/ConfirmDialog.svelte` — `aria-modal` but never
  focuses itself, no Tab trap, no focus restore. One fix benefits every
  destructive flow. Focus cancel on open, trap Tab, restore on close.
- [ ] **U4 · Reliability + values-cache admin endpoints have no UI** (M, med)
  `GET /diagnostics/reliability` (breaker state per interface) + values-cache
  stats/reset routes have no `client.ts` wrapper or SPA surface. Add wrappers +
  a Diagnostics panel next to the interfaces table.
- [ ] **A1 · GET /incidents has no filtering/pagination** (S, med)
  `internal/north/rest/handlers/incidents.go` — unbounded `SELECT`, unlike
  `/audit`. Add `central`/`since`/`until`/`limit` (SQL already scopes by central).
- [ ] **A2 · WS-only capability families invisible to REST/OpenAPI** (M, med)
  `master_profiles.list/get/match/apply`, `incidents.clear`,
  `service_messages.disable` (`assets/wsapi.json`) have no REST counterpart. Add
  `master-profiles` GET/list/match REST + `DELETE /incidents` +
  `POST /service-messages/{id}/disable`.
- [ ] **A3 · Duplicate un-deprecated token-admin API (v1)** (S, med)
  `/auth/tokens` (v1) orphaned (SPA uses v2 only) but no `deprecated: true`;
  `docs/admin/auth.md` documents only v1. Mark v1 deprecated in openapi.yaml,
  fix the doc, remove the dead `listTokens()` client wrapper.
- [x] **P1 · values_cache periodic GC never wired** (S, med)
  `internal/store/sqlite/values_cache.go` `GCDeadRows` only called by tests →
  parameter/channel drift leaves orphan rows forever. Schedule from
  `RegisterStandardJobs` with the alive-key set. Wired instead as a
  low-frequency second ticker inside `WireValuesCacheFlusher`
  (`internal/central/adapter/values_cache_flush.go`), reusing the same
  registry walk pattern; no daemon wiring call site changed.
- [x] **P2 · values_cache flush re-persists whole central per tick** (M, med)
  `internal/central/adapter/values_cache_flush.go` marks a whole central dirty on
  any change and re-UPSERTs all live/stale DPs → write amplification at fleet
  scale (ADR 0019 sizes vs ~1000 DP). Track dirty `(channel, parameter)` keys.
- [x] **O1 · No backup-before-upgrade guidance / rollback path** (M, med)
  All 26 migrations have `Down` blocks but nothing calls `DownContext`; no
  "backup before upgrade" callout. Add the callout to `docs/admin/backup.md` +
  release-notes template; decide (ADR) on downgrade support.

## Tier 4 — Low / cosmetic

- [x] **O2 · Backup docs claim secret.key is bundled in the CLI archive — it isn't** (S, high*)
  `docs/admin/backup.md` vs `cmd/openccu-loom/backup.go` (skips `secret.key`, seals
  archive with it). DR correctness bug in docs. Correct the note + print a
  reminder from `backup create`. *(low effort, high impact — pulled forward.)*
- [x] **M1 · Command-tracker/ping-pong cache metrics never populated** (S, low)
  `internal/metrics/aggregator.go` `Cache()` placeholder loop. Extend the
  `InterfaceClientMetrics` interface (+ `internal/metrics/wiring`) and sum
  `CommandTracker().Size()` / `PingPong().Size()`, or delete the dead metric.
- [x] **M2 · Dead `QoSProfile.Commands` field; doc contradicts code** (S, low)
  `internal/north/mqtt/bridge.go`/`command_subscriber.go` hardcode QoS1;
  `docs/mqtt-topic-schema.md` says QoS0. Wire `cfg.QoS.Commands` + fix the doc,
  or remove the field and reconcile the doc.
  `CommandSubscriber` now carries a `qos` field (default `QoS1`, overridable
  via `WithQoS`, source `Bridge.CommandQoS()`) that every `Start` Subscribe
  call uses; doc corrected to state the real QoS1-default/configurable policy.
  Wiring `WithQoS(bridge.CommandQoS())` at the daemon composition root
  (`cmd/openccu-loom/mqtt_supervisor.go`) is a follow-up — out of this group's
  file scope.
- [ ] **A4 · Two different pagination envelopes across list endpoints** (S, low)
  `/devices` returns `{items,page,per_page,total}`; hub lists return a bare array
  + `X-Total-Count`. Pick one (header form is more common) and align.
- [ ] **A5 · Path-naming drift: snake_case + colon-action segments** (S, low)
  `week_profile` (+ children) and `/devices/values:batch` break the kebab-case /
  plain-segment convention. Rename to `week-profile` and `/devices/values/batch`
  in openapi.yaml + router.go (+ WS mirror if any). *(cosmetic; skip if it
  destabilizes tests.)*
- [ ] **U5 · ~20 routes hand-roll native `<select>` instead of the shared primitive** (M, low)
  Migrate the static filter-dropdown usages to `lib/components/ui/Select.svelte`;
  leave genuine native-only cases documented. *(mechanical; isolated commit.)*

## Release

- [ ] **R1 · 0.28.0 release prep** — finalize `CHANGELOG.md` `[0.28.0]` (dated),
  mirror to `packaging/ha-addon/openccu-loom/CHANGELOG.md`, bump
  `internal/build/version.go` (0.27.2 → 0.28.0),
  `packaging/ha-addon/openccu-loom/config.yaml`, and `APIVersion`
  (`internal/north/rest/handlers/info.go`, 2.15.0 → 2.16.0 for the new endpoints).
- [ ] **R2 · Verify** — `make fmt`, `make lint`, `make test`/`make contract` green;
  record result here. Then push + open the PR. Actual git tag + goreleaser happen
  after PR review/merge onto protected `main` (not part of this branch).

---

## Deferred — feature ideas (next session, per user)

- Local event-driven automation/rules engine (cross-CCU) — L, high
- Scenes: saved multi-device value presets, one-call execution — M, med
- Built-in push sinks (ntfy / Pushover / Telegram) — M, med
- Energy cost/tariff tracking alongside kWh — S, med
- `LinkCoordinator.SetLinkInfo/GetLinkInfo` wiring — S, low
  (backend supports it, but no consumer today — wire only with a REST/WS caller)

## Refuted — do not pursue

- "CCU add-on has no retrievable logs" — false. `hmlog.LiveLog` +
  `GET /diagnostics/logs` (+ SSE stream) + `Logs.svelte` work on every target.
