// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// TestMatterBridgeEnforcesStoredACLAfterBoot asserts the effect of the
// Matter ACL wiring rather than its presence: the dispatcher the started
// bridge hands out answers an operational request according to the entries
// in the daemon's own store.
//
// Both directions are asserted, and each catches a different defect:
//
//   - The subject named in the stored entry is GRANTED. This fails when the
//     composition root stops attaching the store-backed lister, because a
//     dispatcher without a source denies every operational request.
//   - A different subject on the same fabric is REFUSED. This fails when
//     enforcement is absent in the other direction — the historic shape,
//     where a missing source granted everything and every stored entry was
//     unenforced on reads, writes and invokes.
//
// A source-level pin cannot express either half. The wiring it watched was
// once neutralised by leaving the call in the file inside a closure nobody
// invokes, and the pin, the Matter unit tests and the whole contract suite
// all stayed green.
func TestMatterBridgeEnforcesStoredACLAfterBoot(t *testing.T) {
	t.Parallel()

	const (
		fabric      uint8  = 1
		granted     uint64 = 0x00000000_0001B669 // the commissioner's operational node id
		otherPeer   uint64 = 0x00000000_0002CCCC // another node that completed CASE on the same fabric
		bridgedEP   uint16 = 1
		onOffID     uint32 = 0x0006
		privOperate uint8  = 3
	)

	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-acl", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-acl")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Fatal("the Matter bridge did not start; without it this pin asserts nothing")
	}
	t.Cleanup(bundle.stop)

	// A commissioned fabric, then the entry a commissioner writes through
	// the AccessControl cluster on top of it: CASE, Administer, one named
	// subject. The entries are keyed on the fabric row, so both halves are
	// needed to reproduce what a paired controller leaves behind.
	rootKey := make([]byte, 65)
	rootKey[0] = 0x04
	if _, err := bundle.store.AddFabric(ctx, matterstore.FabricRecord{
		FabricIndex:   fabric,
		FabricID:      0x2906_C908_D115_D362,
		NodeID:        granted,
		RootPublicKey: rootKey,
		VendorID:      0x1234,
		Label:         "acl-effect",
	}); err != nil {
		t.Fatalf("seed fabric: %v", err)
	}
	if err := bundle.store.ReplaceACL(ctx, fabric, []matterstore.ACLEntry{{
		FabricIndex: fabric,
		Privilege:   matterstore.PrivilegeAdminister,
		AuthMode:    matterstore.AuthModeCASE,
		Subjects:    []uint64{granted},
	}}); err != nil {
		t.Fatalf("seed acl: %v", err)
	}

	checker, ok := bundle.bridge.Dispatcher().(im.ACLChecker)
	if !ok {
		t.Fatalf("the bridge's dispatcher (%T) does not check ACLs at all", bundle.bridge.Dispatcher())
	}

	if status := checker.CheckACL(ctx, fabric, granted, nil, bridgedEP, onOffID, privOperate); !status.IsSuccess() {
		t.Errorf("the subject named in the stored entry was refused (status 0x%02x) — "+
			"the daemon never attached its ACL source, so the bridge answers nothing on an operational session",
			uint8(status))
	}
	if status := checker.CheckACL(ctx, fabric, otherPeer, nil, bridgedEP, onOffID, privOperate); status.IsSuccess() {
		t.Error("a subject no stored entry names was granted — the AccessControl entries " +
			"a controller wrote are unenforced on every operational read, write and invoke")
	}
}
