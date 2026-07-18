// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	lightcdp "github.com/SukramJ/openccu-loom/internal/model/custom/light"
	sirencdp "github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	switchcdp "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- fixture helpers -------------------------------------------------

// newTestChannel builds a bare device with one channel of the given
// wire type, ready for a custom data point to be attached.
func newTestChannel(t *testing.T, deviceAddress, channelAddress string, channelNo int, channelType string) (*device.Device, *device.Channel) {
	t.Helper()
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Address:     deviceAddress,
		Model:       "HmIP-TEST",
		Name:        "Test Device",
	})
	ch := d.AddChannel(channelAddress, channelNo, channelType, hmenum.ParamsetKeyValues)
	return d, ch
}

// putStringSensor attaches a read-only string VALUES data point carrying
// valueList as its VALUE_LIST, mirroring how ACOUSTIC_ALARM_SELECTION /
// OPTICAL_ALARM_SELECTION / SOUNDFILE expose their label enumerations.
func putStringSensor(ch *device.Channel, p hmenum.Parameter, valueList ...string) {
	dp := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  valueList,
		},
	})
	ch.Put(dp)
}

// putOnTime attaches an ON_TIME VALUES parameter — the wire signal
// channelHasOnTime (candidates.go) treats as device-side auto-off
// support.
func putOnTime(ch *device.Channel) {
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterOnTime),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})
	ch.Put(dp)
}

// newSwitchChannel builds a channel carrying a *switchcdp.Switch custom
// data point (STATE wire DP + optional ON_TIME), attached via
// SetCustomDataPoint so ch.CustomDataPoint() resolves it.
func newSwitchChannel(t *testing.T, deviceAddress, channelAddress string, channelNo int, withOnTime bool) *device.Channel {
	t.Helper()
	_, ch := newTestChannel(t, deviceAddress, channelAddress, channelNo, "SWITCH")
	state := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddress,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(state)
	if withOnTime {
		putOnTime(ch)
	}
	sw := switchcdp.New(ch)
	if sw == nil {
		t.Fatal("switchcdp.New returned nil — STATE wire DP not resolved")
	}
	ch.SetCustomDataPoint(sw)
	return ch
}

// newLightChannel builds a channel carrying a *lightcdp.Light custom data
// point (LEVEL wire DP + optional ON_TIME).
func newLightChannel(t *testing.T, deviceAddress, channelAddress string, channelNo int, caps custom.LightCapabilities, withOnTime bool) *device.Channel {
	t.Helper()
	_, ch := newTestChannel(t, deviceAddress, channelAddress, channelNo, "DIMMER")
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: channelAddress,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(level)
	if withOnTime {
		putOnTime(ch)
	}
	l := lightcdp.New(lightcdp.Config{Channel: ch, Capabilities: caps})
	ch.SetCustomDataPoint(l)
	return ch
}

// newSirenChannel builds a channel carrying a *sirencdp.Siren custom data
// point. The acoustic/optical selection DPs (with their VALUE_LIST tone /
// light labels) are attached only for the capability requested, mirroring
// how a real HmIP-ASIR device only carries the selection parameter it
// actually supports.
func newSirenChannel(t *testing.T, deviceAddress, channelAddress string, caps custom.SirenCapabilities, tones, lights []string) *device.Channel {
	t.Helper()
	_, ch := newTestChannel(t, deviceAddress, channelAddress, 3, "SIREN")
	if caps.SupportsAcoustic {
		putStringSensor(ch, hmenum.ParameterAcousticAlarmSelection, tones...)
	}
	if caps.SupportsOptical {
		putStringSensor(ch, hmenum.ParameterOpticalAlarmSelection, lights...)
	}
	s := sirencdp.New(sirencdp.Config{Channel: ch, Capabilities: caps})
	ch.SetCustomDataPoint(s)
	return ch
}

// newSmokeSirenChannel builds a channel carrying a *sirencdp.SmokeSiren.
func newSmokeSirenChannel(t *testing.T, deviceAddress, channelAddress string) *device.Channel {
	t.Helper()
	_, ch := newTestChannel(t, deviceAddress, channelAddress, 1, "SMOKE_DETECTOR")
	s := sirencdp.NewSmokeSiren(sirencdp.SmokeSirenConfig{Channel: ch})
	ch.SetCustomDataPoint(s)
	return ch
}

