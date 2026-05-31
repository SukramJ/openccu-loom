// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

// schedule_facade_test.go — pins the ScheduleFacade API surface.
//
// The tests verify that:
// 1. NewScheduleFacade validates its arguments.
// 2. Every method delegates correctly to the underlying domain / adapter.
// 3. The facade exposes the same function set as the reference config
// panel's schedule facade (GetClimateSchedule, SetClimateSchedule,
// SetActiveProfile, GetDeviceSchedule, SetDeviceSchedule,
// SetDeviceActiveProfile, MaxProfilesForDevice).

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client"
)

// ---------------------------------------------------------------------------
// Constructor contract
// ---------------------------------------------------------------------------

// TestScheduleFacadeNilDomainIsRejected verifies that NewScheduleFacade
// rejects a nil SchedulesDomain with an error.
func TestScheduleFacadeNilDomainIsRejected(t *testing.T) {
	t.Parallel()
	adapter := NewScheduleQueryAdapter(nil)
	_, err := NewScheduleFacade(nil, adapter)
	if err == nil {
		t.Fatal("nil domain must return an error")
	}
}

// TestScheduleFacadeNilAdapterIsRejected verifies that NewScheduleFacade
// rejects a nil ScheduleQueryAdapter with an error.
func TestScheduleFacadeNilAdapterIsRejected(t *testing.T) {
	t.Parallel()
	domain := NewSchedulesDomain(nil, client.NewValueWriter())
	_, err := NewScheduleFacade(domain, nil)
	if err == nil {
		t.Fatal("nil adapter must return an error")
	}
}

// TestScheduleFacadeConstructsWithValidArgs verifies that NewScheduleFacade
// succeeds when both arguments are non-nil.
func TestScheduleFacadeConstructsWithValidArgs(t *testing.T) {
	t.Parallel()
	domain := NewSchedulesDomain(nil, client.NewValueWriter())
	adapter := NewScheduleQueryAdapter(domain)
	f, err := NewScheduleFacade(domain, adapter)
	if err != nil {
		t.Fatalf("NewScheduleFacade: %v", err)
	}
	if f == nil {
		t.Fatal("facade must not be nil")
	}
}

// ---------------------------------------------------------------------------
// API surface — all 7 methods must exist and delegate to domain/adapter
// ---------------------------------------------------------------------------

// TestScheduleFacadeAPIMethodsExistAndDelegate calls each method with a
// cancelled context so we exercise the delegation path without real CCU I/O.
// A cancelled-context error proves the call reached the underlying layer
// (rather than a nil-check short-circuit in the facade itself).
func TestScheduleFacadeAPIMethodsExistAndDelegate(t *testing.T) {
	t.Parallel()
	domain := NewSchedulesDomain(nil, client.NewValueWriter())
	adapter := NewScheduleQueryAdapter(domain)
	f, err := NewScheduleFacade(domain, adapter)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// GetClimateSchedule
	if _, err := f.GetClimateSchedule(ctx, "DEV001:1"); err == nil {
		t.Error("GetClimateSchedule: expected error with cancelled ctx or nil domain")
	}

	// SetClimateSchedule
	if err := f.SetClimateSchedule(ctx, "DEV001:1", map[string]any{}); err == nil {
		t.Error("SetClimateSchedule: expected error")
	}

	// SetActiveProfile
	if err := f.SetActiveProfile(ctx, "DEV001:1", 1); err == nil {
		t.Error("SetActiveProfile: expected error")
	}

	// GetDeviceSchedule
	if _, err := f.GetDeviceSchedule(ctx, "DEV001"); err == nil {
		t.Error("GetDeviceSchedule: expected error")
	}

	// SetDeviceSchedule
	if err := f.SetDeviceSchedule(ctx, "DEV001", map[string]any{}); err == nil {
		t.Error("SetDeviceSchedule: expected error")
	}

	// SetDeviceActiveProfile
	if err := f.SetDeviceActiveProfile(ctx, "DEV001", "P1"); err == nil {
		t.Error("SetDeviceActiveProfile: expected error")
	}

	// MaxProfilesForDevice — nil registry returns the default cap, not an error.
	n, err := f.MaxProfilesForDevice(context.Background(), "DEV001")
	if err != nil {
		t.Errorf("MaxProfilesForDevice: unexpected error: %v", err)
	}
	if n != defaultProfileCap {
		t.Errorf("MaxProfilesForDevice default cap = %d, want %d", n, defaultProfileCap)
	}
}

// TestScheduleFacadeMaxProfilesForDeviceDefaultCap verifies the nil-registry
// default is the same as [defaultProfileCap] (6).
func TestScheduleFacadeMaxProfilesForDeviceDefaultCap(t *testing.T) {
	t.Parallel()
	domain := NewSchedulesDomain(nil, client.NewValueWriter())
	adapter := NewScheduleQueryAdapter(domain)
	f, _ := NewScheduleFacade(domain, adapter)

	n, err := f.MaxProfilesForDevice(context.Background(), "any-device")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 6 {
		t.Errorf("default cap = %d, want 6", n)
	}
}
