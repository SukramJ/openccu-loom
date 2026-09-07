// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestMatterBridgeSessionLookupResolvesPASEAuthMode pins that the session
// lookup the composition root attaches to the bridge can tell a PASE
// session from a CASE session by auth mode, not by fabric index.
//
// AddNOC adopts the commissioner's PASE session onto the new fabric, after
// which the session's FabricIndex is no longer 0. go-fabric keys the
// implicit Administer grant on the auth mode (matter.js
// FabricAccessControl.ts:189-191) and asks the attached lookup for it;
// without the resolver every IsPASE answers (false, false) and the ACL
// write Apple sends right after AddNOC is denied with UnsupportedAccess.
//
// Negative control: a CASE session on the same manager resolves
// (false, true), and an id nobody opened resolves (false, false) — so a
// resolver that answered "PASE" for everything would fail here too.
func TestMatterBridgeSessionLookupResolvesPASEAuthMode(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-pase", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-pase")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	bundle := startMatterBridge(ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Fatal("the Matter bridge did not start; without it this pin asserts nothing")
	}
	t.Cleanup(bundle.stop)
	if bundle.sessionLookup == nil || bundle.opMgr == nil {
		t.Fatal("the bundle carries no session lookup / manager; the pin cannot reach the wiring")
	}

	pase, err := bundle.opMgr.OpenFromPase(1, 0, 0x1111, make([]byte, 32))
	if err != nil {
		t.Fatalf("open PASE session: %v", err)
	}
	if err := bundle.opMgr.AdoptFabricIndex(pase.SessionID, 1); err != nil {
		t.Fatalf("adopt PASE session onto fabric 1 (what AddNOC does): %v", err)
	}
	if isPASE, ok := bundle.sessionLookup.IsPASE(pase.SessionID); !ok || !isPASE {
		t.Errorf("adopted PASE session resolves (pase=%v, ok=%v); want (true, true) — "+
			"the daemon never chained WithPASEResolver, so the commissioner loses Administer at AddNOC", isPASE, ok)
	}

	if isPASE, ok := bundle.sessionLookup.IsPASE(0xFFFE); ok || isPASE {
		t.Errorf("unknown session resolves (pase=%v, ok=%v); want (false, false)", isPASE, ok)
	}
}
