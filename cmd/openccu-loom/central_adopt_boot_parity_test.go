// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

// central_adopt_boot_parity_test.go pins that a CCU adopted at runtime comes
// up wired like one the boot path brought up.
//
// The seam is deps.onCentralOrchestrator, which hands the test the
// orchestrator the composition root built, with the per-central hooks the
// composition root registered. That matters more than it looks: every
// previous adopt test assembled its own orchestrator and installed the hooks
// it wanted to assert, so it could not tell a hook the daemon registers from
// one it forgot — and the daemon had forgotten five of them, silently, for
// every CCU added without a restart.

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/scheduler"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestAdoptedCentralIsWiredLikeABootTimeCentral boots the real composition
// root with one configured CCU, adopts a second one through the orchestrator
// the daemon built, and asserts the adopted unit carries the same per-central
// wiring the boot-time one does: the scheduled-backup job, the health seed and
// its gauges, the incident recorder, and the program-execute audit subscriber.
//
// Every assertion is on the effect — a job on the scheduler, a sample in the
// tracker, a row in the audit database — never on a setter having been called.
func TestAdoptedCentralIsWiredLikeABootTimeCentral(t *testing.T) {
	dataDir := t.TempDir()

	cfg := config.Default()
	cfg.North.REST.Enabled = new(false)
	cfg.North.UI.Enabled = new(false)
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.DataDir = dataDir
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-boot", Host: "127.0.0.1"}}
	// `backup.schedule` applies daemon-wide, so the adopted CCU must get the
	// job too. One hour keeps the job from ever firing during the test.
	cfg.Backup.Schedule = time.Hour
	cfg.Backup.KeepLast = 3

	orchCh := make(chan *centralOrchestrator, 1)
	deps := newReloadDeps()
	deps.onCentralOrchestrator = func(o *centralOrchestrator) {
		select {
		case orchCh <- o:
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- daemonServeWithDeps(ctx, cfg, &bytes.Buffer{}, &bytes.Buffer{}, deps)
	}()

	var orch *centralOrchestrator
	select {
	case orch = <-orchCh:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for the central orchestrator")
	}
	if orch == nil {
		t.Fatal("the composition root produced no central orchestrator")
	}

	const adopted = "ccu-adopted"
	adoptedCfg := unreachableTestCentralConfig(adopted)
	// The adopted row carries its own primary interface — a wired-only or
	// BidCos-only CCU has no HmIP-RF for the tracker's fallback heuristic to
	// find. It reaches the daemon through the centrals table, not through
	// cfg.Centrals, so a seed that resolves the pin from the boot config alone
	// leaves this CCU scored against an interface it does not have.
	adoptedCfg.PrimaryInterface = "HmIP-Wired"
	if err := orch.adoptCentral(ctx, adoptedCfg); err != nil {
		t.Fatalf("adoptCentral(%s): %v", adopted, err)
	}
	unit, ok := orch.reg.Get(adopted)
	if !ok {
		t.Fatalf("adopted central %q is not in the registry", adopted)
	}
	boot, ok := orch.reg.Get("ccu-boot")
	if !ok {
		t.Fatal("boot-time central 'ccu-boot' is not in the registry")
	}

	// The scheduled backup: without it the adopted CCU produces no automatic
	// backup at all, and the absence is only discoverable when a restore is
	// already needed.
	assertHasSchedulerJob(t, boot, "central.scheduled_backup")
	assertHasSchedulerJob(t, unit, "central.scheduled_backup")

	// The health seed: the synthetic "started" sample plus the gauges the
	// diagnostics dump and the SPA tile read. Without it the adopted CCU's
	// section is simply empty, which reads like an idle CCU rather than an
	// unwatched one.
	if _, found := unit.Health.Get("central"); !found {
		t.Error("the adopted central has no 'central' health sample")
	}
	gauges := unit.Health.Gauges()
	for _, name := range []string{"scheduler.jobs", "event_bus.deferred_depth"} {
		if _, found := gauges[name]; !found {
			t.Errorf("the adopted central is missing the %q health gauge", name)
		}
	}

	// The primary-interface pin, asserted through the verdict it decides:
	// with the pin in force a healthy HmIP-Wired client makes the central's
	// primary client healthy. Unpinned, the tracker falls back to its HmIP-RF
	// preference, finds no such component on this CCU, and reports the primary
	// client as down — a wrong verdict on /health and the SPA health tile.
	unit.Health.Record(adopted+"-HmIP-Wired", health.Sample{Healthy: true})
	if !unit.Health.PrimaryClientHealthy() {
		t.Error("the adopted central's primary interface is not pinned to its configured HmIP-Wired")
	}

	// The incident recorder: reliability incidents resolve it lazily off the
	// cache coordinator and are discarded when the slot is nil, so a flapping
	// adopted CCU would leave an empty incident history.
	if unit.Cache == nil || unit.Cache.GetIncidentRecorder() == nil {
		t.Error("the adopted central has no reliability incident recorder")
	}

	// The program-execute audit: the record that distinguishes a duplicate
	// run the daemon sent from one the CCU produced on its own.
	events.Publish(unit.EventBus, hmevent.ProgramExecutedEvent{
		Base:        hmevent.NewBase(),
		CentralName: adopted,
		ProgramID:   "4711",
		Trigger:     hmenum.ProgramTriggerAPI,
		Success:     true,
		Source:      "rest",
	})
	assertProgramExecuteAudited(t, filepath.Join(dataDir, "openccu-loom.db"), adopted)

	if err := orch.removeCentral(ctx, adopted); err != nil {
		t.Fatalf("removeCentral(%s): %v", adopted, err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("daemon returned error after cancel: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Error("daemon did not shut down in time")
	}
}

// assertHasSchedulerJob fails when u's scheduler carries no job named name.
func assertHasSchedulerJob(t *testing.T, u *central.Unit, name string) {
	t.Helper()
	if u.Scheduler == nil {
		t.Fatalf("central %q has no scheduler", u.Name())
	}
	jobs := u.Scheduler.Jobs()
	if slices.ContainsFunc(jobs, func(j scheduler.Job) bool { return j.Name == name }) {
		return
	}
	got := make([]string, 0, len(jobs))
	for _, j := range jobs {
		got = append(got, j.Name)
	}
	t.Errorf("central %q has no %q job; jobs = %v", u.Name(), name, got)
}

// assertProgramExecuteAudited polls the daemon's own audit database for a
// program_execute row naming centralName. The durable sink writes
// asynchronously, hence the poll; the database is the assertion target
// because that is what GET /api/v1/audit serves once SQLite is present — an
// in-memory buffer check passes while the trail an operator reads stays empty.
func assertProgramExecuteAudited(t *testing.T, dbPath, centralName string) {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", sqlitestore.FileDSN(dbPath))
	if err != nil {
		t.Fatalf("open audit db: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := sqlitestore.NewAuditStore(db)
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, qerr := store.Query(ctx, audit.Query{Limit: 100})
		if qerr == nil && slices.ContainsFunc(entries, func(e audit.Entry) bool {
			return e.Action == audit.ActionProgramExecute && strings.Contains(e.Note, "central="+centralName)
		}) {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("no %q audit row for central %q; the adopted central's program runs are unattributable",
				audit.ActionProgramExecute, centralName)
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}
