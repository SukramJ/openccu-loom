// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// infra_nil_guards_test.go covers nil-guard branches and pure-logic
// helpers in eventbridge.go, interfaces.go, links.go, mqtt_sink.go,
// paramsets.go, link_resolver.go, and schedules.go.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ============================================================
// EventBridge constructor + fluent setters
// ============================================================

func TestEventBridgeNewNilRegistry(t *testing.T) {
	t.Parallel()
	b := NewEventBridge(nil, nil, nil)
	if b == nil {
		t.Fatal("NewEventBridge must return non-nil")
	}
}

func TestEventBridgeWithVisibility(t *testing.T) {
	t.Parallel()
	b := NewEventBridge(nil, nil, nil).WithVisibility(nil)
	if b == nil {
		t.Fatal("WithVisibility must return non-nil")
	}
}

func TestEventBridgeWithParameterLabels(t *testing.T) {
	t.Parallel()
	b := NewEventBridge(nil, nil, nil).WithParameterLabels(nil)
	if b == nil {
		t.Fatal("WithParameterLabels must return non-nil")
	}
}

func TestEventBridgeStartNilRegistry(t *testing.T) {
	t.Parallel()
	// Must not panic with nil registry.
	b := NewEventBridge(nil, nil, nil)
	b.Start(context.Background())
}

func TestEventBridgePublishInitialSnapshotNilRegistry(t *testing.T) {
	t.Parallel()
	b := NewEventBridge(nil, nil, nil)
	b.PublishInitialSnapshot(context.Background()) // must not panic
}

// ============================================================
// InterfacesAdapter nil-guard / basic paths
// ============================================================

func TestInterfacesAdapterNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewInterfacesAdapter(nil, nil)
	if got := a.Interfaces(); got != nil {
		t.Errorf("Interfaces() nil registry = %v, want nil", got)
	}
}

func TestInterfacesAdapterInterfaceNotFound(t *testing.T) {
	t.Parallel()
	a := NewInterfacesAdapter(central.NewRegistry(), nil)
	_, ok := a.Interface("NO_SUCH_IF")
	if ok {
		t.Error("Interface() not found must return false")
	}
}

func TestInterfacesAdapterReconnectNilReconnector(t *testing.T) {
	t.Parallel()
	a := NewInterfacesAdapter(central.NewRegistry(), nil)
	err := a.Reconnect(context.Background(), "HmIP-RF")
	if err == nil {
		t.Fatal("expected ErrNoReconnector")
	}
}

// ============================================================
// LinksDomain constructor + SetAuditRecorder
// ============================================================

func TestLinksDomainNewNil(t *testing.T) {
	t.Parallel()
	d := NewLinksDomain(nil, nil, nil)
	if d == nil {
		t.Fatal("NewLinksDomain must return non-nil")
	}
}

func TestLinksDomainSetAuditRecorderNil(t *testing.T) {
	t.Parallel()
	d := NewLinksDomain(nil, nil, nil)
	got := d.SetAuditRecorder(nil) // nil → noop recorder
	if got == nil {
		t.Fatal("SetAuditRecorder must return non-nil receiver")
	}
}

