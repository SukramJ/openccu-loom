// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// custom_profile_constructor_resolves_test.go closes three gaps in the
// device-profile parity coverage:
//
//  1. TestEveryRegisteredProfileHasConstructor asserts that every profile
//     the registry maps to at least one CCU device model resolves to a
//     registered custom.Constructor — the silent-skip branch in
//     custom.CreateCustomDataPoint ("the constructor for the profile is not
//     registered (skip + log)") is supposed to be unreachable for any
//     profile the generator emits a device mapping for. Without this guard
//     a generator run that adds a DeviceProfile without the matching
//     hand-written Go wrapper (the "Add a new device type" workflow in
//     CLAUDE.md) compiles and passes every other contract test while the
//     daemon silently creates zero custom data points for it at runtime.
//  2. TestRealDeviceModelsMaterializeCustomDataPoint drives the real,
//     unmodified custom.CreateCustomDataPoints pipeline against a handful
//     of well-known CCU models and asserts the profile's primary channel
//     ends up with a non-nil CustomDataPoint — catching a constructor that
//     is registered but returns (nil, nil) once actually invoked, which
//     TestEveryRegisteredProfileHasConstructor cannot see.
//  3. TestProfileRegistryCountsMatchAiohomematicSource and
//     TestProfileFieldMappingsMatchAiohomematicSource pin the generated
//     profile catalogue against values independently derived from the
//     reference implementation's own source (not read back from
//     internal/model/custom/generated_profiles.go's own constants, which is
//     what the existing profile_parity_generated_test.go compares against —
//     a self-referential check that cannot catch a generator bug that
//     miscounts identically on both sides of the same run).
package contract