// newSoundPlayerChannel builds a channel carrying a *sirencdp.SoundPlayer.
func newSoundPlayerChannel(t *testing.T, deviceAddress, channelAddress string, soundfiles []string) *device.Channel {
	t.Helper()
	_, ch := newTestChannel(t, deviceAddress, channelAddress, 2, "AUDIO")
	putStringSensor(ch, hmenum.ParameterSoundfile, soundfiles...)
	sp := sirencdp.NewSoundPlayer(sirencdp.SoundPlayerConfig{Channel: ch})
	ch.SetCustomDataPoint(sp)
	return ch
}

// stubCustomDP is a minimal device.AttachableDataPoint that does not match
// any case in outputCandidateFor's type switch — it exercises the default
// arm without pulling in a whole other custom-DP category package.
type stubCustomDP struct{}

func (stubCustomDP) DataPointKey() hmtypes.DataPointKey { return hmtypes.DataPointKey{} }

// --- outputCandidateFor -----------------------------------------------

func TestOutputCandidateForSirenBothCapabilities(t *testing.T) {
	t.Parallel()

	tones := []string{"DISABLE_ACOUSTIC_SIGNAL", "FREQUENCY_RISING"}
	lights := []string{"DISABLE_OPTICAL_SIGNAL", "BLINKING_RED"}
	ch := newSirenChannel(t, "ASIR0001", "ASIR0001:3", custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsOptical:  true,
	}, tones, lights)

	cand, ok := outputCandidateFor(ch)
	if !ok {
		t.Fatal("expected candidate for a fully-capable siren")
	}
	want := []hmenum.AlarmOutputClass{
		hmenum.AlarmOutputClassAcousticSiren,
		hmenum.AlarmOutputClassOpticalSiren,
		hmenum.AlarmOutputClassChirp,
	}
	if !slices.Equal(cand.Classes, want) {
		t.Errorf("Classes = %v, want %v (canonical order)", cand.Classes, want)
	}
	if !slices.Equal(cand.AvailableTones, tones) {
		t.Errorf("AvailableTones = %v, want %v", cand.AvailableTones, tones)
	}
	if !slices.Equal(cand.AvailableLights, lights) {
		t.Errorf("AvailableLights = %v, want %v", cand.AvailableLights, lights)
	}
	if cand.Kind != "siren" {
		t.Errorf("Kind = %q, want %q", cand.Kind, "siren")
	}
}

func TestOutputCandidateForSirenAcousticOnly(t *testing.T) {
	t.Parallel()

	ch := newSirenChannel(t, "ASIR0002", "ASIR0002:3", custom.SirenCapabilities{
		SupportsAcoustic: true,
	}, []string{"FREQUENCY_RISING"}, nil)

	cand, ok := outputCandidateFor(ch)
	if !ok {
		t.Fatal("expected candidate for an acoustic-only siren")
	}
	want := []hmenum.AlarmOutputClass{hmenum.AlarmOutputClassAcousticSiren, hmenum.AlarmOutputClassChirp}
	if !slices.Equal(cand.Classes, want) {
		t.Errorf("Classes = %v, want %v", cand.Classes, want)
	}
	if cand.AvailableLights != nil {
		t.Errorf("AvailableLights = %v, want nil (no optical capability)", cand.AvailableLights)
	}
}

// TestOutputCandidateForSirenOpticalOnly verifies that chirp is tied to
// acoustic support only — an optical-only siren must not carry chirp.
func TestOutputCandidateForSirenOpticalOnly(t *testing.T) {
	t.Parallel()

	ch := newSirenChannel(t, "ASIR0003", "ASIR0003:3", custom.SirenCapabilities{
		SupportsOptical: true,
	}, nil, []string{"BLINKING_RED"})

	cand, ok := outputCandidateFor(ch)
	if !ok {
		t.Fatal("expected candidate for an optical-only siren")
	}
	want := []hmenum.AlarmOutputClass{hmenum.AlarmOutputClassOpticalSiren}
	if !slices.Equal(cand.Classes, want) {
		t.Errorf("Classes = %v, want %v (chirp must not appear without acoustic support)", cand.Classes, want)
	}
}

func TestOutputCandidateForSirenNoCapabilitiesIsNotACandidate(t *testing.T) {
	t.Parallel()

	ch := newSirenChannel(t, "ASIR0004", "ASIR0004:3", custom.SirenCapabilities{}, nil, nil)

	_, ok := outputCandidateFor(ch)
	if ok {
		t.Error("a siren with neither acoustic nor optical support must not be a candidate")
	}
}

