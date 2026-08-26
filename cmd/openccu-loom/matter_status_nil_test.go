// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	matterbridge "github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
)

// TestMatterFabricRevokerAdapter_NilStore_Errors verifies that
// RevokeFabric returns an error (not a panic) when the store is nil.
func TestMatterFabricRevokerAdapter_NilStore_Errors(t *testing.T) {
	t.Parallel()
	a := &matterFabricRevokerAdapter{store: nil}
	err := a.RevokeFabric(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error when store is nil, got nil")
	}
}

// TestMatterFabricRevokerAdapter_NilReceiver_Errors verifies that a nil
// *matterFabricRevokerAdapter pointer returns an error.
func TestMatterFabricRevokerAdapter_NilReceiver_Errors(t *testing.T) {
	t.Parallel()
	var a *matterFabricRevokerAdapter
	err := a.RevokeFabric(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error from nil receiver, got nil")
	}
}

// TestMatterCommissioningCloserAdapter_NilWindow_Errors verifies that
// CloseCommissioningWindow returns an error when window is nil.
func TestMatterCommissioningCloserAdapter_NilWindow_Errors(t *testing.T) {
	t.Parallel()
	a := &matterCommissioningCloserAdapter{window: nil}
	err := a.CloseCommissioningWindow(context.Background())
	if err == nil {
		t.Fatal("expected error when window is nil, got nil")
	}
	// The error must be ErrCommissioningWindowNotConfigured.
	if !errors.Is(err, matterbridge.ErrCommissioningWindowNotConfigured) {
		t.Errorf("expected ErrCommissioningWindowNotConfigured, got %v", err)
	}
}

// TestMatterCommissioningCloserAdapter_NilReceiver_Errors verifies that
// a nil *matterCommissioningCloserAdapter returns an error.
func TestMatterCommissioningCloserAdapter_NilReceiver_Errors(t *testing.T) {
	t.Parallel()
	var a *matterCommissioningCloserAdapter
	err := a.CloseCommissioningWindow(context.Background())
	if err == nil {
		t.Fatal("expected error from nil receiver, got nil")
	}
}

// TestMatterCandidateProviderAdapter_NilFields_ReturnsNil verifies that
// MatterCandidates returns nil (not panic) when reg and/or cfg is nil.
func TestMatterCandidateProviderAdapter_NilFields_ReturnsNil(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-one")
	cfg := &config.Config{}

	if got := (&matterCandidateProviderAdapter{}).MatterCandidates(context.Background()); got != nil {
		t.Errorf("nil reg + nil cfg: expected nil candidates, got %v", got)
	}
	if got := (&matterCandidateProviderAdapter{reg: reg}).MatterCandidates(context.Background()); got != nil {
		t.Errorf("nil cfg: expected nil candidates, got %v", got)
	}
	if got := (&matterCandidateProviderAdapter{cfg: cfg}).MatterCandidates(context.Background()); got != nil {
		t.Errorf("nil reg: expected nil candidates, got %v", got)
	}
}

// TestMatterCandidateProviderAdapter_NilReceiver_ReturnsNil verifies
// that a nil receiver returns nil rather than panicking.
func TestMatterCandidateProviderAdapter_NilReceiver_ReturnsNil(t *testing.T) {
	t.Parallel()
	var a *matterCandidateProviderAdapter
	if got := a.MatterCandidates(context.Background()); got != nil {
		t.Errorf("expected nil candidates, got %v", got)
	}
}

// TestMatterCandidateProviderAdapter_WalksRegistry_SkipsNilAndEmptyUnits
// verifies that MatterCandidates iterates every registered central without
// panicking and yields no candidates for centrals whose ModelRegistry holds
// no devices — proving the loop + nil-ModelRegistry-skip path rather than
// re-asserting the nil-guard case above.
func TestMatterCandidateProviderAdapter_WalksRegistry_SkipsNilAndEmptyUnits(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-one", "ccu-two")
	a := &matterCandidateProviderAdapter{reg: reg, cfg: &config.Config{}}

	got := a.MatterCandidates(context.Background())
	if len(got) != 0 {
		t.Errorf("expected no candidates from centrals with empty ModelRegistry, got %v", got)
	}
}
