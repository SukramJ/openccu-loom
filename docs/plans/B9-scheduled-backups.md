# Implementation plan — B9: Scheduled / automatic CCU backups

**Summary.** Add a periodic, operator-configurable job that triggers a
CCU backup through the existing backup surface and prunes old backup
files, so a backup happens automatically without a manual REST/UI
action. Off-box backup targets (S3/WebDAV) and restore-to-new-instance
are explicitly **out of scope** here (deferred — see roadmap).

**Status.** Prioritised, not started. Effort: **S–M**.

---

## 1. Current state (verified)

- **Scheduler.** `internal/scheduler/scheduler.go` exposes
  `Scheduler.Add(j Job) error` and the job shape:
  ```go
  type JobFunc func(ctx context.Context) error
  type Job struct {
      Name       string
      Interval   time.Duration   // must be > 0
      Run        JobFunc
      RunOnStart bool            // invoke once immediately after Start
      OnStart    func(name string)
      OnComplete func(name string, durMs int64, ok bool, err error)
  }
  ```
  Jobs added before `Start(ctx)` launch together at start; added after,
  they launch immediately. A job that returns a non-nil error increments
  the per-job failure counter (`recordFailure`), surfaced via
  `JobFailures(name)` / `TotalFailures()` and the `scheduler.failures`
  health gauge.

- **Standard-job wiring.** `internal/central/jobs.go` →
  `RegisterStandardJobs(unit *central.Unit, cfg StandardJobs) ([]string, error)`
  registers per-central jobs onto `unit.Scheduler`. `StandardJobs` is a
  struct of `*Interval time.Duration` fields; each job uses the pattern
  `iv := cfg.XxxInterval; if iv <= 0 { iv = defaultXxx }`. Wired from
  `cmd/openccu-loom/daemon_jobs.go` (`jobs := central.StandardJobs{}` …
  `central.RegisterStandardJobs(u, jobs)` per unit `u`).

- **Backup surface.** `internal/north/rest/handlers/backup.go` delegates
  to `interfaces.BackupService` (`pkg/interfaces/rest_ports.go`):
  ```go
  type BackupService interface {
      TriggerBackup(ctx context.Context) (string, error) // job id
      List(ctx context.Context) ([]hmapi.BackupEntry, error)
      Stream(ctx context.Context, id string, w io.Writer) error
      Restore(ctx context.Context, id string) (string, error)
  }
  ```
  Implemented by `adapter.BackupAdapter` (`internal/central/adapter/`),
  constructed daemon-side as `buildBackupAdapter(cfg, reg, logger)`
  (`cmd/openccu-loom/daemon_southbound.go:136`), wrapping the
  `CentralRegistry` `reg`. Today a backup is only triggered by the REST
  endpoint `POST /api/v1/backups` (`TriggerBackup`).

- **UI.** `assets/ui/src/routes/BackupList.svelte` lists/downloads/restores
  backups via the same service. Scheduled backups will appear here
  automatically (same `List`).

### Committed scope: central-scoped backup + rotation

`BackupService.TriggerBackup(ctx)` is **not central-scoped** (no
`centralName` argument). This plan **commits** to making the backup
surface central-scoped rather than working around it, because a
per-central scheduled job is the only multi-CCU-correct model. Two
additions to `interfaces.BackupService` + `adapter.BackupAdapter` are
in scope (spelled out as steps 3–4 in §3):

1. **Central-scoped trigger.**
   `TriggerBackupForCentral(ctx context.Context, centralName string) (string, error)`
   — each `CentralUnit` is backed up independently. The existing
   `TriggerBackup(ctx)` stays for the REST endpoint / single-central
   convenience.
2. **Rotation primitive.**
   `Prune(ctx context.Context, centralName string, keepLast int) error`
   — lists that central's backups, sorts by age, deletes all but the
   newest `keepLast`. Honours `Backup.KeepLast`.

When implementing, read the existing `TriggerBackup` impl in
`internal/central/adapter/*backup*.go` and mirror its `CentralRegistry`
access pattern for the per-central variant.

---

## 2. Design decisions

- **Interval, not cron.** The scheduler is interval-based
  (`time.Duration`). Use a `time.Duration` config knob; `0` = disabled
  (off by default — backups touch the CCU and produce files, so opt-in).
  Do **not** introduce a cron dependency.
- **Per-central job.** Register one `central.scheduled_backup` job per
  `Unit` via `RegisterStandardJobs`, mirroring the existing
  per-central refresh jobs. Multi-CCU-correct by construction: each
  central backs up its own CCU.
