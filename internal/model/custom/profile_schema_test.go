// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom

import (
	"reflect"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestBareHelper(t *testing.T) {
	t.Parallel()

	fv := Bare(hmenum.ParameterLevel)
	if fv.Parameter != hmenum.ParameterLevel {
		t.Fatalf("Bare: expected Parameter=%q, got %q", hmenum.ParameterLevel, fv.Parameter)
	}
	if fv.Mapping != nil {
		t.Fatalf("Bare: expected nil Mapping, got %+v", fv.Mapping)
	}
}

func TestVisibleHelper(t *testing.T) {
	t.Parallel()

	fv := Visible(hmenum.ParameterColor)
	if fv.Parameter != hmenum.ParameterColor {
		t.Fatalf("Visible: expected Parameter=%q, got %q", hmenum.ParameterColor, fv.Parameter)
	}
	if fv.Mapping == nil {
		t.Fatal("Visible: expected non-nil Mapping")
	}
	if fv.Mapping.Parameter != hmenum.ParameterColor {
		t.Fatalf("Visible: Mapping.Parameter = %q, want %q", fv.Mapping.Parameter, hmenum.ParameterColor)
	}
	if fv.Mapping.IsVisible == nil || *fv.Mapping.IsVisible != true {
		t.Fatalf("Visible: expected IsVisible=*true, got %+v", fv.Mapping.IsVisible)
	}
}

func TestHiddenHelper(t *testing.T) {
	t.Parallel()

	fv := Hidden(hmenum.ParameterState)
	if fv.Parameter != hmenum.ParameterState {
		t.Fatalf("Hidden: expected Parameter=%q, got %q", hmenum.ParameterState, fv.Parameter)
	}
	if fv.Mapping == nil {
		t.Fatal("Hidden: expected non-nil Mapping")
	}
	if fv.Mapping.IsVisible == nil || *fv.Mapping.IsVisible != false {
		t.Fatalf("Hidden: expected IsVisible=*false, got %+v", fv.Mapping.IsVisible)
	}
}

func TestResolveFieldValueBare(t *testing.T) {
	t.Parallel()

	param, vis := ResolveFieldValue(Bare(hmenum.ParameterLevel))
	if param != hmenum.ParameterLevel {
		t.Fatalf("ResolveFieldValue(Bare): parameter=%q, want %q", param, hmenum.ParameterLevel)
	}
	if vis != nil {
		t.Fatalf("ResolveFieldValue(Bare): expected nil visibility, got %+v", vis)
	}
}

func TestResolveFieldValueVisible(t *testing.T) {
	t.Parallel()

	param, vis := ResolveFieldValue(Visible(hmenum.ParameterColor))
	if param != hmenum.ParameterColor {
		t.Fatalf("ResolveFieldValue(Visible): parameter=%q, want %q", param, hmenum.ParameterColor)
	}
	if vis == nil || *vis != true {
		t.Fatalf("ResolveFieldValue(Visible): expected *true, got %+v", vis)
	}
}

func TestResolveFieldValueHidden(t *testing.T) {
	t.Parallel()

	param, vis := ResolveFieldValue(Hidden(hmenum.ParameterState))
	if param != hmenum.ParameterState {
		t.Fatalf("ResolveFieldValue(Hidden): parameter=%q, want %q", param, hmenum.ParameterState)
	}
	if vis == nil || *vis != false {
		t.Fatalf("ResolveFieldValue(Hidden): expected *false, got %+v", vis)
	}
}

func TestRebaseChannelGroupBasic(t *testing.T) {
	t.Parallel()

	cfg := ProfileConfig{
		ProfileType: hmenum.DeviceProfile("IPSwitch"),
		ChannelGroup: ChannelGroupConfig{
			PrimaryChannel:    0,
			PrimaryChannelSet: true,
			SecondaryChannels: []int{1, 2},
			Fields: map[hmenum.Field]FieldValue{
				hmenum.FieldState: Bare(hmenum.ParameterState),
			},
		},
	}

	got := RebaseChannelGroup(cfg, 0)

	if got.PrimaryChannel == nil || *got.PrimaryChannel != 0 {
		t.Fatalf("PrimaryChannel: want *0, got %+v", got.PrimaryChannel)
	}
	if !reflect.DeepEqual(got.SecondaryChannels, []int{1, 2}) {
		t.Fatalf("SecondaryChannels: got %v, want [1 2]", got.SecondaryChannels)
	}
	if got.StateChannel != nil {
		t.Fatalf("StateChannel: want nil, got %+v", got.StateChannel)
	}
	if !reflect.DeepEqual(got.Fields, cfg.ChannelGroup.Fields) {
		t.Fatalf("Fields: got %v, want %v", got.Fields, cfg.ChannelGroup.Fields)
	}
}

func TestRebaseChannelGroupOffset5(t *testing.T) {
	t.Parallel()

	stateOff := -1
	cfg := ProfileConfig{
		ChannelGroup: ChannelGroupConfig{
			PrimaryChannel:     1,
			PrimaryChannelSet:  true,
			SecondaryChannels:  []int{2, 3},
			StateChannelOffset: &stateOff,
			ChannelFields: map[int]map[hmenum.Field]FieldValue{
				1: {hmenum.FieldColor: Bare(hmenum.ParameterColor)},
				2: {hmenum.FieldLevel: Visible(hmenum.ParameterLevel)},
			},
		},
	}

	got := RebaseChannelGroup(cfg, 5)

	if got.PrimaryChannel == nil || *got.PrimaryChannel != 6 {
		t.Fatalf("PrimaryChannel: want *6, got %+v", got.PrimaryChannel)
	}
	if !reflect.DeepEqual(got.SecondaryChannels, []int{7, 8}) {
		t.Fatalf("SecondaryChannels: got %v, want [7 8]", got.SecondaryChannels)
	}
	if got.StateChannel == nil || *got.StateChannel != 4 {
		t.Fatalf("StateChannel: want *4, got %+v", got.StateChannel)
	}
	if _, ok := got.ChannelFields[6]; !ok {
		t.Fatalf("ChannelFields: expected key 6 (1+5), got keys %v", keysOf(got.ChannelFields))
	}
	if _, ok := got.ChannelFields[7]; !ok {
		t.Fatalf("ChannelFields: expected key 7 (2+5), got keys %v", keysOf(got.ChannelFields))
	}
}

func TestRebaseChannelGroupNilStateChannel(t *testing.T) {
	t.Parallel()

	cfg := ProfileConfig{
		ChannelGroup: ChannelGroupConfig{
			PrimaryChannel:    0,
			PrimaryChannelSet: true,
			// StateChannelOffset deliberately nil.
		},
	}

	got := RebaseChannelGroup(cfg, 7)
	if got.StateChannel != nil {
		t.Fatalf("StateChannel: want nil with no source offset, got %+v", got.StateChannel)
	}
}

func TestRebaseChannelGroupPrimaryChannelUnset(t *testing.T) {
	t.Parallel()

	cfg := ProfileConfig{
		ChannelGroup: ChannelGroupConfig{
			// PrimaryChannelSet is false → "no primary channel" (Python None).
			SecondaryChannels: []int{1, 2},
		},
	}

	got := RebaseChannelGroup(cfg, 4)
	if got.PrimaryChannel != nil {
		t.Fatalf("PrimaryChannel: expected nil when PrimaryChannelSet=false, got %+v", got.PrimaryChannel)
	}
	if !reflect.DeepEqual(got.SecondaryChannels, []int{5, 6}) {
		t.Fatalf("SecondaryChannels: got %v, want [5 6]", got.SecondaryChannels)
	}
}

func TestRebaseChannelGroupFixedFieldsUnchanged(t *testing.T) {
	t.Parallel()

	fixed := map[int]map[hmenum.Field]FieldValue{
		0: {hmenum.FieldBurstLimitWarning: Visible(hmenum.ParameterBurstLimitWarning)},
	}
	cfg := ProfileConfig{
		ChannelGroup: ChannelGroupConfig{
			PrimaryChannel:     0,
			PrimaryChannelSet:  true,
			FixedChannelFields: fixed,
		},
	}

	got := RebaseChannelGroup(cfg, 99)

	if !reflect.DeepEqual(got.FixedChannelFields, fixed) {
		t.Fatalf("FixedChannelFields: got %v, want %v (must not be rebased)", got.FixedChannelFields, fixed)
	}
}

func TestRebaseChannelGroupChannelFieldsRebased(t *testing.T) {
	t.Parallel()

	cfg := ProfileConfig{
		ChannelGroup: ChannelGroupConfig{
			PrimaryChannel:    0,
			PrimaryChannelSet: true,
			ChannelFields: map[int]map[hmenum.Field]FieldValue{
				0: {hmenum.FieldLevel: Bare(hmenum.ParameterLevel)},
				3: {hmenum.FieldState: Bare(hmenum.ParameterState)},
			},
		},
	}

	got := RebaseChannelGroup(cfg, 10)

	wantKeys := map[int]bool{10: true, 13: true}
	gotKeys := map[int]bool{}
	for k := range got.ChannelFields {
		gotKeys[k] = true
	}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("ChannelFields keys: got %v, want %v", gotKeys, wantKeys)
	}

	// Inner fields-map identity preserved (no defensive deep copy).
	if !reflect.DeepEqual(got.ChannelFields[10], cfg.ChannelGroup.ChannelFields[0]) {
		t.Fatalf("inner field map at offset 0 not preserved through rebase")
	}
}

