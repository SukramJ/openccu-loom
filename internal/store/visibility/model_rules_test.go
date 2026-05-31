// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// model_rules_test.go covers the ChannelParamsetRules and ModelRules data
// structures, the HiddenParameters / IgnoreParametersByDeviceLower /
// RelevantMasterParamsetsByDevice accessor functions, Registry.ParameterIsHidden,
// Registry.ShouldSkipParameter, ParameterIsHiddenConst, ModelValidator mutators
// (UnIgnoreModel, InvalidatePrefixCache), ParameterDecider.IsRelevantMasterParameter
// and InvalidatePrefixCache, Registry.LoadUnIgnore and InvalidatePrefixCache, and
// nil-safety guards for Apply* helpers.

package visibility

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// HiddenParameters()
// ---------------------------------------------------------------------------

func TestHiddenParametersNotEmpty(t *testing.T) {
	t.Parallel()
	hp := HiddenParameters()
	if len(hp) == 0 {
		t.Error("HiddenParameters() must return a non-empty map")
	}
}

func TestHiddenParametersContainsKnownHidden(t *testing.T) {
	t.Parallel()
	hp := HiddenParameters()
	// UNREACH is a known hidden parameter.
	if _, ok := hp[hmenum.ParameterUnreach]; !ok {
		t.Errorf("HiddenParameters() must contain UNREACH; got %v", hp)
	}
}

func TestHiddenParametersReturnsCopy(t *testing.T) {
	t.Parallel()
	hp1 := HiddenParameters()
	hp2 := HiddenParameters()
	// Mutating the first copy must not affect the second.
	hp1[hmenum.Parameter("FAKE_PARAM")] = struct{}{}
	if _, ok := hp2[hmenum.Parameter("FAKE_PARAM")]; ok {
		t.Error("HiddenParameters() must return independent copies")
	}
}

// ---------------------------------------------------------------------------
// Registry.ParameterIsHidden
// ---------------------------------------------------------------------------

func TestRegistryParameterIsHiddenKnownHidden(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// UNREACH is in hiddenParameters.
	if !r.ParameterIsHidden(hmenum.ParameterUnreach) {
		t.Error("ParameterIsHidden(UNREACH) must return true")
	}
}

func TestRegistryParameterIsHiddenKnownVisible(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// SET_TEMPERATURE is NOT in hiddenParameters.
	if r.ParameterIsHidden(hmenum.ParameterSetTemperature) {
		t.Error("ParameterIsHidden(SET_TEMPERATURE) must return false")
	}
}

// ---------------------------------------------------------------------------
// Registry.ShouldSkipParameter
// ---------------------------------------------------------------------------

func TestRegistryShouldSkipParameterIgnoredValues(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// AES_KEY is in IGNORED_PARAMETERS; should be skipped for VALUES.
	got := r.ShouldSkipParameter("HM-CC-RT-DN", "CLIMATECONTROL_RT_TRANSCEIVER",
		channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.Parameter("AES_KEY"))
	if !got {
		t.Error("ShouldSkipParameter must return true for AES_KEY in VALUES paramset")
	}
}

func TestRegistryShouldSkipParameterAllowed(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// SET_TEMPERATURE is a normal, non-ignored VALUES parameter.
	got := r.ShouldSkipParameter("HM-CC-RT-DN", "CLIMATECONTROL_RT_TRANSCEIVER",
		1, hmenum.ParamsetKeyValues, hmenum.ParameterSetTemperature)
	if got {
		t.Error("ShouldSkipParameter must return false for SET_TEMPERATURE (normal parameter)")
	}
}

func TestRegistryShouldSkipParameterIgnoredModel(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Register an ignored model so any parameter for it returns true.
	r.Model().IgnoreModel("FAKE-DEVICE")
	got := r.ShouldSkipParameter("FAKE-DEVICE", "ANY_CHANNEL",
		0, hmenum.ParamsetKeyValues, hmenum.ParameterSetTemperature)
	if !got {
		t.Error("ShouldSkipParameter must return true for an ignored model")
	}
}

// ---------------------------------------------------------------------------
// ParameterIsHiddenConst
// ---------------------------------------------------------------------------

func TestParameterIsHiddenConstKnownHidden(t *testing.T) {
	t.Parallel()
	if !ParameterIsHiddenConst(hmenum.ParameterUnreach) {
		t.Error("ParameterIsHiddenConst(UNREACH) must return true")
	}
}

func TestParameterIsHiddenConstKnownVisible(t *testing.T) {
	t.Parallel()
	if ParameterIsHiddenConst(hmenum.ParameterSetTemperature) {
		t.Error("ParameterIsHiddenConst(SET_TEMPERATURE) must return false")
	}
}

// ---------------------------------------------------------------------------
// RelevantMasterParamsetsByDevice
// ---------------------------------------------------------------------------

func TestRelevantMasterParamsetsByDeviceNotEmpty(t *testing.T) {
	t.Parallel()
	m := RelevantMasterParamsetsByDevice()
	if len(m) == 0 {
		t.Error("RelevantMasterParamsetsByDevice() must return a non-empty map")
	}
}

