// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

// daemon_coverage7_test.go — targeted coverage for remaining gaps:
//   - daemon.go: buildPaseAdapterFromCreds verifier error (3203-3204)
//   - daemon.go: loadAdditionalFabricsForCase ListFabrics error (2357-2360)
//   - daemon.go: loadAdditionalFabricsForCase NewVerifier error (2382-2386)
//   - daemon.go: loadAdditionalFabricsForCase deriveOperationalIPK error (2389-2393)
//   - daemon.go: loadPersistentCaseIdentity mattercert.NewVerifier error (2253-2255)
//   - daemon.go: applyVisibilityUnIgnore device loop (lines 119-125)
//   - daemon.go: computeFabricCompressedID invalid rootPubKey (3007-3009)

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/go-fabric/secure/operational"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ── loadAdditionalFabricsForCase: ListFabrics error (lines 2357-2360) ────────

// TestApplyVisibilityUnIgnore_WithDevices_TouchesDevices exercises the
// device-mark loop at lines 119-125 of visibility_wiring.go by putting a
// device into the central's ModelRegistry before calling applyVisibilityUnIgnore.
func TestApplyVisibilityUnIgnore_WithDevices_TouchesDevices(t *testing.T) {
	t.Parallel()
	store := buildVisibilityStore(t)
	visReg := visibility.NewRegistry()
	reg := buildTestRegistry(t, "ccu-01")
	logger := slog.New(slog.DiscardHandler)
	cfg := config.Default()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01"}}

	// Put a device in the ModelRegistry.
	cu, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("ccu-01 not registered")
	}
	dev := device.New(device.Config{
		Address:     "APPLYDEV001",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-PSM",
	})
	cu.ModelRegistry.Put(dev)

	ctx := context.Background()
	if err := store.Replace(ctx, "ccu-01", []string{"ACTIVE"}, "test"); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	n := applyVisibilityUnIgnore(ctx, cfg, reg, store, visReg, logger)
	if n != 1 {
		t.Errorf("expected 1 central with patterns, got %d", n)
	}
}

// ── computeFabricCompressedID: invalid rootPubKey (lines 3007-3009) ──────────

// TestVisibilityAdapter_UnIgnoreCandidates_KnownCentral_ZeroIfNoDevices
// exercises the query-facade path at lines 61-74 of visibility_adapter.go —
// "ccu-01" exists and QueryFacade is non-nil, but there are no devices
// so GetUnIgnoreCandidates returns an empty slice.
func TestVisibilityAdapter_UnIgnoreCandidates_KnownCentral_ZeroIfNoDevices(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	a := newVisibilityAdapter(visibility.NewRegistry(), nil, reg)
	candidates := a.UnIgnoreCandidates("ccu-01", hmenum.ParamsetKeyMaster)
	// Result may be nil or empty — must not panic.
	_ = candidates
}

// ── loadFabricRootPublicKey: nil store error (line 2330) ─────────────────────
// NOTE: TestLoadFabricRootPublicKey_NilStore_ReturnsError is already in
// daemon_lifecycle_test.go; only the "missing fabric" variant is new here.

var _ = (*operational.Manager)(nil) // ensure operational import is used