func TestOutputCandidateForSmokeSiren(t *testing.T) {
	t.Parallel()

	ch := newSmokeSirenChannel(t, "SWSD0001", "SWSD0001:1")

	cand, ok := outputCandidateFor(ch)
	if !ok {
		t.Fatal("expected candidate for a smoke siren")
	}
	want := []hmenum.AlarmOutputClass{hmenum.AlarmOutputClassSmokeSounder}
	if !slices.Equal(cand.Classes, want) {
		t.Errorf("Classes = %v, want %v", cand.Classes, want)
	}
	if cand.Kind != "siren_smoke" {
		t.Errorf("Kind = %q, want %q", cand.Kind, "siren_smoke")
	}
}

func TestOutputCandidateForSoundPlayer(t *testing.T) {
	t.Parallel()

	soundfiles := []string{"SOUNDFILE_001", "SOUNDFILE_002"}
	ch := newSoundPlayerChannel(t, "MP3P0001", "MP3P0001:2", soundfiles)

	cand, ok := outputCandidateFor(ch)
	if !ok {
		t.Fatal("expected candidate for a sound player")
	}
	want := []hmenum.AlarmOutputClass{hmenum.AlarmOutputClassChirp}
	if !slices.Equal(cand.Classes, want) {
		t.Errorf("Classes = %v, want %v", cand.Classes, want)
	}
	if !slices.Equal(cand.AvailableSoundfiles, soundfiles) {
		t.Errorf("AvailableSoundfiles = %v, want %v", cand.AvailableSoundfiles, soundfiles)
	}
	if cand.Kind != "siren_sound" {
		t.Errorf("Kind = %q, want %q", cand.Kind, "siren_sound")
	}
}

func TestOutputCandidateForSwitchWithoutOnTime(t *testing.T) {
	t.Parallel()

	ch := newSwitchChannel(t, "VCU0001", "VCU0001:4", 4, false)

	cand, ok := outputCandidateFor(ch)
	if !ok {
		t.Fatal("expected candidate for a plain switch")
	}
	want := []hmenum.AlarmOutputClass{hmenum.AlarmOutputClassAlarmLight}
	if !slices.Equal(cand.Classes, want) {
		t.Errorf("Classes = %v, want %v (no ON_TIME → no switched_siren)", cand.Classes, want)
	}
	if cand.Kind != "switch" {
		t.Errorf("Kind = %q, want %q", cand.Kind, "switch")
	}
}

// TestOutputCandidateForSwitchWithOnTime also pins the canonical class
// ordering: switched_siren precedes alarm_light in candidateClassOrder
// even though the production code appends alarm_light first.
func TestOutputCandidateForSwitchWithOnTime(t *testing.T) {
	t.Parallel()

	ch := newSwitchChannel(t, "VCU0002", "VCU0002:4", 4, true)

	cand, ok := outputCandidateFor(ch)
	if !ok {
		t.Fatal("expected candidate for a switch with ON_TIME")
	}
	want := []hmenum.AlarmOutputClass{hmenum.AlarmOutputClassSwitchedSiren, hmenum.AlarmOutputClassAlarmLight}
	if !slices.Equal(cand.Classes, want) {
		t.Errorf("Classes = %v, want %v (canonical order)", cand.Classes, want)
	}
}

func TestOutputCandidateForLightWithoutOnTime(t *testing.T) {
	t.Parallel()

	ch := newLightChannel(t, "BDT0001", "BDT0001:1", 1, custom.LightCapabilities{Dimmable: true}, false)

	cand, ok := outputCandidateFor(ch)
	if !ok {
		t.Fatal("expected candidate for a dimmer")
	}
	want := []hmenum.AlarmOutputClass{hmenum.AlarmOutputClassAlarmLight}
	if !slices.Equal(cand.Classes, want) {
		t.Errorf("Classes = %v, want %v", cand.Classes, want)
	}
	if !cand.Dimmable {
		t.Error("Dimmable = false, want true (Capabilities.Dimmable was true)")
	}
	if cand.Kind != "light" {
		t.Errorf("Kind = %q, want %q", cand.Kind, "light")
	}
}

func TestOutputCandidateForLightWithOnTime(t *testing.T) {
	t.Parallel()

	ch := newLightChannel(t, "BDT0002", "BDT0002:1", 1, custom.LightCapabilities{Dimmable: true}, true)

	cand, ok := outputCandidateFor(ch)
	if !ok {
		t.Fatal("expected candidate for a dimmer with ON_TIME")
	}
	want := []hmenum.AlarmOutputClass{hmenum.AlarmOutputClassSwitchedSiren, hmenum.AlarmOutputClassAlarmLight}
	if !slices.Equal(cand.Classes, want) {
		t.Errorf("Classes = %v, want %v", cand.Classes, want)
	}
}