func TestRelevantMasterParamsetsByDeviceContainsKnown(t *testing.T) {
	t.Parallel()
	m := RelevantMasterParamsetsByDevice()
	entry, ok := m["HmIP-STH"]
	if !ok {
		t.Fatal("RelevantMasterParamsetsByDevice() must contain HmIP-STH")
	}
	if len(entry.Channels) == 0 {
		t.Error("HmIP-STH entry must have at least one channel")
	}
	if len(entry.Parameters) == 0 {
		t.Error("HmIP-STH entry must have at least one parameter")
	}
}

func TestRelevantMasterParamsetsByDeviceReturnsCopy(t *testing.T) {
	t.Parallel()
	m1 := RelevantMasterParamsetsByDevice()
	m2 := RelevantMasterParamsetsByDevice()
	m1["FAKE-DEVICE"] = ModelMasterEntry{}
	if _, ok := m2["FAKE-DEVICE"]; ok {
		t.Error("RelevantMasterParamsetsByDevice() must return independent copies")
	}
}

// ---------------------------------------------------------------------------
// IgnoreParametersByDeviceLower
// ---------------------------------------------------------------------------

func TestIgnoreParametersByDeviceLowerNotEmpty(t *testing.T) {
	t.Parallel()
	m := IgnoreParametersByDeviceLower()
	if len(m) == 0 {
		t.Error("IgnoreParametersByDeviceLower() must return a non-empty map")
	}
}

func TestIgnoreParametersByDeviceLowerContainsKnown(t *testing.T) {
	t.Parallel()
	m := IgnoreParametersByDeviceLower()
	// "LOWBAT" is in ignoreParametersByDevice.
	models, ok := m["LOWBAT"]
	if !ok {
		t.Fatal("IgnoreParametersByDeviceLower() must contain LOWBAT key")
	}
	if len(models) == 0 {
		t.Error("LOWBAT model set must not be empty")
	}
}

// ---------------------------------------------------------------------------
// ModelValidator — UnIgnoreModel, InvalidatePrefixCache
// ---------------------------------------------------------------------------

func TestModelValidatorUnIgnoreModel(t *testing.T) {
	t.Parallel()
	v := NewModelValidator()
	v.IgnoreModel("HmIP-BROLL")
	if !v.IsModelIgnored("hmip-broll") {
		t.Fatal("model must be ignored after IgnoreModel")
	}
	v.UnIgnoreModel("HmIP-BROLL")
	if v.IsModelIgnored("hmip-broll") {
		t.Fatal("model must not be ignored after UnIgnoreModel")
	}
}

func TestModelValidatorInvalidatePrefixCacheIsNoop(t *testing.T) {
	t.Parallel()
	v := NewModelValidator()
	// Must not panic; result is unchanged.
	v.InvalidatePrefixCache()
	if v.IsModelIgnored("any-model") {
		t.Error("fresh validator must not have any ignored models")
	}
}

// ---------------------------------------------------------------------------
// ParameterDecider — IsRelevantMasterParameter, InvalidatePrefixCache
// ---------------------------------------------------------------------------

func TestParameterDeciderIsRelevantMasterParameter(t *testing.T) {
	t.Parallel()
	// Call with a model not in relevantMasterParamsetsByDevice — should return false.
	if IsRelevantMasterParameter("NOT-A-REAL-MODEL-XYZ", 0, hmenum.ParameterLevel) {
		t.Error("unknown model must return false")
	}
}

func TestParameterDeciderInvalidatePrefixCacheIsNoop(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// Warm cache.
	_ = d.IsParameterIgnored("HmIP-STH", "CLIMATECONTROL_RT_TRANSCEIVER", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterLevel)
	before := d.Len()
	// No-op call must not panic or change cache.
	d.InvalidatePrefixCache()
	if d.Len() != before {
		t.Errorf("InvalidatePrefixCache changed cache len from %d to %d", before, d.Len())
	}
}

// ---------------------------------------------------------------------------
// Registry — LoadUnIgnore, InvalidatePrefixCache
// ---------------------------------------------------------------------------

func TestRegistryLoadUnIgnoreEmptyReader(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	// empty reader should load empty set without error.
	if err := reg.LoadUnIgnore(strings.NewReader("")); err != nil {
		t.Fatalf("LoadUnIgnore(empty) returned error: %v", err)
	}
}

func TestRegistryInvalidatePrefixCacheIsNoop(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	// Warm the decider cache.
	_ = reg.IsAllowed("HmIP-STH", "CLIMATECONTROL_RT_TRANSCEIVER", hmenum.ParamsetKeyValues, hmenum.ParameterLevel)
	before := reg.Len()
	reg.InvalidatePrefixCache()
	if reg.Len() != before {
		t.Errorf("InvalidatePrefixCache changed cache len from %d to %d", before, reg.Len())
	}
}

// ---------------------------------------------------------------------------
// ChannelParamsetRules — all methods
// ---------------------------------------------------------------------------

