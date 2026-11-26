// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	gosql "database/sql"
	"log/slog"
	"testing"

	basicdef "thingfabric.dev/go-fabric/cluster/spec/basicinformation"
	"thingfabric.dev/go-fabric/tlv"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestMatterNodeLabelFollowsTheConfiguration drives north.matter.node_label
// through the paths the daemon ships — the bridge's start and the config
// save's section applier, both over the reload deps the composition root
// binds — and reads the label the running root endpoint serves:
//
//  1. a save that changes the label reaches the running node at once;
//  2. a save that leaves the label alone does not touch it;
//  3. a label a controller gave the node (persisted by go-fabric, restored
//     over the configured one at start) survives a restart with the
//     configuration unchanged;
//  4. a label changed while the bridge was off is served after the start;
//
// and across all of it the root SerialNumber stays the one pinned on the
// first start, although the label it was derived from changed.
func TestMatterNodeLabelFollowsTheConfiguration(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db := openTestLoomDB(t)
	reg := buildTestRegistry(t, "ccu-label")
	cfg := matterLabelTestConfig(t, "Bridge A")

	// Start 1: the configured label is served.
	b := startLabelTestBridge(ctx, t, cfg, reg, db)
	if got := servedRootString(t, b, basicdef.AttrNodeLabel); got != "Bridge A" {
		t.Fatalf("served NodeLabel after the first start = %q, want %q", got, "Bridge A")
	}
	serial := servedRootString(t, b, basicdef.AttrSerialNumber)
	if serial != matterRootSerial(cfg.North.Matter.WithDefaults()) {
		t.Fatalf("served SerialNumber = %q, want the value derived on the first start %q",
			serial, matterRootSerial(cfg.North.Matter.WithDefaults()))
	}

	// 1. A save that changes the label applies it live.
	deps := newReloadDeps()
	deps.SetMatterNodeLabeler(b.nodeLabel)
	saved := matterLabelTestConfig(t, "Bridge B")
	deps.SetConfigAssembler(func(context.Context) (*config.Config, error) { return saved, nil })
	applier := newSectionApplier(nil, matterLabelSection{deps: deps}, nil)
	applied, err := applier.ApplySection(ctx, configstore.SectionMatter)
	if err != nil || !applied {
		t.Fatalf("ApplySection(north.matter) with a changed label = (%v, %v), want (true, nil)", applied, err)
	}
	if got := servedRootString(t, b, basicdef.AttrNodeLabel); got != "Bridge B" {
		t.Fatalf("served NodeLabel after the save = %q, want %q — the save does not reach the running node", got, "Bridge B")
	}

	// 2. A controller renames the node; a save that leaves the label
	// alone must not undo it.
	if err := b.Node().SetNodeLabel(ctx, "Controller Name"); err != nil {
		t.Fatalf("simulate the controller's rename: %v", err)
	}
	applied, err = applier.ApplySection(ctx, configstore.SectionMatter)
	if err != nil || applied {
		t.Fatalf("ApplySection(north.matter) with the label unchanged = (%v, %v), want (false, nil)", applied, err)
	}
	if got := servedRootString(t, b, basicdef.AttrNodeLabel); got != "Controller Name" {
		t.Fatalf("served NodeLabel after an unrelated save = %q, want the controller's %q", got, "Controller Name")
	}
	b.stop()

	// 3. Restart with the configuration unchanged: the controller's name
	// stands.
	b = startLabelTestBridge(ctx, t, saved, reg, db)
	if got := servedRootString(t, b, basicdef.AttrNodeLabel); got != "Controller Name" {
		t.Fatalf("served NodeLabel after a restart = %q, want the controller's %q", got, "Controller Name")
	}
	b.stop()

	// 4. The label changed while the bridge was off reaches it at start.
	off := matterLabelTestConfig(t, "Bridge C")
	b = startLabelTestBridge(ctx, t, off, reg, db)
	t.Cleanup(b.stop)
	if got := servedRootString(t, b, basicdef.AttrNodeLabel); got != "Bridge C" {
		t.Fatalf("served NodeLabel after a change while off = %q, want %q", got, "Bridge C")
	}
	if got := servedRootString(t, b, basicdef.AttrSerialNumber); got != serial {
		t.Fatalf("served SerialNumber after the label changed = %q, want the pinned %q", got, serial)
	}
}

func matterLabelTestConfig(t *testing.T, label string) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.North.Matter.NodeLabel = label
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-label", Host: "127.0.0.1"}}
	return cfg
}

func startLabelTestBridge(ctx context.Context, t *testing.T, cfg *config.Config, reg *central.Registry, db *gosql.DB) *matterBridgeBundle {
	t.Helper()
	b := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler), nil)
	if b == nil {
		t.Fatal("the Matter bridge did not start; without it this test asserts nothing")
	}
	return b
}

// servedRootString reads a string attribute the running root
// BasicInformation serves.
func servedRootString(t *testing.T, b *matterBridgeBundle, attr uint32) string {
	t.Helper()
	v, ok := b.Node().Root().BasicInformation.MatterRead(attr)
	if !ok {
		t.Fatalf("root BasicInformation does not serve attribute 0x%04X", attr)
	}
	switch s := v.(type) {
	case tlv.BoundedString:
		return s.Value
	case string:
		return s
	}
	t.Fatalf("root BasicInformation attribute 0x%04X = %#v, want a string", attr, v)
	return ""
}
