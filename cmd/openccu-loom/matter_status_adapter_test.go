// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	matterbridge "github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// TestMatterStatusAdapter_NilBridge_Returns_Disabled verifies that when
// the bridge pointer is nil (Matter feature disabled), MatterStatus
// returns Enabled=false and zero values for all runtime fields.
func TestMatterStatusAdapter_NilBridge_Returns_Disabled(t *testing.T) {
	t.Parallel()
	adapter := &matterStatusReaderAdapter{
		enabled: false,
		bridge:  nil,
		store:   nil,
		window:  nil,
		cfg:     nil,
	}
	resp := adapter.MatterStatus(context.Background())
	if resp.Enabled {
		t.Error("expected Enabled=false when bridge is nil")
	}
	if resp.Listening {
		t.Error("expected Listening=false when bridge is nil")
	}
	if resp.ListenAddr != "" {
		t.Errorf("expected empty ListenAddr, got %q", resp.ListenAddr)
	}
	if resp.EndpointCount != 0 {
		t.Errorf("expected EndpointCount=0, got %d", resp.EndpointCount)
	}
	if resp.FabricCount != 0 {
		t.Errorf("expected FabricCount=0, got %d", resp.FabricCount)
	}
	if resp.EnabledCount != 0 {
		t.Errorf("expected EnabledCount=0, got %d", resp.EnabledCount)
	}
}

// TestMatterStatusAdapter_EnabledFalse_BridgeNil_ReturnsDisabled
// covers the enabled=true + bridge=nil branch: the bridge is explicitly
// nil so the adapter must short-circuit after setting Enabled=true.
func TestMatterStatusAdapter_EnabledTrue_BridgeNil_ReturnsOnlyEnabled(t *testing.T) {
	t.Parallel()
	adapter := &matterStatusReaderAdapter{
		enabled: true,
		bridge:  nil,
		store:   nil,
		window:  nil,
		cfg:     nil,
	}
	resp := adapter.MatterStatus(context.Background())
	// Enabled reflects the config value even when bridge is nil.
	if !resp.Enabled {
		t.Error("expected Enabled=true (config says enabled)")
	}
	// Runtime fields must be zero — no bridge to interrogate.
	if resp.Listening {
		t.Error("expected Listening=false without a live bridge")
	}
	if resp.ListenAddr != "" {
		t.Errorf("expected empty ListenAddr, got %q", resp.ListenAddr)
	}
	if resp.EndpointCount != 0 {
		t.Errorf("expected EndpointCount=0 without bridge, got %d", resp.EndpointCount)
	}
}

// ── matterFabricRevokerAdapter ────────────────────────────────────────────────

func TestRevokeFabric_NilReceiver_ReturnsError(t *testing.T) {
	t.Parallel()
	var a *matterFabricRevokerAdapter
	err := a.RevokeFabric(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error for nil receiver")
	}
}

func TestRevokeFabric_NilStore_ReturnsError(t *testing.T) {
	t.Parallel()
	a := &matterFabricRevokerAdapter{store: nil}
	err := a.RevokeFabric(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error for nil store")
	}
	if !errors.Is(err, matterbridge.ErrCommissioningWindowNotConfigured) {
		t.Errorf("expected ErrCommissioningWindowNotConfigured, got %v", err)
	}
}

// ── matterCommissioningCloserAdapter ─────────────────────────────────────────

func TestCloseCommissioningWindow_NilReceiver_ReturnsError(t *testing.T) {
	t.Parallel()
	var a *matterCommissioningCloserAdapter
	err := a.CloseCommissioningWindow(context.Background())
	if err == nil {
		t.Fatal("expected error for nil receiver")
	}
}

func TestCloseCommissioningWindow_NilWindow_ReturnsError(t *testing.T) {
	t.Parallel()
	a := &matterCommissioningCloserAdapter{window: nil}
	err := a.CloseCommissioningWindow(context.Background())
	if err == nil {
		t.Fatal("expected error for nil window")
	}
	if !errors.Is(err, matterbridge.ErrCommissioningWindowNotConfigured) {
		t.Errorf("expected ErrCommissioningWindowNotConfigured, got %v", err)
	}
}

