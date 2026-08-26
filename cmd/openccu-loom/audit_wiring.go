// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	gosql "database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/internal/wiring"
)

// auditReadService combines the in-memory audit buffer (List, for the
// recent ring) with the durable SQLite store (Query, for the full
// retained history). The REST audit handler type-asserts for the
// AuditQuerier method to unlock SQL-side filtering, pagination and CSV
// export; without the store it serves the buffer alone.
type auditReadService struct {
	*audit.Buffer
	store *sqlite.AuditStore
}

// Query satisfies handlers.AuditQuerier by delegating to the durable store.
func (a auditReadService) Query(ctx context.Context, q audit.Query) ([]audit.Entry, error) {
	return a.store.Query(ctx, q)
}

// buildBackupAdapter wires the BackupAdapter against a filesystem-
// backed BackupStorage. The directory is cfg.Backup.Dir, defaulting to
// cfg.DataDir/backups, and is auto-created on first use. When it cannot be
// created (read-only filesystem, missing permissions), the adapter
// degrades to in-memory mode — List returns empty, Stream reports the
// storage as not configured, Restore returns ErrRestoreUnsupported.
//
// The per-central BackupRestorer (HTTP-multipart upload to the CCU) is
// wired later, during southbound bring-up (see the
// SetRestorerForCentral call in the adapter's CCU wiring); until a
// central's restorer is up, Restore surfaces ErrRestoreUnsupported.
func buildBackupAdapter(cfg *config.Config, reg *central.Registry, logger *slog.Logger) *adapter.BackupAdapter {
	a := adapter.NewBackupAdapter(reg).SetLogger(logger)
	if cfg == nil {
		return a
	}
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./var"
	}
	backupDir := strings.TrimSpace(cfg.Backup.Dir)
	if backupDir == "" {
		backupDir = filepath.Join(dataDir, ccuBackupsDirName)
	}
	storage, err := adapter.NewFilesystemBackupStorage(backupDir)
	if err != nil {
		logger.Warn(
			"backup.storage.disabled",
			slog.String("dir", backupDir),
			slog.String("err", err.Error()),
		)
		return a
	}
	logger.Info("backup.storage.ready", slog.String("dir", backupDir))
	return a.SetStorage(storage)
}

// wireSessionRecorderPersistence connects the shared SQLite database handle
// to every central's [session.Recorder] so recorded CCU-call sessions
// survive a daemon restart and can be inspected offline. Returns a shutdown
// closer that stops every per-central auto-persist ticker; nil-safe to call
// when the wiring degraded (no DB available, no centrals registered).
//
// It also returns a per-central hook the live-adopt orchestrator installs: the
// loop below walks the registry exactly once, so a CCU adopted at runtime
// persisted no recorded session at all — an operator who activated recording
// on it lost every capture on the next restart.
//
// db is the shared <DataDir>/openccu-loom.db handle opened once by
// [openLoomDB] in the composition root; this function never opens or closes
// it — ownership (and the final Close) stays with the caller that opened it.
func wireSessionRecorderPersistence(
	db *gosql.DB, reg *central.Registry, logger *slog.Logger,
) (teardown func()) {
	if db == nil || reg == nil {
		return func() {}
	}
	store := sqlite.NewSessionRecorderStore(db)
	var centrals atomic.Int64
	remove := reg.OnRegisterDeclared(wiring.Seam{
		Name:         "audit.session_recorder_persistence",
		Collaborator: "*sqlite.SessionRecorderStore",
		Phase:        wiring.PhasePerCentral,
		Why:          "the central's recorded session is never written to disk, so a support bundle taken after a restart has no trace of what the CCU said",
	}, func(u *central.Unit) func() {
		if u == nil || u.Recorder == nil {
			return nil
		}
		centrals.Add(1)
		return u.WireSessionRecorderPersistence(context.Background(), store, "default", 0)
	})
	logger.Info(
		"session.recorder.persist.ready",
		slog.Int64("centrals", centrals.Load()),
	)
	return remove
}