func TestOutputCandidateForLightNotDimmable(t *testing.T) {
	t.Parallel()

	ch := newLightChannel(t, "BDT0003", "BDT0003:1", 1, custom.LightCapabilities{Dimmable: false}, false)

	cand, ok := outputCandidateFor(ch)
	if !ok {
		t.Fatal("expected candidate for a non-dimmable light wrapper")
	}
	if cand.Dimmable {
		t.Error("Dimmable = true, want false (Capabilities.Dimmable was false)")
	}
}

// TestOutputCandidateForNoCustomDataPoint verifies a channel with no
// attached custom DP (the common case for plain generic-DP channels) is
// never a candidate.
func TestOutputCandidateForNoCustomDataPoint(t *testing.T) {
	t.Parallel()

	_, ch := newTestChannel(t, "GEN0001", "GEN0001:1", 1, "SHUTTER_CONTACT")

	_, ok := outputCandidateFor(ch)
	if ok {
		t.Error("a channel without a custom data point must not be a candidate")
	}
}

// TestOutputCandidateForUnrecognisedCustomDataPoint verifies the default
// arm of the type switch for a custom DP type that carries none of the
// five recognised concrete types.
func TestOutputCandidateForUnrecognisedCustomDataPoint(t *testing.T) {
	t.Parallel()

	_, ch := newTestChannel(t, "GEN0002", "GEN0002:1", 1, "MAINTENANCE")
	ch.SetCustomDataPoint(stubCustomDP{})

	_, ok := outputCandidateFor(ch)
	if ok {
		t.Error("an unrecognised custom-DP type must not be a candidate")
	}
}

// --- DeviceBackedOutputClass --------------------------------------------

func TestDeviceBackedOutputClass(t *testing.T) {
	t.Parallel()

	cases := []struct {
		class hmenum.AlarmOutputClass
		want  bool
	}{
		{hmenum.AlarmOutputClassAcousticSiren, true},
		{hmenum.AlarmOutputClassOpticalSiren, true},
		{hmenum.AlarmOutputClassSwitchedSiren, true},
		{hmenum.AlarmOutputClassSmokeSounder, true},
		{hmenum.AlarmOutputClassAlarmLight, true},
		{hmenum.AlarmOutputClassChirp, true},
		{hmenum.AlarmOutputClassNotification, false},
		{hmenum.AlarmOutputClassSysvarMirror, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.class), func(t *testing.T) {
			t.Parallel()
			if got := DeviceBackedOutputClass(tc.class); got != tc.want {
				t.Errorf("DeviceBackedOutputClass(%q) = %v, want %v", tc.class, got, tc.want)
			}
		})
	}
}

// --- Service.OutputCandidates / OutputTargetEligible --------------------
//
// These drive a real *central.Registry + *central.Unit — construction is
// cheap and side-effect free (central.New only requires Config.Name; no
// client, backend or SQL store is touched by ModelDevices()/GetChannel()).
// Because this file lives in package alarm, the Service under test is
// built as a bare struct literal carrying only the field OutputCandidates
// and OutputTargetEligible read: reg.

// newCandidatesRegistry registers one *central.Unit per given name and
// seeds its ModelRegistry with devs.
func newCandidatesRegistry(t *testing.T, centralName string, devs ...*device.Device) *central.Registry {
	t.Helper()
	reg := central.NewRegistry()
	u, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	for _, d := range devs {
		u.ModelRegistry.Put(d)
	}
	if err := reg.Register(u); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	return reg
}

func TestOutputCandidatesEmptyRegistryReturnsNil(t *testing.T) {
	t.Parallel()

	s := &Service{reg: central.NewRegistry()}
	if got := s.OutputCandidates(""); got != nil {
		t.Errorf("OutputCandidates on an empty registry = %v, want nil", got)
	}
}

