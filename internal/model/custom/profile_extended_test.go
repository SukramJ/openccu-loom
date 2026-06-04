// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// =====================================================================
// ExtendedDeviceConfig.RequiredParameters tests
// =====================================================================

// TestExtendedRequiredParametersFromFixedFields verifies that all Parameter
// values referenced inside FixedChannelFields are returned.
func TestExtendedRequiredParametersFromFixedFields(t *testing.T) {
	t.Parallel()

	e := &ExtendedDeviceConfig{
		FixedChannelFields: map[int]map[hmenum.Field]hmenum.Parameter{
			1: {
				hmenum.FieldActiveProfile: hmenum.ParameterActiveProfile,
				hmenum.FieldAutoMode:      hmenum.ParameterAutoMode,
			},
			2: {
				hmenum.FieldBoostMode: hmenum.ParameterBoostMode,
			},
		},
	}

	got := e.RequiredParameters()

	want := []hmenum.Parameter{
		hmenum.ParameterActiveProfile,
		hmenum.ParameterAutoMode,
		hmenum.ParameterBoostMode,
	}
	slices.Sort(want)

	if len(got) != len(want) {
		t.Fatalf("RequiredParameters() len=%d, want %d; got %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtendedRequiredParametersFromAdditional verifies that all Parameter
// values referenced inside AdditionalDataPoints are returned.
func TestExtendedRequiredParametersFromAdditional(t *testing.T) {
	t.Parallel()

	e := &ExtendedDeviceConfig{
		AdditionalDataPoints: map[int][]hmenum.Parameter{
			3: {hmenum.ParameterActualHumidity, hmenum.ParameterActualTemperature},
			4: {hmenum.ParameterActivityState},
		},
	}

	got := e.RequiredParameters()

	want := []hmenum.Parameter{
		hmenum.ParameterActualHumidity,
		hmenum.ParameterActualTemperature,
		hmenum.ParameterActivityState,
	}
	slices.Sort(want)

	if len(got) != len(want) {
		t.Fatalf("RequiredParameters() len=%d, want %d; got %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtendedRequiredParametersDeduplicate verifies that a Parameter
// appearing in both FixedChannelFields and AdditionalDataPoints (or in
// multiple channels of either map) appears exactly once in the output.
func TestExtendedRequiredParametersDeduplicate(t *testing.T) {
	t.Parallel()

	// ParameterAutoMode appears in both sources; ParameterActiveProfile in two channels.
	e := &ExtendedDeviceConfig{
		FixedChannelFields: map[int]map[hmenum.Field]hmenum.Parameter{
			1: {
				hmenum.FieldAutoMode:      hmenum.ParameterAutoMode,
				hmenum.FieldActiveProfile: hmenum.ParameterActiveProfile,
			},
			2: {
				hmenum.FieldActiveProfile: hmenum.ParameterActiveProfile, // duplicate channel
			},
		},
		AdditionalDataPoints: map[int][]hmenum.Parameter{
			3: {hmenum.ParameterAutoMode}, // duplicate from FixedChannelFields
		},
	}

	got := e.RequiredParameters()

	// Expect exactly two distinct parameters.
	if len(got) != 2 {
		t.Fatalf("expected 2 unique parameters, got %d: %v", len(got), got)
	}

	seen := make(map[hmenum.Parameter]int)
	for _, p := range got {
		seen[p]++
	}
	for p, count := range seen {
		if count != 1 {
			t.Errorf("parameter %q appears %d times, want 1", p, count)
		}
	}
}

// TestExtendedRequiredParametersEmpty verifies that a nil ExtendedDeviceConfig
// and a zero-value ExtendedDeviceConfig both return a non-nil empty slice.
func TestExtendedRequiredParametersEmpty(t *testing.T) {
	t.Parallel()

	t.Run("nil receiver", func(t *testing.T) {
		t.Parallel()
		var e *ExtendedDeviceConfig
		got := e.RequiredParameters()
		if got == nil {
			t.Error("nil receiver: expected non-nil empty slice, got nil")
		}
		if len(got) != 0 {
			t.Errorf("nil receiver: expected empty slice, got %v", got)
		}
	})

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()
		e := &ExtendedDeviceConfig{}
		got := e.RequiredParameters()
		if got == nil {
			t.Error("zero value: expected non-nil empty slice, got nil")
		}
		if len(got) != 0 {
			t.Errorf("zero value: expected empty slice, got %v", got)
		}
	})

	t.Run("nil maps", func(t *testing.T) {
		t.Parallel()
		e := &ExtendedDeviceConfig{
			FixedChannelFields:   nil,
			AdditionalDataPoints: nil,
		}
		got := e.RequiredParameters()
		if len(got) != 0 {
			t.Errorf("nil maps: expected empty slice, got %v", got)
		}
	})
}

// TestExtendedRequiredParametersStableOrder verifies that repeated calls to
// RequiredParameters() always return the same sorted order.
func TestExtendedRequiredParametersStableOrder(t *testing.T) {
	t.Parallel()

	e := &ExtendedDeviceConfig{
		FixedChannelFields: map[int]map[hmenum.Field]hmenum.Parameter{
			1: {
				hmenum.FieldBoostMode:     hmenum.ParameterBoostMode,
				hmenum.FieldAutoMode:      hmenum.ParameterAutoMode,
				hmenum.FieldActiveProfile: hmenum.ParameterActiveProfile,
			},
		},
		AdditionalDataPoints: map[int][]hmenum.Parameter{
			2: {hmenum.ParameterActualHumidity, hmenum.ParameterActualTemperature},
		},
	}

	first := e.RequiredParameters()

	const iterations = 20
	for i := range iterations {
		got := e.RequiredParameters()
		if len(got) != len(first) {
			t.Fatalf("iteration %d: len=%d, want %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Errorf("iteration %d: got[%d]=%q, want %q", i, j, got[j], first[j])
			}
		}
	}
}

// TestProfileOptionalFieldsBackwardCompat ensures that existing Profile
// literals that do not set ScheduleChannelNo, Extended, or Config
// compile and behave correctly (zero-value check).
func TestProfileOptionalFieldsBackwardCompat(t *testing.T) {
	t.Parallel()

	p := Profile{
		Name:         "IPSwitch",
		DeviceType:   "HmIP-PS",
		ProductGroup: hmenum.ProductGroupHmIP,
		Category:     hmenum.DataPointCategorySwitch,
		Channels:     []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
	}

	if p.ScheduleChannelNo != nil {
		t.Errorf("ScheduleChannelNo should be nil, got %v", p.ScheduleChannelNo)
	}
	if p.Extended != nil {
		t.Errorf("Extended should be nil, got %v", p.Extended)
	}
	if p.Config != nil {
		t.Errorf("Config should be nil, got %v", p.Config)
	}
}

// TestProfileScheduleChannelNoSet verifies the optional ScheduleChannelNo
// pointer field can be set and read back correctly.
func TestProfileScheduleChannelNoSet(t *testing.T) {
	t.Parallel()

	ch := 1
	p := Profile{
		Name:              "IPThermostat",
		DeviceType:        "HmIP-BWTH",
		ProductGroup:      hmenum.ProductGroupHmIP,
		Category:          hmenum.DataPointCategoryClimate,
		ScheduleChannelNo: &ch,
	}

	if p.ScheduleChannelNo == nil {
		t.Fatal("ScheduleChannelNo should not be nil")
	}
	if *p.ScheduleChannelNo != 1 {
		t.Errorf("*ScheduleChannelNo=%d, want 1", *p.ScheduleChannelNo)
	}
}

// TestProfileExtendedFieldWired verifies that a Profile with Extended set
// correctly exposes RequiredParameters via the embedded struct.
func TestProfileExtendedFieldWired(t *testing.T) {
	t.Parallel()

	p := Profile{
		Name:         "IPCover",
		DeviceType:   "HmIP-BROLL",
		ProductGroup: hmenum.ProductGroupHmIP,
		Category:     hmenum.DataPointCategoryCover,
		Extended: &ExtendedDeviceConfig{
			FixedChannelFields: map[int]map[hmenum.Field]hmenum.Parameter{
				4: {hmenum.FieldButtonLock: hmenum.ParameterButtonLock},
			},
		},
	}

	if p.Extended == nil {
		t.Fatal("Extended should not be nil")
	}

	params := p.Extended.RequiredParameters()
	if len(params) != 1 || params[0] != hmenum.ParameterButtonLock {
		t.Errorf("RequiredParameters()=%v, want [BUTTON_LOCK]", params)
	}
}
