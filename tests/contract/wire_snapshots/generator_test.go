// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build snapshot_gen

// generator_test.go generates golden wire-snapshot files for every
// Custom-DP setter covered by this package.
//
// Run via:
//
//	go test -tags=snapshot_gen ./tests/contract/wire_snapshots/
//
// Each snapshot is written to snapshots/<DPType>__<Setter>.json.
// Existing files are overwritten so that running the generator after
// a production-code change refreshes the golden baseline.
package wire_snapshots

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	"github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/custom/textdisplay"
	"github.com/SukramJ/openccu-loom/internal/model/custom/valve"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// snapshotsDir returns the absolute path to the snapshots/ sub-directory
// relative to this source file so the test works regardless of the working
// directory it is run from.
func snapshotsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "snapshots")
}

func writeSnapshot(t *testing.T, dir string, sf SnapshotFile) {
	t.Helper()
	data, err := MarshalSnapshot(sf)
	if err != nil {
		t.Fatalf("marshal snapshot %s/%s: %v", sf.DPType, sf.Setter, err)
	}
	path := filepath.Join(dir, SnapshotFileName(sf.DPType, sf.Setter))
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write snapshot %s: %v", path, err)
	}
	t.Logf("wrote %s", path)
}

// --- fixture constructors -----------------------------------------------

func newSwitchFixture(t *testing.T, w *fakeWriter) *switchdev.Switch {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0001"})
	ch := d.AddChannel("VCU0001:3", 4, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "VCU0001:3",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(dp)
	return switchdev.New(ch, custom.RebasedChannelGroupConfig{})
}

func newCoverFixture(t *testing.T, w *fakeWriter) *cover.Cover {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ROLL0001"})
	ch := d.AddChannel("ROLL0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "ROLL0001:1",
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
	return cover.New(cover.Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: cover.CoverCaps,
	})
}

func newBlindFixture(t *testing.T, w *fakeWriter) *cover.Blind {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "BBL0001"})
	ch := d.AddChannel("BBL0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	for _, param := range []hmenum.Parameter{hmenum.ParameterLevel, hmenum.ParameterLevel2} {
		fp := generic.NewFloat(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "BBL0001:1",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(param),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeFloat,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			},
			Writer: w,
		})
		ch.Put(fp)
	}
	return cover.NewBlind(cover.BlindConfig{
		Channel:      ch,
		Writer:       w,
		Capabilities: cover.BlindCaps,
		Kind:         cover.BlindKindIP,
	})
}

func newClimateIPFixture(t *testing.T, w *fakeWriter) *climate.Climate {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "BWTH0001"})
	ch := d.AddChannel("BWTH0001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	floatParams := []struct {
		param hmenum.Parameter
		ops   hmenum.Operations
	}{
		{hmenum.ParameterSetPointTemperature, hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		{hmenum.ParameterActualTemperature, hmenum.OperationsRead | hmenum.OperationsEvent},
		{hmenum.ParameterHumidity, hmenum.OperationsRead | hmenum.OperationsEvent},
	}
	for _, fp := range floatParams {
		dp := generic.NewFloat(generic.Spec{
			Key:         hmtypes.DataPointKey{ChannelAddress: "BWTH0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(fp.param)},
			Descriptor:  hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: fp.ops},
			Writer:      w,
			CentralName: "testcentral",
		})
		ch.Put(dp)
	}
	activeProfile := generic.NewInteger(generic.Spec{
		Key:         hmtypes.DataPointKey{ChannelAddress: "BWTH0001:1", ParamsetKey: hmenum.ParamsetKeyMaster, Parameter: string(hmenum.ParameterActiveProfile)},
		Descriptor:  hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite, Min: json.RawMessage("0"), Max: json.RawMessage("5")},
		Writer:      w,
		CentralName: "testcentral",
	})
	ch.PutMaster(activeProfile)

	caps := custom.ClimateCapabilities{
		SupportsBoost:   true,
		SupportsProfile: true,
		SupportsAuto:    true,
		SupportsHeat:    true,
		SupportsOff:     true,
		MinTemperature:  5,
		MaxTemperature:  30,
		TemperatureStep: 0.5,
	}
	return climate.New(climate.Config{Channel: ch, Writer: w, Capabilities: caps, Kind: climate.KindIP})
}

func newIrrigationFixture(t *testing.T, w *fakeWriter) *valve.Irrigation {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "WHS0001"})
	ch := d.AddChannel("WHS0001:1", 1, "VALVE", hmenum.ParamsetKeyValues)
	dp := generic.NewSwitch(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "WHS0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterState)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(dp)
	return valve.NewIrrigation(ch, custom.RebasedChannelGroupConfig{})
}

func newModulatingFixture(t *testing.T, w *fakeWriter) *valve.Modulating {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MOD0001"})
	ch := d.AddChannel("MOD0001:1", 1, "VALVE", hmenum.ParamsetKeyValues)
	dp := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "MOD0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(dp)
	return valve.NewModulating(ch, custom.RebasedChannelGroupConfig{})
}

func newLockFixture(t *testing.T, w *fakeWriter) *lock.Lock {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DLD0001"})
	ch := d.AddChannel("DLD0001:1", 1, "LOCK", hmenum.ParamsetKeyValues)

	// IP lock: LOCK_TARGET_LEVEL (write), LOCK_STATE + DIRECTION (string).
	for _, p := range []hmenum.Parameter{hmenum.ParameterLockState, hmenum.ParameterDirection} {
		dp := generic.NewStringSensor(generic.Spec{
			Key:        hmtypes.DataPointKey{ChannelAddress: "DLD0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(p)},
			Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeEnum, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
			Writer:     w,
		})
		ch.Put(dp)
	}
	ltl := generic.NewStringSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "DLD0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLockTargetLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeEnum, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(ltl)

	caps := custom.LockCapabilities{SupportsOpen: true}
	return lock.New(lock.Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: caps,
		Kind:         lock.KindIP,
	})
}

