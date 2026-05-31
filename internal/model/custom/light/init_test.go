// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

// init_test.go — tests for the D.12 constructor registration.
//
// These tests verify that:
// 1. Every expected DeviceProfile has a constructor registered on the
// DefaultRegistry.
// 2. The constructors produce the correct concrete type when called with
// a fully hydrated channel.
// 3. Nil-channel paths do not panic.

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
)

// lightProfiles lists every DeviceProfile string the light init()
// must register.
var lightProfiles = []hmenum.DeviceProfile{
	hmenum.DeviceProfile("IPDimmer"),
	hmenum.DeviceProfile("RfDimmer"),
	hmenum.DeviceProfile("RfDimmerWithVirtChannel"),
	hmenum.DeviceProfile("IPDRGDALI"),
	hmenum.DeviceProfile("IPFixedColorLight"),
	hmenum.DeviceProfile("IPSimpleFixedColorLightWired"),
	hmenum.DeviceProfile("IPRGBW"),
	hmenum.DeviceProfile("RfDimmer_Color"),
	hmenum.DeviceProfile("RfDimmer_Color_Fixed"),
	hmenum.DeviceProfile("RfDimmer_Color_Temp"),
	// IPSoundPlayerLed is registered here (moved from siren).
	hmenum.DeviceProfileIPSoundPlayerLed,
}

// TestLightConstructorsRegistered verifies that every light profile has a
// non-nil constructor in the default registry after init().
func TestLightConstructorsRegistered(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	for _, p := range lightProfiles {
		ctor, ok := r.Constructor(p)
		if !ok {
			t.Errorf("no constructor registered for profile %q", p)
			continue
		}
		if ctor == nil {
			t.Errorf("nil constructor registered for profile %q", p)
		}
	}
}

// TestIPDimmerConstructorProducesLight verifies that the IPDimmer
// constructor returns a *Light (plain dimmable).
func TestIPDimmerConstructorProducesLight(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("IPDimmer"))
	if !ok {
		t.Fatal("IPDimmer constructor not registered")
	}

	ch := newDimmerChannel(t, "HmIP-BDT:1", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("IPDimmer constructor error: %v", err)
	}
	if _, ok := dp.(*Light); !ok {
		t.Errorf("IPDimmer constructor returned %T, want *Light", dp)
	}
}

// TestIPDRGDALIConstructorProducesDRGDaliLight verifies that the IPDRGDALI
// constructor returns a *DRGDaliLight.
func TestIPDRGDALIConstructorProducesDRGDaliLight(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("IPDRGDALI"))
	if !ok {
		t.Fatal("IPDRGDALI constructor not registered")
	}

	ch := newDimmerChannel(t, "HmIP-DRDI3:1", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("IPDRGDALI constructor error: %v", err)
	}
	if _, ok := dp.(*DRGDaliLight); !ok {
		t.Errorf("IPDRGDALI constructor returned %T, want *DRGDaliLight", dp)
	}
}

// TestIPFixedColorLightConstructorProducesFixedColorLight verifies that the
// IPFixedColorLight constructor returns a *FixedColorLight.
func TestIPFixedColorLightConstructorProducesFixedColorLight(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("IPFixedColorLight"))
	if !ok {
		t.Fatal("IPFixedColorLight constructor not registered")
	}

	ch := newColorChannel(t, "HmIP-BSL:1", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("IPFixedColorLight constructor error: %v", err)
	}
	if _, ok := dp.(*FixedColorLight); !ok {
		t.Errorf("IPFixedColorLight constructor returned %T, want *FixedColorLight", dp)
	}
}

// TestIPRGBWConstructorProducesRGBWLight verifies that the IPRGBW
// constructor returns a *RGBWLight.
func TestIPRGBWConstructorProducesRGBWLight(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("IPRGBW"))
	if !ok {
		t.Fatal("IPRGBW constructor not registered")
	}

	ch := newDimmerChannel(t, "HmIP-RGBW:1", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("IPRGBW constructor error: %v", err)
	}
	if _, ok := dp.(*RGBWLight); !ok {
		t.Errorf("IPRGBW constructor returned %T, want *RGBWLight", dp)
	}
}

