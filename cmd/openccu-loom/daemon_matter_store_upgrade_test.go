// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"testing"

	matterstore "thingfabric.dev/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestMatterBridgeStartRunsGoFabricStoreUpgrade asserts the effect of the
// store upgrade at the bridge's start: a fabric's VID verification data,
// which go-fabric releases before its schema v8 kept as the settings
// matter.fabric.<index>.vid.*, sits in that fabric's columns after the
// start and the settings are gone. The daemon's migrations carry go-fabric's
// DDL only, so without the upgrade the data stays in the settings, where
// the store no longer reads it, and the fabric loses its VID verification
// statement.
func TestMatterBridgeStartRunsGoFabricStoreUpgrade(t *testing.T) {
	t.Parallel()

	const fabric uint8 = 1

	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-upgrade", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-upgrade")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// The database an earlier release left: a commissioned fabric whose
	// VID verification statement is still a setting.
	db := openTestLoomDB(t)
	seed := matterstore.New(db)
	rootKey := make([]byte, 65)
	rootKey[0] = 0x04
	if _, err := seed.AddFabric(ctx, matterstore.FabricRecord{
		FabricIndex:   fabric,
		FabricID:      0x2906_C908_D115_D362,
		NodeID:        0x0001_B669,
		RootPublicKey: rootKey,
		VendorID:      0x1234,
		Label:         "vid-upgrade",
	}); err != nil {
		t.Fatalf("seed fabric: %v", err)
	}
	const statementKey = "matter.fabric.1.vid.statement"
	if err := seed.SetSetting(ctx, statementKey, "0a0b0c"); err != nil {
		t.Fatalf("seed VID setting: %v", err)
	}

	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler), nil)
	if bundle == nil {
		t.Fatal("the Matter bridge did not start; without it this pin asserts nothing")
	}
	t.Cleanup(bundle.stop)

	var statement []byte
	if err := db.QueryRowContext(ctx,
		`SELECT vid_statement FROM matter_fabrics WHERE fabric_index = ?`, fabric).Scan(&statement); err != nil {
		t.Fatalf("read vid_statement: %v", err)
	}
	if string(statement) != "\x0a\x0b\x0c" {
		t.Errorf("vid_statement = %x, want 0a0b0c — startMatterBridge does not run go-fabric's store.Upgrade, "+
			"so the VID settings of an earlier release are never moved into their columns", statement)
	}
	if _, ok, err := seed.GetSetting(ctx, statementKey); err != nil {
		t.Fatalf("read VID setting: %v", err)
	} else if ok {
		t.Errorf("setting %s still present after the start; store.Upgrade moves and deletes it", statementKey)
	}
}