func newSirenFixture(t *testing.T, w *fakeWriter) *siren.Siren {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF, Address: "ASIR0001"})
	ch := d.AddChannel("ASIR0001:3", 3, "SIREN", hmenum.ParamsetKeyValues)

	for _, p := range []hmenum.Parameter{hmenum.ParameterAcousticAlarmActive, hmenum.ParameterOpticalAlarmActive} {
		dp := generic.NewSwitch(generic.Spec{
			Key:        hmtypes.DataPointKey{ChannelAddress: "ASIR0001:3", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(p)},
			Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
			Writer:     w,
		})
		ch.Put(dp)
	}
	// The alarm-selection parameters are write-only ENUMs on the wire
	// (OPERATIONS=2) with their own VALUE_LIST and a string-labelled
	// DEFAULT, so the resolver builds an ActionSelect for each. The
	// previous fixture modelled them as readable string sensors sharing
	// the acoustic value list, which is why the recorded snapshot showed
	// the acoustic disable label being written to the optical parameter.
	for _, sel := range []struct {
		param  hmenum.Parameter
		values []string
	}{
		{hmenum.ParameterAcousticAlarmSelection, []string{"DISABLE_ACOUSTIC_SIGNAL", "FREQUENCY_RISING", "FREQUENCY_FALLING"}},
		{hmenum.ParameterOpticalAlarmSelection, []string{"DISABLE_OPTICAL_SIGNAL", "BLINKING_ALTERNATELY_REPEATING", "BLINKING_BOTH_REPEATING"}},
	} {
		ch.Put(generic.NewActionSelect(generic.Spec{
			Key: hmtypes.DataPointKey{ChannelAddress: "ASIR0001:3", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(sel.param)},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeEnum,
				Operations: hmenum.OperationsWrite,
				ValueList:  sel.values,
				Default:    []byte(`"` + sel.values[0] + `"`),
			},
			Writer: w,
		}))
	}

	caps := custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true, SupportsDuration: false}
	return siren.New(siren.Config{Channel: ch, Writer: w, Capabilities: caps})
}

// --- new fixture constructors -------------------------------------------

// newLightFixture builds a minimal dimmable Light (LEVEL only).
func newLightFixture(t *testing.T, w *fakeWriter) *light.Light {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DIM0001"})
	ch := d.AddChannel("DIM0001:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	fp := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "DIM0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(fp)
	caps := custom.LightCapabilities{Dimmable: true}
	return light.New(light.Config{Channel: ch, Writer: w, Capabilities: caps})
}

// newColorLightFixture builds a ColorLight (LEVEL + HUE + SATURATION).
func newColorLightFixture(t *testing.T, w *fakeWriter) *light.ColorLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "COL0001"})
	ch := d.AddChannel("COL0001:1", 1, "COLOR", hmenum.ParamsetKeyValues)
	for _, spec := range []struct {
		param hmenum.Parameter
		typ   hmenum.ParameterType
	}{
		{hmenum.ParameterLevel, hmenum.ParameterTypeFloat},
	} {
		fp := generic.NewFloat(generic.Spec{
			Key:        hmtypes.DataPointKey{ChannelAddress: "COL0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(spec.param)},
			Descriptor: hmproto.ParameterData{Type: spec.typ, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
			Writer:     w,
		})
		ch.Put(fp)
	}
	ip := generic.NewInteger(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "COL0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterHue)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(ip)
	sat := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "COL0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterSaturation)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(sat)
	caps := custom.LightCapabilities{Dimmable: true}
	return light.NewColorLight(light.Config{Channel: ch, Writer: w, Capabilities: caps})
}

// newColorTempLightFixture builds a ColorTempLight (LEVEL + COLOR_TEMPERATURE).
func newColorTempLightFixture(t *testing.T, w *fakeWriter) *light.ColorTempLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "CT0001"})
	ch := d.AddChannel("CT0001:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	fp := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "CT0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(fp)
	kp := generic.NewInteger(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "CT0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterColorTemperature)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(kp)
	caps := custom.LightCapabilities{Dimmable: true}
	return light.NewColorTempLight(light.Config{Channel: ch, Writer: w, Capabilities: caps}, 2000, 6500)
}

// newFixedColorLightFixture builds a FixedColorLight (LEVEL + COLOR + COLOR_BEHAVIOUR).
func newFixedColorLightFixture(t *testing.T, w *fakeWriter) *light.FixedColorLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "FC0001"})
	ch := d.AddChannel("FC0001:1", 1, "COLOR", hmenum.ParamsetKeyValues)
	fp := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "FC0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(fp)
	colorSel := generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "FC0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterColor)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"BLACK", "BLUE", "GREEN", "TURQUOISE", "RED", "PURPLE", "YELLOW", "WHITE"},
		},
		Writer: w,
	})
	ch.Put(colorSel)
	// COLOR_BEHAVIOUR as an HmIP-BSL declares it. MIN/MAX/DEFAULT are part of
	// the fixture because they carry the parameter's value domain: all three
	// are VALUE_LIST labels here, which is what makes the label — not the
	// index — the wire form. A descriptor without them encodes no device.
	cbSel := generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "FC0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterColorBehaviour)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage(`"OFF"`),
			Max:        json.RawMessage(`"DO_NOT_CARE"`),
			Default:    json.RawMessage(`"OFF"`),
			ValueList: []string{
				"OFF", "ON", "BLINKING_SLOW", "BLINKING_MIDDLE", "BLINKING_FAST",
				"FLASH_SLOW", "FLASH_MIDDLE", "FLASH_FAST",
				"BILLOW_SLOW", "BILLOW_MIDDLE", "BILLOW_FAST",
				"OLD_VALUE", "DO_NOT_CARE",
			},
		},
		Writer: w,
	})
	ch.Put(cbSel)
	caps := custom.LightCapabilities{Dimmable: true}
	return light.NewFixedColorLight(light.Config{Channel: ch, Writer: w, Capabilities: caps})
}