func TestLinksDomainListLinksNilRegistry(t *testing.T) {
	t.Parallel()
	d := NewLinksDomain(nil, nil, nil)
	_, err := d.ListLinks(context.Background(), "DEV001", "en")
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

// ============================================================
// links.go pure package-level helpers
// ============================================================

func TestChannelOf_NilDevice(t *testing.T) {
	t.Parallel()
	if got := channelOf(nil, "DEV:1"); got != nil {
		t.Errorf("channelOf nil device = %v, want nil", got)
	}
}

func TestChannelOf_ValidDevice(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV001", InterfaceID: "test", Model: "M"})
	dev.AddChannel("DEV001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch := channelOf(dev, "DEV001:1")
	if ch == nil {
		t.Fatal("channelOf must find existing channel")
	}
}

func TestChannelTypeOf_NilChannel(t *testing.T) {
	t.Parallel()
	if got := channelTypeOf(nil); got != "" {
		t.Errorf("channelTypeOf nil = %q, want empty", got)
	}
}

func TestChannelTypeOf_ValidChannel(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV002", InterfaceID: "test", Model: "M"})
	ch := dev.AddChannel("DEV002:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	if got := channelTypeOf(ch); got != "DIMMER" {
		t.Errorf("channelTypeOf = %q, want DIMMER", got)
	}
}

func TestChannelNameOf_NilChannel(t *testing.T) {
	t.Parallel()
	if got := channelNameOf(nil); got != "" {
		t.Errorf("channelNameOf nil = %q, want empty", got)
	}
}

func TestChannelNameOf_ValidChannel(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV003", InterfaceID: "test", Model: "M"})
	ch := dev.AddChannel("DEV003:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.Name = "My Switch"
	if got := channelNameOf(ch); got != "My Switch" {
		t.Errorf("channelNameOf = %q, want My Switch", got)
	}
}

func TestDeviceNameOr_NilDevice(t *testing.T) {
	t.Parallel()
	if got := deviceNameOr(nil, "fallback"); got != "fallback" {
		t.Errorf("deviceNameOr nil = %q, want fallback", got)
	}
}

func TestDeviceNameOr_HasName(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV004", InterfaceID: "test", Model: "M"})
	dev.Name = "Named Device"
	if got := deviceNameOr(dev, "fallback"); got != "Named Device" {
		t.Errorf("deviceNameOr with name = %q, want Named Device", got)
	}
}

func TestDeviceNameOr_NoName(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV005", InterfaceID: "test", Model: "M"})
	dev.Name = ""
	if got := deviceNameOr(dev, "fallback"); got != "DEV005" {
		t.Errorf("deviceNameOr no name = %q, want DEV005 (address)", got)
	}
}

func TestModelOf_NilDevice(t *testing.T) {
	t.Parallel()
	if got := modelOf(nil); got != "" {
		t.Errorf("modelOf nil = %q, want empty", got)
	}
}

func TestModelOf_ValidDevice(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV006", InterfaceID: "test", Model: "HmIP-eTRV-2"})
	if got := modelOf(dev); got != "HmIP-eTRV-2" {
		t.Errorf("modelOf = %q, want HmIP-eTRV-2", got)
	}
}

// ============================================================
// LinksDomain.channelTypeLabel (nil guards)
// ============================================================

func TestLinksDomainChannelTypeLabel_NilChannel(t *testing.T) {
	t.Parallel()
	d := NewLinksDomain(nil, nil, nil)
	if got := d.channelTypeLabel("en", nil); got != "" {
		t.Errorf("channelTypeLabel nil ch = %q, want empty", got)
	}
}

func TestLinksDomainChannelTypeLabel_EmptyType(t *testing.T) {
	t.Parallel()
	d := NewLinksDomain(nil, nil, nil)
	dev := device.New(device.Config{Address: "DEV007", InterfaceID: "test", Model: "M"})
	ch := dev.AddChannel("DEV007:1", 1, "", hmenum.ParamsetKeyValues)
	if got := d.channelTypeLabel("en", ch); got != "" {
		t.Errorf("channelTypeLabel empty type = %q, want empty", got)
	}
}

func TestLinksDomainChannelTypeLabel_NilTranslations(t *testing.T) {
	t.Parallel()
	// nil translations → returns ch.Type directly
	d := NewLinksDomain(nil, nil, nil)
	dev := device.New(device.Config{Address: "DEV008", InterfaceID: "test", Model: "M"})
	ch := dev.AddChannel("DEV008:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	if got := d.channelTypeLabel("en", ch); got != "SWITCH_VIRTUAL_RECEIVER" {
		t.Errorf("channelTypeLabel nil translations = %q, want SWITCH_VIRTUAL_RECEIVER", got)
	}
}

// ============================================================
// LinksDomain.findDevice (nil registry / not found)
// ============================================================

func TestLinksDomainFindDevice_NilRegistry(t *testing.T) {
	t.Parallel()
	d := NewLinksDomain(nil, nil, nil)
	if got := d.findDevice("DEV001"); got != nil {
		t.Errorf("findDevice nil registry = %v, want nil", got)
	}
}

func TestLinksDomainFindDevice_EmptyAddress(t *testing.T) {
	t.Parallel()
	d := NewLinksDomain(central.NewRegistry(), nil, nil)
	if got := d.findDevice(""); got != nil {
		t.Errorf("findDevice empty address = %v, want nil", got)
	}
}

func TestLinksDomainFindDevice_NotFound(t *testing.T) {
	t.Parallel()
	d := NewLinksDomain(central.NewRegistry(), nil, nil)
	if got := d.findDevice("NOSUCHDEV"); got != nil {
		t.Errorf("findDevice not found = %v, want nil", got)
	}
}

// ============================================================
// MQTTCommandSink nil-registry / nil-writer guards
// ============================================================

func TestMQTTCommandSinkNilWriter(t *testing.T) {
	t.Parallel()
	s := NewMQTTCommandSink(central.NewRegistry(), nil)
	err := s.SetValue(context.Background(), "ccu", "HmIP-RF", "DEV:1", hmenum.ParameterState, true, hmenum.CommandPriorityLow)
	if err == nil {
		t.Fatal("expected ErrNoWriter for nil writer")
	}
}

func TestMQTTCommandSinkSetSysvarUnknownCentral(t *testing.T) {
	t.Parallel()
	s := NewMQTTCommandSink(central.NewRegistry(), nil)
	err := s.SetSysvar(context.Background(), "no_such_ccu", "varname", true)
	if err == nil {
		t.Fatal("expected error for unknown central")
	}
}

func TestMQTTCommandSinkTriggerProgramUnknownCentral(t *testing.T) {
	t.Parallel()
	s := NewMQTTCommandSink(central.NewRegistry(), nil)
	err := s.TriggerProgram(context.Background(), "no_such_ccu", "prog_id")
	if err == nil {
		t.Fatal("expected error for unknown central")
	}
}

func TestMQTTCommandSinkInvokeCustomDPNotNil(t *testing.T) {
	t.Parallel()
	s := NewMQTTCommandSink(central.NewRegistry(), nil)
	// cdpDispatch is non-nil; the device won't be found but should not panic.
	err := s.InvokeCustomDP(context.Background(), "ccu", "NOSUCHDEV", "Light", "set_level", nil, hmenum.CommandPriorityLow)
	if err == nil {
		t.Fatal("expected error — device not found")
	}
}

func TestMQTTCommandSinkInvokeChannelServiceUnknownCentral(t *testing.T) {
	t.Parallel()
	s := NewMQTTCommandSink(central.NewRegistry(), nil)
	err := s.InvokeChannelService(context.Background(), "no_such_ccu", "HmIP-RF", "DEV001", 1, "set_level", nil, hmenum.CommandPriorityLow)
	if err == nil {
		t.Fatal("expected error for unknown central")
	}
}

// ============================================================
// ParamsetsDomain SetAuditRecorder + SetVisibilityGate
// ============================================================

func TestParamsetsDomainSetAuditRecorderNil(t *testing.T) {
	t.Parallel()
	p := NewParamsetsDomain(nil, nil)
	got := p.SetAuditRecorder(nil)
	if got == nil {
		t.Fatal("SetAuditRecorder must return non-nil receiver")
	}
}

func TestParamsetsDomainSetVisibilityGateNil(t *testing.T) {
	t.Parallel()
	p := NewParamsetsDomain(nil, nil)
	got := p.SetVisibilityGate(nil)
	if got == nil {
		t.Fatal("SetVisibilityGate must return non-nil receiver")
	}
}

// ============================================================
// schedules.go: applyLockEncoding
// ============================================================

func TestApplyLockEncodingDoorLock(t *testing.T) {
	t.Parallel()
	e := applyLockEncoding(handlers.SimpleScheduleEntry{
		LockMode:   "door_lock",
		LockAction: "lock_autorelock_end",
	})
	if e.Level != 0.0 {
		t.Errorf("door_lock lock_autorelock_end level = %v, want 0.0", e.Level)
	}
	if e.Duration == "" {
		t.Error("duration must be non-empty after encoding")
	}
	if len(e.TargetChannels) == 0 || e.TargetChannels[0] != "1_1" {
		t.Errorf("target channels = %v, want [1_1]", e.TargetChannels)
	}
}

func TestApplyLockEncodingDoorLockUnknownAction(t *testing.T) {
	t.Parallel()
	original := handlers.SimpleScheduleEntry{
		LockMode:   "door_lock",
		LockAction: "unknown_action",
		Level:      0.0,
	}
	e := applyLockEncoding(original)
	// Unknown action: passthrough unchanged
	if e.LockMode != "door_lock" {
		t.Errorf("unknown action changed LockMode to %q", e.LockMode)
	}
}

func TestApplyLockEncodingUserPermissionGranted(t *testing.T) {
	t.Parallel()
	e := applyLockEncoding(handlers.SimpleScheduleEntry{
		LockMode:   "user_permission",
		Permission: "granted",
	})
	if e.Level != 1.0 {
		t.Errorf("granted level = %v, want 1.0", e.Level)
	}
	if e.Duration == "" {
		t.Error("duration must be non-empty after encoding")
	}
	if len(e.TargetChannels) == 0 || e.TargetChannels[0] != "2_1" {
		t.Errorf("target channels = %v, want [2_1]", e.TargetChannels)
	}
}

func TestApplyLockEncodingUserPermissionNotGranted(t *testing.T) {
	t.Parallel()
	e := applyLockEncoding(handlers.SimpleScheduleEntry{
		LockMode:   "user_permission",
		Permission: "not_granted",
	})
	if e.Level != 0.0 {
		t.Errorf("not_granted level = %v, want 0.0", e.Level)
	}
}

func TestApplyLockEncodingUserPermissionExistingChannels(t *testing.T) {
	t.Parallel()
	// When TargetChannels already set, preserve them.
	e := applyLockEncoding(handlers.SimpleScheduleEntry{
		LockMode:       "user_permission",
		Permission:     "granted",
		TargetChannels: []string{"3_2"},
	})
	if len(e.TargetChannels) == 0 || e.TargetChannels[0] != "3_2" {
		t.Errorf("target channels = %v, want [3_2]", e.TargetChannels)
	}
}

func TestApplyLockEncodingUnknownMode(t *testing.T) {
	t.Parallel()
	// Unknown lock_mode: passthrough.
	e := applyLockEncoding(handlers.SimpleScheduleEntry{LockMode: "other_mode"})
	if e.LockMode != "other_mode" {
		t.Errorf("unknown mode mutated to %q", e.LockMode)
	}
}

// ============================================================
// schedules.go: parseSimpleScheduleWithDomain
// ============================================================

func TestParseSimpleScheduleWithDomainNonLock(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"01_WP_WEEKDAY":      4,
		"01_WP_FIXED_HOUR":   7,
		"01_WP_FIXED_MINUTE": 30,
		"01_WP_LEVEL":        1.0,
	}
	entries := parseSimpleScheduleWithDomain(raw, "switch")
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].LockMode != "" {
		t.Errorf("non-lock domain must not set LockMode, got %q", entries[0].LockMode)
	}
}

func TestParseSimpleScheduleWithDomainLockDoorLock(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"01_WP_WEEKDAY":         4,
		"01_WP_FIXED_HOUR":      7,
		"01_WP_FIXED_MINUTE":    30,
		"01_WP_LEVEL":           0.0,
		"01_WP_TARGET_CHANNELS": 1, // bit 0 → 1_1
	}
	entries := parseSimpleScheduleWithDomain(raw, "lock")
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].LockMode != "door_lock" {
		t.Errorf("LockMode = %q, want door_lock", entries[0].LockMode)
	}
}

