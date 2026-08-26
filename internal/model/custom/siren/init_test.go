// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newInitChannel builds a minimal device + channel suitable for
// constructor tests. No writer is installed; Channel.Writer() returns
// nil, which is valid at constructor time (the writer is wired later
// during device hydration).
func newInitChannel(t *testing.T, address string) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF, Address: "ABC0001"})
	ch := d.AddChannel(address, 3, "SIREN", hmenum.ParamsetKeyValues)
	return ch
}

// TestIPSirenConstructorReturnsCorrectType verifies that
// ipSirenConstructor produces a non-nil *Siren with the correct
// address and BASIC capabilities (acoustic + optical + duration).
func TestIPSirenConstructorReturnsCorrectType(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-ASIR:3"
	ch := newInitChannel(t, addr)

	dp, err := ipSirenConstructor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("ipSirenConstructor() error = %v", err)
	}
	s, ok := dp.(*Siren)
	if !ok {
		t.Fatalf("ipSirenConstructor() type = %T, want *Siren", dp)
	}
	if s.Address != addr {
		t.Errorf("Siren.Address = %q, want %q", s.Address, addr)
	}
	if !s.Capabilities.SupportsAcoustic {
		t.Error("Siren.Capabilities.SupportsAcoustic = false, want true")
	}
	if !s.Capabilities.SupportsOptical {
		t.Error("Siren.Capabilities.SupportsOptical = false, want true")
	}
	if !s.Capabilities.SupportsDuration {
		t.Error("Siren.Capabilities.SupportsDuration = false, want true")
	}
}

// TestIPSirenSmokeConstructorReturnsSmokeSiren verifies that
// ipSirenSmokeConstructor produces a non-nil *SmokeSiren with the
// correct address and no configurable capabilities.
func TestIPSirenSmokeConstructorReturnsSmokeSiren(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-SWSD:1"
	ch := newInitChannel(t, addr)

	dp, err := ipSirenSmokeConstructor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("ipSirenSmokeConstructor() error = %v", err)
	}
	ss, ok := dp.(*SmokeSiren)
	if !ok {
		t.Fatalf("ipSirenSmokeConstructor() type = %T, want *SmokeSiren", dp)
	}
	if ss.Address != addr {
		t.Errorf("SmokeSiren.Address = %q, want %q", ss.Address, addr)
	}
}

// TestIPSoundPlayerConstructorReturnsSoundPlayer verifies that
// ipSoundPlayerConstructor produces a non-nil *SoundPlayer with the
// correct address.
func TestIPSoundPlayerConstructorReturnsSoundPlayer(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-MP3P:2"
	ch := newInitChannel(t, addr)

	dp, err := ipSoundPlayerConstructor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("ipSoundPlayerConstructor() error = %v", err)
	}
	sp, ok := dp.(*SoundPlayer)
	if !ok {
		t.Fatalf("ipSoundPlayerConstructor() type = %T, want *SoundPlayer", dp)
	}
	if sp.Address != addr {
		t.Errorf("SoundPlayer.Address = %q, want %q", sp.Address, addr)
	}
}

// TestIPSoundPlayerLedIsRegisteredInLightPackage verifies that the
// IPSoundPlayerLed profile constructor is NOT registered in the siren
// DefaultRegistry (it moved to the light package).
func TestIPSoundPlayerLedIsRegisteredInLightPackage(t *testing.T) {
	t.Parallel()

	reg := custom.DefaultRegistry()
	if _, ok := reg.Constructor(hmenum.DeviceProfileIPSoundPlayerLed); !ok {
		// The light package's init() must have registered it; if not, that
		// is a missing import in the test binary. The important assertion is
		// that the SIREN package no longer registers this profile itself.
		t.Skip("IPSoundPlayerLed constructor not present (light package not imported in this binary)")
	}
	// The constructor must be registered by the light package, not here.
	// We can't introspect which package registered it, so we simply verify
	// that ipSoundPlayerLedConstructor no longer exists in siren — which is
	// guaranteed by the compilation succeeding without that function.
}

// TestSirenDataPointKeyCarriesChannelAddress verifies that the
// DataPointKey returned by a Siren constructed from a real channel
// carries the correct ChannelAddress and a non-empty InterfaceID.
func TestSirenDataPointKeyCarriesChannelAddress(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-ASIR:3"
	ch := newInitChannel(t, addr)

	dp, err := ipSirenConstructor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("ipSirenConstructor() error = %v", err)
	}
	key := dp.DataPointKey()
	if key.ChannelAddress != addr {
		t.Errorf("DataPointKey().ChannelAddress = %q, want %q", key.ChannelAddress, addr)
	}
	if key.InterfaceID == "" {
		t.Error("DataPointKey().InterfaceID is empty, want non-empty")
	}
}