// newEffectLightFixture builds an EffectLight with a predefined effects list.
func newEffectLightFixture(t *testing.T, w *fakeWriter) *light.EffectLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "EFF0001"})
	ch := d.AddChannel("EFF0001:1", 1, "COLOR", hmenum.ParamsetKeyValues)
	fp := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "EFF0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(fp)
	ip := generic.NewInteger(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "EFF0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterHue)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(ip)
	sat := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "EFF0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterSaturation)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(sat)
	prog := generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "EFF0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterProgram)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"Off", "Slow color change", "Fast color change", "Campfire", "Waterfall"},
		},
		Writer: w,
	})
	ch.Put(prog)
	caps := custom.LightCapabilities{Dimmable: true}
	return light.NewEffectLight(light.Config{Channel: ch, Writer: w, Capabilities: caps})
}

// newDaliLightFixture builds a DRGDaliLight (LEVEL + COLOR_TEMPERATURE + EFFECT).
func newDaliLightFixture(t *testing.T, w *fakeWriter) *light.DRGDaliLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DALI0001"})
	ch := d.AddChannel("DALI0001:1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	fp := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "DALI0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(fp)
	kp := generic.NewInteger(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "DALI0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterColorTemperature)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(kp)
	// EFFECT is a write-only action-select: labels are sent as integer indices on the wire.
	ep := generic.NewActionSelect(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "DALI0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterEffect)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"Off", "Slow_color_change", "Medium_color_change", "Fast_color_change", "Flash", "Smooth_slow", "Smooth_fast"},
		},
		Writer: w,
	})
	ch.Put(ep)
	caps := custom.LightCapabilities{Dimmable: true}
	return light.NewDRGDaliLight(light.Config{Channel: ch, Writer: w, Capabilities: caps}, 2000, 6500)
}

// newRGBWLightFixture builds an RGBWLight with LEVEL + HUE + SATURATION + KELVIN + EFFECT.
// Mode is pre-set to RGB by calling recordMode via the Subscribe pathway.
func newRGBWLightFixture(t *testing.T, w *fakeWriter) *light.RGBWLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "RGBW0001"})
	ch := d.AddChannel("RGBW0001:1", 1, "COLOR", hmenum.ParamsetKeyValues)
	fp := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "RGBW0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(fp)
	ip := generic.NewInteger(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "RGBW0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterHue)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(ip)
	sat := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "RGBW0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterSaturation)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(sat)
	modeSensor := generic.NewStringSensor(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "RGBW0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterDeviceOperationMode)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeEnum, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(modeSensor)
	effectSel := generic.NewActionSelect(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "RGBW0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterEffect)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"BLINKING_SLOW", "BLINKING_FAST", "FLASH_SHORT", "RAMPING_CONTINUOUS"},
		},
		Writer: w,
	})
	ch.Put(effectSel)
	caps := custom.LightCapabilities{Dimmable: true}
	r := light.NewRGBWLight(light.Config{Channel: ch, Writer: w, Capabilities: caps})
	// Seed RGB mode so SetColor and SetEffect are accepted.
	modeSensor.OnEvent("RGB")
	r.Subscribe(ch)
	return r
}

// newSoundPlayerLEDFixture builds a SoundPlayerLED (FixedColorLight + ON_TIME_LIST + REPETITIONS).
func newSoundPlayerLEDFixture(t *testing.T, w *fakeWriter) (*light.SoundPlayerLED, string) {
	t.Helper()
	const addr = "MP3P0001:6"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MP3P0001"})
	ch := d.AddChannel(addr, 6, "LIGHT", hmenum.ParamsetKeyValues)
	fp := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: addr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(fp)
	colorSel := generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: addr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterColor)},
		Descriptor: hmproto.ParameterData{
			Type:      hmenum.ParameterTypeEnum,
			ValueList: []string{"BLACK", "BLUE", "GREEN", "TURQUOISE", "RED", "PURPLE", "YELLOW", "WHITE"},
		},
		Writer: w,
	})
	ch.Put(colorSel)
	otl := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: addr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterOnTimeList1)},
		Descriptor: hmproto.ParameterData{
			Type:      hmenum.ParameterTypeEnum,
			ValueList: []string{"100MS", "200MS", "500MS", "1S", "2S", "5S", "PERMANENTLY_ON"},
		},
		Writer: w,
	})
	ch.Put(otl)
	reps := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: addr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterRepetitions)},
		Descriptor: hmproto.ParameterData{
			Type:      hmenum.ParameterTypeEnum,
			ValueList: []string{"NO_REPETITION", "REPETITIONS_001", "REPETITIONS_002", "INFINITE_REPETITIONS"},
		},
		Writer: w,
	})
	ch.Put(reps)
	caps := custom.LightCapabilities{Dimmable: true}
	return light.NewSoundPlayerLED(light.Config{Channel: ch, Writer: w, Capabilities: caps}), addr
}