func TestRebaseChannelGroupAnyChannelKey(t *testing.T) {
	t.Parallel()

	innerFields := map[hmenum.Field]FieldValue{
		hmenum.FieldTemperatureMaximum: Bare(hmenum.ParameterTemperatureMaximum),
	}
	cfg := ProfileConfig{
		ChannelGroup: ChannelGroupConfig{
			PrimaryChannel:    0,
			PrimaryChannelSet: true,
			ChannelFields: map[int]map[hmenum.Field]FieldValue{
				AnyChannelOffset: innerFields,
				0:                {hmenum.FieldValveState: Visible(hmenum.ParameterValveState)},
			},
		},
	}

	got := RebaseChannelGroup(cfg, 12)

	// AnyChannelOffset entry must survive verbatim — *not* rebased to
	// AnyChannelOffset+12.
	gotInner, ok := got.ChannelFields[AnyChannelOffset]
	if !ok {
		t.Fatalf("ChannelFields: AnyChannelOffset key missing after rebase; keys=%v", keysOf(got.ChannelFields))
	}
	if !reflect.DeepEqual(gotInner, innerFields) {
		t.Fatalf("AnyChannelOffset inner map mutated: got %v, want %v", gotInner, innerFields)
	}

	// And a sanity check: we did not accidentally also create the
	// rebased phantom key.
	if _, accidentally := got.ChannelFields[AnyChannelOffset+12]; accidentally {
		t.Fatalf("ChannelFields: AnyChannelOffset key should not be rebased; got phantom key %d", AnyChannelOffset+12)
	}

	// And the regular key 0 must rebase to 12.
	if _, ok := got.ChannelFields[12]; !ok {
		t.Fatalf("ChannelFields: expected rebased key 12, got keys %v", keysOf(got.ChannelFields))
	}
}

