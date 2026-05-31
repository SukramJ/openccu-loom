// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// ── newMatterEphemeralProvider ────────────────────────────────────────────────

func TestNewMatterEphemeralProvider_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	p := newMatterEphemeralProvider(nil, config.NorthMatterCommissioning{}, nil, nil, nil, nil, nil)
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

// TestMatterEphemeralProvider_NilBridge_Errors verifies that
// GenerateAndInstall returns an error (not panic) when the bridge is nil.
func TestMatterEphemeralProvider_NilBridge_Errors(t *testing.T) {
	t.Parallel()
	mgr := buildTestOperationalManager(t)
	p := newMatterEphemeralProvider(nil /* bridge */, config.NorthMatterCommissioning{}, mgr, nil, nil, nil, nil)
	_, err := p.GenerateAndInstall(context.Background())
	if err == nil {
		t.Fatal("expected error when bridge is nil, got nil")
	}
}

// TestMatterEphemeralProvider_NilOpMgr_Errors verifies that
// GenerateAndInstall returns an error when opMgr is nil.
func TestMatterEphemeralProvider_NilOpMgr_Errors(t *testing.T) {
	t.Parallel()
	// We cannot build a real bridge without network, but the nil guard
	// fires before using the bridge when opMgr is also nil.
	p := newMatterEphemeralProvider(nil /* bridge */, config.NorthMatterCommissioning{}, nil /* opMgr */, nil, nil, nil, nil)
	_, err := p.GenerateAndInstall(context.Background())
	if err == nil {
		t.Fatal("expected error when opMgr is nil, got nil")
	}
}

// TestMatterEphemeralProvider_NilReceiver_Errors verifies that a nil
// *matterEphemeralProvider pointer returns an error.
func TestMatterEphemeralProvider_NilReceiver_Errors(t *testing.T) {
	t.Parallel()
	var p *matterEphemeralProvider
	_, err := p.GenerateAndInstall(context.Background())
	if err == nil {
		t.Fatal("expected error from nil receiver, got nil")
	}
}

// ── matterCommissioningOpenerAdapter ─────────────────────────────────────────

// TestOpenCommissioningWindow_NilAdapter_ReturnsError verifies that a
// nil *matterCommissioningOpenerAdapter returns ErrCommissioningInProgress.
func TestOpenCommissioningWindow_NilAdapter_ReturnsError(t *testing.T) {
	t.Parallel()
	var a *matterCommissioningOpenerAdapter
	_, err := a.OpenCommissioningWindow(context.Background(), 180)
	if err == nil {
		t.Fatal("expected error from nil adapter, got nil")
	}
	if !errors.Is(err, handlers.ErrCommissioningInProgress) {
		t.Errorf("expected ErrCommissioningInProgress, got %v", err)
	}
}

// TestOpenCommissioningWindow_NilInner_ReturnsError verifies that an
// adapter with nil inner opener returns ErrCommissioningInProgress.
func TestOpenCommissioningWindow_NilInner_ReturnsError(t *testing.T) {
	t.Parallel()
	a := &matterCommissioningOpenerAdapter{inner: nil}
	_, err := a.OpenCommissioningWindow(context.Background(), 180)
	if err == nil {
		t.Fatal("expected error when inner is nil, got nil")
	}
	if !errors.Is(err, handlers.ErrCommissioningInProgress) {
		t.Errorf("expected ErrCommissioningInProgress, got %v", err)
	}
}

// TestOpenCommissioningWindow_AlreadyOpen_MapsError verifies that the
// nil-inner path maps to ErrCommissioningInProgress on the REST handler
// side. The bridge error-mapping path (ErrCommissioningWindowAlreadyOpen
// → ErrCommissioningInProgress) is separately exercised in the bridge's
// own test suite.
func TestOpenCommissioningWindow_AlreadyOpen_MapsError(t *testing.T) {
	t.Parallel()
	// nil-inner guard: fires before reaching bridge logic.
	a := &matterCommissioningOpenerAdapter{inner: nil, bridge: nil}
	_, err := a.OpenCommissioningWindow(context.Background(), 180)
	if !errors.Is(err, handlers.ErrCommissioningInProgress) {
		t.Errorf("expected ErrCommissioningInProgress from nil-inner path, got %v", err)
	}
}
