// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// fakeCustomDP is a tiny stand-in that satisfies
// [device.AttachableDataPoint] so the materializer's SetCustomDataPoint
// hook works without pulling a real sub-package.
type fakeCustomDP struct {
	key       hmtypes.DataPointKey
	channel   *device.Channel
	rebased   RebasedChannelGroupConfig
	profileID hmenum.DeviceProfile
}

func (f *fakeCustomDP) DataPointKey() hmtypes.DataPointKey { return f.key }

// fakeCtor builds a [Constructor] that records every invocation so
// tests can assert which (channel, group) pairs the materializer
// presented. Callers use the returned `*[]*fakeCustomDP` to inspect
// the captures and the `Profile` argument to set the constructor's
// profile name.
func fakeCtor(profile hmenum.DeviceProfile) (Constructor, *[]*fakeCustomDP) {
	var (
		mu       sync.Mutex
		captured []*fakeCustomDP
	)
	c := func(ch *device.Channel, group RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
		mu.Lock()
		defer mu.Unlock()
		dp := &fakeCustomDP{
			key:       hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(profile)},
			channel:   ch,
			rebased:   group,
			profileID: profile,
		}
		captured = append(captured, dp)
		return dp, nil
	}
	return c, &captured
}

// erroringCtor is a [Constructor] that always returns an error, used
// to assert that materialise reports per-profile failures without
// short-circuiting other profiles.
func erroringCtor(err error) Constructor {
	return func(_ *device.Channel, _ RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
		return nil, err
	}
}

// putBoolDP is a tiny helper that drops a generic.Switch under
// (channel, parameter) so the materializer can later call
// SetForcedUsage on it through the forcer interface.
func putBoolDP(ch *device.Channel, param hmenum.Parameter) *generic.Switch {
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
	}
	dp := generic.NewSwitch(cfg)
	ch.Put(dp)
	return dp
}

// newHmIPBwthDevice constructs a HmIP-BWTH-shaped device with the
// channels (0 through 8) the IPThermostat + IPButtonLock profiles
// touch. Channel 0 carries BUTTON_LOCK; channel 1 carries SET_POINT_TEMPERATURE
// for the climate stub. Other channels are empty placeholders.
func newHmIPBwthDevice() *device.Device {
	d := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "0001D7",
		Model:        "HmIP-BWTH",
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	for i := range 9 {
		d.AddChannel("0001D7:"+itoaSmall(i), i, "T", hmenum.ParamsetKeyValues)
	}
	return d
}

// itoaSmall is a private digit-only itoa for tests; we deliberately
// duplicate it instead of pulling strconv here to keep the test file
// import surface tight.
func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 10 {
		return string(rune('0' + n)) //nolint:gosec // G115: n is 0..9; '0'+n is 48..57, well within valid rune range
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10)) //nolint:gosec // G115: n is 10..99; each digit is 0..9 so '0'+digit is 48..57
}

// makeMaterializeProfile builds a Profile whose Config has the given primary
// channel and an optional Field map. Used by several tests.
func makeMaterializeProfile(name hmenum.DeviceProfile, primary, baseChannel int, cat hmenum.DataPointCategory, fields map[hmenum.Field]FieldValue) Profile {
	cg := ChannelGroupConfig{
		PrimaryChannel:    primary,
		PrimaryChannelSet: true,
		Fields:            fields,
	}
	return Profile{
		Name:       name,
		DeviceType: "hmip-bwth",
		Category:   cat,
		Channels:   []ChannelRoleAssignment{{Channel: baseChannel, Role: ChannelRolePrimary}},
		Config:     &ProfileConfig{ProfileType: name, ChannelGroup: cg},
	}
}

// =====================================================================
// CreateCustomDataPoints — HmIP-BWTH end-to-end (the user-bug repro)
// =====================================================================