func TestProfileConfigDefaults(t *testing.T) {
	t.Parallel()

	const wantType hmenum.DeviceProfile = "IPSwitch"
	cfg := NewProfileConfig(wantType, ChannelGroupConfig{
		PrimaryChannel:    0,
		PrimaryChannelSet: true,
	})

	if !cfg.IncludeDefaultDataPoints {
		t.Fatal("NewProfileConfig: IncludeDefaultDataPoints should default to true")
	}
	if cfg.ProfileType != wantType {
		t.Fatalf("NewProfileConfig: ProfileType=%q, want %q", cfg.ProfileType, wantType)
	}
	if !cfg.ChannelGroup.PrimaryChannelSet {
		t.Fatal("NewProfileConfig: ChannelGroup must round-trip PrimaryChannelSet")
	}
}

func TestRebaseChannelGroupNilChannelFields(t *testing.T) {
	t.Parallel()

	cfg := ProfileConfig{
		ChannelGroup: ChannelGroupConfig{
			PrimaryChannel:    0,
			PrimaryChannelSet: true,
			// ChannelFields nil
		},
	}

	got := RebaseChannelGroup(cfg, 3)
	if got.ChannelFields != nil {
		t.Fatalf("ChannelFields: want nil when source is nil, got %v", got.ChannelFields)
	}
}

// keysOf returns a slice of keys for diagnostic output. Order is not stable.
func keysOf(m map[int]map[hmenum.Field]FieldValue) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