- **Trigger injection.** `RegisterStandardJobs` receives `unit` but not
  the daemon-level `BackupAdapter`. Inject the trigger as a closure on
  `StandardJobs` (the daemon already holds both `reg` and the adapter):
  `BackupTrigger func(ctx context.Context) error`. daemon_jobs.go sets it
  to `func(ctx){ _, err := backupAdapter.TriggerBackupForCentral(ctx, u.Name); return err }`.
- **Retention / rotation.** Keep the last `N` scheduled backups per
  central (config `KeepLast`, default e.g. 7; `0` = keep all). After a
  successful trigger, the job calls the committed
  `Prune(ctx, centralName, keepLast)` method (added to
  `interfaces.BackupService` + `adapter.BackupAdapter`, step 4): list
  that central's backups, sort by creation time, delete all but the
  newest `keepLast`.
- **Audit + failure handling.** Emit an audit entry on each scheduled
  backup (reuse the daemon's audit recorder; see `cmd/openccu-loom/audit_wiring.go`).
  Job errors propagate via the `Run` return → `scheduler.failures` gauge;
  no extra plumbing needed.
- **RunOnStart = false.** Don't back up on every daemon start — wait for
  the first interval tick.

---

## 3. Implementation steps

1. **Config (`internal/config/config.go`).** Add a top-level
   `Backup BackupConfig` field to `Config` (a new section; backups are
   operational, not persistence-tuning, so prefer a dedicated `backup:`
   block over nesting under `persistence`). Verify hot-reload
   classification afterwards (likely hot — it only changes a job
   interval; not in the restart-required set, `internal/config/restart.go`).
   ```go
   // BackupConfig configures automatic, scheduled CCU backups.
   type BackupConfig struct {
       // Schedule is how often each central is backed up automatically.
       // Zero disables scheduled backups (manual backups still work).
       Schedule time.Duration `yaml:"schedule,omitempty" json:"schedule,omitzero" cfg:"expert"`
       // KeepLast bounds retained scheduled backups per central. Zero keeps all.
       KeepLast int `yaml:"keep_last,omitempty" json:"keep_last,omitzero" cfg:"expert"`
   }
   ```

2. **i18n labels (MANDATORY).** Add label + help for **both** new fields
   in **both** locales in `assets/ui/src/lib/i18n.ts` (`EN` and `DE`):
   `config.field.backup.schedule`, `config.help.backup.schedule`,
   `config.field.backup.keep_last`, `config.help.backup.keep_last`.
   Missing entries fail `TestConfigFieldsHaveLabelsAndHelp`
   (`tests/contract/`).

3. **StandardJobs (`internal/central/jobs.go`).** Add fields:
   ```go
   BackupInterval time.Duration                 // 0 = disabled
   BackupTrigger  func(ctx context.Context) error // central-scoped, daemon-injected
   ```
   In `RegisterStandardJobs`, after the existing jobs, add:
   ```go
   if cfg.BackupInterval > 0 && cfg.BackupTrigger != nil {
       if err := unit.Scheduler.Add(scheduler.Job{
           Name:     "central.scheduled_backup",
           Interval: cfg.BackupInterval,
           Run:      func(ctx context.Context) error { return cfg.BackupTrigger(ctx) },
       }); err != nil { return registered, err }
       registered = append(registered, "central.scheduled_backup")
   }
   ```

4. **Central-scoped backup + rotation (committed).** Extend the backup
   surface so each central can be backed up and pruned independently.

   4a. **Interface (`pkg/interfaces/rest_ports.go`).** Add two methods to
   `BackupService`:
   ```go
   type BackupService interface {
       TriggerBackup(ctx context.Context) (string, error)         // existing
       List(ctx context.Context) ([]hmapi.BackupEntry, error)     // existing
       Stream(ctx context.Context, id string, w io.Writer) error  // existing
       Restore(ctx context.Context, id string) (string, error)    // existing

       // TriggerBackupForCentral backs up exactly one central by name and
       // returns the backup/job id. Used by the per-central scheduled job.
       TriggerBackupForCentral(ctx context.Context, centralName string) (string, error)
       // Prune deletes a central's oldest backups, keeping the newest
       // keepLast. keepLast <= 0 is a no-op (keep all).
       Prune(ctx context.Context, centralName string, keepLast int) error
   }
   ```
   Update any fakes/mocks implementing `BackupService` (e.g. in
   `internal/north/rest/handlers/*_test.go`) so the build stays green.

   4b. **Adapter impl (`internal/central/adapter/*backup*.go`).**
   - `TriggerBackupForCentral`: resolve the `CentralUnit` from the
     `CentralRegistry` by `centralName` (mirror how `TriggerBackup`
     reaches a central today); return `hmerr`-wrapped errors for an
     unknown central. Multi-CCU-correct.
   - `Prune`: call `List`, filter to the named central (verify
     `hmapi.BackupEntry` carries a central field + a creation timestamp;
     if `List` is not central-scoped, add a central-filtered listing in
     the adapter), sort by creation time descending, delete every entry
     past index `keepLast` via the store's file-delete path. If no
     delete path exists on the backup store, add one
     (`Delete(ctx, id string) error`) and have `Prune` call it — this
     also enables a future per-id delete in the UI.

5. **Daemon wiring (`cmd/openccu-loom/daemon_jobs.go`).** Populate the new
   `StandardJobs` fields per unit `u`:
   ```go
   jobs.BackupInterval = cfg.Backup.Schedule
   jobs.BackupTrigger = func(ctx context.Context) error {
       if _, err := backupAdapter.TriggerBackupForCentral(ctx, u.Name); err != nil { return err }
       if cfg.Backup.KeepLast > 0 {
           return backupAdapter.Prune(ctx, u.Name, cfg.Backup.KeepLast)
       }
       return nil
   }
   ```
   `backupAdapter` is already in scope at daemon construction
   (`daemon.go:330`, `daemon_southbound.go:136`).

6. **example.config.yaml.** Document the new `backup:` block with
   annotated defaults (disabled by default).

7. **CHANGELOG.md.** Add a user-visible entry (and the HA add-on
   changelog on the next release bump).

---

## 4. Config & API contract changes

- **Config:** new `backup.schedule`, `backup.keep_last` (see steps 1–2).
  No REST/WS schema change if no new endpoint is added.
- **No `openapi.yaml`/`wsapi.json` change** is required for the MVP (the
  job is internal; backups already have endpoints). If you later expose
  "last scheduled backup time" via REST, that needs `make export-schemas`
  + an `APIVersion` bump (PR-only "api contract guard").

---

## 5. Tests

- **`internal/central/jobs_test.go`** (extend, do not create a
  coverage-named file): with a fake `BackupTrigger` recording calls,
  assert (a) the job is registered iff `BackupInterval > 0 && BackupTrigger != nil`,
  (b) a trigger error propagates (increments `scheduler.failures`),
  (c) `KeepLast == 0` skips prune.
- **`internal/central/adapter/<backup>_test.go`**: unit-test
  `TriggerBackupForCentral` + `Prune` against a fake registry/store
  (full multi-CCU matrix: prune keeps exactly the newest `keepLast`).
- Drive the scheduler deterministically with `clock.NewFake`
  (`scheduler.New(logger, clock.NewFake(...))`).

---

## 6. Project-rule checklist

- [ ] SPDX header on every new `.go` file.
- [ ] No CGo; pure-Go only.
- [ ] Multi-CCU-safe: one job per central, central-scoped trigger.
- [ ] `ctx context.Context` first arg on every new I/O method.
- [ ] No `panic` from library code; errors wrapped (`fmt.Errorf("…: %w", err)`).
- [ ] New `cfg:` fields carry `config.field.*` + `config.help.*` in EN+DE.
- [ ] `make lint && make test` green.

---

## 7. Acceptance criteria

- With `backup.schedule: 24h`, each configured central produces a backup
  roughly every 24 h with no manual action; the new backups appear in
  `GET /api/v1/backups` and in `BackupList.svelte`.
- With `backup.keep_last: 7`, never more than 7 scheduled backups per
  central remain on disk.
- A failing CCU backup surfaces on the `scheduler.failures` gauge and is
  retried on the next tick (does not crash the daemon).
- `backup.schedule: 0` (default) → no scheduled backups; manual backups
  unaffected.

---

## 8. References

- CLAUDE.md → *Common Tasks* (job/config patterns), *Architecture Quick
  Reference* (scheduler, multi-CCU), *Completion checklist*.
- `internal/scheduler/scheduler.go`, `internal/central/jobs.go`,
  `cmd/openccu-loom/daemon_jobs.go`, `internal/north/rest/handlers/backup.go`,
  `pkg/interfaces/rest_ports.go`.
- Roadmap entry: *Operations & multi-CCU → Scheduled / automatic CCU
  backups* (off-box targets deferred).
