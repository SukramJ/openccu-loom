// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ============================================================
// normalizeParamsetKey tests
// ============================================================

func TestNormalizeParamsetKeyValues(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "VALUES", "values", "Values"} {
		got, err := normalizeParamsetKey(raw)
		if err != nil {
			t.Errorf("normalizeParamsetKey(%q): %v", raw, err)
		}
		if got != hmenum.ParamsetKeyValues {
			t.Errorf("normalizeParamsetKey(%q) = %v, want Values", raw, got)
		}
	}
}

func TestNormalizeParamsetKeyMaster(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"MASTER", "master", "Master"} {
		got, err := normalizeParamsetKey(raw)
		if err != nil {
			t.Errorf("normalizeParamsetKey(%q): %v", raw, err)
		}
		if got != hmenum.ParamsetKeyMaster {
			t.Errorf("normalizeParamsetKey(%q) = %v, want Master", raw, got)
		}
	}
}

func TestNormalizeParamsetKeyLink(t *testing.T) {
	t.Parallel()
	got, err := normalizeParamsetKey("LINK")
	if err != nil {
		t.Fatalf("LINK: %v", err)
	}
	if got != hmenum.ParamsetKeyLink {
		t.Errorf("normalizeParamsetKey(LINK) = %v, want Link", got)
	}
}

func TestNormalizeParamsetKeyUnknownErrors(t *testing.T) {
	t.Parallel()
	_, err := normalizeParamsetKey("UNKNOWN")
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

// ============================================================
// cloneRaw tests
// ============================================================

func TestCloneRawNil(t *testing.T) {
	t.Parallel()
	if got := cloneRaw(nil); got != nil {
		t.Errorf("cloneRaw(nil) = %v, want nil", got)
	}
}

func TestCloneRawEmpty(t *testing.T) {
	t.Parallel()
	if got := cloneRaw(json.RawMessage{}); got != nil {
		t.Errorf("cloneRaw(empty) = %v, want nil", got)
	}
}

func TestCloneRawCopies(t *testing.T) {
	t.Parallel()
	src := json.RawMessage(`{"key":"val"}`)
	dst := cloneRaw(src)
	if string(dst) != string(src) {
		t.Errorf("cloneRaw = %q, want %q", dst, src)
	}
	// Mutate src; dst must not change.
	src[0] = '['
	if dst[0] != '{' {
		t.Error("cloneRaw must produce an independent copy")
	}
}

// ============================================================
// humanizeRaw tests
// ============================================================

func TestHumanizeRawEmpty(t *testing.T) {
	t.Parallel()
	if got := humanizeRaw(""); got != "" {
		t.Errorf("humanizeRaw('') = %q, want empty", got)
	}
}

func TestHumanizeRawSnakeCase(t *testing.T) {
	t.Parallel()
	if got := humanizeRaw("HIGH_PRIORITY"); got != "High Priority" {
		t.Errorf("humanizeRaw('HIGH_PRIORITY') = %q, want 'High Priority'", got)
	}
}

func TestHumanizeRawSingleWord(t *testing.T) {
	t.Parallel()
	if got := humanizeRaw("OPEN"); got != "Open" {
		t.Errorf("humanizeRaw('OPEN') = %q, want 'Open'", got)
	}
}

func TestHumanizeRawNumeric(t *testing.T) {
	t.Parallel()
	// Numeric strings must pass through unchanged.
	if got := humanizeRaw("42"); got != "42" {
		t.Errorf("humanizeRaw('42') = %q, want '42'", got)
	}
}

// ============================================================
// isSchedulePattern tests
// ============================================================

func TestIsSchedulePatternTrue(t *testing.T) {
	t.Parallel()
	cases := []string{
		"1_WP_ENDTIME_SUNDAY_5",
		"2_WP_TEMPERATURE",
		"10_WP_STARTTIME",
	}
	for _, name := range cases {
		if !isSchedulePattern(name) {
			t.Errorf("isSchedulePattern(%q) = false, want true", name)
		}
	}
}

func TestIsSchedulePatternFalse(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"STATE",
		"LEVEL",
		"_WP_NO_LEADING_DIGIT",
		"1_NOTWP",
		"short",
	}
	for _, name := range cases {
		if isSchedulePattern(name) {
			t.Errorf("isSchedulePattern(%q) = true, want false", name)
		}
	}
}