import (
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/light"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/textdisplay"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/valve"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestEveryRegisteredProfileHasConstructor walks every profile the
// process-wide registry maps to at least one CCU device model and asserts a
// [custom.Constructor] is registered for its name.
func TestEveryRegisteredProfileHasConstructor(t *testing.T) {
	registry := custom.DefaultRegistry()

	// One witnessing device-type string per distinct profile name, so a
	// failure message points at a concrete model to investigate.
	witness := make(map[hmenum.DeviceProfile]string)
	for _, model := range registry.DeviceTypes() {
		for _, profile := range registry.ForDevice(model) {
			if _, ok := witness[profile.Name]; !ok {
				witness[profile.Name] = model
			}
		}
	}
	if len(witness) == 0 {
		t.Fatal("registry has no device-mapped profiles at all — DefaultRegistry population is broken")
	}

	for name, model := range witness {
		if _, ok := registry.Constructor(name); !ok {
			t.Errorf("profile %q (e.g. mapped from device type %q) has no registered Constructor — "+
				"custom.CreateCustomDataPoint will silently skip every device carrying it; add a "+
				"hand-written Go wrapper + MustRegisterConstructor call (see CLAUDE.md 'Add a new "+
				"device type')", name, model)
		}
	}
}

// TestRealDeviceModelsMaterializeCustomDataPoint drives
// custom.CreateCustomDataPoints — the exact function the daemon calls at
// device-hydration time — against a handful of well-known CCU models and
// asserts the profile's primary channel receives a non-nil custom data
// point. This is a stronger guard than
// TestEveryRegisteredProfileHasConstructor for profiles whose constructor
// can legitimately return (nil, nil): e.g.
// internal/model/custom/switch/switch.go's New() returns nil when the
// channel carries no STATE data point, and ipSwitchConstructor forwards
// that nil straight through — a constructor registered against the wrong
// parameter would still pass the first test but fail this one.
func TestRealDeviceModelsMaterializeCustomDataPoint(t *testing.T) {
	registry := custom.DefaultRegistry()

	cases := []struct {
		// name identifies the case; model is the real CCU TYPE string the
		// profile is registered against in generated_profiles.go.
		name     string
		model    string
		category hmenum.DataPointCategory
		// wire attaches the minimal set of generic data points the
		// constructor needs to produce a non-nil result.
		wire func(ch *device.Channel)
	}{
		{
			name:     "IPSwitch",
			model:    "hmip-bs2",
			category: hmenum.DataPointCategorySwitch,
			wire: func(ch *device.Channel) {
				ch.Put(generic.NewSwitch(generic.Spec{
					Key: hmtypes.DataPointKey{
						ChannelAddress: ch.Address,
						ParamsetKey:    hmenum.ParamsetKeyValues,
						Parameter:      string(hmenum.ParameterState),
					},
					Descriptor: hmproto.ParameterData{
						Type:       hmenum.ParameterTypeBool,
						Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
					},
				}))
			},
		},
		{
			name:     "IPCover",
			model:    "hmip-broll",
			category: hmenum.DataPointCategoryCover,
			wire: func(ch *device.Channel) {
				ch.Put(generic.NewFloat(generic.Spec{
					Key: hmtypes.DataPointKey{
						ChannelAddress: ch.Address,
						ParamsetKey:    hmenum.ParamsetKeyValues,
						Parameter:      string(hmenum.ParameterLevel),
					},
					Descriptor: hmproto.ParameterData{
						Type:       hmenum.ParameterTypeFloat,
						Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
					},
				}))
			},
		},
		{
			name:     "IPThermostat",
			model:    "hmip-bwth",
			category: hmenum.DataPointCategoryClimate,
			wire: func(ch *device.Channel) {
				ch.Put(generic.NewFloat(generic.Spec{
					Key: hmtypes.DataPointKey{
						ChannelAddress: ch.Address,
						ParamsetKey:    hmenum.ParamsetKeyValues,
						Parameter:      string(hmenum.ParameterSetPointTemperature),
					},
					Descriptor: hmproto.ParameterData{
						Type:       hmenum.ParameterTypeFloat,
						Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
					},
				}))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile, err := registry.Get(tc.category, tc.model)
			if err != nil {
				t.Fatalf("registry.Get(%v, %q): %v — has generated_profiles.go dropped this device mapping?", tc.category, tc.model, err)
			}
			if len(profile.Channels) == 0 || profile.Config == nil {
				t.Fatalf("profile %q for %q has no channel/config to materialise against", profile.Name, tc.model)
			}

			// Absolute primary-channel number: base channel + the
			// profile's relative PrimaryChannel offset. Mirrors
			// addChannelGroupsToDevice's groupNo computation
			// (internal/model/custom/materialize.go).
			primary := profile.Channels[0].Channel + profile.Config.ChannelGroup.PrimaryChannel

			dev := device.New(device.Config{
				InterfaceID: "HmIP-RF",
				Interface:   hmenum.InterfaceHmIPRF,
				Address:     "TESTDEV",
				Model:       tc.model,
			})
			addr := "TESTDEV:" + strconv.Itoa(primary)
			ch := dev.AddChannel(addr, primary, "T", hmenum.ParamsetKeyValues)
			tc.wire(ch)

			if err := custom.CreateCustomDataPoints(dev, registry); err != nil {
				t.Fatalf("CreateCustomDataPoints: %v", err)
			}
			if ch.CustomDataPoint() == nil {
				t.Fatalf("channel %s carries no custom data point after materialise — profile %q's "+
					"constructor silently no-opped", addr, profile.Name)
			}
		})
	}
}

// TestProfileRegistryCountsMatchAiohomematicSource pins the three generated
// profile-catalogue counts against values independently counted from the
// reference implementation's own registry, not read back from
// internal/model/custom/generated_profiles.go's own constants (that
// self-comparison is what profile_parity_generated_test.go already does,
// and it cannot detect a generator bug that mis-counts identically on both
// sides of the same run).
//
// Independently derived by running, against a checkout of the reference
// implementation (module path elided; see script/generate_profiles.py for
// the exact import root):
//
//	import model.custom  # noqa: F401 (triggers registrations)
//	from model.custom.registry import DeviceProfileRegistry
//	from model.custom.profile import DEFAULT_DATA_POINTS, PROFILE_CONFIGS
//	sum(len(v) if isinstance(v, tuple) else 1
//	    for m in DeviceProfileRegistry._configs.values() for v in m.values())  # profile entry count
//	len(PROFILE_CONFIGS)      # profile-config catalogue size
//	len(DEFAULT_DATA_POINTS)  # default-data-point channel-offset count
//
// Re-run the snippet above against the pinned reference-implementation
// version whenever intentionally regenerating, and update the three
// constants below together with script/generate_profiles.py's output — a
// drift here means the generator's count and the source's count disagree,
// which the generated file's own self-referential constant cannot reveal.
func TestProfileRegistryCountsMatchAiohomematicSource(t *testing.T) {
	// Counted against reference-implementation 2026.8.3, the version the
	// checked-in tables were generated from.
	const (
		wantProfileCount          = 142
		wantProfileConfigCount    = 33
		wantDefaultDataPointCount = 3
	)

	if got := custom.DefaultRegistry().Len(); got != wantProfileCount {
		t.Errorf("registry has %d profiles; aiohomematic DeviceProfileRegistry independently counts %d", got, wantProfileCount)
	}
	if got := len(custom.ProfileConfigs); got != wantProfileConfigCount {
		t.Errorf("ProfileConfigs has %d entries; aiohomematic PROFILE_CONFIGS independently counts %d", got, wantProfileConfigCount)
	}
	if got := len(custom.DefaultDataPoints); got != wantDefaultDataPointCount {
		t.Errorf("DefaultDataPoints has %d entries; aiohomematic DEFAULT_DATA_POINTS independently counts %d", got, wantDefaultDataPointCount)
	}
}

// fieldValueRef is the independently-authored expected shape of a
// [custom.FieldValue]: the wire parameter plus the optional visibility
// forcing decision, mirroring [custom.ResolveFieldValue]'s return shape.
type fieldValueRef struct {
	parameter hmenum.Parameter
	isVisible *bool
}

func bareRef(p hmenum.Parameter) fieldValueRef { return fieldValueRef{parameter: p} }

func visibleRef(p hmenum.Parameter) fieldValueRef {
	v := true
	return fieldValueRef{parameter: p, isVisible: &v}
}

// assertFieldsMatchReference compares a ProfileConfig's field map against an
// independently-authored reference, field-by-field (both the wire parameter
// and the visibility-forcing decision), erroring on any key present in only
// one side or a parameter/visibility mismatch on a shared key.
func assertFieldsMatchReference(t *testing.T, label string, got map[hmenum.Field]custom.FieldValue, want map[hmenum.Field]fieldValueRef) {
	t.Helper()
	for field, wantFV := range want {
		gotFV, ok := got[field]
		if !ok {
			t.Errorf("%s: missing field %q (aiohomematic reference maps it to %q)", label, field, wantFV.parameter)
			continue
		}
		gotParam, gotVisible := custom.ResolveFieldValue(gotFV)
		if gotParam != wantFV.parameter {
			t.Errorf("%s: field %q parameter = %q, want %q", label, field, gotParam, wantFV.parameter)
		}
		if boolPtrValue(gotVisible) != boolPtrValue(wantFV.isVisible) {
			t.Errorf("%s: field %q visibility = %v, want %v", label, field, boolPtrValue(gotVisible), boolPtrValue(wantFV.isVisible))
		}
	}
	for field := range got {
		if _, ok := want[field]; !ok {
			t.Errorf("%s: field %q is not in the aiohomematic reference composition", label, field)
		}
	}
}

// boolPtrValue renders a *bool for comparison/printing: nil means "no
// forcing", matching [custom.ResolveFieldValue]'s nil-is-bare convention.
func boolPtrValue(b *bool) string {
	if b == nil {
		return "unforced"
	}
	if *b {
		return "visible"
	}
	return "hidden"
}

// TestProfileFieldMappingsMatchAiohomematicSource pins the wrapped
// generic-DP composition (the Field→Parameter mapping, plus visibility
// forcing) of four representative profiles — spanning cover, switch, lock
// and climate — against literals hand-derived directly from the reference
// implementation's model/custom/profile.py PROFILE_CONFIGS
// entries (IP_COVER_CONFIG, IP_SWITCH_CONFIG, IP_LOCK_CONFIG,
// IP_THERMOSTAT_CONFIG). The wrapped-DP set was previously only exercised
// by the cross-stack model-snapshot pipeline, which tolerates it as an
// unchecked field (notes/parity/by_design.md); this test cross-checks the
// composition directly, at contract-test speed, without needing a live CCU
// or the model-snapshot pipeline's ~70 MB fixtures.
func TestProfileFieldMappingsMatchAiohomematicSource(t *testing.T) {
	registry := custom.DefaultRegistry()

	cfg := func(name hmenum.DeviceProfile) custom.ChannelGroupConfig {
		t.Helper()
		pc, ok := custom.ProfileConfigs[name]
		if !ok || pc == nil {
			t.Fatalf("no ProfileConfig registered for %q", name)
		}
		return pc.ChannelGroup
	}
	_ = registry // registry itself is exercised by the other tests in this file

	// IPCover — reference implementation model/custom/profile.py:297 IP_COVER_CONFIG.
	coverCG := cfg(hmenum.DeviceProfile("IPCover"))
	assertFieldsMatchReference(t, "IPCover.Fields", coverCG.Fields, map[hmenum.Field]fieldValueRef{
		hmenum.FieldCombinedParameter: bareRef(hmenum.ParameterCombinedParameter),
		hmenum.FieldLevel:             bareRef(hmenum.ParameterLevel),
		hmenum.FieldLevel2:            bareRef(hmenum.ParameterLevel2),
		hmenum.FieldStop:              bareRef(hmenum.ParameterStop),
	})
	if state := coverCG.ChannelFields[-1]; state == nil {
		t.Error("IPCover.ChannelFields[STATE offset] is missing")
	} else {
		assertFieldsMatchReference(t, "IPCover.ChannelFields[STATE]", state, map[hmenum.Field]fieldValueRef{
			hmenum.FieldDirection:     bareRef(hmenum.ParameterActivityState),
			hmenum.FieldOperationMode: bareRef(hmenum.ParameterChannelOperationMode),
			hmenum.FieldGroupLevel:    visibleRef(hmenum.ParameterLevel),
			hmenum.FieldGroupLevel2:   visibleRef(hmenum.ParameterLevel2),
		})
	}

	// IPSwitch — reference implementation model/custom/profile.py:545 IP_SWITCH_CONFIG.
	switchCG := cfg(hmenum.DeviceProfile("IPSwitch"))
	assertFieldsMatchReference(t, "IPSwitch.Fields", switchCG.Fields, map[hmenum.Field]fieldValueRef{
		hmenum.FieldState:       bareRef(hmenum.ParameterState),
		hmenum.FieldOnTimeValue: bareRef(hmenum.ParameterOnTime),
	})
	if state := switchCG.ChannelFields[-1]; state == nil {
		t.Error("IPSwitch.ChannelFields[STATE offset] is missing")
	} else {
		assertFieldsMatchReference(t, "IPSwitch.ChannelFields[STATE]", state, map[hmenum.Field]fieldValueRef{
			hmenum.FieldGroupState: visibleRef(hmenum.ParameterState),
		})
	}
	wantSwitchAdditional := []hmenum.Parameter{
		hmenum.ParameterCurrent,
		hmenum.ParameterEnergyCounter,
		hmenum.ParameterEnergyCounterFeedIn,
		hmenum.ParameterFrequency,
		hmenum.ParameterPower,
		hmenum.ParameterActualTemperature,
		hmenum.ParameterVoltage,
	}
	switchPC := custom.ProfileConfigs[hmenum.DeviceProfile("IPSwitch")]
	if got := switchPC.AdditionalDataPoints[3]; !parameterSlicesEqualAsSets(got, wantSwitchAdditional) {
		t.Errorf("IPSwitch.AdditionalDataPoints[3] = %v, want %v (aiohomematic profile.py:560)", got, wantSwitchAdditional)
	}

	// IPLock — reference implementation model/custom/profile.py:618 IP_LOCK_CONFIG.
	lockCG := cfg(hmenum.DeviceProfile("IPLock"))
	assertFieldsMatchReference(t, "IPLock.Fields", lockCG.Fields, map[hmenum.Field]fieldValueRef{
		hmenum.FieldDirection:       bareRef(hmenum.ParameterActivityState),
		hmenum.FieldLockState:       bareRef(hmenum.ParameterLockState),
		hmenum.FieldLockTargetLevel: bareRef(hmenum.ParameterLockTargetLevel),
	})
	if state := lockCG.ChannelFields[-1]; state == nil {
		t.Error("IPLock.ChannelFields[STATE offset] is missing")
	} else {
		assertFieldsMatchReference(t, "IPLock.ChannelFields[STATE]", state, map[hmenum.Field]fieldValueRef{
			hmenum.FieldError: bareRef(hmenum.ParameterErrorJammed),
		})
	}

	// IPThermostat — reference implementation model/custom/profile.py:763 IP_THERMOSTAT_CONFIG.
	thermoCG := cfg(hmenum.DeviceProfile("IPThermostat"))
	assertFieldsMatchReference(t, "IPThermostat.Fields", thermoCG.Fields, map[hmenum.Field]fieldValueRef{
		hmenum.FieldActiveProfile:                     bareRef(hmenum.ParameterActiveProfile),
		hmenum.FieldBoostMode:                         bareRef(hmenum.ParameterBoostMode),
		hmenum.FieldControlMode:                       bareRef(hmenum.ParameterControlMode),
		hmenum.FieldMinMaxValueNotRelevantForManuMode: bareRef(hmenum.ParameterMinMaxNotRelevantForManuMode),
		hmenum.FieldOptimumStartStop:                  bareRef(hmenum.ParameterOptimumStartStop),
		hmenum.FieldPartyMode:                         bareRef(hmenum.ParameterPartyMode),
		hmenum.FieldSetpoint:                          bareRef(hmenum.ParameterSetPointTemperature),
		hmenum.FieldSetPointMode:                      bareRef(hmenum.ParameterSetPointMode),
		hmenum.FieldTemperatureMaximum:                bareRef(hmenum.ParameterTemperatureMaximum),
		hmenum.FieldTemperatureMinimum:                bareRef(hmenum.ParameterTemperatureMinimum),
		hmenum.FieldTemperatureOffset:                 bareRef(hmenum.ParameterTemperatureOffset),
		hmenum.FieldHeatingCooling:                    visibleRef(hmenum.ParameterHeatingCooling),
		hmenum.FieldHumidity:                          visibleRef(hmenum.ParameterHumidity),
		hmenum.FieldTemperature:                       visibleRef(hmenum.ParameterActualTemperature),
	})
	if cf := thermoCG.ChannelFields[-5]; cf == nil {
		t.Error("IPThermostat.ChannelFields[-5] (config channel, WGTC) is missing")
	} else {
		assertFieldsMatchReference(t, "IPThermostat.ChannelFields[-5]", cf, map[hmenum.Field]fieldValueRef{
			hmenum.FieldState: bareRef(hmenum.ParameterState),
		})
	}
	if cf := thermoCG.ChannelFields[0]; cf == nil {
		t.Error("IPThermostat.ChannelFields[0] is missing")
	} else {
		assertFieldsMatchReference(t, "IPThermostat.ChannelFields[0]", cf, map[hmenum.Field]fieldValueRef{
			hmenum.FieldConcentration: visibleRef(hmenum.ParameterConcentration),
			hmenum.FieldLevel:         visibleRef(hmenum.ParameterLevel),
		})
	}
	if cf := thermoCG.ChannelFields[7]; cf == nil {
		t.Error("IPThermostat.ChannelFields[7] is missing")
	} else {
		assertFieldsMatchReference(t, "IPThermostat.ChannelFields[7]", cf, map[hmenum.Field]fieldValueRef{
			hmenum.FieldHeatingValveType: bareRef(hmenum.ParameterHeatingValveType),
		})
	}
	if cf := thermoCG.ChannelFields[8]; cf == nil {
		t.Error("IPThermostat.ChannelFields[8] (BWTH) is missing")
	} else {
		assertFieldsMatchReference(t, "IPThermostat.ChannelFields[8]", cf, map[hmenum.Field]fieldValueRef{
			hmenum.FieldState: visibleRef(hmenum.ParameterState),
		})
	}
}

// parameterSlicesEqualAsSets compares two Parameter slices as sets
// (order-independent, no duplicates expected on either side).
func parameterSlicesEqualAsSets(got, want []hmenum.Parameter) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[hmenum.Parameter]struct{}, len(want))
	for _, p := range want {
		seen[p] = struct{}{}
	}
	for _, p := range got {
		if _, ok := seen[p]; !ok {
			return false
		}
	}
	return true
}