func TestChannelParamsetRules(t *testing.T) {
	t.Parallel()
	r := NewChannelParamsetRules()

	// Initially empty.
	if r.Contains(0, hmenum.ParamsetKeyValues, "LEVEL") {
		t.Fatal("Contains must return false on empty rules")
	}
	if r.GetParameters(0, hmenum.ParamsetKeyValues) != nil {
		t.Fatal("GetParameters must return nil on empty rules")
	}

	// Add a single entry and query.
	r.Add(0, hmenum.ParamsetKeyValues, "LEVEL")
	if !r.Contains(0, hmenum.ParamsetKeyValues, "LEVEL") {
		t.Fatal("Contains must return true after Add")
	}
	if !r.Contains(0, hmenum.ParamsetKeyValues, "LEVEL") {
		t.Fatal("second Contains call must still return true")
	}

	// GetParameters.
	params := r.GetParameters(0, hmenum.ParamsetKeyValues)
	if len(params) != 1 {
		t.Fatalf("GetParameters len=%d, want 1", len(params))
	}
	if _, ok := params["LEVEL"]; !ok {
		t.Fatal("LEVEL not in GetParameters result")
	}

	// Update adds more.
	r.Update(0, hmenum.ParamsetKeyValues, []string{"STATE", "DIRECTION"})
	params2 := r.GetParameters(0, hmenum.ParamsetKeyValues)
	if len(params2) != 3 {
		t.Fatalf("GetParameters after Update len=%d, want 3", len(params2))
	}

	// Different paramset key remains independent.
	if r.Contains(0, hmenum.ParamsetKeyMaster, "LEVEL") {
		t.Fatal("different paramset key must be independent")
	}
}

// ---------------------------------------------------------------------------
// ModelRules — all methods
// ---------------------------------------------------------------------------

func TestModelRules(t *testing.T) {
	t.Parallel()
	m := NewModelRules()

	// Initially empty.
	if m.Contains("HmIP-STH", 0, hmenum.ParamsetKeyValues, "LEVEL") {
		t.Fatal("Contains must return false on empty ModelRules")
	}
	models := m.GetModels()
	if len(models) != 0 {
		t.Fatalf("GetModels empty: %v", models)
	}

	// AddParameter.
	m.AddParameter("HmIP-STH", 0, hmenum.ParamsetKeyValues, "LEVEL")
	if !m.Contains("HmIP-STH", 0, hmenum.ParamsetKeyValues, "LEVEL") {
		t.Fatal("Contains must return true after AddParameter")
	}
	if m.Contains("OTHER-MODEL", 0, hmenum.ParamsetKeyValues, "LEVEL") {
		t.Fatal("different model must not match")
	}

	// GetModels.
	models = m.GetModels()
	if len(models) != 1 || models[0] != "HmIP-STH" {
		t.Fatalf("GetModels=%v, want [HmIP-STH]", models)
	}

	// AddRelevantChannel.
	m.AddRelevantChannel("HmIP-STH", 1)
	if !m.HasRelevantChannel("HmIP-STH", 1) {
		t.Fatal("HasRelevantChannel must return true after AddRelevantChannel")
	}
	if m.HasRelevantChannel("HmIP-STH", 99) {
		t.Fatal("unreachable channel must not be relevant")
	}
	channels := m.GetRelevantChannels("HmIP-STH")
	if len(channels) != 1 {
		t.Fatalf("GetRelevantChannels len=%d, want 1", len(channels))
	}
	if m.GetRelevantChannels("UNKNOWN") != nil {
		t.Fatal("GetRelevantChannels for unknown model must be nil")
	}

	// UpdateParameters.
	m.UpdateParameters("HmIP-STH", 0, hmenum.ParamsetKeyValues, []string{"STATE", "ERROR"})
	if !m.Contains("HmIP-STH", 0, hmenum.ParamsetKeyValues, "STATE") {
		t.Fatal("STATE must be present after UpdateParameters")
	}

	// New model via UpdateParameters.
	m.UpdateParameters("HmIP-PSM", 1, hmenum.ParamsetKeyMaster, []string{"OVERLOAD"})
	if !m.Contains("HmIP-PSM", 1, hmenum.ParamsetKeyMaster, "OVERLOAD") {
		t.Fatal("new model OVERLOAD must be present")
	}
}

// ---------------------------------------------------------------------------
// Nil-safety guards for Apply* helpers
// ---------------------------------------------------------------------------

func TestApplyChannelOperationModeGatingDeviceNilIsSafe(t *testing.T) {
	t.Parallel()
	// Must not panic.
	ApplyChannelOperationModeGatingDevice(nil)
}

func TestApplyUnIgnoredMarksNilIsSafe(t *testing.T) {
	t.Parallel()
	// Both nil guards must not panic.
	ApplyUnIgnoredMarks(nil, nil)
}

func TestApplyForceSensorMarksNilIsSafe(t *testing.T) {
	t.Parallel()
	// nil device must not panic.
	ApplyForceSensorMarks(nil)
}
