// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// ─── stub implementations ────────────────────────────────────────────────────

// stubDeviceListProvider implements both [SignalQualityProvider] and
// [ScheduleDevicesProvider] for test purposes.
type stubDeviceListProvider struct {
	devs []*device.Device
}

func (s *stubDeviceListProvider) AllDevices() []*device.Device { return s.devs }

// stubHubData implements [HubDataProvider].
type stubHubData struct {
	svc   *int
	alarm *int
}

func (s *stubHubData) HubMessageCounts() (svc, alarm *int) { return s.svc, s.alarm }

// stubUserPermissions implements [UserPermissionsProvider].
type stubUserPermissions struct {
	model string
}

func (s *stubUserPermissions) BackendModel() string { return s.model }

// stubScheduleEnabler implements [ScheduleEnabler].
type stubScheduleEnabler struct {
	lastAddress string
	lastEnabled bool
	lastKey     string
}

func (s *stubScheduleEnabler) SetScheduleEnabled(_ context.Context, addr string, enabled bool, key string) error {
	s.lastAddress = addr
	s.lastEnabled = enabled
	s.lastKey = key
	return nil
}

// stubLinkFormSchema implements [LinkFormSchemaProvider].
type stubLinkFormSchema struct{}

func (s *stubLinkFormSchema) GetLinkFormSchema(_ context.Context, _, _, _ string) (map[string]any, error) {
	return map[string]any{"fields": []any{}}, nil
}

// stubLinkProfiles implements [LinkProfilesProvider].
type stubLinkProfiles struct {
	profiles        []map[string]any
	activeProfileID int
}

func (s *stubLinkProfiles) GetLinkProfiles(_ context.Context, _, _, _ string) (profiles []map[string]any, activeID int, err error) {
	return s.profiles, s.activeProfileID, nil
}

func (s *stubLinkProfiles) TestLinkProfile(_ context.Context, _, _, _ string, profileID int) (map[string]any, error) {
	return map[string]any{
		"success":        true,
		"applied_values": map[string]any{},
		"profile_id":     profileID,
	}, nil
}

// stubParameterDeterminer implements [ParameterDeterminer].
type stubParameterDeterminer struct {
	result any
}

func (s *stubParameterDeterminer) DetermineParameter(_ context.Context, _, _, _ string) (any, error) {
	return s.result, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func newMissingRouter(cfg MissingCommandsConfig) *Router {
	r := NewRouter()
	RegisterMissingCommands(r, cfg)
	return r
}

// dispatchMissing dispatches a command and round-trips the result through
// JSON so that all assertions see map[string]any shapes.
func dispatchMissing(t *testing.T, r *Router, name string, params any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	res := r.Dispatch(ctxForCommand(name), name, raw)
	if res.Error != nil {
		t.Fatalf("%s: unexpected error: %v", name, res.Error.Message)
	}
	// Round-trip through JSON to normalise the type.
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatalf("%s: re-marshal error: %v", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s: re-unmarshal error: %v", name, err)
	}
	return out
}

// ─── ccu.get_signal_quality ──────────────────────────────────────────────────

func TestMissingCCUGetSignalQuality_Empty(t *testing.T) {
	p := &stubDeviceListProvider{devs: []*device.Device{}}
	r := newMissingRouter(MissingCommandsConfig{SignalQuality: p})
	out := dispatchMissing(t, r, "ccu.get_signal_quality", nil)
	devs, ok := out["devices"].([]any)
	if !ok {
		t.Fatalf("expected devices array, got %T: %v", out["devices"], out["devices"])
	}
	if len(devs) != 0 {
		t.Fatalf("expected 0 devices, got %d", len(devs))
	}
}

func TestMissingCCUGetSignalQuality_WithDevices(t *testing.T) {
	d1 := device.New(device.Config{Address: "ABC0001", Name: "Thermostat", Model: "HmIP-eTRV-2"})
	d2 := device.New(device.Config{Address: "ABC0002", Name: "Switch", Model: "HM-LC-SW1-BA-PCB"})
	p := &stubDeviceListProvider{devs: []*device.Device{d1, d2}}
	r := newMissingRouter(MissingCommandsConfig{SignalQuality: p})
	out := dispatchMissing(t, r, "ccu.get_signal_quality", nil)
	devs, ok := out["devices"].([]any)
	if !ok {
		t.Fatalf("expected devices array, got %T", out["devices"])
	}
	if len(devs) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devs))
	}
	// Each entry must contain mandatory fields.
	for i, raw := range devs {
		d, ok2 := raw.(map[string]any)
		if !ok2 {
			t.Fatalf("device[%d] is not a map", i)
		}
		for _, field := range []string{"address", "is_reachable", "signal_strength", "low_battery"} {
			if _, has := d[field]; !has {
				t.Errorf("device[%d] missing field %q", i, field)
			}
		}
	}
}

