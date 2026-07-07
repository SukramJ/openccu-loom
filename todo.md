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

- [ ] **B1 · Central edit/disable is a silent no-op until restart** (M, high)
  `cmd/openccu-loom/central_adopt.go` — `liveCentralAdmin.Put` persists the row
  but, for an already-registered central, returns without touching the running
  `central.Unit`; the `enabled:false` branch does so with no log and no teardown,
  while the SPA shows a success toast. Fix: call `orch.removeCentral` on disable
  (mirror `Delete`), and surface a "restart required" signal for a live config
  edit. Also `assets/ui/.../CentralsAdmin.svelte` toast wording.
- [ ] **B2 · Manual backup/restore pinned to one central — breaks multi-CCU** (M, high)
  `internal/central/adapter/stubs.go`, `ccu_wiring.go` — `TriggerBackup` backs up
  only the first central; `HTTPBackupRestorer` uploads every `.sbk` to one fixed
  CCU regardless of the backup's owning central (restore-to-wrong-CCU). Expose
  the existing per-central `CreateBackupForCentral`/`TriggerBackupForCentral` +
  a central-scoped restore over REST/WS; SPA picker. Violates ADR 0002.
- [ ] **B3 · MQTT handlers run blocking I/O on the client read loop → deadlock** (M, high)
  `internal/north/mqtt/birth_sync.go`, `command_subscriber.go` — `go-mqtt`
  requires handlers to return fast; `BirthSync.handle` calls `RepublishDiscovery`
  (per-entity QoS1 publish, blocks on PUBACK processed by the same loop) → self-
  deadlock on every HA restart; command handlers block up to seconds on CCU stall.
  Fix: dispatch handler bodies onto a bounded worker queue.
- [ ] **B4 · MQTT raw-plane state topics never retracted on device removal** (M, high)
  `internal/central/adapter/eventbridge.go` `onDeviceRemoved` only clears
  HA-Discovery `/config`; retained raw topics (`values`/`master`/`availability`/
  `info`/`diagnostics`) stay forever → non-HA consumers see removed devices as
  `available:true`. Reuse `Bridge.EvictState` per data point + availability topics.

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
- [ ] **P1 · values_cache periodic GC never wired** (S, med)
  `internal/store/sqlite/values_cache.go` `GCDeadRows` only called by tests →
  parameter/channel drift leaves orphan rows forever. Schedule from
  `RegisterStandardJobs` with the alive-key set.
- [ ] **P2 · values_cache flush re-persists whole central per tick** (M, med)
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
- [ ] **M2 · Dead `QoSProfile.Commands` field; doc contradicts code** (S, low)
  `internal/north/mqtt/bridge.go`/`command_subscriber.go` hardcode QoS1;
  `docs/mqtt-topic-schema.md` says QoS0. Wire `cfg.QoS.Commands` + fix the doc,
  or remove the field and reconcile the doc.
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