func TestParseSimpleScheduleWithDomainLockUserPermission(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"01_WP_WEEKDAY":         4,
		"01_WP_FIXED_HOUR":      7,
		"01_WP_FIXED_MINUTE":    30,
		"01_WP_LEVEL":           1.0,
		"01_WP_TARGET_CHANNELS": 8, // bit 3 → 2_1 (channel 2)
	}
	entries := parseSimpleScheduleWithDomain(raw, "lock")
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].LockMode != "user_permission" {
		t.Errorf("LockMode = %q, want user_permission", entries[0].LockMode)
	}
	if entries[0].Permission != "granted" {
		t.Errorf("Permission = %q, want granted", entries[0].Permission)
	}
}

// ============================================================
// schedules.go: formatTimeBaseFactor / parseTimeBaseFactor
// ============================================================

func TestFormatTimeBaseFactor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base, factor int
		want         string
	}{
		{4, 5, "5min"},
		{1, 30, "30s"},
		{7, 2, "2h"},
		{0, 10, "1s"}, // base 0 = 0.1s/unit → 10 units = 1.0s
		{0, 0, ""},    // zero factor
		{-1, 5, ""},   // negative base
		{99, 5, ""},   // out of range base
	}
	for _, tc := range cases {
		got := formatTimeBaseFactor(tc.base, tc.factor)
		if got != tc.want {
			t.Errorf("formatTimeBaseFactor(%d,%d) = %q, want %q", tc.base, tc.factor, got, tc.want)
		}
	}
}

