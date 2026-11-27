// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	gosql "database/sql"
	"log/slog"
	"time"

	mattercore "thingfabric.dev/go-fabric/cluster/core"
	matterstore "thingfabric.dev/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// bootRecordTimeout bounds each boot-record statement. The record is a
// single row; a database that does not answer within it is logged and the
// boot goes on without the fact.
const bootRecordTimeout = 5 * time.Second

// takeStopRecordAtBoot retires the previous process's stop marker into the
// record this boot reads (sqlite.TakeStopRecordAtBoot). A failure is logged:
// this boot then reads the value the previous boot took, so its BootReason
// may describe the stop before last.
func takeStopRecordAtBoot(db *gosql.DB, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), bootRecordTimeout)
	defer cancel()
	if err := sqlite.TakeStopRecordAtBoot(ctx, db); err != nil {
		logger.Warn("boot_record.take_failed", slog.String("err", err.Error()))
	}
}

// pendingScrubTimeout bounds the boot's scrub: a VACUUM rewrites the whole
// database file, history included, so it gets the bound go-fabric-ui gives
// the erase that left it pending.
const pendingScrubTimeout = 2 * time.Minute

// runPendingScrub finishes the scrub an erase of the Matter node could not:
// the erase removed the node's rows, but a read transaction of the running
// daemon made its VACUUM or WAL checkpoint fail, so their bytes may remain
// in the database file or its -wal
// (matterRuntime.Erase). The boot runs it right after the database opens,
// before the subsystems that read it start, and clears the flag once it finished; a scrub
// that fails again keeps the flag for the next boot.
func runPendingScrub(db *gosql.DB, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), pendingScrubTimeout)
	defer cancel()
	rec, err := sqlite.LoadBootRecord(ctx, db)
	if err != nil {
		logger.Warn("boot_record.scrub_read_failed", slog.String("err", err.Error()))
		return
	}
	if !rec.ScrubPending {
		return
	}
	if err := matterstore.Scrub(ctx, db); err != nil {
		logger.Warn("matter.bridge.scrub_pending_failed", slog.String("err", err.Error()))
		return
	}
	if err := sqlite.ClearScrubPending(ctx, db); err != nil {
		logger.Warn("boot_record.scrub_clear_failed", slog.String("err", err.Error()))
		return
	}
	logger.Info("matter.bridge.scrub_pending_done")
}

// recordRequestedStop writes the stop marker when the shutdown running now
// followed an operator's restart request. It runs at the end of the graceful
// shutdown, before the database closes.
func recordRequestedStop(db *gosql.DB, requested bool, logger *slog.Logger) {
	if !requested {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), bootRecordTimeout)
	defer cancel()
	if err := sqlite.RecordStopRequested(ctx, db); err != nil {
		logger.Warn("boot_record.stop_record_failed", slog.String("err", err.Error()))
	}
}

// matterBootReason is the GeneralDiagnostics BootReason the node states for
// this start, from what the daemon knows and nothing else:
// SoftwareUpdateCompleted when the node last started with another version
// than the one running now, else SoftwareReset when the previous process
// ended in a graceful shutdown an operator requested, else Unspecified —
// go-fabric's zero value, matter.js's default. An update wins over a
// requested restart: it is the more specific fact about the running
// software. Power loss, a crash or a plain SIGTERM from a supervisor are
// indistinguishable to the daemon and stay Unspecified.
func matterBootReason(rec sqlite.BootRecord, running string) uint8 {
	switch {
	case rec.MatterStartedVersion != "" && rec.MatterStartedVersion != running:
		return mattercore.BootReasonSoftwareUpdate
	case rec.LastStopRequested:
		return mattercore.BootReasonSoftwareReset
	default:
		return mattercore.BootReasonUnspecified
	}
}

// loadMatterBootReason reads the boot record and derives the BootReason. A
// record that cannot be read is logged and states Unspecified.
func loadMatterBootReason(ctx context.Context, db *gosql.DB, running string, logger *slog.Logger) uint8 {
	readCtx, cancel := context.WithTimeout(ctx, bootRecordTimeout)
	defer cancel()
	rec, err := sqlite.LoadBootRecord(readCtx, db)
	if err != nil {
		logger.Warn("matter.bridge.boot_reason_unknown", slog.String("err", err.Error()))
		return mattercore.BootReasonUnspecified
	}
	return matterBootReason(rec, running)
}

// recordMatterStartedVersion stores the version the node started with, so
// the next start into another version reports a completed update. A
// failure is logged: that next start then compares against an older value.
func recordMatterStartedVersion(ctx context.Context, db *gosql.DB, running string, logger *slog.Logger) {
	writeCtx, cancel := context.WithTimeout(ctx, bootRecordTimeout)
	defer cancel()
	if err := sqlite.SetMatterStartedVersion(writeCtx, db, running); err != nil {
		logger.Warn("matter.bridge.started_version_record_failed", slog.String("err", err.Error()))
	}
}