// ============================================================
// stringSet / filterAvailable / difference / sortStrings tests
// ============================================================

func TestStringSet(t *testing.T) {
	t.Parallel()
	s := stringSet([]string{"a", "b", "c", "a"})
	if len(s) != 3 {
		t.Errorf("len = %d, want 3", len(s))
	}
	if _, ok := s["b"]; !ok {
		t.Error("b must be in set")
	}
}

func TestFilterAvailable(t *testing.T) {
	t.Parallel()
	available := stringSet([]string{"a", "c"})
	got := filterAvailable([]string{"a", "b", "c", "d"}, available)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, v := range got {
		if v != "a" && v != "c" {
			t.Errorf("unexpected value %q", v)
		}
	}
}

func TestFilterAvailableEmpty(t *testing.T) {
	t.Parallel()
	got := filterAvailable(nil, stringSet([]string{"a"}))
	if len(got) != 0 {
		t.Errorf("nil input → len=%d, want 0", len(got))
	}
}

func TestDifference(t *testing.T) {
	t.Parallel()
	available := stringSet([]string{"a", "b", "c"})
	used := stringSet([]string{"a", "c"})
	got := difference(available, used)
	if len(got) != 1 || got[0] != "b" {
		t.Errorf("difference = %v, want [b]", got)
	}
}

func TestDifferenceAllUsed(t *testing.T) {
	t.Parallel()
	s := stringSet([]string{"x"})
	got := difference(s, s)
	if len(got) != 0 {
		t.Errorf("all used → len=%d, want 0", len(got))
	}
}

func TestSortStrings(t *testing.T) {
	t.Parallel()
	in := []string{"c", "a", "b"}
	sortStrings(in)
	if in[0] != "a" || in[1] != "b" || in[2] != "c" {
		t.Errorf("sortStrings = %v, want [a b c]", in)
	}
}

func TestSortStringsEmpty(t *testing.T) {
	t.Parallel()
	sortStrings(nil) // must not panic
}

func TestSortStringsSingle(t *testing.T) {
	t.Parallel()
	in := []string{"z"}
	sortStrings(in) // must not panic
	if in[0] != "z" {
		t.Errorf("single-element sort = %q, want z", in[0])
	}
}

// ============================================================
// lookupDPSource — all branches
// ============================================================

func TestLookupDPSourceNilChannel(t *testing.T) {
	t.Parallel()
	src, dp := lookupDPSource(nil, "STATE", payload.BucketValues)
	if src != nil || dp != nil {
		t.Error("nil channel must return (nil, nil)")
	}
}