func TestMissingCCUGetSignalQuality_NotRegisteredWhenNilProvider(t *testing.T) {
	r := newMissingRouter(MissingCommandsConfig{})
	res := r.Dispatch(context.Background(), "ccu.get_signal_quality", nil)
	if res.Error == nil || res.Error.Code != CommandErrorUnknownCommand {
		t.Fatalf("expected unknown_command when provider is nil, got %v", res.Error)
	}
}

// ─── schedules.list_devices ──────────────────────────────────────────────────

func TestMissingSchedulesListDevices_Empty(t *testing.T) {
	p := &stubDeviceListProvider{devs: []*device.Device{}}
	r := newMissingRouter(MissingCommandsConfig{ScheduleDevices: p})
	out := dispatchMissing(t, r, "schedules.list_devices", nil)
	devs, ok := out["devices"].([]any)
	if !ok {
		t.Fatalf("expected devices array, got %T", out["devices"])
	}
	if len(devs) != 0 {
		t.Fatalf("expected 0 schedule devices, got %d", len(devs))
	}
}

func TestMissingSchedulesListDevices_FiltersNonScheduleDevices(t *testing.T) {
	// Plain devices without week profiles — should be excluded.
	d1 := device.New(device.Config{Address: "ABC0001", Name: "Switch", Model: "HM-LC-SW1-BA-PCB"})
	d2 := device.New(device.Config{Address: "ABC0002", Name: "Motion", Model: "HmIP-SMI"})
	p := &stubDeviceListProvider{devs: []*device.Device{d1, d2}}
	r := newMissingRouter(MissingCommandsConfig{ScheduleDevices: p})
	out := dispatchMissing(t, r, "schedules.list_devices", nil)
	devs := out["devices"].([]any)
	// Neither device has HasWeekProfile() == true in this minimal config.
	if len(devs) != 0 {
		t.Fatalf("expected 0 schedule-capable devices, got %d: %v", len(devs), devs)
	}
}

// ─── ccu.get_hub_data ────────────────────────────────────────────────────────

func TestMissingCCUGetHubData_BothObserved(t *testing.T) {
	svc := 3
	alarm := 1
	r := newMissingRouter(MissingCommandsConfig{HubData: &stubHubData{svc: &svc, alarm: &alarm}})
	out := dispatchMissing(t, r, "ccu.get_hub_data", nil)
	if out["service_messages"] == nil {
		t.Fatalf("service_messages must not be null when observed; got %v", out)
	}
	if out["alarm_messages"] == nil {
		t.Fatalf("alarm_messages must not be null when observed; got %v", out)
	}
}

func TestMissingCCUGetHubData_BothNil(t *testing.T) {
	r := newMissingRouter(MissingCommandsConfig{HubData: &stubHubData{}})
	out := dispatchMissing(t, r, "ccu.get_hub_data", nil)
	if _, ok := out["service_messages"]; !ok {
		t.Fatalf("response must contain service_messages key")
	}
	if _, ok := out["alarm_messages"]; !ok {
		t.Fatalf("response must contain alarm_messages key")
	}
}

// ─── system.user_permissions ─────────────────────────────────────────────────

func TestMissingSystemUserPermissions_NoAuth(t *testing.T) {
	r := newMissingRouter(MissingCommandsConfig{UserPermissions: &stubUserPermissions{model: "CCU3"}})
	out := dispatchMissing(t, r, "system.user_permissions", nil)
	if out["is_admin"] != false {
		t.Fatalf("is_admin should be false when no auth context, got %v", out["is_admin"])
	}
	if out["role"] != string(auth.RoleViewer) {
		t.Fatalf("role should be %s, got %v", auth.RoleViewer, out["role"])
	}
	if out["backend"] != "CCU3" {
		t.Fatalf("backend should be CCU3, got %v", out["backend"])
	}
}

