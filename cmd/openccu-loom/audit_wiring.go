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
// degrades to in-memory mode — List returns empty, Stream/Restore
// return ErrUnimplemented / ErrRestoreUnsupported.
//
// The BackupRestorer (HTTP-multipart upload to the CCU's
// `/config/cp_security.cgi`) is not wired yet; Restore therefore
// surfaces ErrRestoreUnsupported until that path lands.
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

// wireSessionRecorderPersistence opens (or shares) the SQLite database
// and connects it to every central's [session.Recorder] so recorded
// CCU-call sessions survive a daemon restart and can be inspected
// offline. Returns a shutdown closer that stops every per-central
// auto-persist ticker; nil safe to call when the wiring degraded
// (no DB available, no centrals registered).
//
// this is the production-replay path that was
// deferred in the audit. The daemon calls it once after StartAll;
// without it the recorder works as before (in-memory only).
func wireSessionRecorderPersistence(cfg *config.Config, reg *central.Registry, logger *slog.Logger) func() {
	if cfg == nil || reg == nil {
		return func() {}
	}
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./var"
	}
	dsn := sqlite.FileDSN(filepath.Join(dataDir, "openccu-loom.db"))
	ctx, cancel := context.WithTimeout(context.Background(), 10_000_000_000) // 10s
	defer cancel()
	db, err := sqlite.Open(ctx, dsn)
	if err != nil {
		logger.Warn(
			"session.recorder.persist.disabled",
			slog.String("dsn", dsn),
			slog.String("err", err.Error()),
		)
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
		slog.String("dsn", dsn),
		slog.Int("centrals", len(closers)),
	)
	return func() {
		for _, c := range closers {
			c()
		}
		// Release the DB handle last, after the per-central persistence
		// closers have flushed. Windows refuses to delete an open SQLite
		// file at temp-dir cleanup, so a leaked handle fails tests there.
		_ = db.Close()
	}
}

// wireIncidentRecorder constructs a SQLite-backed [reliability.IncidentRecorder]
// (via [sqlite.IncidentStore]) and installs it on every central's
// [coordinators.CacheCoordinator] via SetIncidentRecorder.
//
// It returns a teardown func that closes the underlying DB handle; the
// caller must defer it so the SQLite file is released on shutdown (a
// leaked handle blocks temp-dir cleanup on Windows). The returned func
// is never nil — it is a no-op when persistence is disabled.
//
// Degrades gracefully — if the database cannot be opened the centrals run
// without incident persistence (the slot remains nil / no-op).
func wireIncidentRecorder(cfg *config.Config, reg *central.Registry, logger *slog.Logger) (store *sqlite.IncidentStore, teardown func()) {
	if cfg == nil || reg == nil {
		return nil, func() {}
	}
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./var"
	}
	dsn := sqlite.FileDSN(filepath.Join(dataDir, "openccu-loom.db"))
	ctx, cancel := context.WithTimeout(context.Background(), 10_000_000_000) // 10s
	defer cancel()
	db, err := sqlite.Open(ctx, dsn)
	if err != nil {
		logger.Warn(
			"incident.recorder.disabled",
			slog.String("dsn", dsn),
			slog.String("err", err.Error()),
		)
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
		slog.String("dsn", dsn),
		slog.Int("centrals", len(reg.List())),
	)
	return store, func() { _ = db.Close() }
}

// wireAuditPersistence layers SQLite persistence on top of the in-
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
func wireAuditPersistence(cfg *config.Config, buf *audit.Buffer, logger *slog.Logger) audit.Recorder {
	rec, _, _ := wireAuditPersistenceWithDB(cfg, buf, logger)
	return rec
}

// wireAuditPersistenceWithDB is the variant used by callers that also
// need access to the underlying *sql.DB — currently the diagnostics
// health probe that pings `SELECT 1` on a fixed cadence. The DB is
// nil when persistence is disabled (no config, open failure); the
// caller must check before using.
func wireAuditPersistenceWithDB(cfg *config.Config, buf *audit.Buffer, logger *slog.Logger) (audit.Recorder, *gosql.DB, *audit.DurableSinkStats) {
	if cfg == nil || buf == nil {
		return buf, nil, nil
	}
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = "./var"
	}
	dsn := sqlite.FileDSN(filepath.Join(dataDir, "openccu-loom.db"))
	ctx, cancel := context.WithTimeout(context.Background(), 10_000_000_000) // 10s
	defer cancel()
	db, err := sqlite.Open(ctx, dsn)
	if err != nil {
		logger.Warn(
			"audit.persist.disabled",
			slog.String("dsn", dsn),
			slog.String("err", err.Error()),
		)
		return buf, nil, nil
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
	return audit.NewPersistedRecorder(buf, durableSink, logger), db, durableStats
}
