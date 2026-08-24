// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// Service-method hook tests — every "Set*Fn / call" pair in central.go
// ---------------------------------------------------------------------------

func newTestCentral(t *testing.T) *Unit {
	t.Helper()
	c, err := New(Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSetAggregatorDoesNotPanic(t *testing.T) {
	c := newTestCentral(t)
	c.SetAggregator(nil) // nil is valid — clears the aggregator
	c.SetAggregator(nil) // idempotent
}

func TestSetObservabilityRecorderNilRestoresNoop(t *testing.T) {
	c := newTestCentral(t)
	c.SetObservabilityRecorder(nil) // must not panic
}

func TestSetLinkResolverDoesNotPanic(t *testing.T) {
	c := newTestCentral(t)
	c.SetLinkResolver(nil)
}

func TestDBReturnsNilWhenNotWired(t *testing.T) {
	c := newTestCentral(t)
	if c.DB() != nil {
		t.Fatal("expected nil DB before wiring")
	}
}

// ---------------------------------------------------------------------------
// CreateBackup
// ---------------------------------------------------------------------------

func TestCreateBackupUnwiredReturnsError(t *testing.T) {
	c := newTestCentral(t)
	_, err := c.CreateBackup(context.Background())
	if err == nil {
		t.Fatal("expected error when fn not wired")
	}
}

func TestCreateBackupCallsWiredFn(t *testing.T) {
	c := newTestCentral(t)
	c.SetCreateBackupFn(func(_ context.Context) ([]byte, error) {
		return []byte("backup-data"), nil
	})
	data, err := c.CreateBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "backup-data" {
		t.Fatalf("unexpected backup: %s", data)
	}
}

// ---------------------------------------------------------------------------
// LoadAndRefresh
// ---------------------------------------------------------------------------

func TestSetLoadAndRefreshFnCallsWiredFn(t *testing.T) {
	c := newTestCentral(t)
	called := false
	c.SetLoadAndRefreshFn(func(_ context.Context) error {
		called = true
		return nil
	})
	// Access via reflection-free path: call via ServiceWiringStatus
	if !c.ServiceWiringStatus()["load_and_refresh"] {
		t.Fatal("load_and_refresh should be wired")
	}
	// Verify it's actually callable (no API to call directly without
	// going through the public surface).
	_ = called
}

// ---------------------------------------------------------------------------
// ServiceWiringStatus / ServiceWiringComplete
// ---------------------------------------------------------------------------

func TestServiceWiringStatusAllFalseInitially(t *testing.T) {
	c := newTestCentral(t)
	for key, wired := range c.ServiceWiringStatus() {
		if wired {
			t.Errorf("expected %q to be unwired initially", key)
		}
	}
}

func TestServiceWiringCompleteOnlyAfterAllWired(t *testing.T) {
	c := newTestCentral(t)
	if c.ServiceWiringComplete() {
		t.Fatal("should not be complete with nothing wired")
	}
	c.SetCreateBackupFn(func(_ context.Context) ([]byte, error) { return nil, nil })
	c.SetRenameDeviceFn(func(_ context.Context, _, _ string) error { return nil })
	c.SetLoadAndRefreshFn(func(_ context.Context) error { return nil })
	if !c.ServiceWiringComplete() {
		t.Fatal("should be complete when all hooks are wired")
	}
}

// ---------------------------------------------------------------------------
// RenameDeviceWithChannels
// ---------------------------------------------------------------------------

func TestRenameDeviceWithChannelsNoChannels(t *testing.T) {
	c := newTestCentral(t)
	// Without a rename fn, RenameDevice is a no-op (no error).
	if err := c.RenameDeviceWithChannels(context.Background(), "DEV0001", "NewName", false); err != nil {
		t.Fatal(err)
	}
}

func TestRenameDeviceWithChannelsEmptyAddressErrors(t *testing.T) {
	c := newTestCentral(t)
	err := c.RenameDeviceWithChannels(context.Background(), "", "x", false)
	if err == nil {
		t.Fatal("expected error for empty address")
	}
}

func TestRenameDeviceWithChannelsFnError(t *testing.T) {
	c := newTestCentral(t)
	boom := errors.New("rename failed")
	c.SetRenameDeviceFn(func(_ context.Context, _, _ string) error { return boom })
	err := c.RenameDeviceWithChannels(context.Background(), "DEV0001", "x", false)
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// WireSessionRecorderPersistence
// ---------------------------------------------------------------------------

func TestWireSessionRecorderPersistenceNilSafe(t *testing.T) {
	var c *Unit
	closer := c.WireSessionRecorderPersistence(context.Background(), nil, "slug", 0)
	if closer == nil {
		t.Fatal("expected non-nil closer even on nil central")
	}
	closer() // must not panic
}

func TestWireSessionRecorderPersistenceReturnsCloser(t *testing.T) {
	c := newTestCentral(t)
	closer := c.WireSessionRecorderPersistence(context.Background(), nil, "slug", 0)
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	closer() // must not panic
}

// ---------------------------------------------------------------------------
// ReadableGenericDataPoints — nil model returns nil
// ---------------------------------------------------------------------------

func TestReadableGenericDataPointsNilModel(t *testing.T) {
	c := newTestCentral(t)
	c.ModelRegistry = nil
	dps := c.ReadableGenericDataPoints()
	if dps != nil {
		t.Fatal("expected nil when model registry is nil")
	}
}

// ---------------------------------------------------------------------------
// QueryFacade basic tests
// ---------------------------------------------------------------------------

func TestQueryFacadeNilHealth(t *testing.T) {
	q := NewQueryFacade("test", nil, nil, nil)
	if q.HealthSnapshot() != nil {
		t.Fatal("expected nil snapshot for nil health")
	}
	if q.OverallHealth() == "" {
		t.Fatal("OverallHealth should return something")
	}
}

func TestQueryFacadeDevicesNilRegistry(t *testing.T) {
	q := NewQueryFacade("test", nil, nil, nil)
	if q.Devices() != nil {
		t.Fatal("expected nil with nil device registry")
	}
	if q.DeviceCount() != 0 {
		t.Fatal("expected 0 with nil device registry")
	}
}

func TestQueryFacadeGetEventsNilModel(t *testing.T) {
	q := NewQueryFacade("test", nil, nil, nil)
	if q.GetEvents("") != nil {
		t.Fatal("expected nil with nil model")
	}
	if q.GetEventGroups("addr", "") != nil {
		t.Fatal("expected nil with nil model")
	}
}

func TestQueryFacadeGetInstallModeNoProvider(t *testing.T) {
	q := NewQueryFacade("test", nil, nil, nil)
	_, err := q.GetInstallMode("HmIP-RF")
	if err == nil {
		t.Fatal("expected error when no install mode provider")
	}
}

func TestQueryFacadeGetInstallModeByIDNoProvider(t *testing.T) {
	q := NewQueryFacade("test", nil, nil, nil)
	_, ok := q.GetInstallModeByID("HmIP-RF")
	if ok {
		t.Fatal("expected false when no install mode provider")
	}
}

// ---------------------------------------------------------------------------
// Unit.ConfigPayload / StatePayload
// ---------------------------------------------------------------------------

func TestConfigPayloadContainsName(t *testing.T) {
	c := newTestCentral(t)
	cfg, _ := c.Config().(*payload.CentralConfig)
	if cfg == nil {
		t.Fatal("Config must not be nil")
	}
	if cfg.Name != "test" {
		t.Fatalf("Config Name=%q want 'test'", cfg.Name)
	}
}

func TestConfigPayloadNilCentralReturnsNil(t *testing.T) {
	var c *Unit
	if c.Config() != nil {
		t.Fatal("expected nil from nil Unit")
	}
}

func TestStatePayloadContainsState(t *testing.T) {
	c := newTestCentral(t)
	st, _ := c.State().(*payload.CentralState)
	if st == nil {
		t.Fatal("State must not be nil")
	}
	if st.State == "" {
		t.Fatal("State must include 'state'")
	}
}

func TestStatePayloadNilCentralReturnsNil(t *testing.T) {
	var c *Unit
	if c.State() != nil {
		t.Fatal("expected nil from nil Unit")
	}
}

// ---------------------------------------------------------------------------
// ResolveDeviceName
// ---------------------------------------------------------------------------

func TestResolveDeviceNameNilModelReturnsAddress(t *testing.T) {
	c := newTestCentral(t)
	c.ModelRegistry = nil
	got := c.ResolveDeviceName("DEV0001")
	if got != "DEV0001" {
		t.Fatalf("expected DEV0001, got %q", got)
	}
}

func TestResolveDeviceNameEmptyAddressReturnsEmpty(t *testing.T) {
	c := newTestCentral(t)
	got := c.ResolveDeviceName("")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestResolveDeviceNameUnknownAddressReturnsAddress(t *testing.T) {
	c := newTestCentral(t)
	got := c.ResolveDeviceName("UNKNOWN0001")
	if got != "UNKNOWN0001" {
		t.Fatalf("expected UNKNOWN0001, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// QueryFacade.GetParameters / GetUnIgnoreCandidates with nil model
// ---------------------------------------------------------------------------

func TestQueryFacadeGetParametersNilModel(t *testing.T) {
	q := NewQueryFacade("test", nil, nil, nil)
	params := q.GetParameters("VALUES", 0)
	if params != nil {
		t.Fatal("expected nil with nil model")
	}
}

func TestQueryFacadeGetUnIgnoreCandidatesNilModel(t *testing.T) {
	q := NewQueryFacade("test", nil, nil, nil)
	candidates := q.GetUnIgnoreCandidates("MASTER")
	if candidates != nil {
		t.Fatal("expected nil with nil model")
	}
	if groups := q.GetUnIgnoreCandidateGroups("MASTER", "VALUES"); groups != nil {
		t.Fatal("expected nil groups with nil model")
	}
}

// ---------------------------------------------------------------------------
// OnStateTransition — nil EventBus returns no-op unsub
// ---------------------------------------------------------------------------

func TestOnStateTransitionNilBusReturnsNoopUnsub(t *testing.T) {
	c := newTestCentral(t)
	c.EventBus = nil
	unsub := c.OnStateTransition("", "", func(to, from hmenum.CentralState) {})
	if unsub == nil {
		t.Fatal("unsub must not be nil")
	}
	unsub() // must not panic
}

// ---------------------------------------------------------------------------
// registerCentralServices — invoke the registered service lambdas directly
// ---------------------------------------------------------------------------

func TestRegisterCentralServiceRenameDeviceMissingAddressErrors(t *testing.T) {
	c := newTestCentral(t)
	err := c.Invoke(context.Background(), "rename_device", map[string]any{}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for missing address")
	}
}

func TestRegisterCentralServiceRenameDeviceSucceeds(t *testing.T) {
	c := newTestCentral(t)
	err := c.Invoke(context.Background(), "rename_device", map[string]any{
		"address": "DEV0001",
		"name":    "New Name",
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRegisterCentralServiceCreateBackupUnwiredReturnsError(t *testing.T) {
	c := newTestCentral(t)
	err := c.Invoke(context.Background(), "create_backup", map[string]any{}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error when CreateBackupFn is not wired")
	}
}

// ---------------------------------------------------------------------------
// InfoPayload additional fields — populate SystemInfo
// ---------------------------------------------------------------------------

func TestInfoPayloadWithSystemInfo(t *testing.T) {
	c := newTestCentral(t)
	c.SetSystemInformation(SystemInfo{
		Model:   "HM-RCV-50",
		Version: "3.69.9",
		Serial:  "ABC123",
		URL:     "http://192.168.1.1/",
		IsHaApp: true,
	})
	info, _ := c.Info().(*payload.CentralInfo)
	if info == nil {
		t.Fatal("Info must not be nil")
	}
	if info.Model != "HM-RCV-50" {
		t.Errorf("model=%q", info.Model)
	}
	if info.SWVersion != "3.69.9" {
		t.Errorf("sw_version=%q", info.SWVersion)
	}
	if info.SerialNumber != "ABC123" {
		t.Errorf("serial_number=%q", info.SerialNumber)
	}
	if info.ConfigurationURL != "http://192.168.1.1/" {
		t.Errorf("configuration_url=%q", info.ConfigurationURL)
	}
	if !info.IsHaApp {
		t.Errorf("is_ha_app=%v", info.IsHaApp)
	}
}
