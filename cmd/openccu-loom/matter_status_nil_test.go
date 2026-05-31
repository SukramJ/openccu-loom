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

// TestMatterCandidateProviderAdapter_NilWalk_ReturnsNil verifies that
// MatterCandidates returns nil (not panic) when walk is nil.
func TestMatterCandidateProviderAdapter_NilWalk_ReturnsNil(t *testing.T) {
	t.Parallel()
	a := &matterCandidateProviderAdapter{walk: nil}
	if got := a.MatterCandidates(context.Background()); got != nil {
		t.Errorf("expected nil candidates, got %v", got)
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

// TestMatterCandidateProviderAdapter_WalkReturnsResults verifies that
// when walk returns a non-empty slice, MatterCandidates forwards it.
func TestMatterCandidateProviderAdapter_WalkReturnsResults(t *testing.T) {
	t.Parallel()
	want := []eligibility.Candidate{
		{DisplayName: "Lamp"},
	}
	a := &matterCandidateProviderAdapter{
		walk: func() []eligibility.Candidate { return want },
	}
	got := a.MatterCandidates(context.Background())
	if len(got) != 1 || got[0].DisplayName != "Lamp" {
		t.Errorf("MatterCandidates: got %v, want %v", got, want)
	}
}