// TestOutputCandidatesOrderedByCentralThenDeviceThenChannel builds two
// centrals with out-of-order device addresses and channel numbers and
// verifies OutputCandidates sorts central → device address → channel no.
func TestOutputCandidatesOrderedByCentralThenDeviceThenChannel(t *testing.T) {
	t.Parallel()

	devB, chB := newTestChannel(t, "ZZZ0001", "ZZZ0001:2", 2, "SWITCH")
	putSwitchState(chB)
	ch2 := devB.AddChannel("ZZZ0001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	putSwitchState(ch2)
	ch2.SetCustomDataPoint(switchcdp.New(ch2))
	chB.SetCustomDataPoint(switchcdp.New(chB))

	devA, chA := newTestChannel(t, "AAA0001", "AAA0001:1", 1, "SWITCH")
	putSwitchState(chA)
	chA.SetCustomDataPoint(switchcdp.New(chA))

	reg := central.NewRegistry()
	unitB, err := central.New(central.Config{Name: "zz-central"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	unitB.ModelRegistry.Put(devB)
	if err := reg.Register(unitB); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	unitA, err := central.New(central.Config{Name: "aa-central"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	unitA.ModelRegistry.Put(devA)
	if err := reg.Register(unitA); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	s := &Service{reg: reg}
	got := s.OutputCandidates("")
	if len(got) != 3 {
		t.Fatalf("len(candidates) = %d, want 3: %+v", len(got), got)
	}
	// aa-central sorts before zz-central; within zz-central, channel :1
	// (Number 1) sorts before :2 (Number 2).
	if got[0].Central != "aa-central" {
		t.Errorf("got[0].Central = %q, want aa-central", got[0].Central)
	}
	if got[1].Central != "zz-central" || got[1].ChannelNo != 1 {
		t.Errorf("got[1] = %+v, want zz-central channel 1", got[1])
	}
	if got[2].Central != "zz-central" || got[2].ChannelNo != 2 {
		t.Errorf("got[2] = %+v, want zz-central channel 2", got[2])
	}
}

// putSwitchState attaches the STATE wire DP a *switchcdp.Switch requires.
func putSwitchState(ch *device.Channel) {
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
}

// TestOutputCandidatesPopulatesMetadata verifies the per-candidate
// metadata (central/device/channel identity) is stamped from the live
// model objects, not left zero-valued.
func TestOutputCandidatesPopulatesMetadata(t *testing.T) {
	t.Parallel()

	ch := newSwitchChannel(t, "VCU2128127", "VCU2128127:4", 4, false)
	dev := ch.Device()
	reg := newCandidatesRegistry(t, "my-ccu", dev)

	s := &Service{reg: reg}
	got := s.OutputCandidates("")
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1: %+v", len(got), got)
	}
	c := got[0]
	if c.Central != "my-ccu" {
		t.Errorf("Central = %q, want my-ccu", c.Central)
	}
	if c.DeviceAddress != "VCU2128127" {
		t.Errorf("DeviceAddress = %q, want VCU2128127", c.DeviceAddress)
	}
	if c.DeviceName != "Test Device" {
		t.Errorf("DeviceName = %q, want %q", c.DeviceName, "Test Device")
	}
	if c.Model != "HmIP-TEST" {
		t.Errorf("Model = %q, want HmIP-TEST", c.Model)
	}
	if c.ChannelAddress != "VCU2128127:4" {
		t.Errorf("ChannelAddress = %q, want VCU2128127:4", c.ChannelAddress)
	}
	if c.ChannelNo != 4 {
		t.Errorf("ChannelNo = %d, want 4", c.ChannelNo)
	}
}

// TestOutputCandidatesSkipsNonCandidateChannels verifies a channel
// without an eligible custom DP is silently skipped, next to one that is
// a candidate.
func TestOutputCandidatesSkipsNonCandidateChannels(t *testing.T) {
	t.Parallel()

	dev, plainCh := newTestChannel(t, "MIX0001", "MIX0001:1", 1, "SHUTTER_CONTACT")
	_ = plainCh // no custom DP attached — not a candidate
	swCh := dev.AddChannel("MIX0001:4", 4, "SWITCH", hmenum.ParamsetKeyValues)
	putSwitchState(swCh)
	swCh.SetCustomDataPoint(switchcdp.New(swCh))

	reg := newCandidatesRegistry(t, "mix-ccu", dev)
	s := &Service{reg: reg}

	got := s.OutputCandidates("")
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1 (plain channel must be skipped): %+v", len(got), got)
	}
	if got[0].ChannelAddress != "MIX0001:4" {
		t.Errorf("ChannelAddress = %q, want MIX0001:4", got[0].ChannelAddress)
	}
}

// TestOutputCandidatesClassFilter verifies the class parameter narrows
// the result to channels carrying that class, and an empty class
// returns every candidate.
func TestOutputCandidatesClassFilter(t *testing.T) {
	t.Parallel()

	dev, swCh := newTestChannel(t, "FLT0001", "FLT0001:4", 4, "SWITCH")
	putSwitchState(swCh)
	swCh.SetCustomDataPoint(switchcdp.New(swCh))
	smokeCh := dev.AddChannel("FLT0001:1", 1, "SMOKE_DETECTOR", hmenum.ParamsetKeyValues)
	smokeCh.SetCustomDataPoint(sirencdp.NewSmokeSiren(sirencdp.SmokeSirenConfig{Channel: smokeCh}))

	reg := newCandidatesRegistry(t, "flt-ccu", dev)
	s := &Service{reg: reg}

	all := s.OutputCandidates("")
	if len(all) != 2 {
		t.Fatalf("len(all) = %d, want 2", len(all))
	}

	smokeOnly := s.OutputCandidates(hmenum.AlarmOutputClassSmokeSounder)
	if len(smokeOnly) != 1 || smokeOnly[0].ChannelAddress != "FLT0001:1" {
		t.Errorf("smokeOnly = %+v, want just FLT0001:1", smokeOnly)
	}

	lightOnly := s.OutputCandidates(hmenum.AlarmOutputClassAlarmLight)
	if len(lightOnly) != 1 || lightOnly[0].ChannelAddress != "FLT0001:4" {
		t.Errorf("lightOnly = %+v, want just FLT0001:4", lightOnly)
	}

	none := s.OutputCandidates(hmenum.AlarmOutputClassOpticalSiren)
	if len(none) != 0 {
		t.Errorf("none = %+v, want empty (no channel supports optical_siren)", none)
	}
}

// --- Service.OutputTargetEligible ---------------------------------------

func TestOutputTargetEligibleNonDeviceBackedClassAlwaysEligible(t *testing.T) {
	t.Parallel()

	s := &Service{reg: central.NewRegistry()}
	eligible, known := s.OutputTargetEligible("unknown-central", "does-not-matter:1", hmenum.AlarmOutputClassNotification)
	if !eligible || !known {
		t.Errorf("eligible=%v known=%v, want true/true for a non-device-backed class", eligible, known)
	}
}

func TestOutputTargetEligibleUnknownCentralIsUnknownButSoftEligible(t *testing.T) {
	t.Parallel()

	s := &Service{reg: central.NewRegistry()}
	eligible, known := s.OutputTargetEligible("no-such-central", "VCU0001:4", hmenum.AlarmOutputClassAlarmLight)
	if !eligible {
		t.Error("eligible = false, want true (soft validation: unknown central must not block config save)")
	}
	if known {
		t.Error("known = true, want false (central is not registered)")
	}
}

func TestOutputTargetEligibleUnknownChannelIsUnknownButSoftEligible(t *testing.T) {
	t.Parallel()

	ch := newSwitchChannel(t, "VCU0003", "VCU0003:4", 4, false)
	reg := newCandidatesRegistry(t, "my-ccu", ch.Device())
	s := &Service{reg: reg}

	eligible, known := s.OutputTargetEligible("my-ccu", "VCU0003:99", hmenum.AlarmOutputClassAlarmLight)
	if !eligible {
		t.Error("eligible = false, want true (soft validation: unresolvable channel must not block config save)")
	}
	if known {
		t.Error("known = true, want false (channel address does not resolve)")
	}
}

// TestOutputTargetEligibleChannelNotACandidate verifies a resolvable
// channel that carries no eligible custom DP reports known=true (the
// central + channel both resolved) but eligible=false — this is a hard
// mismatch, not the soft-validation path.
func TestOutputTargetEligibleChannelNotACandidate(t *testing.T) {
	t.Parallel()

	_, plainCh := newTestChannel(t, "VCU0004", "VCU0004:1", 1, "SHUTTER_CONTACT")
	reg := newCandidatesRegistry(t, "my-ccu", plainCh.Device())
	s := &Service{reg: reg}

	eligible, known := s.OutputTargetEligible("my-ccu", "VCU0004:1", hmenum.AlarmOutputClassAlarmLight)
	if eligible {
		t.Error("eligible = true, want false (channel carries no alarm-eligible custom DP)")
	}
	if !known {
		t.Error("known = false, want true (central and channel both resolved)")
	}
}

// TestOutputTargetEligibleClassMismatch verifies a resolvable candidate
// channel that does not carry the requested class reports (false, true).
func TestOutputTargetEligibleClassMismatch(t *testing.T) {
	t.Parallel()

	ch := newSirenChannel(t, "ASIR0005", "ASIR0005:3", custom.SirenCapabilities{
		SupportsAcoustic: true,
	}, []string{"FREQUENCY_RISING"}, nil)
	reg := newCandidatesRegistry(t, "my-ccu", ch.Device())
	s := &Service{reg: reg}

	eligible, known := s.OutputTargetEligible("my-ccu", "ASIR0005:3", hmenum.AlarmOutputClassOpticalSiren)
	if eligible {
		t.Error("eligible = true, want false (siren has no optical support)")
	}
	if !known {
		t.Error("known = false, want true")
	}
}

// TestOutputTargetEligibleMatchingClass verifies the success path: a
// resolvable candidate channel carrying the requested class reports
// (true, true).
func TestOutputTargetEligibleMatchingClass(t *testing.T) {
	t.Parallel()

	ch := newSirenChannel(t, "ASIR0006", "ASIR0006:3", custom.SirenCapabilities{
		SupportsAcoustic: true,
	}, []string{"FREQUENCY_RISING"}, nil)
	reg := newCandidatesRegistry(t, "my-ccu", ch.Device())
	s := &Service{reg: reg}

	eligible, known := s.OutputTargetEligible("my-ccu", "ASIR0006:3", hmenum.AlarmOutputClassAcousticSiren)
	if !eligible || !known {
		t.Errorf("eligible=%v known=%v, want true/true", eligible, known)
	}
}

// --- Service.RemoteKeyCandidates -----------------------------------------
//
// RemoteKeyCandidates enumerates channels that emit the intent router's
// press-parameter dispatch set (remoteKeyParams). These fixtures attach
// generic events directly via [device.Channel.AttachGenericEvent] — no
// custom data point is involved, since the candidate is purely event-driven.

// stubGenericEvent is a minimal device.AttachableEvent: RemoteKeyCandidates
// only reads DataPointKey().Parameter, so a bare key/kind pair is enough
// (mirrors fakeEvent in internal/model/device/aggregate_test.go, redefined
// here because that type lives in a different package).
type stubGenericEvent struct {
	key hmtypes.DataPointKey
}

func (s *stubGenericEvent) DataPointKey() hmtypes.DataPointKey { return s.key }
func (s *stubGenericEvent) EventKind() string                  { return "homematic.keypress" }

// attachPressEvent attaches a generic event source carrying parameter p to
// ch, keyed under ch's own address.
func attachPressEvent(ch *device.Channel, p hmenum.Parameter) {
	ch.AttachGenericEvent(&stubGenericEvent{
		key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
	})
}

// newVirtualRemoteChannel builds a device whose Model marks it as one of
// the CCU's virtual-remote pseudo-devices ([device.Device.IsVirtualRemote]),
// with one channel ready for generic events.
func newVirtualRemoteChannel(t *testing.T, deviceAddress, channelAddress string, channelNo int) *device.Channel {
	t.Helper()
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Address:     deviceAddress,
		Model:       "HmIP-RCV-50",
		Name:        "Virtual Remote",
	})
	return d.AddChannel(channelAddress, channelNo, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)
}

// TestRemoteKeyCandidatesPressShortOnly verifies a channel emitting only
// PRESS_SHORT is a candidate carrying just that parameter.
func TestRemoteKeyCandidatesPressShortOnly(t *testing.T) {
	t.Parallel()

	_, ch := newTestChannel(t, "WRC0001", "WRC0001:1", 1, "KEY_TRANSCEIVER")
	attachPressEvent(ch, hmenum.ParameterPressShort)

	reg := newCandidatesRegistry(t, "my-ccu", ch.Device())
	s := &Service{reg: reg}

	got := s.RemoteKeyCandidates()
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1: %+v", len(got), got)
	}
	want := []string{"PRESS_SHORT"}
	if !slices.Equal(got[0].Parameters, want) {
		t.Errorf("Parameters = %v, want %v", got[0].Parameters, want)
	}
}

