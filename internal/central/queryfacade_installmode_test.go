// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central_test

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeInstallModeProvider is a test double for [central.InstallModeProvider].
type fakeInstallModeProvider struct {
	dps []*hub.InstallMode
}

func (f *fakeInstallModeProvider) InstallModeDPs() []*hub.InstallMode {
	return f.dps
}

// TestQueryFacadeGetInstallModeByID verifies that GetInstallModeByID delegates
// to the InstallModeProvider and returns the cached remaining duration.
func TestQueryFacadeGetInstallModeByID(t *testing.T) {
	im := hub.NewInstallMode("HmIP-RF", nil)
	im.OnState(true, 45*time.Second)

	facade := central.NewQueryFacade("test", nil, nil, nil)
	facade.SetInstallModeProvider(&fakeInstallModeProvider{dps: []*hub.InstallMode{im}})

	remaining, ok := facade.GetInstallModeByID("HmIP-RF")
	if !ok {
		t.Fatal("GetInstallModeByID: expected ok=true for active install mode")
	}
	if remaining <= 0 {
		t.Errorf("remaining should be positive, got %v", remaining)
	}
}

// TestQueryFacadeGetInstallModeByIDNotFound verifies that GetInstallModeByID
// returns (0, false) when the interface ID does not match.
func TestQueryFacadeGetInstallModeByIDNotFound(t *testing.T) {
	im := hub.NewInstallMode("HmIP-RF", nil)
	im.OnState(true, 30*time.Second)

	facade := central.NewQueryFacade("test", nil, nil, nil)
	facade.SetInstallModeProvider(&fakeInstallModeProvider{dps: []*hub.InstallMode{im}})

	_, ok := facade.GetInstallModeByID("BidCos-RF")
	if ok {
		t.Error("GetInstallModeByID: expected ok=false for unregistered interface")
	}
}

// TestQueryFacadeGetInstallModeByIDNoProvider verifies that GetInstallModeByID
// returns (0, false) when no provider is wired.
func TestQueryFacadeGetInstallModeByIDNoProvider(t *testing.T) {
	facade := central.NewQueryFacade("test", nil, nil, nil)

	_, ok := facade.GetInstallModeByID("HmIP-RF")
	if ok {
		t.Error("expected ok=false without InstallModeProvider")
	}
}

// --- typed GetInstallMode(hmenum.Interface) ---

// TestQueryFacadeGetInstallMode verifies that the typed GetInstallMode
// returns InstallModeInfo with Active=true and positive Remaining when
// install mode is active.
func TestQueryFacadeGetInstallMode(t *testing.T) {
	im := hub.NewInstallMode("HmIP-RF", nil)
	im.OnState(true, 45*time.Second)

	facade := central.NewQueryFacade("test", nil, nil, nil)
	facade.SetInstallModeProvider(&fakeInstallModeProvider{dps: []*hub.InstallMode{im}})

	info, err := facade.GetInstallMode(hmenum.InterfaceHmIPRF)
	if err != nil {
		t.Fatalf("GetInstallMode: unexpected error: %v", err)
	}
	if !info.Active {
		t.Error("expected Active=true")
	}
	if info.Remaining <= 0 {
		t.Errorf("Remaining should be positive, got %v", info.Remaining)
	}
	if info.Mode != "HmIP-RF" {
		t.Errorf("Mode = %q, want HmIP-RF", info.Mode)
	}
}

// TestQueryFacadeGetInstallModeInactive verifies that GetInstallMode
// returns InstallModeInfo with Active=false when install mode is off.
func TestQueryFacadeGetInstallModeInactive(t *testing.T) {
	im := hub.NewInstallMode("HmIP-RF", nil)
	im.OnState(false, 0)

	facade := central.NewQueryFacade("test", nil, nil, nil)
	facade.SetInstallModeProvider(&fakeInstallModeProvider{dps: []*hub.InstallMode{im}})

	info, err := facade.GetInstallMode(hmenum.InterfaceHmIPRF)
	if err != nil {
		t.Fatalf("GetInstallMode: unexpected error: %v", err)
	}
	if info.Active {
		t.Error("expected Active=false for disabled install mode")
	}
}

// TestQueryFacadeGetInstallModeUnknownInterface verifies that GetInstallMode
// returns a non-nil error when the interface is not registered.
func TestQueryFacadeGetInstallModeUnknownInterface(t *testing.T) {
	im := hub.NewInstallMode("HmIP-RF", nil)
	im.OnState(true, 30*time.Second)

	facade := central.NewQueryFacade("test", nil, nil, nil)
	facade.SetInstallModeProvider(&fakeInstallModeProvider{dps: []*hub.InstallMode{im}})

	_, err := facade.GetInstallMode(hmenum.InterfaceBidCosRF)
	if err == nil {
		t.Error("expected error for unregistered interface, got nil")
	}
}

// TestQueryFacadeGetInstallModeNoProvider verifies that GetInstallMode
// returns an error when no provider is wired.
func TestQueryFacadeGetInstallModeNoProvider(t *testing.T) {
	facade := central.NewQueryFacade("test", nil, nil, nil)

	_, err := facade.GetInstallMode(hmenum.InterfaceHmIPRF)
	if err == nil {
		t.Error("expected error without InstallModeProvider")
	}
}
