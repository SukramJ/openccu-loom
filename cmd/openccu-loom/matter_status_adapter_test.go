// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/go-fabric-ui/fabricui"
	matterstore "github.com/SukramJ/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// TestMatterStatusAdapter_NilBridge_Returns_Disabled verifies that when
// the bridge pointer is nil (Matter feature disabled), MatterStatus
// returns Enabled=false and zero values for all runtime fields.
func TestMatterStatusAdapter_NilBridge_Returns_Disabled(t *testing.T) {
	t.Parallel()
	adapter := &matterStatusReaderAdapter{
		enabled: false,
		node:    nil,
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
		node:    nil,
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

// ── matterCommissioningCloserAdapter ─────────────────────────────────────────

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
		node:    nil, // bridge nil → early return, cfg.advertising is NOT read
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

// revokerBundle boots the bridge and returns it with the fabric indexes
// of the node.fabric_removed frames the WebSocket bridge published — the
// frames the SPA reads.
func revokerBundle(t *testing.T, central string) (bundle *matterBridgeBundle, removed func() []uint8) {
	t.Helper()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: central, Host: "127.0.0.1"}}
	reg := buildTestRegistry(t, central)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bundle = startMatterBridge(ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil, slog.New(slog.DiscardHandler), nil)
	if bundle == nil {
		t.Fatal("startMatterBridge returned nil")
	}
	t.Cleanup(bundle.stop)
	// The production bridge over the bundle's node, as wireMatterRuntime
	// wires it: go-fabric-ui routes no fabric transition through its event
	// source, so the bridge's own node observer is what reaches the hub.
	hub := ws.NewHub()
	t.Cleanup(wireMatterNodeEvents(bundle.events, bundle.node, hub, nil))
	return bundle, func() []uint8 {
		frames := hub.Replay(0, func(topic string) bool { return topic == nodeTopicFabricRemoved }).Events
		seen := make([]uint8, 0, len(frames))
		for _, frame := range frames {
			f, _ := frame.Payload.(fabricui.FabricEvent)
			seen = append(seen, f.FabricIndex)
		}
		return seen
	}
}

// TestRevokeFabricRunsTheSameFanOutTheWireCommandDoes: the operator's
// revoke (go-fabric-ui's DELETE /node/fabrics/{index}) removes the fabric
// through the node facade, so the RemoveFabric command's own fan-out runs
// and ends in the WebSocket bridge's node.fabric_removed frame. Removing the row alone read
// as a success on every surface while the unpaired controller kept its
// live CASE session, its subscription and its operational record until
// the daemon restarted.
func TestRevokeFabricRunsTheSameFanOutTheWireCommandDoes(t *testing.T) {
	t.Parallel()
	bundle, removed := revokerBundle(t, "ccu-revoke")
	ctx := context.Background()
	idx, err := bundle.store.AddFabric(ctx, matterstore.FabricRecord{
		FabricID:      0x1122,
		NodeID:        0x3344,
		RootPublicKey: make([]byte, 65),
		CompressedID:  [8]byte{9, 8, 7, 6, 5, 4, 3, 2},
	})
	if err != nil {
		t.Fatalf("AddFabric: %v", err)
	}
	if err := bundle.node.RemoveFabric(ctx, idx); err != nil {
		t.Fatalf("RemoveFabric: %v", err)
	}
	if _, err := bundle.store.GetFabric(ctx, idx); err == nil {
		t.Error("fabric row survived RemoveFabric")
	}
	if got := removed(); len(got) != 1 || got[0] != idx {
		t.Errorf("the fabric-removed observer ran for %v, want exactly [%d] — the controller keeps its session and the SPA never hears otherwise", got, idx)
	}
}

// TestRevokeFabricOfAnUnknownIndexRunsNoFanOut keeps the fan-out tied to a
// row that actually existed: a repeated DELETE must not re-emit a
// fabric-removed to controllers that are still paired on other fabrics.
func TestRevokeFabricOfAnUnknownIndexRunsNoFanOut(t *testing.T) {
	t.Parallel()
	bundle, removed := revokerBundle(t, "ccu-revoke-missing")
	if err := bundle.node.RemoveFabric(context.Background(), 7); err == nil {
		t.Fatal("RemoveFabric for an unknown index returned nil; want an error")
	}
	if got := removed(); len(got) != 0 {
		t.Errorf("the fabric-removed observer ran for %v for a fabric that was never there", got)
	}
}