// TestRfDimmerColorConstructorProducesColorLight verifies the RF colour
// dimmer path returns an *EffectLight (carries PROGRAM effect presets).
func TestRfDimmerColorConstructorProducesColorLight(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("RfDimmer_Color"))
	if !ok {
		t.Fatal("RfDimmer_Color constructor not registered")
	}

	ch := newColorChannel(t, "HM-DW-WM:1", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("RfDimmer_Color constructor error: %v", err)
	}
	if _, ok := dp.(*EffectLight); !ok {
		t.Errorf("RfDimmer_Color constructor returned %T, want *EffectLight", dp)
	}
}

// TestRfDimmerColorTempConstructorProducesColorTempLight verifies the RF
// colour-temperature dimmer path returns a *ColorTempLight.
func TestRfDimmerColorTempConstructorProducesColorTempLight(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("RfDimmer_Color_Temp"))
	if !ok {
		t.Fatal("RfDimmer_Color_Temp constructor not registered")
	}

	ch := newDimmerChannel(t, "HM-LC-DW-WM:1", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("RfDimmer_Color_Temp constructor error: %v", err)
	}
	if _, ok := dp.(*ColorTempLight); !ok {
		t.Errorf("RfDimmer_Color_Temp constructor returned %T, want *ColorTempLight", dp)
	}
}

// TestIPDimmerConstructorNilChannelDoesNotPanic verifies that nil channel
// does not panic.
func TestIPDimmerConstructorNilChannelDoesNotPanic(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("IPDimmer"))
	if !ok {
		t.Fatal("IPDimmer constructor not registered")
	}

	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("IPDimmer constructor panicked with nil channel: %v", rec)
		}
	}()
	dp, err := ctor(nil, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dp == nil {
		t.Fatal("constructor returned nil DP")
	}
}

// TestLightConstructorCapabilitiesDimmable verifies that the dimmable
// capability flag is set on lights built by the IPDimmer constructor.
func TestLightConstructorCapabilitiesDimmable(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("IPDimmer"))
	if !ok {
		t.Fatal("IPDimmer constructor not registered")
	}

	ch := newDimmerChannel(t, "HmIP-BDT:1", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	light, ok := dp.(*Light)
	if !ok {
		t.Fatalf("got %T, want *Light", dp)
	}
	if !light.Capabilities.Dimmable {
		t.Error("IPDimmer Light.Capabilities.Dimmable must be true")
	}
}

// --- helpers ---

// newDimmerChannel returns a channel with a single LEVEL data point.
func newDimmerChannel(t *testing.T, address string, w Writer) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "DIMMER", hmenum.ParamsetKeyValues)
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(level)
	return ch
}

// newColorChannel returns a channel with LEVEL + HUE + SATURATION data
// points (used for ColorLight and FixedColorLight tests — FixedColorLight
// actually uses COLOR, but the constructor falls back gracefully to nil).
func newColorChannel(t *testing.T, address string, w Writer) *device.Channel {
	t.Helper()
	ch := newDimmerChannel(t, address, w)

	mkInt := func(p hmenum.Parameter) {
		dp := generic.NewInteger(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeInteger,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
			Writer: w,
		})
		ch.Put(dp)
	}
	mkInt(hmenum.ParameterHue)
	return ch
}

// TestHmIPMP3PChannel6IsLight is a snapshot-assertion contract test that
// verifies the HmIP-MP3P channel-6 LED strip is registered under the light
// category (fix). It asserts:
//
// 1. IPSoundPlayerLed has a constructor in the DefaultRegistry. 2. The
// constructor produces a *SoundPlayerLED — a light-domain type — NOT a
// *siren.SoundPlayer.
func TestHmIPMP3PChannel6IsLight(t *testing.T) {
	t.Parallel()

	reg := custom.DefaultRegistry()
	ctor, ok := reg.Constructor(hmenum.DeviceProfileIPSoundPlayerLed)
	if !ok {
		t.Fatal("IPSoundPlayerLed constructor not registered in DefaultRegistry — light/init.go init() must register it")
	}

	// Build a minimal channel that the SoundPlayerLED constructor can use.
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF, Address: "VCU1543608"})
	ch := d.AddChannel("VCU1543608:6", 6, "DIMMER_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)

	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("IPSoundPlayerLed constructor error: %v", err)
	}
	if dp == nil {
		t.Fatal("IPSoundPlayerLed constructor returned nil")
	}

	if _, ok := dp.(*SoundPlayerLED); !ok {
		t.Errorf("IPSoundPlayerLed constructor returned %T, want *light.SoundPlayerLED", dp)
	}
}
