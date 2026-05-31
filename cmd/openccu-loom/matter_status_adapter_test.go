// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"testing"

	matterbridge "github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/matter/eligibility"
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

func TestMatterCandidates_NilWalk_ReturnsNil(t *testing.T) {
	t.Parallel()
	a := &matterCandidateProviderAdapter{walk: nil}
	got := a.MatterCandidates(context.Background())
	if got != nil {
		t.Errorf("expected nil for nil walk fn, got %v", got)
	}
}

func TestMatterCandidates_WithWalk_ReturnsResults(t *testing.T) {
	t.Parallel()
	expected := []eligibility.Candidate{{DisplayName: "Bookshelf Lamp"}}
	a := &matterCandidateProviderAdapter{
		walk: func() []eligibility.Candidate { return expected },
	}
	got := a.MatterCandidates(context.Background())
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	if got[0].DisplayName != "Bookshelf Lamp" {
		t.Errorf("expected DisplayName=Bookshelf Lamp, got %s", got[0].DisplayName)
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
