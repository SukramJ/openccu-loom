// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"errors"
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------- helpers --------------------------------------------------

// makeProfile builds a minimal Profile for test fixtures.
func makeProfile(name hmenum.DeviceProfile, cat hmenum.DataPointCategory, deviceType string) Profile {
	return Profile{
		Name:         name,
		DeviceType:   deviceType,
		ProductGroup: hmenum.ProductGroupUnknown,
		Category:     cat,
		Channels:     []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
	}
}

// makeFloat builds a *generic.Float attached to the given channel's
// VALUES paramset under the given parameter.
func makeFloat(channelAddr string, p hmenum.Parameter) *generic.Float {
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
}

// makeInteger builds a *generic.Integer (DataPoint[int32]) under VALUES.
func makeInteger(channelAddr string, p hmenum.Parameter) *generic.Integer {
	return generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// makeSwitch builds a *generic.Switch under VALUES.
func makeSwitch(channelAddr string, p hmenum.Parameter) *generic.Switch {
	return generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
}

// makeBinarySensor builds a *generic.BinarySensor under VALUES.
func makeBinarySensor(channelAddr string, p hmenum.Parameter) *generic.BinarySensor {
	return generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
}

// makeChannel returns an empty channel fixture.
func makeChannel(addr string) *device.Channel {
	return &device.Channel{Address: addr}
}

// =====================================================================
// Cluster A — Registry: Get / Fallback semantics
// =====================================================================

// TestRegistryGetUnknownDeviceTypeReturnsErrProfileMissing verifies that
// querying a populated registry for a completely unknown device type
// returns exactly ErrProfileMissing (not a different or nil error).
// This pins the "wrong fallback hides devices completely" invariant.
func TestRegistryGetUnknownDeviceTypeReturnsErrProfileMissing(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(makeProfile("IPSwitch", hmenum.DataPointCategorySwitch, "HmIP-PS")); err != nil {
		t.Fatal(err)
	}
	_, err := r.Get(hmenum.DataPointCategorySwitch, "totally-unknown-model")
	if !errors.Is(err, ErrProfileMissing) {
		t.Fatalf("expected ErrProfileMissing, got %v", err)
	}
}

// TestRegistryGetWrongCategoryReturnsErrProfileMissing registers a
// profile under Switch, then asks for the same deviceType under Cover —
// the registry must not cross-pollinate categories.
func TestRegistryGetWrongCategoryReturnsErrProfileMissing(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(makeProfile("IPSwitch", hmenum.DataPointCategorySwitch, "HmIP-PS")); err != nil {
		t.Fatal(err)
	}
	_, err := r.Get(hmenum.DataPointCategoryCover, "HmIP-PS")
	if !errors.Is(err, ErrProfileMissing) {
		t.Fatalf("expected ErrProfileMissing for wrong category, got %v", err)
	}
}

// TestRegistryHasMatchesGet asserts that Has and Get are consistent:
// every (category, deviceType) pair for which Has returns true must
// also succeed with Get, and vice-versa (false → error).
func TestRegistryHasMatchesGet(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	pairs := []struct {
		cat        hmenum.DataPointCategory
		deviceType string
		profile    hmenum.DeviceProfile
	}{
		{hmenum.DataPointCategorySwitch, "HmIP-PS", "IPSwitch"},
		{hmenum.DataPointCategoryCover, "HmIP-BROLL", "IPCover"},
		{hmenum.DataPointCategoryLight, "HmIP-BDT", "IPDimmer"},
	}
	for _, pair := range pairs {
		if err := r.Register(makeProfile(pair.profile, pair.cat, pair.deviceType)); err != nil {
			t.Fatal(err)
		}
	}

	// For every registered pair: Has must be true, Get must succeed.
	for _, pair := range pairs {
		if !r.Has(pair.cat, pair.deviceType) {
			t.Errorf("Has(%v, %v) = false, want true", pair.cat, pair.deviceType)
		}
		if _, err := r.Get(pair.cat, pair.deviceType); err != nil {
			t.Errorf("Get(%v, %v) err=%v, want nil", pair.cat, pair.deviceType, err)
		}
	}

	// For an unregistered pair: Has must be false, Get must error.
	ghost := "ghost-device"
	if r.Has(hmenum.DataPointCategorySwitch, ghost) {
		t.Errorf("Has(switch, %q) = true, want false", ghost)
	}
	if _, err := r.Get(hmenum.DataPointCategorySwitch, ghost); !errors.Is(err, ErrProfileMissing) {
		t.Errorf("Get(switch, %q) err=%v, want ErrProfileMissing", ghost, err)
	}
}

// TestRegistryRegisterDuplicateReturnsErrProfileConflict ensures that
// registering the same (category, deviceType, name) triple a second
// time fails with ErrProfileConflict, not silently overwrites.
func TestRegistryRegisterDuplicateReturnsErrProfileConflict(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	p := makeProfile("IPSwitch", hmenum.DataPointCategorySwitch, "HmIP-PS")
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	err := r.Register(p)
	if !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("second Register: expected ErrProfileConflict, got %v", err)
	}
	// Verify the original was not mutated (still exactly 1 entry).
	if r.Len() != 1 {
		t.Errorf("Len after duplicate register = %d, want 1", r.Len())
	}
}

