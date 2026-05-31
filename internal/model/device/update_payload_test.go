// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestDeviceUpdateImplementsPayloadSource is a compile-time + runtime
// check that *Update satisfies the [payload.Source] interface.
func TestDeviceUpdateImplementsPayloadSource(t *testing.T) {
	t.Parallel()
	d := newTestDevice(t)
	upd := d.Update()
	if upd == nil {
		t.Fatal("Update() must be non-nil for an updatable device")
	}
	var _ payload.Source = upd // compile-time assertion
	// Runtime: all three payload accessors must return non-nil maps.
	if got := upd.Info(); got == nil {
		t.Error("Info() returned nil")
	}
	if got := upd.Config(); got == nil {
		t.Error("Config() returned nil")
	}
	if got := upd.State(); got == nil {
		t.Error("State() returned nil")
	}
	// ServiceMethodNames must include "install".
	names := upd.ServiceMethodNames()
	found := false
	for _, n := range names {
		if n == "install" {
			found = true
		}
	}
	if !found {
		t.Errorf("ServiceMethodNames() = %v; want to contain \"install\"", names)
	}
}

// TestDeviceUpdateImplementsHADiscoveryPayloadBuilder is a compile-time
// + runtime check that *Update satisfies [payload.HADiscoveryPayloadBuilder].
func TestDeviceUpdateImplementsHADiscoveryPayloadBuilder(t *testing.T) {
	t.Parallel()
	d := newTestDevice(t)
	upd := d.Update()
	if upd == nil {
		t.Fatal("Update() must be non-nil for an updatable device")
	}
	var _ payload.HADiscoveryPayloadBuilder = upd // compile-time assertion
	// Build a stub context to drive HADiscoveryPayload.
	comp, body := upd.HADiscoveryPayload(stubDiscoveryCtx{
		stateTopic:   "openccu-loom/ccu/iface/ADDR/update/state",
		installTopic: "openccu-loom/ccu/iface/ADDR/update/install",
	})
	if comp != "update" {
		t.Errorf("component = %q, want \"update\"", comp)
	}
	if body == nil {
		t.Fatal("HADiscoveryPayload() returned nil body")
	}
}

// TestDeviceUpdateStatePayloadShape verifies that all four mandatory
// firmware-state fields are present in the JSON shape HA consumes.
func TestDeviceUpdateStatePayloadShape(t *testing.T) {
	t.Parallel()
	d := New(Config{
		Address:   "AABB1122",
		Interface: hmenum.InterfaceHmIPRF,
		Updatable: true,
		Firmware: FirmwareInfo{
			Current:     "1.0.0",
			Available:   "2.0.0",
			UpdateState: hmenum.DeviceFirmwareStateReadyForUpdate,
		},
	})
	upd := d.Update()
	if upd == nil {
		t.Fatal("Update() must not be nil")
	}
	state, _ := upd.State().(map[string]any)
	requiredKeys := []string{"firmware", "latest_firmware", "in_progress", "firmware_update_state"}
	for _, k := range requiredKeys {
		if _, ok := state[k]; !ok {
			t.Errorf("State() missing key %q (state=%v)", k, state)
		}
	}
	if got := state["firmware"]; got != "1.0.0" {
		t.Errorf("firmware = %v, want \"1.0.0\"", got)
	}
	// HmIP-RF + ReadyForUpdate → LatestFirmware() returns available.
	if got := state["latest_firmware"]; got != "2.0.0" {
		t.Errorf("latest_firmware = %v, want \"2.0.0\"", got)
	}
	if got, ok := state["in_progress"].(bool); !ok || got {
		t.Errorf("in_progress = %v (type %T), want false bool", state["in_progress"], state["in_progress"])
	}
}

// TestDeviceUpdateHADiscoveryPayloadShape verifies the HA-canonical fields
// the broker must publish: device_class, entity_category, value_template,
// latest_version_template, payload_install, command_topic, state_topic.
func TestDeviceUpdateHADiscoveryPayloadShape(t *testing.T) {
	t.Parallel()
	d := newTestDevice(t)
	upd := d.Update()
	if upd == nil {
		t.Fatal("Update() must not be nil")
	}
	const (
		wantStateTopic   = "openccu-loom/ccu/iface/DEV/update/state"
		wantInstallTopic = "openccu-loom/ccu/iface/DEV/update/install"
	)
	_, body := upd.HADiscoveryPayload(stubDiscoveryCtx{
		stateTopic:   wantStateTopic,
		installTopic: wantInstallTopic,
	})
	if body == nil {
		t.Fatal("body must not be nil")
	}
	checks := map[string]any{
		"device_class":            "firmware",
		"entity_category":         "config",
		"value_template":          "{{ value_json.firmware }}",
		"latest_version_template": "{{ value_json.latest_firmware }}",
		"payload_install":         "INSTALL",
		"state_topic":             wantStateTopic,
		"command_topic":           wantInstallTopic,
		"latest_version_topic":    wantStateTopic,
	}
	for k, want := range checks {
		got, ok := body[k]
		if !ok {
			t.Errorf("body missing key %q", k)
			continue
		}
		if got != want {
			t.Errorf("body[%q] = %v, want %v", k, got, want)
		}
	}
}

// stubDiscoveryCtx is a minimal [payload.HADiscoveryContext] for tests.
type stubDiscoveryCtx struct {
	stateTopic   string
	installTopic string
}

func (s stubDiscoveryCtx) AggregatedStateTopic() string              { return s.stateTopic }
func (s stubDiscoveryCtx) CustomDPStateTopic() string                { return s.stateTopic }
func (s stubDiscoveryCtx) ServiceMethodCommandTopic(_ string) string { return s.installTopic }
func (s stubDiscoveryCtx) WireParameterCommandTopic(_ string) string { return "" }
func (s stubDiscoveryCtx) WireParameterStateTopic(_ string) string   { return "" }