// TestRemoteKeyCandidatesBothPressParametersDispatchOrder verifies a
// channel emitting both PRESS_SHORT and PRESS_LONG lists both parameters
// in the router's dispatch order (PRESS_SHORT before PRESS_LONG), even
// though PRESS_LONG is attached first here and sorts before PRESS_SHORT
// alphabetically — proving the order comes from remoteKeyParams, not
// attach order or GenericEvents()'s own key-sorted iteration.
func TestRemoteKeyCandidatesBothPressParametersDispatchOrder(t *testing.T) {
	t.Parallel()

	_, ch := newTestChannel(t, "WRC0002", "WRC0002:1", 1, "KEY_TRANSCEIVER")
	attachPressEvent(ch, hmenum.ParameterPressLong)
	attachPressEvent(ch, hmenum.ParameterPressShort)

	reg := newCandidatesRegistry(t, "my-ccu", ch.Device())
	s := &Service{reg: reg}

	got := s.RemoteKeyCandidates()
	if len(got) != 1 {
		t.Fatalf("len(candidates) = %d, want 1: %+v", len(got), got)
	}
	want := []string{"PRESS_SHORT", "PRESS_LONG"}
	if !slices.Equal(got[0].Parameters, want) {
		t.Errorf("Parameters = %v, want %v (dispatch order)", got[0].Parameters, want)
	}
}

