// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	gosql "database/sql"
	"log/slog"
	"testing"

	mattercore "thingfabric.dev/go-fabric/cluster/core"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// TestMatterBootReasonFromTheBootRecord: each fact the record carries maps
// to its BootReason, an update wins over a requested restart, and a record
// without either fact — a fresh installation included — states Unspecified.
func TestMatterBootReasonFromTheBootRecord(t *testing.T) {
	t.Parallel()
	const running = "0.108.0"
	for _, tc := range []struct {
		name string
		rec  sqlite.BootRecord
		want uint8
	}{
		{name: "fresh", rec: sqlite.BootRecord{}, want: mattercore.BootReasonUnspecified},
		{name: "same version", rec: sqlite.BootRecord{MatterStartedVersion: running}, want: mattercore.BootReasonUnspecified},
		{name: "other version", rec: sqlite.BootRecord{MatterStartedVersion: "0.107.1"}, want: mattercore.BootReasonSoftwareUpdate},
		{name: "requested stop", rec: sqlite.BootRecord{LastStopRequested: true, MatterStartedVersion: running}, want: mattercore.BootReasonSoftwareReset},
		{name: "requested stop, first start", rec: sqlite.BootRecord{LastStopRequested: true}, want: mattercore.BootReasonSoftwareReset},
		{name: "update wins", rec: sqlite.BootRecord{LastStopRequested: true, MatterStartedVersion: "0.107.1"}, want: mattercore.BootReasonSoftwareUpdate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := matterBootReason(tc.rec, running); got != tc.want {
				t.Fatalf("matterBootReason(%+v, %q) = %d, want %d", tc.rec, running, got, tc.want)
			}
		})
	}
}

// TestStartMatterBridgeStatesTheBootReason: the node startMatterBridge builds
// reports the BootReason the boot record gives, and the start records the
// running version for the next one.
func TestStartMatterBridgeStatesTheBootReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		prepare func(ctx context.Context, t *testing.T, db *gosql.DB)
		want    uint8
	}{
		{
			name:    "fresh",
			prepare: func(context.Context, *testing.T, *gosql.DB) {},
			want:    mattercore.BootReasonUnspecified,
		},
		{
			name: "after an update",
			prepare: func(ctx context.Context, t *testing.T, db *gosql.DB) {
				t.Helper()
				if err := sqlite.SetMatterStartedVersion(ctx, db, build.Version+"-previous"); err != nil {
					t.Fatal(err)
				}
			},
			want: mattercore.BootReasonSoftwareUpdate,
		},
		{
			name: "after a requested restart",
			prepare: func(ctx context.Context, t *testing.T, db *gosql.DB) {
				t.Helper()
				if err := sqlite.SetMatterStartedVersion(ctx, db, build.Version); err != nil {
					t.Fatal(err)
				}
				recordRequestedStop(db, true, slog.New(slog.DiscardHandler))
				takeStopRecordAtBoot(db, slog.New(slog.DiscardHandler))
			},
			want: mattercore.BootReasonSoftwareReset,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			central := "ccu-boot-reason"
			cfg := config.Default()
			cfg.North.Matter.Enabled = true
			cfg.North.Matter.MDNSAdvertise = "noop"
			cfg.North.Matter.Listen = "127.0.0.1:0"
			cfg.DataDir = t.TempDir()
			cfg.Centrals = []config.CentralConfig{{Name: central, Host: "127.0.0.1"}}
			db := openTestLoomDB(t)
			tc.prepare(ctx, t, db)

			bundle := startMatterBridge(ctx, cfg, buildTestRegistry(t, central), db, health.NewTracker(), nil, slog.New(slog.DiscardHandler), nil)
			if bundle == nil {
				t.Fatal("startMatterBridge returned nil")
			}
			t.Cleanup(bundle.stop)
			if got := bundle.Node().BootReason(); got != tc.want {
				t.Fatalf("node BootReason = %d, want %d", got, tc.want)
			}
			rec, err := sqlite.LoadBootRecord(ctx, db)
			if err != nil {
				t.Fatalf("load boot record: %v", err)
			}
			if rec.MatterStartedVersion != build.Version {
				t.Fatalf("recorded started version = %q, want the running %q", rec.MatterStartedVersion, build.Version)
			}
		})
	}
}