// newTextDisplayFixture builds a minimal TextDisplay.
func newTextDisplayFixture(_ *testing.T, w *fakeWriter) *textdisplay.TextDisplay {
	return textdisplay.New("SDV0001:1", w)
}

// newClimateRFFixture builds a Climate with KindRF and RF-specific parameters.
func newClimateRFFixture(t *testing.T, w *fakeWriter) *climate.Climate {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "BidCos-RF", Address: "RFTHR0001"})
	ch := d.AddChannel("RFTHR0001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	for _, pp := range []struct {
		param hmenum.Parameter
		ops   hmenum.Operations
	}{
		{hmenum.ParameterSetTemperature, hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		{hmenum.ParameterActualTemperature, hmenum.OperationsRead | hmenum.OperationsEvent},
	} {
		fp := generic.NewFloat(generic.Spec{
			Key:         hmtypes.DataPointKey{ChannelAddress: "RFTHR0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(pp.param)},
			Descriptor:  hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: pp.ops},
			Writer:      w,
			CentralName: "testcentral",
		})
		ch.Put(fp)
	}
	// WEEK_PROGRAM_POINTER and TEMPERATURE_OFFSET live on the device-root MASTER
	// paramset on classic RF thermostats (real device: HM-TC-IT-WM-W-EU
	// VCU0000341/MASTER — they exist in no VALUES paramset), so the write must
	// target the device root, not the climate channel's VALUES.
	root := d.EnsureRootChannel()
	for _, p := range []hmenum.Parameter{hmenum.ParameterWeekProgramPointer, hmenum.ParameterTemperatureOffset} {
		root.PutMaster(generic.NewInteger(generic.Spec{
			Key:         hmtypes.DataPointKey{ChannelAddress: d.Address, ParamsetKey: hmenum.ParamsetKeyMaster, Parameter: string(p)},
			Descriptor:  hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
			Writer:      w,
			CentralName: "testcentral",
		}))
	}
	caps := custom.ClimateCapabilities{
		SupportsBoost:   true,
		SupportsProfile: true,
		SupportsAuto:    true,
		SupportsHeat:    true,
		SupportsOff:     true,
		SupportsComfort: true,
		SupportsEco:     true,
		MinTemperature:  5,
		MaxTemperature:  30,
		TemperatureStep: 0.5,
	}
	return climate.New(climate.Config{Channel: ch, Writer: w, Capabilities: caps, Kind: climate.KindRF})
}

// newGarageFixture builds a Garage custom DP.
func newGarageFixture(t *testing.T, w *fakeWriter) *cover.Garage {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MOHO0001"})
	ch := d.AddChannel("MOHO0001:1", 1, "GARAGE_DOOR", hmenum.ParamsetKeyValues)
	doorState := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "MOHO0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterDoorState)},
		Descriptor: hmproto.ParameterData{
			Type:      hmenum.ParameterTypeEnum,
			ValueList: []string{"UNKNOWN", "OPEN", "CLOSED", "VENTILATION_POSITION"},
		},
		Writer: w,
	})
	ch.Put(doorState)
	doorCmd := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "MOHO0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterDoorCommand)},
		Descriptor: hmproto.ParameterData{
			Type:      hmenum.ParameterTypeEnum,
			ValueList: []string{"OPEN", "CLOSE", "STOP", "PARTIAL_OPEN", "NOP"},
		},
		Writer: w,
	})
	ch.Put(doorCmd)
	caps := custom.CoverCapabilities{SupportsVent: true}
	return cover.NewGarage(cover.GarageConfig{Channel: ch, Writer: w, Capabilities: caps})
}

// newSmokeSirenFixture builds a SmokeSiren custom DP.
func newSmokeSirenFixture(t *testing.T, w *fakeWriter) *siren.SmokeSiren {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SWSD0001"})
	ch := d.AddChannel("SWSD0001:1", 1, "SMOKE_DETECTOR", hmenum.ParamsetKeyValues)
	statusDP := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "SWSD0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterSmokeDetectorAlarmStatus)},
		Descriptor: hmproto.ParameterData{
			Type:      hmenum.ParameterTypeEnum,
			ValueList: []string{"IDLE_OFF", "IDLE_ON", "PRIMARY_ALARM", "SECONDARY_ALARM", "INTRUSION_ALARM"},
		},
		Writer: w,
	})
	ch.Put(statusDP)
	cmdDP := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "SWSD0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterSmokeDetectorCommand)},
		Descriptor: hmproto.ParameterData{
			Type:      hmenum.ParameterTypeEnum,
			ValueList: []string{"INTRUSION_ALARM", "INTRUSION_ALARM_OFF"},
		},
		Writer: w,
	})
	ch.Put(cmdDP)
	return siren.NewSmokeSiren(siren.SmokeSirenConfig{Channel: ch, Writer: w})
}

// newSoundPlayerFixture builds a SoundPlayer custom DP.
func newSoundPlayerFixture(t *testing.T, w *fakeWriter) *siren.SoundPlayer {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MP3P0001"})
	ch := d.AddChannel("MP3P0001:2", 2, "SOUND_PLAYER", hmenum.ParamsetKeyValues)
	levelDP := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "MP3P0001:2", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(levelDP)
	sfDP := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "MP3P0001:2", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterSoundfile)},
		Descriptor: hmproto.ParameterData{
			Type:      hmenum.ParameterTypeEnum,
			ValueList: buildSoundfileList(),
		},
		Writer: w,
	})
	ch.Put(sfDP)
	repDP := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "MP3P0001:2", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterRepetitions)},
		Descriptor: hmproto.ParameterData{
			Type:      hmenum.ParameterTypeEnum,
			ValueList: []string{"NO_REP", "REPETITIONS_2", "REPETITIONS_5", "INFINITE"},
		},
		Writer: w,
	})
	ch.Put(repDP)
	return siren.NewSoundPlayer(siren.SoundPlayerConfig{Channel: ch, Writer: w})
}

