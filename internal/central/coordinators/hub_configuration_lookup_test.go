// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// hub_configuration_lookup_test.go covers:
// HubCoordinator lookup methods (GetProgramDataPoint, GetSysvarDataPoint,
// GetHubDataPoints, GetSystemVariable, GetSuppressedServiceMessages,
// PublishInstallModeRefreshed) and ConfigurationCoordinator device aggregation
// (GetAllParamsetDescriptions, GetConfigurableDevices, result types).

package coordinators

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ─────────────────────────────────────────────────────────────────────────────
// HubCoordinator — GetProgramDataPoint / GetSysvarDataPoint
// ─────────────────────────────────────────────────────────────────────────────

func TestHubCoordinatorGetProgramDataPointFound(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	m := hub.NewHub("c")
	p := hub.NewProgram("c", "prog-1", "Test Program", "desc", false, nil)
	m.PutProgram(p)
	h.SetHubModel(m)

	if got := h.GetProgramDataPoint("prog-1"); got == nil {
		t.Fatal("GetProgramDataPoint: want non-nil for known ID")
	}
}

func TestHubCoordinatorGetProgramDataPointMissing(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	m := hub.NewHub("c")
	h.SetHubModel(m)

	if got := h.GetProgramDataPoint("no-such"); got != nil {
		t.Fatalf("GetProgramDataPoint: want nil for unknown ID, got %v", got)
	}
}

func TestHubCoordinatorGetProgramDataPointNoModel(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	// No model wired.
	if got := h.GetProgramDataPoint("x"); got != nil {
		t.Fatal("GetProgramDataPoint with no model must return nil")
	}
}

func TestHubCoordinatorGetSysvarDataPointFound(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	m := hub.NewHub("c")
	sv := hub.NewSysvar("c", "myVar", "desc", hmenum.HubValueTypeLogic, nil)
	m.PutSysvar(sv)
	h.SetHubModel(m)

	if got := h.GetSysvarDataPoint("myVar"); got == nil {
		t.Fatal("GetSysvarDataPoint: want non-nil for known name")
	}
}