func TestParseTimeBaseFactor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s      string
		wantOK bool
	}{
		{"30s", true},
		{"5min", true},
		{"1h", true},
		{"500ms", true},
		{"", false},
		{"badvalue", false},
		{"-5s", false},
	}
	for _, tc := range cases {
		_, _, ok := parseTimeBaseFactor(tc.s)
		if ok != tc.wantOK {
			t.Errorf("parseTimeBaseFactor(%q) ok=%v, want %v", tc.s, ok, tc.wantOK)
		}
	}
}

// ============================================================
// schedules.go: decodeTargetChannels / encodeTargetChannels
// ============================================================

func TestDecodeEncodeTargetChannels(t *testing.T) {
	t.Parallel()
	// Encode "1_1" (bit 0) → 1, then decode back.
	encoded := encodeTargetChannels([]string{"1_1"})
	if encoded == 0 {
		t.Fatal("encodeTargetChannels(1_1) = 0, want non-zero")
	}
	decoded := decodeTargetChannels(encoded)
	found := false
	for _, ch := range decoded {
		if ch == "1_1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("decodeTargetChannels → %v, expected to contain 1_1", decoded)
	}
}

func TestDecodeTargetChannelsZero(t *testing.T) {
	t.Parallel()
	if got := decodeTargetChannels(0); len(got) != 0 {
		t.Errorf("decodeTargetChannels(0) = %v, want empty", got)
	}
}

