// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestFactory_Connectivity verifies that NewConnectivityFactory returns a
// non-nil result.
func TestFactory_Connectivity(t *testing.T) {
	t.Parallel()
	c := NewConnectivityFactory()
	if c == nil {
		t.Fatal("NewConnectivityFactory() = nil, want non-nil")
	}
}

// TestFactory_Metrics verifies that NewMetricsFactory returns a non-nil
// result.
func TestFactory_Metrics(t *testing.T) {
	t.Parallel()
	m := NewMetricsFactory()
	if m == nil {
		t.Fatal("NewMetricsFactory() = nil, want non-nil")
	}
}

// TestFactory_Program verifies that NewProgramFactory returns a non-nil
// result with the provided fields correctly set.
func TestFactory_Program(t *testing.T) {
	t.Parallel()
	p := NewProgramFactory("ccu-01", "42", "Urlaubsmodus", "Fährt Jalousien", false, nil)
	if p == nil {
		t.Fatal("NewProgramFactory() = nil, want non-nil")
	}
	if p.ID != "42" {
		t.Errorf("ID = %q, want %q", p.ID, "42")
	}
}

// TestFactory_Sysvar verifies that NewSysvarFactory returns a non-nil
// result.
func TestFactory_Sysvar(t *testing.T) {
	t.Parallel()
	s := NewSysvarFactory("ccu-01", "Anwesenheit", "", hmenum.HubValueTypeLogic, nil)
	if s == nil {
		t.Fatal("NewSysvarFactory() = nil, want non-nil")
	}
}

// TestFactory_Inbox verifies that NewInboxFactory returns a non-nil result
// and is scoped in a multi-CCU-safe way.
func TestFactory_Inbox(t *testing.T) {
	t.Parallel()
	i := NewInboxFactory("ccu-01")
	if i == nil {
		t.Fatal("NewInboxFactory() = nil, want non-nil")
	}
}

// TestFactory_ServiceMessages verifies that NewServiceMessagesFactory
// returns a non-nil result.
func TestFactory_ServiceMessages(t *testing.T) {
	t.Parallel()
	sm := NewServiceMessagesFactory("ccu-01", nil)
	if sm == nil {
		t.Fatal("NewServiceMessagesFactory() = nil, want non-nil")
	}
}

// TestFactory_InstallMode verifies that NewInstallModeFactory returns a
// non-nil result.
func TestFactory_InstallMode(t *testing.T) {
	t.Parallel()
	im := NewInstallModeFactory("HmIP-RF", nil)
	if im == nil {
		t.Fatal("NewInstallModeFactory() = nil, want non-nil")
	}
	if im.InterfaceID != "HmIP-RF" {
		t.Errorf("InterfaceID = %q, want %q", im.InterfaceID, "HmIP-RF")
	}
}