// buildSoundfileList returns a minimal VALUE_LIST for the SOUNDFILE parameter.
// The HmIP-MP3P ships SOUNDFILE_001..SOUNDFILE_252; we use a 5-entry stub.
func buildSoundfileList() []string {
	return []string{"SOUNDFILE_001", "SOUNDFILE_002", "SOUNDFILE_003", "SOUNDFILE_004", "SOUNDFILE_005"}
}

// --- generator entries --------------------------------------------------

// entry describes one (setter, inputs) pair to generate.
type entry struct {
	dpType string
	setter string
	run    func(t *testing.T, w *fakeWriter) []SnapshotEntry
}

// helpers.
func ptr[T any](v T) *T { return &v }

func TestGenerateWireSnapshots(t *testing.T) {
	dir := snapshotsDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir snapshots: %v", err)
	}

	ctx := context.Background()
	pri := hmenum.CommandPriorityHigh

	entries := []entry{
		// ── Switch ───────────────────────────────────────────────────────────
		{
			dpType: "Switch",
			setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				sw := newSwitchFixture(t, w)
				_ = sw.TurnOn(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		{
			dpType: "Switch",
			setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				sw := newSwitchFixture(t, w)
				sw.OnEvent(true) // seed so TurnOff is a state change
				_ = sw.TurnOff(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		{
			dpType: "Switch",
			setter: "TurnOnFor",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				sw := newSwitchFixture(t, w)
				_ = sw.TurnOnFor(ctx, 60*time.Second, pri)
				return []SnapshotEntry{{Label: "duration=60s", Calls: w.Capture()}}
			},
		},
		// ── Cover ────────────────────────────────────────────────────────────
		{
			dpType: "Cover",
			setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				c := newCoverFixture(t, w)
				_ = c.Open(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		{
			dpType: "Cover",
			setter: "Close",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				c := newCoverFixture(t, w)
				_ = c.Close(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		{
			dpType: "Cover",
			setter: "SetPosition",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				levels := []struct {
					label string
					v     float64
				}{
					{"level=0.0", 0.0},
					{"level=0.5", 0.5},
					{"level=1.0", 1.0},
				}
				var entries []SnapshotEntry
				for _, l := range levels {
					c := newCoverFixture(t, w)
					_ = c.SetPosition(ctx, l.v, pri)
					entries = append(entries, SnapshotEntry{Label: l.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		// ── Blind ────────────────────────────────────────────────────────────
		{
			dpType: "Blind",
			setter: "SetTilt",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				tilts := []struct {
					label string
					v     float64
				}{
					{"tilt=0.0", 0.0},
					{"tilt=0.5", 0.5},
					{"tilt=1.0", 1.0},
				}
				var entries []SnapshotEntry
				for _, tl := range tilts {
					b := newBlindFixture(t, w)
					_ = b.SetTilt(ctx, tl.v, pri)
					calls := NormaliseCalls(w.Capture())
					entries = append(entries, SnapshotEntry{Label: tl.label, Calls: calls})
				}
				return entries
			},
		},
		{
			dpType: "Blind",
			setter: "SetCombined",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				b := newBlindFixture(t, w)
				_ = b.SetCombined(ctx, 0.5, 0.25, pri)
				return []SnapshotEntry{{Label: "level=0.5,tilt=0.25", Calls: NormaliseCalls(w.Capture())}}
			},
		},
		{
			dpType: "Blind",
			setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				b := newBlindFixture(t, w)
				_ = b.Open(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: NormaliseCalls(w.Capture())}}
			},
		},
		{
			dpType: "Blind",
			setter: "Close",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				b := newBlindFixture(t, w)
				_ = b.Close(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: NormaliseCalls(w.Capture())}}
			},
		},
		{
			dpType: "Blind",
			setter: "OpenTilt",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				b := newBlindFixture(t, w)
				_ = b.OpenTilt(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: NormaliseCalls(w.Capture())}}
			},
		},
		{
			dpType: "Blind",
			setter: "CloseTilt",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				b := newBlindFixture(t, w)
				_ = b.CloseTilt(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: NormaliseCalls(w.Capture())}}
			},
		},
		// ── Climate (IP) ─────────────────────────────────────────────────────
		{
			dpType: "ClimateIP",
			setter: "SetTemperature",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				temps := []struct {
					label string
					v     float64
				}{
					{"temp=5", 5.0},
					{"temp=20", 20.0},
					{"temp=30", 30.0},
				}
				var entries []SnapshotEntry
				for _, tc := range temps {
					c := newClimateIPFixture(t, w)
					_ = c.SetTemperature(ctx, tc.v, pri)
					entries = append(entries, SnapshotEntry{Label: tc.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		{
			dpType: "ClimateIP",
			setter: "SetMode",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				modes := []struct {
					label string
					m     climate.Mode
				}{
					{"mode=auto", climate.ModeAuto},
					{"mode=heat", climate.ModeHeat},
					{"mode=off", climate.ModeOff},
				}
				var entries []SnapshotEntry
				for _, mc := range modes {
					c := newClimateIPFixture(t, w)
					_ = c.SetMode(ctx, mc.m, pri)
					entries = append(entries, SnapshotEntry{Label: mc.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		{
			dpType: "ClimateIP",
			setter: "EnableBoost",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				c := newClimateIPFixture(t, w)
				_ = c.EnableBoost(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		{
			dpType: "ClimateIP",
			setter: "DisableBoost",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				c := newClimateIPFixture(t, w)
				_ = c.DisableBoost(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		// ── Valve (Irrigation) ───────────────────────────────────────────────
		{
			dpType: "IrrigationValve",
			setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				v := newIrrigationFixture(t, w)
				_ = v.Open(ctx, 120*time.Second, pri)
				return []SnapshotEntry{{Label: "duration=120s", Calls: w.Capture()}}
			},
		},
		{
			dpType: "IrrigationValve",
			setter: "Close",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				v := newIrrigationFixture(t, w)
				v.OnEvent(true) // seed so Close is a state change
				_ = v.Close(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		// ── Valve (Modulating) ───────────────────────────────────────────────
		{
			dpType: "ModulatingValve",
			setter: "SetLevel",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				levels := []struct {
					label string
					v     float64
				}{
					{"level=0.0", 0.0},
					{"level=0.5", 0.5},
					{"level=1.0", 1.0},
				}
				var entries []SnapshotEntry
				for _, l := range levels {
					mv := newModulatingFixture(t, w)
					_ = mv.SetLevel(ctx, l.v, pri)
					entries = append(entries, SnapshotEntry{Label: l.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		// ── Lock ─────────────────────────────────────────────────────────────
		{
			dpType: "Lock",
			setter: "Lock",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				l := newLockFixture(t, w)
				_ = l.Lock(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		{
			dpType: "Lock",
			setter: "Unlock",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				l := newLockFixture(t, w)
				_ = l.Unlock(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		{
			dpType: "Lock",
			setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				l := newLockFixture(t, w)
				_ = l.Open(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		// ── Siren ────────────────────────────────────────────────────────────
		{
			dpType: "Siren",
			setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				s := newSirenFixture(t, w)
				acoustic := ptr("FREQUENCY_RISING")
				optical := ptr("BLINKING_RED")
				cfg := siren.OnConfig{AcousticSelection: acoustic, OpticalSelection: optical}
				_ = s.TurnOn(ctx, cfg, pri)
				return []SnapshotEntry{{Label: "acoustic=FREQUENCY_RISING,optical=BLINKING_RED", Calls: NormaliseCalls(w.Capture())}}
			},
		},
		{
			dpType: "Siren",
			setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				s := newSirenFixture(t, w)
				_ = s.TurnOff(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: NormaliseCalls(w.Capture())}}
			},
		},
		// ── Light (base) ─────────────────────────────────────────────────────
		{
			dpType: "Light",
			setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				l := newLightFixture(t, w)
				_ = l.TurnOn(ctx, pri)
				return []SnapshotEntry{{Label: "default-brightness", Calls: w.Capture()}}
			},
		},
		{
			dpType: "Light",
			setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				l := newLightFixture(t, w)
				l.OnLevel(1.0) // seed so TurnOff is a state change
				_ = l.TurnOff(ctx, pri)
				return []SnapshotEntry{{Label: "from-on", Calls: w.Capture()}}
			},
		},
		{
			dpType: "Light",
			setter: "SetLevel",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				levels := []struct {
					label string
					v     float64
				}{
					{"level=0.0", 0.0},
					{"level=0.5", 0.5},
					{"level=1.0", 1.0},
				}
				var entries []SnapshotEntry
				for _, lv := range levels {
					l := newLightFixture(t, w)
					_ = l.SetLevel(ctx, lv.v, pri)
					entries = append(entries, SnapshotEntry{Label: lv.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		// ── ColorLight ───────────────────────────────────────────────────────
		{
			dpType: "ColorLight",
			setter: "SetColor",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				colors := []struct {
					label string
					hue   int32
					sat   float64
				}{
					{"hue=0,sat=100", 0, 100},
					{"hue=120,sat=80", 120, 80},
					{"hue=240,sat=50", 240, 50},
				}
				var entries []SnapshotEntry
				for _, c := range colors {
					cl := newColorLightFixture(t, w)
					_ = cl.SetColor(ctx, c.hue, c.sat, pri)
					entries = append(entries, SnapshotEntry{Label: c.label, Calls: NormaliseCalls(w.Capture())})
				}
				return entries
			},
		},
		// ── ColorTempLight ───────────────────────────────────────────────────
		{
			dpType: "ColorTempLight",
			setter: "SetKelvin",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				kelvins := []struct {
					label string
					k     int32
				}{
					{"kelvin=2700", 2700},
					{"kelvin=4000", 4000},
					{"kelvin=6500", 6500},
				}
				var entries []SnapshotEntry
				for _, kv := range kelvins {
					ct := newColorTempLightFixture(t, w)
					_ = ct.SetKelvin(ctx, kv.k, pri)
					entries = append(entries, SnapshotEntry{Label: kv.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		// ── FixedColorLight ──────────────────────────────────────────────────
		{
			dpType: "FixedColorLight",
			setter: "SetColor",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				colors := []struct {
					label string
					c     light.FixedColor
				}{
					{"WHITE", light.FixedColorWhite},
					{"RED", light.FixedColorRed},
					{"GREEN", light.FixedColorGreen},
					{"BLUE", light.FixedColorBlue},
					{"CYAN", light.FixedColorCyan},
					{"YELLOW", light.FixedColorYellow},
					{"MAGENTA", light.FixedColorMagenta},
				}
				var entries []SnapshotEntry
				for _, cv := range colors {
					fc := newFixedColorLightFixture(t, w)
					_ = fc.SetColor(ctx, cv.c, pri)
					entries = append(entries, SnapshotEntry{Label: cv.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		{
			dpType: "FixedColorLight",
			setter: "SetColorBehaviour",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				behaviours := []struct {
					label string
					b     light.ColorBehaviour
				}{
					{"DO_NOT_CARE", light.ColorBehaviourDoNotCare},
					{"OLD_VALUE", light.ColorBehaviourOldValue},
					{"ON", light.ColorBehaviourOn},
				}
				var entries []SnapshotEntry
				for _, bv := range behaviours {
					fc := newFixedColorLightFixture(t, w)
					_ = fc.SetColorBehaviour(ctx, bv.b, pri)
					entries = append(entries, SnapshotEntry{Label: bv.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		// ── EffectLight ──────────────────────────────────────────────────────
		{
			dpType: "EffectLight",
			setter: "SetEffect",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				effects := []struct {
					label string
					idx   int32
				}{
					{"effect=0", 0},
					{"effect=1", 1},
					{"effect=2", 2},
				}
				var entries []SnapshotEntry
				for _, ev := range effects {
					el := newEffectLightFixture(t, w)
					_ = el.SetEffect(ctx, ev.idx, pri)
					entries = append(entries, SnapshotEntry{Label: ev.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		// ── DRGDaliLight ─────────────────────────────────────────────────────
		{
			dpType: "DRGDaliLight",
			setter: "SetKelvin",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				dl := newDaliLightFixture(t, w)
				_ = dl.SetKelvin(ctx, 4000, pri)
				return []SnapshotEntry{{Label: "kelvin=4000", Calls: w.Capture()}}
			},
		},
		{
			dpType: "DRGDaliLight",
			setter: "SetEffect",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				// DALI SetEffect sends a string label; the wire value is the
				// corresponding integer index from the VALUE_LIST.
				effects := []string{"Off", "Flash", "Smooth_fast"}
				var entries []SnapshotEntry
				for _, lbl := range effects {
					dl := newDaliLightFixture(t, w)
					_ = dl.SetEffect(ctx, lbl, pri)
					entries = append(entries, SnapshotEntry{Label: "effect=" + lbl, Calls: w.Capture()})
				}
				return entries
			},
		},
		// ── RGBWLight ────────────────────────────────────────────────────────
		{
			dpType: "RGBWLight",
			setter: "SetColor",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				colors := []struct {
					label string
					hue   int32
					sat   float64
				}{
					{"hue=0,sat=100", 0, 100},
					{"hue=180,sat=70", 180, 70},
				}
				var entries []SnapshotEntry
				for _, c := range colors {
					r := newRGBWLightFixture(t, w)
					_ = r.SetColor(ctx, c.hue, c.sat, pri)
					entries = append(entries, SnapshotEntry{Label: c.label, Calls: NormaliseCalls(w.Capture())})
				}
				return entries
			},
		},
		{
			dpType: "RGBWLight",
			setter: "SetEffect",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				// RGBW SetEffect uses string label (DpActionSelect).
				effects := []struct {
					label string
					eff   string
				}{
					{"BLINKING_SLOW", "BLINKING_SLOW"},
					{"FLASH_SHORT", "FLASH_SHORT"},
				}
				var entries []SnapshotEntry
				for _, ev := range effects {
					r := newRGBWLightFixture(t, w)
					_ = r.SetEffect(ctx, ev.eff, pri)
					entries = append(entries, SnapshotEntry{Label: ev.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		// ── SoundPlayerLED ───────────────────────────────────────────────────
		{
			dpType: "SoundPlayerLED",
			setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				led, addr := newSoundPlayerLEDFixture(t, w)
				cfg := light.LedOnConfig{
					Brightness:  128,
					FlashTimeMS: 500,
					Repetitions: 3,
				}
				_ = led.TurnOn(ctx, cfg, w, addr, pri)
				return []SnapshotEntry{{Label: "brightness=128,flash=500ms,rep=3", Calls: NormaliseCalls(w.Capture())}}
			},
		},
		{
			dpType: "SoundPlayerLED",
			setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				led, addr := newSoundPlayerLEDFixture(t, w)
				_ = led.TurnOff(ctx, w, addr, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: NormaliseCalls(w.Capture())}}
			},
		},
		// ── TextDisplay ──────────────────────────────────────────────────────
		{
			dpType: "TextDisplay",
			setter: "Write",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				rows := []struct {
					label string
					r     textdisplay.Row
				}{
					{"row1-text", textdisplay.Row{ID: 1, Text: "Hello"}},
					{"row2-text", textdisplay.Row{ID: 2, Text: "World"}},
					{"row3-empty", textdisplay.Row{ID: 3, Text: ""}},
				}
				var entries []SnapshotEntry
				for _, rv := range rows {
					td := newTextDisplayFixture(t, w)
					_ = td.Write(ctx, rv.r, pri)
					entries = append(entries, SnapshotEntry{Label: rv.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		{
			dpType: "TextDisplay",
			setter: "WriteRows",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				td := newTextDisplayFixture(t, w)
				rows := []textdisplay.Row{
					{ID: 1, Text: "Line one"},
					{ID: 2, Text: "Line two"},
					{ID: 3, Text: "Line three"},
				}
				_ = td.WriteRows(ctx, rows, pri)
				return []SnapshotEntry{{Label: "3-rows", Calls: w.Capture()}}
			},
		},
		{
			dpType: "TextDisplay",
			setter: "WriteWithSound",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				td := newTextDisplayFixture(t, w)
				row := textdisplay.Row{ID: 1, Text: "Alert"}
				opts := textdisplay.SoundOptions{Sound: "LONG_SHORT"}
				_ = td.WriteWithSound(ctx, row, opts, pri)
				return []SnapshotEntry{{Label: "row1-sound=LONG_SHORT", Calls: w.Capture()}}
			},
		},
		{
			dpType: "TextDisplay",
			setter: "Clear",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				td := newTextDisplayFixture(t, w)
				_ = td.Clear(ctx, 1, pri)
				return []SnapshotEntry{{Label: "row1", Calls: w.Capture()}}
			},
		},
		// ── Climate RF ───────────────────────────────────────────────────────
		{
			dpType: "ClimateRF",
			setter: "SetMode",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				modes := []struct {
					label string
					m     climate.Mode
				}{
					{"HEAT", climate.ModeHeat},
					{"AUTO", climate.ModeAuto},
					{"OFF", climate.ModeOff},
				}
				var entries []SnapshotEntry
				for _, mv := range modes {
					c := newClimateRFFixture(t, w)
					_ = c.SetMode(ctx, mv.m, pri)
					entries = append(entries, SnapshotEntry{Label: mv.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		{
			dpType: "ClimateRF",
			setter: "SetProfile",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				profiles := []struct {
					label string
					p     climate.Profile
				}{
					{"WeekProgram1", climate.ProfileWeekProgram1},
					{"WeekProgram2", climate.ProfileWeekProgram2},
					{"WeekProgram3", climate.ProfileWeekProgram3},
				}
				var entries []SnapshotEntry
				for _, pv := range profiles {
					c := newClimateRFFixture(t, w)
					_ = c.SetProfile(ctx, pv.p, pri)
					entries = append(entries, SnapshotEntry{Label: pv.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		{
			dpType: "ClimateRF",
			setter: "SetTemperature",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				temps := []struct {
					label string
					v     float64
				}{
					{"temp=5", 5.0},
					{"temp=15", 15.0},
					{"temp=30", 30.0},
				}
				var entries []SnapshotEntry
				for _, tv := range temps {
					c := newClimateRFFixture(t, w)
					_ = c.SetTemperature(ctx, tv.v, pri)
					entries = append(entries, SnapshotEntry{Label: tv.label, Calls: w.Capture()})
				}
				return entries
			},
		},
		// ── Garage ───────────────────────────────────────────────────────────
		{
			dpType: "Garage",
			setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				g := newGarageFixture(t, w)
				_ = g.Open(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		{
			dpType: "Garage",
			setter: "Close",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				g := newGarageFixture(t, w)
				_ = g.Close(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		{
			dpType: "Garage",
			setter: "Vent",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				g := newGarageFixture(t, w)
				_ = g.Vent(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
		// ── SmokeSiren ───────────────────────────────────────────────────────
		{
			dpType: "SmokeSiren",
			setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				s := newSmokeSirenFixture(t, w)
				_ = s.TurnOn(ctx, pri)
				return []SnapshotEntry{{Label: "INTRUSION_ALARM", Calls: w.Capture()}}
			},
		},
		{
			dpType: "SmokeSiren",
			setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				s := newSmokeSirenFixture(t, w)
				_ = s.TurnOff(ctx, pri)
				return []SnapshotEntry{{Label: "INTRUSION_ALARM_OFF", Calls: w.Capture()}}
			},
		},
		// ── SoundPlayer ──────────────────────────────────────────────────────
		{
			dpType: "SoundPlayer",
			setter: "PlaySound",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				configs := []struct {
					label string
					cfg   siren.PlayConfig
				}{
					{"file=1,rep=0", siren.PlayConfig{SoundfileIndex: 1, Volume: 0.8, RepetitionsIndex: 0}},
					{"file=3,rep=2", siren.PlayConfig{SoundfileIndex: 3, Volume: 0.5, RepetitionsIndex: 2}},
					{"file=5,loop", siren.PlayConfig{SoundfileIndex: 5, Volume: 1.0, Loop: true}},
				}
				var entries []SnapshotEntry
				for _, pc := range configs {
					sp := newSoundPlayerFixture(t, w)
					_ = sp.PlaySound(ctx, pc.cfg, pri)
					entries = append(entries, SnapshotEntry{Label: pc.label, Calls: NormaliseCalls(w.Capture())})
				}
				return entries
			},
		},
		{
			dpType: "SoundPlayer",
			setter: "StopSound",
			run: func(t *testing.T, w *fakeWriter) []SnapshotEntry {
				t.Helper()
				sp := newSoundPlayerFixture(t, w)
				_ = sp.StopSound(ctx, pri)
				return []SnapshotEntry{{Label: "priority=normal", Calls: w.Capture()}}
			},
		},
	}

	var total int
	for _, e := range entries {
		e := e
		t.Run(e.dpType+"/"+e.setter, func(t *testing.T) {
			w := NewFakeWriter()
			inputs := e.run(t, w)
			// Discard empty-call entries that indicate the setter was a no-op
			// (e.g. state-change gate suppressed the write). We still record
			// them so the snapshot documents the suppression intentionally.
			sf := SnapshotFile{
				DPType: e.dpType,
				Setter: e.setter,
				Inputs: inputs,
			}
			writeSnapshot(t, dir, sf)
			total += len(inputs)
		})
	}

	t.Logf("generated %d snapshot entries across %d setters", total, len(entries))
}