func TestLookupDPSourceValuesParameterFound(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DPDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	ch := dev.AddChannel("DPDEV001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	// Channel has no parameters yet → should return (nil, nil)
	src, dp := lookupDPSource(ch, "ACTUAL_TEMPERATURE", payload.BucketValues)
	if src != nil || dp != nil {
		t.Errorf("empty channel VALUES = (%v, %v), want (nil, nil)", src, dp)
	}
}

func TestLookupDPSourceMasterParameterNotFound(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DPDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	ch := dev.AddChannel("DPDEV002:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	// No master parameters → returns (nil, nil)
	src, dp := lookupDPSource(ch, "BOOST_MODE", payload.BucketMaster)
	if src != nil || dp != nil {
		t.Errorf("BucketMaster not found = (%v, %v), want (nil, nil)", src, dp)
	}
}

func TestLookupDPSourceCalculatedEmpty(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DPDEV003", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	ch := dev.AddChannel("DPDEV003:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	// No calculated DPs → returns (nil, nil)
	src, dp := lookupDPSource(ch, "COMBINED_TEMP", payload.BucketCalculated)
	if src != nil || dp != nil {
		t.Errorf("BucketCalculated empty = (%v, %v), want (nil, nil)", src, dp)
	}
}

func TestLookupDPSourceDefaultBucket(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DPDEV004", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	ch := dev.AddChannel("DPDEV004:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	// Unknown bucket falls through to default (same as VALUES)
	src, dp := lookupDPSource(ch, "STATE", payload.Bucket("unknown"))
	if src != nil || dp != nil {
		t.Errorf("unknown bucket empty = (%v, %v), want (nil, nil)", src, dp)
	}
}

// ============================================================
// UISchemaAdapter.peerChannelType — device-found paths (new paths)
// ============================================================

func TestPeerChannelTypeDeviceFoundWithChannel(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-peer"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{Address: "PDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-PS"})
	dev.AddChannel("PDEV001:1", 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	a := &UISchemaAdapter{registry: reg}
	got := a.peerChannelType("PDEV001:1")
	if got != "SWITCH_VIRTUAL_RECEIVER" {
		t.Errorf("channel found = %q, want SWITCH_VIRTUAL_RECEIVER", got)
	}
}

func TestPeerChannelTypeDeviceFoundChannelNotFound(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-peer2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{Address: "PDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-PS"})
	// No channel added
	c.ModelRegistry.Put(dev)

	a := &UISchemaAdapter{registry: reg}
	// Channel :9 doesn't exist → returns ""
	got := a.peerChannelType("PDEV002:9")
	if got != "" {
		t.Errorf("channel not found = %q, want empty", got)
	}
}

// ============================================================
// CallbackHandlers.NewDevices — non-empty descriptors path
// ============================================================

func TestCallbackHandlersNewDevicesNonEmptyDescs(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-newdev"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	h := NewCallbackHandlers(c, nil)
	// Pass a non-empty ArrayValue with a struct (minimal device descriptor)
	// Even if the struct doesn't parse as a full DeviceDescription,
	// the code still iterates over it — must not panic.
	descs := xmlrpc.ArrayValue{
		xmlrpc.StructValue{
			Members: []xmlrpc.Member{
				{Name: "ADDRESS", Value: xmlrpc.StringValue("TEST001")},
				{Name: "TYPE", Value: xmlrpc.StringValue("HmIP-STH")},
			},
		},
	}
	if err := h.NewDevices(context.Background(), "HmIP-RF", descs); err != nil {
		t.Fatalf("NewDevices non-empty: %v", err)
	}
}

// nilAdapter returns a UISchemaAdapter with no backing data — all
// optional fields nil. The nil-guard branches fire immediately.
func nilAdapter() *UISchemaAdapter { return &UISchemaAdapter{} }

// ============================================================
// channelLabel
// ============================================================

func TestChannelLabelNilTranslations(t *testing.T) {
	t.Parallel()
	if got := nilAdapter().channelLabel("en", "DIMMER"); got != "" {
		t.Errorf("channelLabel nil translations = %q, want empty", got)
	}
}

// ============================================================
// parameterLabel
// ============================================================

func TestParameterLabelNilTranslations(t *testing.T) {
	t.Parallel()
	if got := nilAdapter().parameterLabel("en", "DIMMER", "LEVEL"); got != "" {
		t.Errorf("parameterLabel nil translations = %q, want empty", got)
	}
}

// ============================================================
// parameterHelp
// ============================================================

func TestParameterHelpNilTranslations(t *testing.T) {
	t.Parallel()
	if got := nilAdapter().parameterHelp("en", "LEVEL"); got != "" {
		t.Errorf("parameterHelp nil translations = %q, want empty", got)
	}
}

// ============================================================
// groupLabel
// ============================================================

func TestGroupLabelNilTranslations(t *testing.T) {
	t.Parallel()
	if got := nilAdapter().groupLabel("en", "temps"); got != "temps" {
		t.Errorf("groupLabel nil translations = %q, want key passthrough", got)
	}
}

func TestGroupLabelEmptyKey(t *testing.T) {
	t.Parallel()
	if got := nilAdapter().groupLabel("en", ""); got != "" {
		t.Errorf("groupLabel empty key = %q, want empty", got)
	}
}

// ============================================================
// errorLabel
// ============================================================

func TestErrorLabelNilTranslations(t *testing.T) {
	t.Parallel()
	if got := nilAdapter().errorLabel("en", "my_key"); got != "my_key" {
		t.Errorf("errorLabel nil translations = %q, want key passthrough", got)
	}
}

func TestErrorLabelEmptyKey(t *testing.T) {
	t.Parallel()
	if got := nilAdapter().errorLabel("en", ""); got != "" {
		t.Errorf("errorLabel empty key = %q, want empty", got)
	}
}

// ============================================================
// expandPresets
// ============================================================

func TestExpandPresetsNilEasymode(t *testing.T) {
	t.Parallel()
	if got := nilAdapter().expandPresets("en", "some_preset"); got != nil {
		t.Errorf("expandPresets nil easymode = %v, want nil", got)
	}
}

func TestExpandPresetsEmptyID(t *testing.T) {
	t.Parallel()
	if got := nilAdapter().expandPresets("en", ""); got != nil {
		t.Errorf("expandPresets empty id = %v, want nil", got)
	}
}

func TestExpandPresetsUnknownID(t *testing.T) {
	t.Parallel()
	a := &UISchemaAdapter{
		easymode: &ccudata.Easymode{
			OptionPresets: map[string]ccudata.OptionPreset{},
		},
	}
	if got := a.expandPresets("en", "no_such_preset"); got != nil {
		t.Errorf("expandPresets unknown id = %v, want nil", got)
	}
}

// ============================================================
// valueList
// ============================================================

func TestValueListNilTranslations(t *testing.T) {
	t.Parallel()
	// nil translations → humanize fallback
	got := nilAdapter().valueList("en", "DIMMER", "LEVEL", []string{"LOW", "HIGH"})
	if len(got) != 2 {
		t.Fatalf("valueList len = %d, want 2", len(got))
	}
	if got[0].Key != "LOW" || got[1].Key != "HIGH" {
		t.Errorf("valueList keys = %v", got)
	}
}

func TestValueListEmpty(t *testing.T) {
	t.Parallel()
	got := nilAdapter().valueList("en", "DIMMER", "LEVEL", nil)
	if len(got) != 0 {
		t.Errorf("valueList nil input → len=%d, want 0", len(got))
	}
}

// ============================================================
// dpIsWritable
// ============================================================

// writableFakeDP implements IsWritable() → true.
type writableFakeDP struct {
	*generic.Switch
}

func (f *writableFakeDP) IsWritable() bool { return true }

// readonlyFakeDP implements IsWritable() → false.
type readonlyFakeDP struct {
	*generic.Switch
}

func (f *readonlyFakeDP) IsWritable() bool { return false }

func TestDpIsWritableTypedTrue(t *testing.T) {
	t.Parallel()
	sw := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "A:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "STATE"},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})
	dp := &writableFakeDP{Switch: sw}
	pd := hmproto.ParameterData{Operations: hmenum.OperationsRead} // descriptor says read-only
	if !dpIsWritable(dp, pd) {
		t.Error("typed IsWritable()=true must win over descriptor")
	}
}

func TestDpIsWritableTypedFalse(t *testing.T) {
	t.Parallel()
	sw := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "A:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "STATE"},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})
	dp := &readonlyFakeDP{Switch: sw}
	pd := hmproto.ParameterData{Operations: hmenum.OperationsRead | hmenum.OperationsWrite}
	if dpIsWritable(dp, pd) {
		t.Error("typed IsWritable()=false must win over descriptor")
	}
}

func TestDpIsWritableFallbackDescriptorWritable(t *testing.T) {
	t.Parallel()
	// Use a generic.Switch directly — it implements IsWritable() via embedded base.
	sw := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "A:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "STATE"},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})
	pd := hmproto.ParameterData{Operations: hmenum.OperationsRead | hmenum.OperationsWrite}
	if !dpIsWritable(sw, pd) {
		t.Error("read+write descriptor → dpIsWritable should return true")
	}
}

// ============================================================
// groupForParam
// ============================================================

func TestGroupForParamFound(t *testing.T) {
	t.Parallel()
	meta := &ccudata.SenderTypeMetadata{
		ParameterGroups: []ccudata.ParameterGroupDef{
			{ID: "temps", Parameters: []string{"SETPOINT", "LEVEL"}},
		},
	}
	if got := groupForParam(meta, "SETPOINT"); got != "temps" {
		t.Errorf("groupForParam(SETPOINT) = %q, want temps", got)
	}
}

func TestGroupForParamNotFound(t *testing.T) {
	t.Parallel()
	meta := &ccudata.SenderTypeMetadata{
		ParameterGroups: []ccudata.ParameterGroupDef{
			{ID: "temps", Parameters: []string{"SETPOINT"}},
		},
	}
	if got := groupForParam(meta, "UNKNOWN"); got != "" {
		t.Errorf("groupForParam(UNKNOWN) = %q, want empty", got)
	}
}

func TestGroupForParamEmptyMeta(t *testing.T) {
	t.Parallel()
	meta := &ccudata.SenderTypeMetadata{}
	if got := groupForParam(meta, "LEVEL"); got != "" {
		t.Errorf("groupForParam empty meta = %q, want empty", got)
	}
}

// ============================================================
// channelTypeOf
// ============================================================

func TestChannelTypeOfFromChannelType(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	dev := device.New(device.Config{Address: "DEV001", InterfaceID: "test", Model: "MyModel"})
	ch := dev.AddChannel("DEV001:1", 1, "MY_CHANNEL_TYPE", hmenum.ParamsetKeyValues)
	got := a.channelTypeOf(dev, ch)
	if got != "MY_CHANNEL_TYPE" {
		t.Errorf("channelTypeOf = %q, want MY_CHANNEL_TYPE", got)
	}
}

func TestChannelTypeOfFallsBackToModel(t *testing.T) {
	t.Parallel()
	a := nilAdapter()
	dev := device.New(device.Config{Address: "DEV002", InterfaceID: "test", Model: "MyModel"})
	// Add channel with empty type by creating it and not setting a type.
	ch := dev.AddChannel("DEV002:1", 1, "", hmenum.ParamsetKeyValues)
	got := a.channelTypeOf(dev, ch)
	if got != "MyModel" {
		t.Errorf("channelTypeOf empty type falls back to model = %q, want MyModel", got)
	}
}

func TestChannelTypeOfWithAliasStore(t *testing.T) {
	t.Parallel()
	a := &UISchemaAdapter{
		profiles: &ccudata.ProfileStore{
			Aliases: map[string]string{
				"OPTICAL_SIGNAL_RECEIVER": "DIMMER_VIRTUAL_RECEIVER",
			},
		},
	}
	dev := device.New(device.Config{Address: "DEV003", InterfaceID: "test", Model: "MyModel"})
	ch := dev.AddChannel("DEV003:1", 1, "OPTICAL_SIGNAL_RECEIVER", hmenum.ParamsetKeyValues)
	got := a.channelTypeOf(dev, ch)
	if got != "DIMMER_VIRTUAL_RECEIVER" {
		t.Errorf("channelTypeOf with alias = %q, want DIMMER_VIRTUAL_RECEIVER", got)
	}
}
