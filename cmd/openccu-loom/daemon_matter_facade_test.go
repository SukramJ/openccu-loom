// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/go-fabric/node"
	mattersetup "github.com/SukramJ/go-fabric/secure/setup"
	"github.com/SukramJ/go-fabric/secure/spake2"
	matterstore "github.com/SukramJ/go-fabric/store"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestMatterCommissioningCredentialsKeepAConfiguredPasscode: a valid
// configured passcode and salt reach the node verbatim and count as
// configured.
func TestMatterCommissioningCredentialsKeepAConfiguredPasscode(t *testing.T) {
	t.Parallel()
	cm := config.NorthMatterCommissioning{Passcode: 20202021, Salt: "openccu-loom-dev0", Iterations: 1000}
	passcode, salt, iterations, configured, err := matterCommissioningCredentials(cm, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("matterCommissioningCredentials: %v", err)
	}
	if passcode != 20202021 || string(salt) != "openccu-loom-dev0" || iterations != 1000 || !configured {
		t.Fatalf("got passcode=%d salt=%q iterations=%d configured=%v; want the configured values, configured", passcode, salt, iterations, configured)
	}
}

// TestMatterCommissioningCredentialsSubstituteWhatTheFacadeRefuses: the
// facade will not build without a valid passcode and a salt of spake2's
// size, the daemon runs without either — so a missing passcode becomes a
// random valid one that is NOT reported as configured, and a short salt
// becomes a random one of a valid size.
func TestMatterCommissioningCredentialsSubstituteWhatTheFacadeRefuses(t *testing.T) {
	t.Parallel()
	cm := config.NorthMatterCommissioning{Passcode: 0, Salt: "short", Iterations: 1000}
	passcode, salt, _, configured, err := matterCommissioningCredentials(cm, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("matterCommissioningCredentials: %v", err)
	}
	if configured {
		t.Fatal("passcode 0 was reported as configured")
	}
	if !mattersetup.IsValidSetupPIN(passcode) || passcode == 0 {
		t.Fatalf("substitute passcode %d is not a valid setup PIN", passcode)
	}
	if len(salt) < spake2.PBKDFMinSaltSize || len(salt) > spake2.PBKDFMaxSaltSize {
		t.Fatalf("substitute salt is %d bytes, want %d..%d", len(salt), spake2.PBKDFMinSaltSize, spake2.PBKDFMaxSaltSize)
	}
	// A trivially guessable passcode is invalid too, not merely unset.
	cm.Passcode = 11111111
	if _, _, _, configured, err = matterCommissioningCredentials(cm, slog.New(slog.DiscardHandler)); err != nil || configured {
		t.Fatalf("passcode 11111111: configured=%v err=%v; want not configured", configured, err)
	}
}

func TestMatterLocales(t *testing.T) {
	t.Parallel()
	if got := matterLocales("de"); len(got) != 2 || got[0] != "de-DE" || got[1] != "en-US" {
		t.Fatalf("matterLocales(de) = %v, want [de-DE en-US]", got)
	}
	if got := matterLocales("en"); len(got) != 1 || got[0] != "en-US" {
		t.Fatalf("matterLocales(en) = %v, want [en-US]", got)
	}
}

// TestMatterAttestationWithoutVendorMaterialIsTheFacadesTestChain: no
// configured DAC / PAI / CD leaves the facade to build the CSA test chain
// (a zero node.Attestation); the dev self-signed fallback is gone.
func TestMatterAttestationWithoutVendorMaterialIsTheFacadesTestChain(t *testing.T) {
	t.Parallel()
	got := matterAttestation(config.NorthMatter{VendorID: 0xFFF1, ProductID: 0x8000}, slog.New(slog.DiscardHandler))
	if got.Chain != nil || got.CertificationDeclaration != nil {
		t.Fatalf("matterAttestation without vendor material = %+v, want the zero value", got)
	}
}

// TestMatterFabricEventsRelayToThePublisherAttachedLater: the facade's
// observers are fixed at node.New; the WebSocket publisher is attached
// afterwards and still receives the fabric index.
func TestMatterFabricEventsRelayToThePublisherAttachedLater(t *testing.T) {
	t.Parallel()
	ev := &matterFabricEvents{}
	if err := ev.fabricAdded(node.FabricInfo{FabricIndex: 3}); err != nil {
		t.Fatalf("fabricAdded before attach: %v", err)
	}
	var added, removed []uint8
	ev.set(func(i uint8) { added = append(added, i) }, func(i uint8) { removed = append(removed, i) })
	_ = ev.fabricAdded(node.FabricInfo{FabricIndex: 4})
	_ = ev.fabricRemoved(node.FabricInfo{FabricIndex: 4})
	if len(added) != 1 || added[0] != 4 || len(removed) != 1 || removed[0] != 4 {
		t.Fatalf("added=%v removed=%v, want [4] [4]", added, removed)
	}
}

// TestStartMatterBridgeHandsTheFacadeTheAssemblersGroupManager: the
// endpoints' Groups servers (the assembler's manager) and the facade's
// root GroupKeyManagement plus the bridge's group routing (node.Config.Groups)
// must be one manager — two over the same store diverge in memory and an
// AddGroup on an endpoint is not routed until the next load.
func TestStartMatterBridgeHandsTheFacadeTheAssemblersGroupManager(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = ":0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-groups", Host: "127.0.0.1"}}
	reg := buildTestRegistry(t, "ccu-groups")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bundle := startMatterBridge(ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Fatal("startMatterBridge returned nil")
	}
	t.Cleanup(bundle.stop)
	if bundle.groups == nil || bundle.node.Groups() != bundle.groups {
		t.Fatalf("the facade runs on group manager %p, the assembler on %p; want one manager", bundle.node.Groups(), bundle.groups)
	}
	if bundle.node.Aggregator() != nil {
		t.Fatal("the facade built its own Aggregator although the daemon supplies the topology")
	}
	if bundle.opMgr == nil || bundle.subMgr == nil || bundle.opMgr != bundle.node.Sessions() || bundle.subMgr != bundle.node.Subscriptions() {
		t.Fatal("the diagnostics managers on the bundle are not the facade's")
	}
}

// TestMatterScenesStorePersistsTheDaemonsKeys: the daemon's stable keys
// are matterendpoint.SourceKey values, not endpoint.StringKeys; a scene
// table saved under one is read back under the same key.
func TestMatterScenesStorePersistsTheDaemonsKeys(t *testing.T) {
	t.Parallel()
	st := matterstore.New(openMigratedTestDB(t, "matter_scenes_store_test.db"))
	s := matterScenesStore{store: st, logger: slog.New(slog.DiscardHandler)}
	key := matterendpoint.SourceKey{CentralName: "ccu-01", DeviceAddress: "0001D3C9A4B5C6", ChannelNo: 4, DPKind: matterendpoint.DPKindGeneric, DPKey: "STATE"}
	s.SaveScenes(key, []byte(`{"scene":1}`))
	if got := string(s.LoadScenes(key)); got != `{"scene":1}` {
		t.Fatalf("LoadScenes after SaveScenes = %q, want the saved table", got)
	}
	other := key
	other.ChannelNo = 5
	if got := s.LoadScenes(other); got != nil {
		t.Fatalf("LoadScenes of another channel = %q, want nothing", got)
	}
}