func TestCreateCustomDataPointsHmIPBWTH(t *testing.T) {
	dev := newHmIPBwthDevice()

	registry := NewRegistry()

	// IPThermostat: primary channel 1, base channel 0 → relevant {1}.
	thermostat := makeMaterializeProfile("IPThermostat", 1, 0, hmenum.DataPointCategoryClimate, nil)
	if err := registry.Register(thermostat); err != nil {
		t.Fatal(err)
	}
	thermostatCtor, thermostatCaptures := fakeCtor("IPThermostat")
	if err := registry.RegisterConstructor("IPThermostat", thermostatCtor); err != nil {
		t.Fatal(err)
	}

	// IPButtonLock: primary channel 0, base channel 0 → relevant {0}.
	buttonLock := makeMaterializeProfile("IPButtonLock", 0, 0, hmenum.DataPointCategoryLock, nil)
	if err := registry.Register(buttonLock); err != nil {
		t.Fatal(err)
	}
	lockCtor, lockCaptures := fakeCtor("IPButtonLock")
	if err := registry.RegisterConstructor("IPButtonLock", lockCtor); err != nil {
		t.Fatal(err)
	}

	if err := CreateCustomDataPoints(dev, registry); err != nil {
		t.Fatalf("CreateCustomDataPoints failed: %v", err)
	}

	// IPThermostat materialised on channel 1.
	if len(*thermostatCaptures) != 1 {
		t.Fatalf("IPThermostat captures = %d, want 1", len(*thermostatCaptures))
	}
	if (*thermostatCaptures)[0].channel.Number != 1 {
		t.Fatalf("IPThermostat channel = %d, want 1", (*thermostatCaptures)[0].channel.Number)
	}
	ch1 := dev.Channel("0001D7:1")
	if ch1 == nil {
		t.Fatal("missing channel 1")
	}
	if ch1.CustomDataPoint() == nil {
		t.Fatal("channel 1 has no custom DP — IPThermostat materialise broke")
	}

	// IPButtonLock materialised on channel 0.
	if len(*lockCaptures) != 1 {
		t.Fatalf("IPButtonLock captures = %d, want 1", len(*lockCaptures))
	}
	ch0 := dev.Channel("0001D7:0")
	if ch0 == nil {
		t.Fatal("missing channel 0")
	}
	if ch0.CustomDataPoint() == nil {
		t.Fatal("channel 0 has no custom DP — IPButtonLock materialise broke")
	}

	// All other channels stay empty.
	for i := 2; i <= 8; i++ {
		ch := dev.Channel("0001D7:" + itoaSmall(i))
		if ch == nil {
			continue
		}
		if ch.CustomDataPoint() != nil {
			t.Fatalf("channel %d unexpectedly carries a custom DP", i)
		}
	}
}

// =====================================================================
// _get_relevant_channels skip path
// =====================================================================

func TestCreateCustomDataPointSkipsNonRelevantChannel(t *testing.T) {
	dev := newHmIPBwthDevice()
	registry := NewRegistry()

	// Profile with primary channel 1 + base 0 → relevant set {1}.
	profile := makeMaterializeProfile("IPThermostat", 1, 0, hmenum.DataPointCategoryClimate, nil)
	if err := registry.Register(profile); err != nil {
		t.Fatal(err)
	}
	ctor, captures := fakeCtor("IPThermostat")
	if err := registry.RegisterConstructor("IPThermostat", ctor); err != nil {
		t.Fatal(err)
	}

	// Materialise just channel 5 — outside the relevant set.
	ch5 := dev.Channel("0001D7:5")
	if err := CreateCustomDataPoint(dev, ch5, profile, registry); err != nil {
		t.Fatal(err)
	}
	if len(*captures) != 0 {
		t.Fatalf("constructor invoked for non-relevant channel: %d", len(*captures))
	}
	if ch5.CustomDataPoint() != nil {
		t.Fatal("non-relevant channel must not receive a custom DP")
	}
}

// =====================================================================
// Missing constructor is a no-op
// =====================================================================

func TestCreateCustomDataPointMissingConstructorIsNoop(t *testing.T) {
	dev := newHmIPBwthDevice()
	registry := NewRegistry()

	profile := makeMaterializeProfile("IPThermostat", 1, 0, hmenum.DataPointCategoryClimate, nil)
	if err := registry.Register(profile); err != nil {
		t.Fatal(err)
	}

	// No RegisterConstructor call → materializer must skip silently.
	ch1 := dev.Channel("0001D7:1")
	if err := CreateCustomDataPoint(dev, ch1, profile, registry); err != nil {
		t.Fatalf("missing constructor must not error: %v", err)
	}
	if ch1.CustomDataPoint() != nil {
		t.Fatal("no DP should attach when constructor is missing")
	}
}

// =====================================================================
// FieldMapping with IsVisible flips DP usage
// =====================================================================

func TestCreateCustomDataPointSetsForcedUsage(t *testing.T) {
	dev := newHmIPBwthDevice()
	registry := NewRegistry()

	// Drop a switch DP at channel 1 / parameter STATE so the
	// materializer's visibility forcing has something to mark.
	ch1 := dev.Channel("0001D7:1")
	state := putBoolDP(ch1, hmenum.ParameterState)

	// Profile whose primary-channel field maps STATE with IsVisible=true.
	profile := Profile{
		Name:       "IPSwitch",
		DeviceType: "hmip-bwth",
		Category:   hmenum.DataPointCategorySwitch,
		Channels:   []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		Config: &ProfileConfig{
			ProfileType: "IPSwitch",
			ChannelGroup: ChannelGroupConfig{
				PrimaryChannel:    1,
				PrimaryChannelSet: true,
				Fields: map[hmenum.Field]FieldValue{
					hmenum.FieldState: Visible(hmenum.ParameterState),
				},
			},
		},
	}
	if err := registry.Register(profile); err != nil {
		t.Fatal(err)
	}
	ctor, _ := fakeCtor("IPSwitch")
	if err := registry.RegisterConstructor("IPSwitch", ctor); err != nil {
		t.Fatal(err)
	}

	if err := CreateCustomDataPoint(dev, ch1, profile, registry); err != nil {
		t.Fatal(err)
	}

	got, ok := state.ForcedUsage()
	if !ok {
		t.Fatal("STATE DP should have been force-marked by the materializer")
	}
	if got != hmenum.DataPointUsageCDPVisible {
		t.Fatalf("STATE DP usage = %q, want CDPVisible", got)
	}
	if !state.Visible() {
		t.Fatal("STATE DP must be Visible() after force-mark")
	}
}