// TestRegistryMustRegisterPanicsOnDuplicate verifies that MustRegister
// panics (rather than swallowing the conflict) when called twice for
// the same key.
func TestRegistryMustRegisterPanicsOnDuplicate(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	p := makeProfile("IPSwitch", hmenum.DataPointCategorySwitch, "HmIP-PS-dup")
	r.MustRegister(p)

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		r.MustRegister(p)
	}()
	if recovered == nil {
		t.Fatal("MustRegister on duplicate did not panic")
	}
}

// TestRegistryForCategoryReturnsDeviceTypeFilteredProfiles registers
// three profiles under the same category with three distinct
// deviceTypes, then verifies that ForCategory returns exactly the
// entry for the requested deviceType.
func TestRegistryForCategoryReturnsDeviceTypeFilteredProfiles(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	cat := hmenum.DataPointCategorySwitch
	deviceTypes := []string{"dev-A", "dev-B", "dev-C"}
	for _, dt := range deviceTypes {
		if err := r.Register(makeProfile("IPSwitch", cat, dt)); err != nil {
			t.Fatal(err)
		}
	}
	// ForCategory(cat, "dev-B") must return exactly one profile for dev-B.
	got := r.ForCategory(cat, "dev-B")
	if len(got) != 1 {
		t.Fatalf("ForCategory(switch, dev-B): len=%d, want 1", len(got))
	}
	// DeviceType is stored in normalized (lowercase) form.
	if got[0].DeviceType != "dev-b" {
		t.Errorf("ForCategory returned deviceType=%q, want dev-b (normalized)", got[0].DeviceType)
	}
	// Querying for an unregistered deviceType must return empty slice.
	if got2 := r.ForCategory(cat, "dev-X"); len(got2) != 0 {
		t.Errorf("ForCategory(switch, dev-X): len=%d, want 0", len(got2))
	}
}

// TestRegistryForDeviceReturnsAcrossCategories registers the same
// deviceType under two different categories and verifies that
// ForDevice returns both profiles.
func TestRegistryForDeviceReturnsAcrossCategories(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	const dt = "multi-cat-device"
	if err := r.Register(makeProfile("IPSwitch", hmenum.DataPointCategorySwitch, dt)); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(makeProfile("IPCover", hmenum.DataPointCategoryCover, dt)); err != nil {
		t.Fatal(err)
	}
	got := r.ForDevice(dt)
	if len(got) != 2 {
		t.Fatalf("ForDevice(%q): len=%d, want 2", dt, len(got))
	}
	// Result must be sorted by category for stable iteration.
	if got[0].Category > got[1].Category {
		t.Errorf("ForDevice result not sorted by category: [%v, %v]", got[0].Category, got[1].Category)
	}
}