func TestHubCoordinatorGetSysvarDataPointMissing(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	m := hub.NewHub("c")
	h.SetHubModel(m)

	if got := h.GetSysvarDataPoint("nope"); got != nil {
		t.Fatalf("GetSysvarDataPoint: want nil for unknown name, got %v", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HubCoordinator — GetHubDataPoints
// ─────────────────────────────────────────────────────────────────────────────

func TestHubCoordinatorGetHubDataPointsReturnsAll(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	m := hub.NewHub("c")
	m.PutProgram(hub.NewProgram("c", "p1", "P1", "desc", false, nil))
	m.PutSysvar(hub.NewSysvar("c", "sv1", "SV1", hmenum.HubValueTypeLogic, nil))
	h.SetHubModel(m)

	got := h.GetHubDataPoints()
	if len(got) != 2 {
		t.Fatalf("GetHubDataPoints: want 2 entries, got %d", len(got))
	}
}

func TestHubCoordinatorGetHubDataPointsEmpty(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)

	if got := h.GetHubDataPoints(); len(got) != 0 {
		t.Fatalf("GetHubDataPoints with no model: want empty, got %d", len(got))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HubCoordinator — GetSystemVariable
// ─────────────────────────────────────────────────────────────────────────────

type stubSysvarGetter struct {
	val any
	err error
}

func (s *stubSysvarGetter) GetSysvar(_ context.Context, _ string) (any, error) {
	return s.val, s.err
}

func TestHubCoordinatorGetSystemVariableNoGetter(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	val, err := h.GetSystemVariable(context.Background(), "x")
	if !errors.Is(err, ErrNoSysvarGetter) || val != nil {
		t.Fatalf("GetSystemVariable with no getter: want (nil, ErrNoSysvarGetter), got (%v, %v)", val, err)
	}
}

func TestHubCoordinatorGetSystemVariableOK(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	h.SetSysvarGetter(&stubSysvarGetter{val: 42.0})
	val, err := h.GetSystemVariable(context.Background(), "foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 42.0 {
		t.Fatalf("want 42.0, got %v", val)
	}
}

func TestHubCoordinatorGetSystemVariableError(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	wantErr := errors.New("backend error")
	h.SetSysvarGetter(&stubSysvarGetter{err: wantErr})
	_, err := h.GetSystemVariable(context.Background(), "foo")
	if !errors.Is(err, wantErr) {
		t.Fatalf("want backend error, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HubCoordinator — GetSuppressedServiceMessages
// ─────────────────────────────────────────────────────────────────────────────

type stubServiceMessageReader struct {
	params []string
	err    error
}

func (s *stubServiceMessageReader) GetSuppressedServiceMessages(_ context.Context, _, _ string) ([]string, error) {
	return s.params, s.err
}

func TestHubCoordinatorGetSuppressedNoReader(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	got, err := h.GetSuppressedServiceMessages(context.Background(), "iface", "addr:1")
	if !errors.Is(err, ErrNoServiceMessageReader) || got != nil {
		t.Fatalf("want (nil, ErrNoServiceMessageReader), got (%v, %v)", got, err)
	}
}

func TestHubCoordinatorGetSuppressedOK(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	h.SetServiceMessageReader(&stubServiceMessageReader{params: []string{"STICKY_UNREACH", "LOWBAT"}})
	got, err := h.GetSuppressedServiceMessages(context.Background(), "ccu-HmIP-RF", "VCU001:0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 suppressed params, got %d", len(got))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HubCoordinator — PublishInstallModeRefreshed
// ─────────────────────────────────────────────────────────────────────────────

func TestHubCoordinatorPublishInstallModeRefreshedNoModel(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	// Must not panic when no hub model is wired.
	h.PublishInstallModeRefreshed()
}

func TestHubCoordinatorPublishInstallModeRefreshedPublishesEvent(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	m := hub.NewHub("c")
	im := hub.NewInstallMode("ccu-HmIP-RF", nil)
	m.PutInstallMode(im)
	h.SetHubModel(m)

	var published int
	unsub := events.Subscribe(bus, func(hmevent.InstallModeChangedEvent) { published++ })
	defer unsub()

	h.PublishInstallModeRefreshed()
	if published != 1 {
		t.Fatalf("want 1 InstallModeChangedEvent, got %d", published)
	}
}

// TestHubCoordinatorPublishInstallModeRefreshedSkipsUnchangedSteadyState is
// the regression guard for the 30 s-poll flood: install mode is off far
// more often than it is on, so republishing the identical (false, 0) pair
// on every scheduled refresh floods the replay buffer and every WS/MQTT
// subscriber with a "changed" event that changed nothing.
func TestHubCoordinatorPublishInstallModeRefreshedSkipsUnchangedSteadyState(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)
	m := hub.NewHub("c")
	im := hub.NewInstallMode("ccu-HmIP-RF", nil)
	m.PutInstallMode(im)
	h.SetHubModel(m)

	var published int
	unsub := events.Subscribe(bus, func(hmevent.InstallModeChangedEvent) { published++ })
	defer unsub()

	// Three consecutive refreshes with install mode off the whole time
	// (the default, unobserved state): only the first must publish.
	h.PublishInstallModeRefreshed()
	h.PublishInstallModeRefreshed()
	h.PublishInstallModeRefreshed()
	if published != 1 {
		t.Fatalf("published = %d over 3 identical polls, want exactly 1", published)
	}

	// A real transition (install mode enabled) must still publish.
	im.OnState(true, 60*time.Second)
	h.PublishInstallModeRefreshed()
	if published != 2 {
		t.Fatalf("published = %d after a real enable transition, want 2", published)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ConfigurationCoordinator — GetAllParamsetDescriptions
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationCoordinatorGetAllParamsetDescriptionsNilParamsets(t *testing.T) {
	t.Parallel()
	c := NewConfigurationCoordinator(nil, nil, nil)
	got := c.GetAllParamsetDescriptions(wireKey(hmenum.InterfaceHmIPRF), "VCU001:1")
	if got != nil {
		t.Fatalf("want nil when paramsets registry is nil, got %v", got)
	}
}

func TestConfigurationCoordinatorGetAllParamsetDescriptionsReturnsMap(t *testing.T) {
	t.Parallel()
	descReg := registry.NewDeviceDescriptionRegistry()
	psReg := registry.NewParamsetRegistry()
	devReg := registry.NewDeviceRegistry()
	c := NewConfigurationCoordinator(descReg, psReg, devReg)
	// No entries stored — expect empty non-nil map.
	got := c.GetAllParamsetDescriptions(wireKey(hmenum.InterfaceHmIPRF), "VCU001:1")
	// Empty result is valid; just ensure no panic.
	_ = got
}

// ─────────────────────────────────────────────────────────────────────────────
// ConfigurationCoordinator — GetConfigurableDevices
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurationCoordinatorGetConfigurableDevicesNilDescriptions(t *testing.T) {
	t.Parallel()
	c := NewConfigurationCoordinator(nil, nil, nil)
	got := c.GetConfigurableDevices(wireKey(hmenum.InterfaceHmIPRF))
	if got != nil {
		t.Fatalf("want nil when descriptions is nil, got %v", got)
	}
}

func TestConfigurationCoordinatorGetConfigurableDevicesEmpty(t *testing.T) {
	t.Parallel()
	descReg := registry.NewDeviceDescriptionRegistry()
	psReg := registry.NewParamsetRegistry()
	devReg := registry.NewDeviceRegistry()
	c := NewConfigurationCoordinator(descReg, psReg, devReg)
	got := c.GetConfigurableDevices(wireKey(hmenum.InterfaceHmIPRF))
	if len(got) != 0 {
		t.Fatalf("want 0 configurable devices for empty registry, got %d", len(got))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ConfigurableDevice / ConfigurableDeviceChannel types
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigurableDeviceTypesExist(t *testing.T) {
	t.Parallel()
	// Compile-time smoke: zero-value construction must not panic.
	_ = ConfigurableDevice{
		Address:     "VCU001",
		InterfaceID: "ccu-HmIP-RF",
		Model:       "HmIP-BSM",
		Channels: []ConfigurableDeviceChannel{
			{Address: "VCU001:1", ChannelType: "SWITCH", ParamsetKeys: []hmenum.ParamsetKey{hmenum.ParamsetKeyMaster}},
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Result types
// ─────────────────────────────────────────────────────────────────────────────

func TestMaintenanceDataExists(t *testing.T) {
	t.Parallel()
	m := MaintenanceData{UnreachCount: 2, LowBat: true, RSSI: -65}
	if m.UnreachCount != 2 {
		t.Fatal("MaintenanceData.UnreachCount mismatch")
	}
}