// ── matterCandidateProviderAdapter ───────────────────────────────────────────

func TestMatterCandidates_NilReceiver_ReturnsNil(t *testing.T) {
	t.Parallel()
	var a *matterCandidateProviderAdapter
	got := a.MatterCandidates(context.Background())
	if got != nil {
		t.Errorf("expected nil for nil receiver, got %v", got)
	}
}

func TestMatterCandidates_NilFields_ReturnsNil(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-one")
	cfg := &config.Config{}

	cases := map[string]*matterCandidateProviderAdapter{
		"both nil": {},
		"nil cfg":  {reg: reg},
		"nil reg":  {cfg: cfg},
	}
	for name, a := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := a.MatterCandidates(context.Background())
			if got != nil {
				t.Errorf("expected nil, got %v", got)
			}
		})
	}
}

func TestMatterCandidates_EmptyRegistry_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-one", "ccu-two")
	a := &matterCandidateProviderAdapter{reg: reg, cfg: &config.Config{}}

	got := a.MatterCandidates(context.Background())
	if len(got) != 0 {
		t.Errorf("expected no candidates from centrals with empty ModelRegistry, got %v", got)
	}
}

// TestMatterStatusAdapter_Advertising_Propagated verifies that the
// advertising flag from the config struct is surfaced in the response
// when the bridge is nil (disabled path).
func TestMatterStatusAdapter_Advertising_Propagated(t *testing.T) {
	t.Parallel()
	adapter := &matterStatusReaderAdapter{
		enabled: true,
		bridge:  nil, // bridge nil → early return, cfg.advertising is NOT read
		store:   nil,
		window:  nil,
		cfg:     &matterStatusConfig{advertising: true},
	}
	// When bridge == nil the adapter short-circuits before reading cfg.
	// The test asserts the response is disabled-like (no panic).
	resp := adapter.MatterStatus(context.Background())
	if !resp.Enabled {
		t.Error("expected Enabled=true")
	}
	// Advertising should remain false — cfg is never consulted when bridge==nil.
	if resp.Advertising {
		t.Error("expected Advertising=false when bridge is nil")
	}
}

// TestRevokeFabricRunsTheSameTeardownTheWireCommandDoes pins that the
// operator path is not a store delete.
//
// Removing the row alone reads as a success on every surface — the SPA says
// "removed", fabric_count drops to zero — while the unpaired controller keeps
// its live CASE session, its subscription and the operational `_matter._tcp`
// record until the daemon restarts. The assertion is on the effects the
// caller cannot see: the teardown fan-out ran, and it ran for the fabric that
// was removed, with the identity read off the row before it was deleted.
func TestRevokeFabricRunsTheSameTeardownTheWireCommandDoes(t *testing.T) {
	t.Parallel()

	store := matterstore.New(openMigratedTestDB(t, "matter_revoke_test.db"))
	ctx := context.Background()
	idx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0x1122,
		NodeID:        0x3344,
		RootPublicKey: make([]byte, 65),
		CompressedID:  [8]byte{9, 8, 7, 6, 5, 4, 3, 2},
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}

	var (
		tornDown  []uint8
		withdrawn [][8]byte
		withdrewN []uint64
	)
	a := &matterFabricRevokerAdapter{
		store:    store,
		teardown: func(_ context.Context, fabricIndex uint8) { tornDown = append(tornDown, fabricIndex) },
		withdraw: func(_ context.Context, compressedID [8]byte, nodeID uint64) {
			withdrawn = append(withdrawn, compressedID)
			withdrewN = append(withdrewN, nodeID)
		},
	}
	if err := a.RevokeFabric(ctx, idx); err != nil {
		t.Fatalf("RevokeFabric: %v", err)
	}

	if _, err := store.GetFabric(ctx, idx); err == nil {
		t.Error("fabric row survived RevokeFabric")
	}
	if len(tornDown) != 1 || tornDown[0] != idx {
		t.Errorf("teardown ran for %v, want exactly [%d] — the controller keeps its session otherwise", tornDown, idx)
	}
	if len(withdrawn) != 1 || withdrawn[0] != [8]byte{9, 8, 7, 6, 5, 4, 3, 2} {
		t.Errorf("withdraw ran for %v, want the removed fabric's compressed id", withdrawn)
	}
	if len(withdrewN) != 1 || withdrewN[0] != 0x3344 {
		t.Errorf("withdraw ran for node ids %v, want [0x3344]", withdrewN)
	}
}