// TestRemoteKeyCandidatesUnrelatedEventIsNotACandidate verifies a channel
// whose only generic event is not a press parameter (e.g. SABOTAGE) is not
// a remote-key candidate.
func TestRemoteKeyCandidatesUnrelatedEventIsNotACandidate(t *testing.T) {
	t.Parallel()

	_, ch := newTestChannel(t, "WRC0003", "WRC0003:1", 1, "SHUTTER_CONTACT")
	attachPressEvent(ch, hmenum.ParameterSabotage)

	reg := newCandidatesRegistry(t, "my-ccu", ch.Device())
	s := &Service{reg: reg}

	got := s.RemoteKeyCandidates()
	if len(got) != 0 {
		t.Errorf("candidates = %+v, want none (no press parameter emitted)", got)
	}
}

// TestRemoteKeyCandidatesExcludeVirtualRemoteDevice verifies a device that
// device.IsVirtualRemote reports true for is excluded even though its
// channel emits PRESS_SHORT — virtual-remote press channels would flood
// the picker; binding one remains possible via the raw-JSON expert path.
func TestRemoteKeyCandidatesExcludeVirtualRemoteDevice(t *testing.T) {
	t.Parallel()

	ch := newVirtualRemoteChannel(t, "VIRT0001", "VIRT0001:1", 1)
	attachPressEvent(ch, hmenum.ParameterPressShort)

	reg := newCandidatesRegistry(t, "my-ccu", ch.Device())
	s := &Service{reg: reg}

	got := s.RemoteKeyCandidates()
	if len(got) != 0 {
		t.Errorf("candidates = %+v, want none (virtual-remote devices are excluded)", got)
	}
}