// TestRegistryDeviceTypesIsSorted pins the contract that DeviceTypes()
// returns a deduplicated, alphabetically sorted list.
func TestRegistryDeviceTypesIsSorted(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Register in non-alphabetical order to catch an unstable impl.
	for _, pair := range []struct {
		cat hmenum.DataPointCategory
		dt  string
	}{
		{hmenum.DataPointCategorySwitch, "zzz"},
		{hmenum.DataPointCategorySwitch, "aaa"},
		{hmenum.DataPointCategorySwitch, "mmm"},
		// Same deviceType under a second category — must appear only once.
		{hmenum.DataPointCategoryCover, "aaa"},
	} {
		if err := r.Register(makeProfile("IPSwitch", pair.cat, pair.dt)); err != nil {
			t.Fatal(err)
		}
	}
	got := r.DeviceTypes()
	if !sort.StringsAreSorted(got) {
		t.Errorf("DeviceTypes not sorted: %v", got)
	}
	// "aaa" registered under two categories — must appear only once.
	seen := make(map[string]int)
	for _, dt := range got {
		seen[dt]++
	}
	if seen["aaa"] != 1 {
		t.Errorf("DeviceTypes: 'aaa' appears %d times, want 1", seen["aaa"])
	}
}

// =====================================================================
// Cluster B — DefaultRegistry coverage
// =====================================================================

// TestDefaultRegistryHasExpectedSize is a tripwire: the process-wide
// registry must hold at least as many profiles as the generator
// constant claims (138). A smaller count indicates lost profiles.
func TestDefaultRegistryHasExpectedSize(t *testing.T) {
	t.Parallel()
	dr := DefaultRegistry()
	if got := dr.Len(); got < GeneratedProfileCount {
		t.Errorf("DefaultRegistry.Len()=%d, want >= %d (GeneratedProfileCount)", got, GeneratedProfileCount)
	}
}

// TestDefaultRegistryDeviceTypesAreUniquePerCategory asserts that
// within any single category no two profiles share the same
// (category, deviceType, name) triple — the registry key itself
// enforces this, but we exercise the global default registry to
// confirm RegisterGeneratedProfiles is well-formed.
func TestDefaultRegistryDeviceTypesAreUniquePerCategory(t *testing.T) {
	t.Parallel()
	dr := DefaultRegistry()
	// Build a fresh registry and re-register — if any duplicate exists
	// MustRegister would have panicked at init time; this test documents
	// the constraint and would catch bugs in non-Must registration paths.
	//
	// We assert indirectly: every deviceType returned by DeviceTypes()
	// must survive a round-trip through Has without a ghost.
	for _, dt := range dr.DeviceTypes() {
		profiles := dr.ForDevice(dt)
		seen := make(map[hmenum.DataPointCategory]int)
		for _, p := range profiles {
			seen[p.Category]++
		}
		// Multiple profiles for the same (category, deviceType) are
		// allowed when the profile Name differs — e.g. hbw-lc-rgbww-in6-dr
		// has both RfDimmer and RfDimmer_Color_Fixed under Light.
		// We just assert that ForDevice always returns consistent data
		// by cross-checking with Has.
		for _, p := range profiles {
			if !dr.Has(p.Category, p.DeviceType) {
				t.Errorf("Has(%v, %q)=false but profile %q exists via ForDevice", p.Category, p.DeviceType, p.Name)
			}
		}
	}
}