// wireIncidentRecorder constructs a SQLite-backed [reliability.IncidentRecorder]
// (via [sqlite.IncidentStore]) against the shared database handle and
// installs it on every central's [coordinators.CacheCoordinator] via
// SetIncidentRecorder.
//
// db is the shared <DataDir>/openccu-loom.db handle opened once by
// [openLoomDB] in the composition root; this function never opens or closes
// it — the shared handle is closed exactly once, by whoever opened it. The
// returned teardown only detaches the registry observer.
//
// Degrades gracefully — when db is nil the centrals run without incident
// persistence (the slot remains nil / no-op).
//
// The install rides the registry observer rather than a boot walk: a CCU
// adopted at runtime used to register no reliability incident at all — its
// circuit-breaker trips and callback failures were absent from the incidents
// surface, which reads identically to a CCU that never had a problem.
func wireIncidentRecorder(
	db *gosql.DB, reg *central.Registry, logger *slog.Logger,
) (store *sqlite.IncidentStore, teardown func()) {
	if db == nil || reg == nil {
		return nil, func() {}
	}
	store = sqlite.NewIncidentStore(db)
	// Decorate the SQLite recorder so a successful persist also publishes an
	// IncidentRecordedEvent onto the recording central's bus (north-bound
	// consumers such as the webhook bridge subscribe to it). The store
	// returned to the caller stays the raw SQLite store for read access.
	recorder := adapter.NewPublishingIncidentRecorder(store, reg)
	// The recorder is a single shared instance, so attaching is just the
	// install; there is nothing per-central to unwire (the slot dies with the
	// unit), hence the nil unwire.
	remove := reg.OnRegisterDeclared(wiring.Seam{
		Name:         "audit.incident_recorder",
		Collaborator: "*adapter.PublishingIncidentRecorder",
		Phase:        wiring.PhasePerCentral,
		Why:          "the central records no reliability incident, so GET /incidents stays empty and no IncidentRecordedEvent reaches the webhook bridge",
	}, func(u *central.Unit) func() {
		if u == nil || u.Cache == nil {
			return nil
		}
		u.Cache.SetIncidentRecorder(recorder)
		return nil
	})
	logger.Info(
		"incident.recorder.ready",
		slog.Int("centrals", len(reg.List())),
	)
	return store, remove
}

// wireAuditPersistenceWithDB layers SQLite persistence on top of the in-
// memory audit buffer. The result is what the adapter layer hands
// every domain that records changes.
//
// The wiring degrades gracefully — if the data directory cannot host
// a SQLite database, the buffered Recorder is returned unchanged so
// the daemon still boots. Persistent reads return empty in that
// fallback path.
//
// The persistence sink is wrapped in [audit.AsyncSink] so the producer
// (paramset / link / schedule writes) never blocks on disk I/O.
//
// db is the shared <DataDir>/openccu-loom.db handle opened once by
// [openLoomDB] in the composition root; this function never opens or closes
// it. A nil db (persistence disabled, or the shared open failed) degrades to
// the buffered Recorder unchanged, matching the previous no-DB behaviour.
//
// The third return value stops the sink's worker: it drains whatever is still
// queued through the sink and joins the goroutine. The caller MUST run it
// before closing db — the drain writes through that handle — and must not run
// it while producers are still recording, because an unconsumed queue makes
// them wait out the block timeout. It is safe to call more than once.
//
//nolint:gocritic // unnamedResult: mirrors audit.NewDurableSink's own (sink, stats, stop) shape
func wireAuditPersistenceWithDB(
	db *gosql.DB, buf *audit.Buffer, logger *slog.Logger,
) (audit.Recorder, *audit.DurableSinkStats, func()) {
	if db == nil || buf == nil {
		return buf, nil, func() {}
	}
	store := sqlite.NewAuditStore(db)
	durableSink, durableStats, stopSink := audit.NewDurableSink(store.Append, audit.DurableSinkOptions{
		Capacity:     256,
		BlockTimeout: 2 * time.Second,
		Logger:       logger,
	})
	// durableStats is returned to the caller (not stashed in a package
	// global) so concurrent daemon instances — e.g. parallel reload
	// tests — never race on shared state. The health tracker reads it
	// per-daemon in daemon.go.
	return audit.NewPersistedRecorder(buf, durableSink, logger), durableStats, sync.OnceFunc(stopSink)
}