// TestRemoteKeyCandidatesOrderedByCentralThenDeviceThenChannelAndStampsMetadata
// mirrors TestOutputCandidatesOrderedByCentralThenDeviceThenChannel: two
// centrals with out-of-order device addresses and channel numbers, and
// verifies both the central -> device address -> channel number ordering
// and that every metadata field is stamped from the live model objects.
func TestRemoteKeyCandidatesOrderedByCentralThenDeviceThenChannelAndStampsMetadata(t *testing.T) {
	t.Parallel()

	devB, chB := newTestChannel(t, "ZZZ0002", "ZZZ0002:2", 2, "KEY_TRANSCEIVER")
	attachPressEvent(chB, hmenum.ParameterPressShort)
	ch2 := devB.AddChannel("ZZZ0002:1", 1, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)
	attachPressEvent(ch2, hmenum.ParameterPressShort)

	devA, chA := newTestChannel(t, "AAA0002", "AAA0002:1", 1, "KEY_TRANSCEIVER")
	attachPressEvent(chA, hmenum.ParameterPressShort)

	reg := central.NewRegistry()
	unitB, err := central.New(central.Config{Name: "zz-central"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	unitB.ModelRegistry.Put(devB)
	if err := reg.Register(unitB); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	unitA, err := central.New(central.Config{Name: "aa-central"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	unitA.ModelRegistry.Put(devA)
	if err := reg.Register(unitA); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	s := &Service{reg: reg}
	got := s.RemoteKeyCandidates()
	if len(got) != 3 {
		t.Fatalf("len(candidates) = %d, want 3: %+v", len(got), got)
	}
	if got[0].Central != "aa-central" {
		t.Errorf("got[0].Central = %q, want aa-central", got[0].Central)
	}
	if got[1].Central != "zz-central" || got[1].ChannelNo != 1 {
		t.Errorf("got[1] = %+v, want zz-central channel 1", got[1])
	}
	if got[2].Central != "zz-central" || got[2].ChannelNo != 2 {
		t.Errorf("got[2] = %+v, want zz-central channel 2", got[2])
	}

	c := got[0]
	if c.DeviceAddress != "AAA0002" {
		t.Errorf("DeviceAddress = %q, want AAA0002", c.DeviceAddress)
	}
	if c.DeviceName != "Test Device" {
		t.Errorf("DeviceName = %q, want %q", c.DeviceName, "Test Device")
	}
	if c.Model != "HmIP-TEST" {
		t.Errorf("Model = %q, want HmIP-TEST", c.Model)
	}
	if c.ChannelAddress != "AAA0002:1" {
		t.Errorf("ChannelAddress = %q, want AAA0002:1", c.ChannelAddress)
	}
	if c.ChannelNo != 1 {
		t.Errorf("ChannelNo = %d, want 1", c.ChannelNo)
	}
	if !slices.Equal(c.Parameters, []string{"PRESS_SHORT"}) {
		t.Errorf("Parameters = %v, want [PRESS_SHORT]", c.Parameters)
	}
}