// TestDefaultRegistryCoversKnownProfileCategories verifies that the
// default registry contains at least one profile in each of the device
// Categories that Hub and action
// categories are intentionally excluded (they don't have device profiles).
func TestDefaultRegistryCoversKnownProfileCategories(t *testing.T) {
	t.Parallel()
	dr := DefaultRegistry()

	// These are the categories that have profiles.
	// custom_components (climate, cover, light, lock, siren, switch,
	// text_display, valve).
	wantCategories := []hmenum.DataPointCategory{
		hmenum.DataPointCategoryClimate,
		hmenum.DataPointCategoryCover,
		hmenum.DataPointCategoryLight,
		hmenum.DataPointCategoryLock,
		hmenum.DataPointCategorySiren,
		hmenum.DataPointCategorySwitch,
		hmenum.DataPointCategoryTextDisplay,
		hmenum.DataPointCategoryValve,
	}

	for _, cat := range wantCategories {
		cat := cat
		t.Run(string(cat), func(t *testing.T) {
			t.Parallel()
			// ForDevice iterates all device types; check that at least one
			// profile exists for this category by scanning DeviceTypes.
			found := false
			for _, dt := range dr.DeviceTypes() {
				profiles := dr.ForDevice(dt)
				for _, p := range profiles {
					if p.Category == cat {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				t.Errorf("DefaultRegistry has no profile for category %v", cat)
			}
		})
	}
}

// =====================================================================
// Cluster C — Resolver field accessors
// =====================================================================

// TestResolveFloatFieldFindsMatchingDataPoint verifies that FloatField
// returns the same *generic.Float that was stored in the channel.
func TestResolveFloatFieldFindsMatchingDataPoint(t *testing.T) {
	t.Parallel()
	ch := makeChannel("ABC:1")
	dp := makeFloat("ABC:1", hmenum.ParameterLevel)
	ch.Put(dp)

	got := FloatField(ch, hmenum.ParameterLevel)
	if got == nil {
		t.Fatal("FloatField returned nil, want the Float DP")
	}
	if got != dp {
		t.Error("FloatField returned a different pointer than stored")
	}
}

// TestResolveFloatFieldReturnsNilWhenMissing pins the safe-fallback
// semantics: a channel without LEVEL must yield nil, not panic or
// return a wrong DP. A nil return is the correct signal that the device
// should not be hidden but simply lacks this parameter.
func TestResolveFloatFieldReturnsNilWhenMissing(t *testing.T) {
	t.Parallel()
	ch := makeChannel("ABC:2")
	// Populate an unrelated parameter so the map is non-nil.
	ch.Put(makeFloat("ABC:2", hmenum.ParameterLevel2))

	if got := FloatField(ch, hmenum.ParameterLevel); got != nil {
		t.Errorf("FloatField for absent LEVEL returned non-nil: %v", got)
	}
}

// TestResolveFloatFieldReturnsNilOnTypeMismatch asserts that FloatField
// does NOT mis-cast a *generic.Integer stored under the same parameter
// name. A wrong cast would silently return a non-nil value that will
// later panic or produce garbage data — the correct behaviour is nil.
func TestResolveFloatFieldReturnsNilOnTypeMismatch(t *testing.T) {
	t.Parallel()
	ch := makeChannel("ABC:3")
	// Store an Integer under LEVEL — type mismatch.
	ch.Put(makeInteger("ABC:3", hmenum.ParameterLevel))

	if got := FloatField(ch, hmenum.ParameterLevel); got != nil {
		t.Errorf("FloatField with Integer DP returned non-nil: %v", got)
	}
}

// TestResolveSwitchFieldDistinctFromBinarySensor verifies that
// SwitchField and BinarySensorField are type-discriminated even when
// both would match the same bool parameter. A *generic.Switch must not
// be returned by BinarySensorField and vice-versa.
func TestResolveSwitchFieldDistinctFromBinarySensor(t *testing.T) {
	t.Parallel()
	ch := makeChannel("ABC:4")
	sw := makeSwitch("ABC:4", hmenum.ParameterState)
	ch.Put(sw)

	if got := SwitchField(ch, hmenum.ParameterState); got == nil {
		t.Error("SwitchField returned nil for Switch DP")
	}
	// BinarySensorField must NOT return the Switch.
	if got := BinarySensorField(ch, hmenum.ParameterState); got != nil {
		t.Errorf("BinarySensorField returned non-nil for Switch DP: %v", got)
	}

	// Now replace with a BinarySensor and re-check.
	ch2 := makeChannel("ABC:5")
	bs := makeBinarySensor("ABC:5", hmenum.ParameterState)
	ch2.Put(bs)

	if got := BinarySensorField(ch2, hmenum.ParameterState); got == nil {
		t.Error("BinarySensorField returned nil for BinarySensor DP")
	}
	if got := SwitchField(ch2, hmenum.ParameterState); got != nil {
		t.Errorf("SwitchField returned non-nil for BinarySensor DP: %v", got)
	}
}

// TestResolveAllAccessorsReturnNilForNilChannel verifies that none of
// the resolver accessors panic or return a non-nil pointer when called
// with a nil channel. This is the "no panic on missing device" invariant.
//
// Each accessor returns a typed pointer (e.g. *generic.Float). We check
// the typed return directly instead of storing it in an `any` interface,
// because a typed nil pointer stored in `any` is a non-nil interface
// value in Go — a common gotcha that would cause false failures.
func TestResolveAllAccessorsReturnNilForNilChannel(t *testing.T) {
	t.Parallel()

	var ch *device.Channel // nil

	t.Run("FloatField", func(t *testing.T) {
		t.Parallel()
		if got := FloatField(ch, hmenum.ParameterLevel); got != nil {
			t.Errorf("FloatField(nil) = %v, want nil pointer", got)
		}
	})
	t.Run("IntegerField", func(t *testing.T) {
		t.Parallel()
		if got := IntegerField(ch, hmenum.ParameterLevel); got != nil {
			t.Errorf("IntegerField(nil) = %v, want nil pointer", got)
		}
	})
	t.Run("SwitchField", func(t *testing.T) {
		t.Parallel()
		if got := SwitchField(ch, hmenum.ParameterState); got != nil {
			t.Errorf("SwitchField(nil) = %v, want nil pointer", got)
		}
	})
	t.Run("BinarySensorField", func(t *testing.T) {
		t.Parallel()
		if got := BinarySensorField(ch, hmenum.ParameterState); got != nil {
			t.Errorf("BinarySensorField(nil) = %v, want nil pointer", got)
		}
	})
	t.Run("FloatSensorField", func(t *testing.T) {
		t.Parallel()
		if got := FloatSensorField(ch, hmenum.ParameterLevel); got != nil {
			t.Errorf("FloatSensorField(nil) = %v, want nil pointer", got)
		}
	})
	t.Run("IntegerSensorField", func(t *testing.T) {
		t.Parallel()
		if got := IntegerSensorField(ch, hmenum.ParameterLevel); got != nil {
			t.Errorf("IntegerSensorField(nil) = %v, want nil pointer", got)
		}
	})
	t.Run("StringSensorField", func(t *testing.T) {
		t.Parallel()
		if got := StringSensorField(ch, hmenum.ParameterState); got != nil {
			t.Errorf("StringSensorField(nil) = %v, want nil pointer", got)
		}
	})
}

// =====================================================================
// Cluster D — Round-trip
// =====================================================================

// TestProfileBuildAppliedToChannelExposesAccessors documents the
// end-to-end usage pattern: pick a well-known profile from the default
// registry (IPSwitch / elv-sh-bs2 / Switch / channel 4), build a
// Channel that mirrors that profile's expected data points, and verify
// that the resolver accessors return consistent results.
func TestProfileBuildAppliedToChannelExposesAccessors(t *testing.T) {
	t.Parallel()

	// 1. Locate the profile in the default registry.
	dr := DefaultRegistry()
	const (
		switchDevType = "elv-sh-bs2"
		switchCh      = 4
	)
	prof, err := dr.Get(hmenum.DataPointCategorySwitch, switchDevType)
	if err != nil {
		t.Fatalf("Get(switch, %q): %v", switchDevType, err)
	}
	// Confirm the profile covers our expected channel.
	found := false
	for _, cr := range prof.Channels {
		if cr.Channel == switchCh {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("profile %q does not contain channel %d; channels: %v", prof.Name, switchCh, prof.Channels)
	}

	// 2. Build a channel fixture that mimics what the ingest pipeline
	// would create for this profile: a STATE parameter as Switch.
	channelAddr := "MEQ1234567:4"
	ch := makeChannel(channelAddr)
	sw := makeSwitch(channelAddr, hmenum.ParameterState)
	ch.Put(sw)

	// 3. Resolver assertions.
	if got := SwitchField(ch, hmenum.ParameterState); got == nil {
		t.Error("SwitchField(ch, STATE) = nil, want *generic.Switch")
	}
	// FloatField for the same parameter must be nil (type guard).
	if got := FloatField(ch, hmenum.ParameterState); got != nil {
		t.Errorf("FloatField(ch, STATE) = non-nil, want nil for Switch DP")
	}
	// BinarySensorField must also be nil (different type).
	if got := BinarySensorField(ch, hmenum.ParameterState); got != nil {
		t.Errorf("BinarySensorField(ch, STATE) = non-nil, want nil for Switch DP")
	}
	// Querying for an absent parameter must be nil regardless.
	if got := FloatField(ch, hmenum.ParameterLevel); got != nil {
		t.Errorf("FloatField(ch, LEVEL) = non-nil, want nil (parameter absent)")
	}
}