func TestMissingSystemUserPermissions_AdminUser(t *testing.T) {
	r := newMissingRouter(MissingCommandsConfig{UserPermissions: &stubUserPermissions{model: "CCU2"}})
	// Inject an admin identity into the context.
	ctx := auth.ContextWithIdentity(context.Background(), auth.Identity{
		Subject: "admin",
		Role:    auth.RoleAdmin,
		Scheme:  auth.SchemeBasic,
	})
	raw, _ := json.Marshal(nil)
	res := r.Dispatch(ctx, "system.user_permissions", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	b, _ := json.Marshal(res.Data)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out["is_admin"] != true {
		t.Fatalf("is_admin must be true for admin identity, got %v", out["is_admin"])
	}
	if out["role"] != string(auth.RoleAdmin) {
		t.Fatalf("role must be %s, got %v", auth.RoleAdmin, out["role"])
	}
}

func TestMissingSystemUserPermissions_NilProvider(t *testing.T) {
	// system.user_permissions must always be registered — even without provider.
	r := newMissingRouter(MissingCommandsConfig{})
	out := dispatchMissing(t, r, "system.user_permissions", nil)
	if _, ok := out["is_admin"]; !ok {
		t.Fatalf("response must contain is_admin key, got %v", out)
	}
}

// ─── stub commands (no provider wired) ───────────────────────────────────────

func TestMissingStubCommandsReturnError(t *testing.T) {
	r := newMissingRouter(MissingCommandsConfig{}) // all providers nil → stubs
	stubs := []string{
		"schedules.set_enabled",
		"links.get_form_schema",
		"links.get_profiles",
		"links.test_profile",
		"paramset.determine",
	}
	for _, name := range stubs {
		raw, _ := json.Marshal(map[string]any{})
		res := r.Dispatch(ctxForCommand(name), name, raw)
		if res.Error == nil {
			t.Errorf("%s: expected stub error, got nil", name)
			continue
		}
		// Stub errors must use the typed not_implemented code so
		// callers can branch without string-matching, and never
		// unknown_command (which would imply no handler is wired).
		if res.Error.Code != CommandErrorNotImplemented {
			t.Errorf("%s: stub error.code = %q, want %q", name, res.Error.Code, CommandErrorNotImplemented)
		}
	}
}

// ─── schedules.set_enabled (wired) ───────────────────────────────────────────

func TestMissingSchedulesSetEnabled_WiredPath(t *testing.T) {
	stub := &stubScheduleEnabler{}
	r := newMissingRouter(MissingCommandsConfig{ScheduleEnabler: stub})
	dispatchMissing(t, r, "schedules.set_enabled", map[string]any{
		"device_address": "ABC0001",
		"enabled":        true,
		"channel_key":    "1_1",
	})
	if stub.lastAddress != "ABC0001" {
		t.Fatalf("expected ABC0001, got %s", stub.lastAddress)
	}
	if !stub.lastEnabled {
		t.Fatalf("expected enabled=true")
	}
	if stub.lastKey != "1_1" {
		t.Fatalf("expected channel_key=1_1, got %s", stub.lastKey)
	}
	// Validation: missing device_address.
	dispatchExpectErr(t, r, "schedules.set_enabled",
		map[string]any{"enabled": true}, "device_address is required")
}

// ─── links.get_form_schema (wired) ──────────────────────────────────────────

func TestMissingLinksGetFormSchema_WiredPath(t *testing.T) {
	r := newMissingRouter(MissingCommandsConfig{LinkFormSchema: &stubLinkFormSchema{}})
	out := dispatchMissing(t, r, "links.get_form_schema", map[string]any{
		"interface_id":             "HmIP-RF",
		"sender_channel_address":   "ABC0001:1",
		"receiver_channel_address": "DEF0002:1",
	})
	if _, ok := out["fields"]; !ok {
		t.Fatalf("expected 'fields' in form schema response, got %v", out)
	}
	dispatchExpectErr(t, r, "links.get_form_schema",
		map[string]any{"interface_id": "HmIP-RF"},
		"required")
}

// ─── links.get_profiles (wired) ─────────────────────────────────────────────

func TestMissingLinksGetProfiles_WiredPath(t *testing.T) {
	profs := []map[string]any{
		{"id": 1, "name": "Standard"},
		{"id": 2, "name": "Nacht"},
	}
	// activeProfileID is non-zero so this asserts the handler forwards the
	// provider's derived value rather than hard-coding a literal.
	r := newMissingRouter(MissingCommandsConfig{LinkProfiles: &stubLinkProfiles{profiles: profs, activeProfileID: 2}})
	out := dispatchMissing(t, r, "links.get_profiles", map[string]any{
		"interface_id":             "HmIP-RF",
		"sender_channel_address":   "ABC0001:1",
		"receiver_channel_address": "DEF0002:1",
	})
	if out["active_profile_id"] != float64(2) {
		t.Fatalf("expected active_profile_id=2, got %v", out["active_profile_id"])
	}
	list, ok := out["profiles"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("expected 2 profiles, got %v", out["profiles"])
	}
	dispatchExpectErr(t, r, "links.get_profiles",
		map[string]any{"interface_id": "HmIP-RF"},
		"required")
}

// ─── links.test_profile (wired) ─────────────────────────────────────────────

func TestMissingLinksTestProfile_WiredPath(t *testing.T) {
	r := newMissingRouter(MissingCommandsConfig{LinkProfiles: &stubLinkProfiles{}})
	out := dispatchMissing(t, r, "links.test_profile", map[string]any{
		"interface_id":             "HmIP-RF",
		"sender_channel_address":   "ABC0001:1",
		"receiver_channel_address": "DEF0002:1",
		"profile_id":               7,
	})
	if out["success"] != true {
		t.Fatalf("expected success=true, got %v", out["success"])
	}
}

// ─── paramset.determine (wired) ──────────────────────────────────────────────

func TestMissingParamsetDetermine_WiredPath(t *testing.T) {
	r := newMissingRouter(MissingCommandsConfig{
		ParameterDeterminer: &stubParameterDeterminer{result: 42},
	})
	out := dispatchMissing(t, r, "paramset.determine", map[string]any{
		"interface_id":    "HmIP-RF",
		"channel_address": "ABC0001:1",
		"parameter_id":    "TEMPERATURE",
	})
	if out["success"] != true {
		t.Fatalf("expected success=true, got %v", out)
	}
	// JSON round-trip turns int64 → float64.
	if out["value"] != float64(42) {
		t.Fatalf("expected value=42 (as float64), got %v (%T)", out["value"], out["value"])
	}
	dispatchExpectErr(t, r, "paramset.determine",
		map[string]any{"channel_address": "ABC0001:1"},
		"required")
}

// ─── ccu.get_rssi_info ──────────────────────────────────────────────────────

// stubRSSI implements [RSSIProvider] for tests.
type stubRSSI struct {
	result map[string]any
	err    error
}

func (s *stubRSSI) RSSIInfo(_ context.Context) (map[string]any, error) {
	return s.result, s.err
}

func TestMissingCCUGetRSSIInfo_WithProvider(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		"devices": []any{
			map[string]any{
				"address":       "ABC0001",
				"name":          "Lamp",
				"interface_id":  "HmIP-RF",
				"central":       "ccu-01",
				"rssi_device":   -65,
				"rssi_peer":     -70,
				"battery_level": 80,
				"low_battery":   false,
				"reachable":     true,
			},
		},
	}
	p := &stubRSSI{result: payload}
	r := newMissingRouter(MissingCommandsConfig{RSSIInfo: p})
	out := dispatchMissing(t, r, "ccu.get_rssi_info", nil)
	devs, ok := out["devices"].([]any)
	if !ok {
		t.Fatalf("expected devices array, got %T", out["devices"])
	}
	if len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devs))
	}
	d, ok := devs[0].(map[string]any)
	if !ok {
		t.Fatal("device entry is not a map")
	}
	if d["address"] != "ABC0001" {
		t.Errorf("address: got %v", d["address"])
	}
}