func TestEncodeTargetChannelsEmpty(t *testing.T) {
	t.Parallel()
	if got := encodeTargetChannels(nil); got != 0 {
		t.Errorf("encodeTargetChannels(nil) = %d, want 0", got)
	}
}

// ============================================================
// schedules.go: weekdayBitsToNames / weekdayNamesToBits
// ============================================================

func TestWeekdayBitsToNamesRoundtrip(t *testing.T) {
	t.Parallel()
	names := []string{"MONDAY", "WEDNESDAY", "FRIDAY"}
	bits := weekdayNamesToBits(names)
	if bits == 0 {
		t.Fatal("weekdayNamesToBits returned 0 for MONDAY/WEDNESDAY/FRIDAY")
	}
	decoded := weekdayBitsToNames(bits)
	if len(decoded) != 3 {
		t.Errorf("weekdayBitsToNames → %v, want 3 names", decoded)
	}
}

func TestWeekdayNamesToBitsZero(t *testing.T) {
	t.Parallel()
	if got := weekdayNamesToBits(nil); got != 0 {
		t.Errorf("weekdayNamesToBits(nil) = %d, want 0", got)
	}
}

func TestWeekdayBitsToNamesZero(t *testing.T) {
	t.Parallel()
	if got := weekdayBitsToNames(0); len(got) != 0 {
		t.Errorf("weekdayBitsToNames(0) = %v, want empty", got)
	}
}

// ============================================================
// linkClientAdapter nil-guard tests
// ============================================================

func TestLinkClientAdapterNilDomain(t *testing.T) {
	t.Parallel()
	a := &linkClientAdapter{domain: nil}

	if err := a.AddLink(context.Background(), "s", "r", "n", "d"); err == nil {
		t.Error("AddLink nil domain must return error")
	}
	if err := a.RemoveLink(context.Background(), "s", "r"); err == nil {
		t.Error("RemoveLink nil domain must return error")
	}
	if _, err := a.GetLinks(context.Background(), "DEV001"); err == nil {
		t.Error("GetLinks nil domain must return error")
	}
	if _, err := a.GetLinkableChannels(context.Background(), "DEV001"); err == nil {
		t.Error("GetLinkableChannels must return sentinel error")
	}
	if err := a.SetLinkInfo(context.Background(), "s", "r", "n", "d"); err == nil {
		t.Error("SetLinkInfo must return unsupported sentinel")
	}
	if _, err := a.GetLinkInfo(context.Background(), "s", "r"); err == nil {
		t.Error("GetLinkInfo must return unsupported sentinel")
	}
}

func TestLinkClientAdapterNilReceiver(t *testing.T) {
	t.Parallel()
	var a *linkClientAdapter
	if err := a.AddLink(context.Background(), "s", "r", "n", "d"); err == nil {
		t.Error("AddLink nil receiver must return error")
	}
	if err := a.RemoveLink(context.Background(), "s", "r"); err == nil {
		t.Error("RemoveLink nil receiver must return error")
	}
	if _, err := a.GetLinks(context.Background(), "DEV001"); err == nil {
		t.Error("GetLinks nil receiver must return error")
	}
}

// ============================================================
// schedules.go: SchedulesDomain nil-registry guards
// ============================================================

func TestSchedulesDomainNilRegistryDetectDomain(t *testing.T) {
	t.Parallel()
	s := NewSchedulesDomain(nil, nil)
	got := s.detectScheduleDomain("DEV001", 1)
	if got != "" {
		t.Errorf("detectScheduleDomain nil registry = %q, want empty", got)
	}
}