// =====================================================================
// FixedChannelFields are absolute (not rebased by group_no)
// =====================================================================

func TestCreateCustomDataPointFixedChannelFieldsAreAbsolute(t *testing.T) {
	dev := newHmIPBwthDevice()
	registry := NewRegistry()

	// Drop a DP on channel 0 (not the primary, not a relative offset
	// from group_no=2; the FixedChannelFields entry must reach it
	// regardless).
	ch0 := dev.Channel("0001D7:0")
	keyDP := putBoolDP(ch0, hmenum.ParameterState)

	// Profile whose primary is at relative offset 2 + base 0 →
	// group_no resolves to 2. FixedChannelFields[0] must NOT be
	// shifted by that group_no.
	profile := Profile{
		Name:       "IPSwitch",
		DeviceType: "hmip-bwth",
		Category:   hmenum.DataPointCategorySwitch,
		Channels:   []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		Config: &ProfileConfig{
			ProfileType: "IPSwitch",
			ChannelGroup: ChannelGroupConfig{
				PrimaryChannel:    2,
				PrimaryChannelSet: true,
				FixedChannelFields: map[int]map[hmenum.Field]FieldValue{
					0: {
						hmenum.FieldState: Visible(hmenum.ParameterState),
					},
				},
			},
		},
	}
	if err := registry.Register(profile); err != nil {
		t.Fatal(err)
	}
	ctor, _ := fakeCtor("IPSwitch")
	if err := registry.RegisterConstructor("IPSwitch", ctor); err != nil {
		t.Fatal(err)
	}

	// Walk via CreateCustomDataPoints so addChannelGroupsToDevice
	// runs and sets group_no = 2 on the relevant channels.
	if err := CreateCustomDataPoints(dev, registry); err != nil {
		t.Fatalf("CreateCustomDataPoints err: %v", err)
	}

	got, ok := keyDP.ForcedUsage()
	if !ok {
		t.Fatal("FixedChannelFields[0] must reach absolute channel 0")
	}
	if got != hmenum.DataPointUsageCDPVisible {
		t.Fatalf("forced usage = %q, want CDPVisible", got)
	}
}

// =====================================================================
// AdditionalDataPoints are force-marked as DataPoint usage
// =====================================================================

func TestCreateCustomDataPointAdditionalDataPointsAreMarked(t *testing.T) {
	dev := newHmIPBwthDevice()
	registry := NewRegistry()

	ch1 := dev.Channel("0001D7:1")
	humidity := putBoolDP(ch1, hmenum.ParameterHumidity)

	profile := Profile{
		Name:       "IPThermostat",
		DeviceType: "hmip-bwth",
		Category:   hmenum.DataPointCategoryClimate,
		Channels:   []ChannelRoleAssignment{{Channel: 0, Role: ChannelRolePrimary}},
		Config: &ProfileConfig{
			ProfileType: "IPThermostat",
			ChannelGroup: ChannelGroupConfig{
				PrimaryChannel:    1,
				PrimaryChannelSet: true,
			},
			AdditionalDataPoints: map[int][]hmenum.Parameter{
				// primary at +1 → group_no = 1; relCh 0 + group_no 1 = 1.
				0: {hmenum.ParameterHumidity},
			},
		},
	}
	if err := registry.Register(profile); err != nil {
		t.Fatal(err)
	}
	ctor, _ := fakeCtor("IPThermostat")
	if err := registry.RegisterConstructor("IPThermostat", ctor); err != nil {
		t.Fatal(err)
	}

	if err := CreateCustomDataPoints(dev, registry); err != nil {
		t.Fatal(err)
	}

	got, ok := humidity.ForcedUsage()
	if !ok {
		t.Fatal("HUMIDITY DP must be force-marked by AdditionalDataPoints")
	}
	if got != hmenum.DataPointUsageDataPoint {
		t.Fatalf("HUMIDITY DP forced usage = %q, want DataPoint", got)
	}
	if !humidity.Visible() {
		t.Fatal("HUMIDITY DP must be Visible() after DataPoint mark")
	}
}