// TestRevokeFabricOfAnUnknownIndexRunsNoTeardown keeps the fan-out tied to a
// row that actually existed: a repeated DELETE must not re-emit a
// fabric-removed to controllers that are still paired on other fabrics.
func TestRevokeFabricOfAnUnknownIndexRunsNoTeardown(t *testing.T) {
	t.Parallel()

	store := matterstore.New(openMigratedTestDB(t, "matter_revoke_missing_test.db"))
	var ran int
	a := &matterFabricRevokerAdapter{
		store:    store,
		teardown: func(context.Context, uint8) { ran++ },
		withdraw: func(context.Context, [8]byte, uint64) { ran++ },
	}
	if err := a.RevokeFabric(context.Background(), 7); err == nil {
		t.Fatal("RevokeFabric for an unknown index returned nil; want ErrFabricNotFound")
	}
	if ran != 0 {
		t.Errorf("teardown ran %d times for a fabric that was never there", ran)
	}
}

// TestRevokeFabricKeepsTheRowWhenTheIdentityCannotBeRead pins the failure
// direction of the pre-read.
//
// The operational advertisement is keyed by the fabric's compressed ID and
// node ID, and the row is the only place that knows them: a revoke that
// cannot read them cannot retire the record. Deleting the row anyway and
// treating the failed read as "no withdraw needed" answers 204, drops
// fabric_count to zero and leaves the unpaired controller's `_matter._tcp`
// instance advertised until the daemon restarts — with no surface left that
// could tell anyone. Failing the revoke keeps the two halves together and
// leaves the operator something to retry.
func TestRevokeFabricKeepsTheRowWhenTheIdentityCannotBeRead(t *testing.T) {
	t.Parallel()

	db := openMigratedTestDB(t, "matter_revoke_unreadable_test.db")
	store := matterstore.New(db)
	ctx := context.Background()
	idx, err := store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0x1122,
		NodeID:        0x3344,
		RootPublicKey: make([]byte, 65),
		CompressedID:  [8]byte{1, 2, 3, 4, 5, 6, 7, 8},
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}
	// A row whose identity no longer decodes: the read fails while the
	// DELETE would still succeed, which is the shape every unreadable-row
	// failure has (a truncated column here, a busy table under a concurrent
	// write in production).
	if _, err := db.ExecContext(ctx,
		`UPDATE matter_fabrics SET compressed_id = ? WHERE fabric_index = ?`, []byte{1, 2}, idx); err != nil {
		t.Fatalf("corrupt the fabric row: %v", err)
	}

	var ran int
	a := &matterFabricRevokerAdapter{
		store:    store,
		teardown: func(context.Context, uint8) { ran++ },
		withdraw: func(context.Context, [8]byte, uint64) { ran++ },
	}
	if err := a.RevokeFabric(ctx, idx); err == nil {
		t.Fatal("RevokeFabric returned nil although the fabric's identity could not be read; " +
			"the caller is told the controller was unpaired while its record stays advertised")
	}
	if ran != 0 {
		t.Errorf("the removal fan-out ran %d times without the identity it needs", ran)
	}
	var rows int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM matter_fabrics WHERE fabric_index = ?`, idx).Scan(&rows); err != nil {
		t.Fatalf("count fabric rows: %v", err)
	}
	if rows != 1 {
		t.Error("the fabric row was deleted although its advertisement could not be retired; " +
			"nothing can withdraw it after this point")
	}
}
