// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	gosql "database/sql"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
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
// backed BackupStorage. The directory lives under cfg.DataDir/backups
// and is auto-created on first use. When the directory cannot be
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
	backupDir := filepath.Join(dataDir, "backups")
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
// db is the shared <DataDir>/openccu-loom.db handle opened once by
// [openLoomDB] in the composition root; this function never opens or closes
// it — ownership (and the final Close) stays with the caller that opened it.
func wireSessionRecorderPersistence(db *gosql.DB, reg *central.Registry, logger *slog.Logger) func() {
	if db == nil || reg == nil {
		return func() {}
	}
	store := sqlite.NewSessionRecorderStore(db)
	var closers []func()
	for _, u := range reg.List() {
		if u == nil || u.Recorder == nil {
			continue
		}
		closer := u.WireSessionRecorderPersistence(context.Background(), store, "default", 0)
		if closer != nil {
			closers = append(closers, closer)
		}
	}
	logger.Info(
		"session.recorder.persist.ready",
		slog.Int("centrals", len(closers)),
	)
	return func() {
		for _, c := range closers {
			c()
		}
	}
}

// wireIncidentRecorder constructs a SQLite-backed [reliability.IncidentRecorder]
// (via [sqlite.IncidentStore]) against the shared database handle and
// installs it on every central's [coordinators.CacheCoordinator] via
// SetIncidentRecorder.
//
// db is the shared <DataDir>/openccu-loom.db handle opened once by
// [openLoomDB] in the composition root; this function never opens or closes
// it. The returned teardown is kept for call-site symmetry with the other
// wireX helpers but is a no-op — the shared handle is closed exactly once,
// by whoever opened it.
//
// Degrades gracefully — when db is nil the centrals run without incident
// persistence (the slot remains nil / no-op).
func wireIncidentRecorder(db *gosql.DB, reg *central.Registry, logger *slog.Logger) (store *sqlite.IncidentStore, teardown func()) {
	if db == nil || reg == nil {
		return nil, func() {}
	}
	store = sqlite.NewIncidentStore(db)
	// Decorate the SQLite recorder so a successful persist also publishes an
	// IncidentRecordedEvent onto the recording central's bus (north-bound
	// consumers such as the webhook bridge subscribe to it). The store
	// returned to the caller stays the raw SQLite store for read access.
	recorder := adapter.NewPublishingIncidentRecorder(store, reg)
	for _, u := range reg.List() {
		if u == nil || u.Cache == nil {
			continue
		}
		u.Cache.SetIncidentRecorder(recorder)
	}
	logger.Info(
		"incident.recorder.ready",
		slog.Int("centrals", len(reg.List())),
	)
	return store, func() {}
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
func wireAuditPersistenceWithDB(db *gosql.DB, buf *audit.Buffer, logger *slog.Logger) (audit.Recorder, *audit.DurableSinkStats) {
	if db == nil || buf == nil {
		return buf, nil
	}
	store := sqlite.NewAuditStore(db)
	durableSink, durableStats, _ := audit.NewDurableSink(store.Append, audit.DurableSinkOptions{
		Capacity:     256,
		BlockTimeout: 2 * time.Second,
		Logger:       logger,
	})
	// durableStats is returned to the caller (not stashed in a package
	// global) so concurrent daemon instances — e.g. parallel reload
	// tests — never race on shared state. The health tracker reads it
	// per-daemon in daemon.go.
	return audit.NewPersistedRecorder(buf, durableSink, logger), durableStats
}