// =====================================================================
// Two profiles for the same device both materialise
// =====================================================================

func TestCreateCustomDataPointsMultiCategoryAggregation(t *testing.T) {
	dev := newHmIPBwthDevice()
	registry := NewRegistry()

	// Profile A — IPThermostat at channel 1.
	thermostat := makeMaterializeProfile("IPThermostat", 1, 0, hmenum.DataPointCategoryClimate, nil)
	if err := registry.Register(thermostat); err != nil {
		t.Fatal(err)
	}
	thermCtor, thermCaptures := fakeCtor("IPThermostat")
	if err := registry.RegisterConstructor("IPThermostat", thermCtor); err != nil {
		t.Fatal(err)
	}

	// Profile B — IPButtonLock at channel 0.
	lock := makeMaterializeProfile("IPButtonLock", 0, 0, hmenum.DataPointCategoryLock, nil)
	if err := registry.Register(lock); err != nil {
		t.Fatal(err)
	}
	lockCtor, lockCaptures := fakeCtor("IPButtonLock")
	if err := registry.RegisterConstructor("IPButtonLock", lockCtor); err != nil {
		t.Fatal(err)
	}

	if err := CreateCustomDataPoints(dev, registry); err != nil {
		t.Fatal(err)
	}

	if len(*thermCaptures) != 1 {
		t.Fatalf("IPThermostat materialised %d times, want 1", len(*thermCaptures))
	}
	if len(*lockCaptures) != 1 {
		t.Fatalf("IPButtonLock materialised %d times, want 1", len(*lockCaptures))
	}

	// Custom DPs landed on the right channels.
	if dev.Channel("0001D7:1").CustomDataPoint() == nil {
		t.Fatal("channel 1 missing IPThermostat DP")
	}
	if dev.Channel("0001D7:0").CustomDataPoint() == nil {
		t.Fatal("channel 0 missing IPButtonLock DP")
	}
}

// =====================================================================
// Constructor errors are reported but do not abort other profiles
// =====================================================================

func TestCreateCustomDataPointsConstructorErrorContinues(t *testing.T) {
	dev := newHmIPBwthDevice()
	registry := NewRegistry()

	// Failing profile.
	failing := makeMaterializeProfile("IPThermostat", 1, 0, hmenum.DataPointCategoryClimate, nil)
	if err := registry.Register(failing); err != nil {
		t.Fatal(err)
	}
	bang := errors.New("boom")
	if err := registry.RegisterConstructor("IPThermostat", erroringCtor(bang)); err != nil {
		t.Fatal(err)
	}

	// Succeeding profile.
	ok := makeMaterializeProfile("IPButtonLock", 0, 0, hmenum.DataPointCategoryLock, nil)
	if err := registry.Register(ok); err != nil {
		t.Fatal(err)
	}
	okCtor, okCaptures := fakeCtor("IPButtonLock")
	if err := registry.RegisterConstructor("IPButtonLock", okCtor); err != nil {
		t.Fatal(err)
	}

	err := CreateCustomDataPoints(dev, registry)
	if err == nil {
		t.Fatal("expected joined error from failing constructor")
	}
	if !errors.Is(err, bang) {
		t.Fatalf("err = %v, want errors.Is(boom)", err)
	}

	// IPButtonLock still materialised.
	if len(*okCaptures) != 1 {
		t.Fatalf("IPButtonLock should have materialised despite the IPThermostat failure: %d", len(*okCaptures))
	}
	if dev.Channel("0001D7:0").CustomDataPoint() == nil {
		t.Fatal("channel 0 must still carry the IPButtonLock DP")
	}
}

// =====================================================================
// RegisterConstructor conflict
// =====================================================================

func TestRegisterConstructorConflict(t *testing.T) {
	registry := NewRegistry()
	ctor, _ := fakeCtor("IPSwitch")
	if err := registry.RegisterConstructor("IPSwitch", ctor); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterConstructor("IPSwitch", ctor); !errors.Is(err, ErrConstructorConflict) {
		t.Fatalf("second RegisterConstructor err = %v, want ErrConstructorConflict", err)
	}
}

// Smoke test: a parallel materialise across a few constructors stays
// race-free. Run with `-race` to validate the registry's internal
// locking. The atomic counter exists purely to confirm every goroutine
// observed the call.
func TestRegisterConstructorParallelLookup(t *testing.T) {
	registry := NewRegistry()
	ctor, _ := fakeCtor("IPSwitch")
	if err := registry.RegisterConstructor("IPSwitch", ctor); err != nil {
		t.Fatal(err)
	}

	var hits int64
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, ok := registry.Constructor("IPSwitch"); ok {
				atomic.AddInt64(&hits, 1)
			}
		})
	}
	wg.Wait()
	if hits != 16 {
		t.Fatalf("Constructor() lookups observed = %d, want 16", hits)
	}
}