func TestMissingCCUGetRSSIInfo_NotRegisteredWhenNilProvider(t *testing.T) {
	t.Parallel()
	r := newMissingRouter(MissingCommandsConfig{})
	res := r.Dispatch(context.Background(), "ccu.get_rssi_info", nil)
	if res.Error == nil || res.Error.Code != CommandErrorUnknownCommand {
		t.Fatalf("expected unknown_command when provider is nil, got %v", res.Error)
	}
}

// ─── all 9 commands registered ───────────────────────────────────────────────

func TestMissingAllNineCommandsRegistered(t *testing.T) {
	want := []string{
		"ccu.get_signal_quality",
		"ccu.get_hub_data",
		"schedules.list_devices",
		"schedules.set_enabled",
		"system.user_permissions",
		"links.get_form_schema",
		"links.get_profiles",
		"links.test_profile",
		"paramset.determine",
	}
	if len(want) != 9 {
		t.Fatalf("sanity: expected 9 commands in want, got %d", len(want))
	}
	svcVal := 0
	alarmVal := 0
	r := newMissingRouter(MissingCommandsConfig{
		SignalQuality:       &stubDeviceListProvider{},
		ScheduleDevices:     &stubDeviceListProvider{},
		HubData:             &stubHubData{svc: &svcVal, alarm: &alarmVal},
		UserPermissions:     &stubUserPermissions{model: "CCU3"},
		ScheduleEnabler:     &stubScheduleEnabler{},
		LinkFormSchema:      &stubLinkFormSchema{},
		LinkProfiles:        &stubLinkProfiles{},
		ParameterDeterminer: &stubParameterDeterminer{result: nil},
	})
	registered := make(map[string]bool)
	for _, n := range r.Commands() {
		registered[n] = true
	}
	for _, w := range want {
		if !registered[w] {
			t.Errorf("command %q not registered", w)
		}
	}
}