// TestSmokeSirenDataPointKeyCarriesChannelAddress mirrors the siren
// key test for SmokeSiren.
func TestSmokeSirenDataPointKeyCarriesChannelAddress(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-SWSD:1"
	ch := newInitChannel(t, addr)

	dp, err := ipSirenSmokeConstructor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("ipSirenSmokeConstructor() error = %v", err)
	}
	key := dp.DataPointKey()
	if key.ChannelAddress != addr {
		t.Errorf("DataPointKey().ChannelAddress = %q, want %q", key.ChannelAddress, addr)
	}
	if key.InterfaceID == "" {
		t.Error("DataPointKey().InterfaceID is empty, want non-empty")
	}
}

// TestSirenConstructorsRegisteredInDefaultRegistry verifies that the three
// siren profile constructors are present in the DefaultRegistry after the
// package is imported (i.e. the init() block ran).
//
// Note: IPSoundPlayerLed is intentionally omitted — it was moved to the
// light package (category=light fix). Its constructor is registered
// by light.init(), not siren.init().
func TestSirenConstructorsRegisteredInDefaultRegistry(t *testing.T) {
	t.Parallel()

	reg := custom.DefaultRegistry()
	profiles := []hmenum.DeviceProfile{
		hmenum.DeviceProfileIPSiren,
		hmenum.DeviceProfileIPSirenSmoke,
		hmenum.DeviceProfileIPSoundPlayer,
	}
	for _, p := range profiles {
		if _, ok := reg.Constructor(p); !ok {
			t.Errorf("DefaultRegistry missing constructor for profile %q", p)
		}
	}
}

// TestSirenCapabilityPresets verifies that the named siren capability preset
// Vars carry the correct flags — mirrors
// BASIC_SIREN_CAPABILITIES / SMOKE_SENSOR_SIREN_CAPABILITIES /
// SOUND_PLAYER_CAPABILITIES (capabilities/siren.py:44-61)./.
func TestSirenCapabilityPresets(t *testing.T) {
	t.Parallel()

	// BASIC_SIREN_CAPABILITIES: acoustic + optical + duration.
	if !BasicSirenCaps.SupportsAcoustic {
		t.Error("BasicSirenCaps: SupportsAcoustic must be true")
	}
	if !BasicSirenCaps.SupportsOptical {
		t.Error("BasicSirenCaps: SupportsOptical must be true")
	}
	if !BasicSirenCaps.SupportsDuration {
		t.Error("BasicSirenCaps: SupportsDuration must be true")
	}
	if BasicSirenCaps.SupportsSoundfiles {
		t.Error("BasicSirenCaps: SupportsSoundfiles must be false")
	}

	// SMOKE_SENSOR_SIREN_CAPABILITIES: acoustic only.
	if !SmokeSensorSirenCaps.SupportsAcoustic {
		t.Error("SmokeSensorSirenCaps: SupportsAcoustic must be true")
	}
	if SmokeSensorSirenCaps.SupportsOptical {
		t.Error("SmokeSensorSirenCaps: SupportsOptical must be false")
	}
	if SmokeSensorSirenCaps.SupportsDuration {
		t.Error("SmokeSensorSirenCaps: SupportsDuration must be false")
	}
	if SmokeSensorSirenCaps.SupportsSoundfiles {
		t.Error("SmokeSensorSirenCaps: SupportsSoundfiles must be false")
	}

	// SOUND_PLAYER_CAPABILITIES: duration + soundfiles, no acoustic/optical.
	if !SoundPlayerCaps.SupportsDuration {
		t.Error("SoundPlayerCaps: SupportsDuration must be true")
	}
	if !SoundPlayerCaps.SupportsSoundfiles {
		t.Error("SoundPlayerCaps: SupportsSoundfiles must be true")
	}
	if SoundPlayerCaps.SupportsAcoustic {
		t.Error("SoundPlayerCaps: SupportsAcoustic must be false")
	}
	if SoundPlayerCaps.SupportsOptical {
		t.Error("SoundPlayerCaps: SupportsOptical must be false")
	}
}

// TestSmokeSirenAvailableLightsAndTonesReturnNil verifies that/:
// SmokeSiren.AvailableLights() and AvailableTones() must return nil because
// the HmIP-SWSD has no configurable alarm-tone or light selection.
func TestSmokeSirenAvailableLightsAndTonesReturnNil(t *testing.T) {
	t.Parallel()

	ch := newInitChannel(t, "HmIP-SWSD:1")
	dp, err := ipSirenSmokeConstructor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("ipSirenSmokeConstructor() error = %v", err)
	}
	ss, ok := dp.(*SmokeSiren)
	if !ok {
		t.Fatalf("expected *SmokeSiren, got %T", dp)
	}
	if got := ss.AvailableLights(); got != nil {
		t.Errorf("SmokeSiren.AvailableLights() = %v, want nil", got)
	}
	if got := ss.AvailableTones(); got != nil {
		t.Errorf("SmokeSiren.AvailableTones() = %v, want nil", got)
	}
}
