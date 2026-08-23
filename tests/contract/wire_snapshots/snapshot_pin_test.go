// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build !snapshot_gen

// snapshot_pin_test.go pins Custom-DP wire calls against golden JSON
// snapshots. It fails with a diff when a setter produces a different
// sequence of SetValue / PutParamset calls than the stored baseline.
//
// Regenerate the baselines after intentional production-code changes:
//
//	go test -tags=snapshot_gen ./tests/contract/wire_snapshots/
package wire_snapshots

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// snapshotsDir returns the absolute path to the snapshots/ sub-directory.
func snapshotsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "snapshots")
}

func loadSnapshot(t *testing.T, dir, dpType, setter string) (SnapshotFile, bool) {
	t.Helper()
	path := filepath.Join(dir, SnapshotFileName(dpType, setter))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return SnapshotFile{}, false
	}
	if err != nil {
		t.Fatalf("read snapshot %s: %v", path, err)
	}
	sf, err := UnmarshalSnapshot(data)
	if err != nil {
		t.Fatalf("parse snapshot %s: %v", path, err)
	}
	return sf, true
}

// diffCalls returns a human-readable diff string when got != want, or "".
func diffCalls(label string, want, got []CapturedCall) string {
	if len(want) != len(got) {
		return fmt.Sprintf("[%s] call count: want %d, got %d\n  want: %s\n  got:  %s",
			label, len(want), len(got), marshalCompact(want), marshalCompact(got))
	}
	for i := range want {
		wb, _ := json.Marshal(want[i])
		gb, _ := json.Marshal(got[i])
		if !bytes.Equal(wb, gb) {
			return fmt.Sprintf("[%s] call[%d] mismatch:\n  want: %s\n  got:  %s",
				label, i, wb, gb)
		}
	}
	return ""
}

func marshalCompact(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

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

	// IP lock: LOCK_TARGET_LEVEL (write), LOCK_STATE (string), DIRECTION (string).
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

// --- new fixture constructors (pin build) --------------------------------

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

func newColorLightFixture(t *testing.T, w *fakeWriter) *light.ColorLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "COL0001"})
	ch := d.AddChannel("COL0001:1", 1, "COLOR", hmenum.ParamsetKeyValues)
	fp := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "COL0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(fp)
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
			Type:      hmenum.ParameterTypeEnum,
			ValueList: []string{"BLACK", "RED", "GREEN", "YELLOW", "BLUE", "PURPLE", "TURQUOISE", "WHITE"},
		},
		Writer: w,
	})
	ch.Put(colorSel)
	cbSel := generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "FC0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterColorBehaviour)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"DO_NOT_CARE", "OFF", "OLD_VALUE", "ON"},
		},
		Writer: w,
	})
	ch.Put(cbSel)
	caps := custom.LightCapabilities{Dimmable: true}
	return light.NewFixedColorLight(light.Config{Channel: ch, Writer: w, Capabilities: caps})
}

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
			Type:      hmenum.ParameterTypeAction,
			ValueList: []string{"BLINKING_SLOW", "BLINKING_FAST", "FLASH_SHORT", "RAMPING_CONTINUOUS"},
		},
		Writer: w,
	})
	ch.Put(effectSel)
	caps := custom.LightCapabilities{Dimmable: true}
	r := light.NewRGBWLight(light.Config{Channel: ch, Writer: w, Capabilities: caps})
	modeSensor.OnEvent("RGB")
	r.Subscribe(ch)
	return r
}

func newSoundPlayerLEDFixture(t *testing.T, w *fakeWriter) (led *light.SoundPlayerLED, addr string) {
	t.Helper()
	addr = "MP3P0001:6"
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
			ValueList: []string{"BLACK", "RED", "GREEN", "YELLOW", "BLUE", "PURPLE", "TURQUOISE", "WHITE"},
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

func newTextDisplayFixture(_ *testing.T, w *fakeWriter) *textdisplay.TextDisplay {
	return textdisplay.New("SDV0001:1", w)
}

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
	// target the device root, not the climate channel's VALUES. Register them
	// there so the fixture resolves exactly as it does against a real CCU.
	registerRFThermostatRootMaster(d, w)
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

// registerRFThermostatRootMaster registers WEEK_PROGRAM_POINTER and
// TEMPERATURE_OFFSET as MASTER data points on the device-root channel, where
// the classic RF thermostat family exposes them (the climate channel carries
// neither). The write value is supplied by the climate setter, so the DP type
// is irrelevant to the wire output — it only has to make the config-parameter
// resolver find the parameter on the device root.
func registerRFThermostatRootMaster(d *device.Device, w *fakeWriter) {
	root := d.EnsureRootChannel()
	for _, p := range []hmenum.Parameter{hmenum.ParameterWeekProgramPointer, hmenum.ParameterTemperatureOffset} {
		root.PutMaster(generic.NewInteger(generic.Spec{
			Key:         hmtypes.DataPointKey{ChannelAddress: d.Address, ParamsetKey: hmenum.ParamsetKeyMaster, Parameter: string(p)},
			Descriptor:  hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
			Writer:      w,
			CentralName: "testcentral",
		}))
	}
}

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
			ValueList: []string{"SOUNDFILE_001", "SOUNDFILE_002", "SOUNDFILE_003", "SOUNDFILE_004", "SOUNDFILE_005"},
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

// --- pin runner ---------------------------------------------------------

// pinCase pairs a setter invocation with the DPType + Setter key.
type pinCase struct {
	dpType string
	setter string
	run    func(t *testing.T, w *fakeWriter) []WireCapture
}

// TestWireSnapshots loads every golden snapshot and verifies that
// re-running the same setter with the same inputs produces identical
// wire calls.
func TestWireSnapshots(t *testing.T) {
	t.Parallel()
	dir := snapshotsDir(t)

	ctx := context.Background()
	pri := hmenum.CommandPriorityHigh

	cases := []pinCase{
		// Switch
		{
			dpType: "Switch", setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				sw := newSwitchFixture(t, w)
				_ = sw.TurnOn(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Switch", setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				sw := newSwitchFixture(t, w)
				sw.OnEvent(true)
				_ = sw.TurnOff(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Switch", setter: "TurnOnFor",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				sw := newSwitchFixture(t, w)
				_ = sw.TurnOnFor(ctx, 60*time.Second, pri)
				return []WireCapture{w.Capture()}
			},
		},
		// Cover
		{
			dpType: "Cover", setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				c := newCoverFixture(t, w)
				_ = c.Open(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Cover", setter: "Close",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				c := newCoverFixture(t, w)
				_ = c.Close(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Cover", setter: "SetPosition",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				out := make([]WireCapture, 0, 3)
				for _, v := range []float64{0.0, 0.5, 1.0} {
					c := newCoverFixture(t, w)
					_ = c.SetPosition(ctx, v, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// Blind
		{
			dpType: "Blind", setter: "SetTilt",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				out := make([]WireCapture, 0, 3)
				for _, v := range []float64{0.0, 0.5, 1.0} {
					b := newBlindFixture(t, w)
					_ = b.SetTilt(ctx, v, pri)
					out = append(out, NormaliseCalls(w.Capture()))
				}
				return out
			},
		},
		{
			dpType: "Blind", setter: "SetCombined",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				b := newBlindFixture(t, w)
				_ = b.SetCombined(ctx, 0.5, 0.25, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		{
			dpType: "Blind", setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				b := newBlindFixture(t, w)
				_ = b.Open(ctx, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		{
			dpType: "Blind", setter: "Close",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				b := newBlindFixture(t, w)
				_ = b.Close(ctx, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		{
			dpType: "Blind", setter: "OpenTilt",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				b := newBlindFixture(t, w)
				_ = b.OpenTilt(ctx, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		{
			dpType: "Blind", setter: "CloseTilt",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				b := newBlindFixture(t, w)
				_ = b.CloseTilt(ctx, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		// ClimateIP
		{
			dpType: "ClimateIP", setter: "SetTemperature",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				out := make([]WireCapture, 0, 3)
				for _, v := range []float64{5.0, 20.0, 30.0} {
					c := newClimateIPFixture(t, w)
					_ = c.SetTemperature(ctx, v, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "ClimateIP", setter: "SetMode",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				out := make([]WireCapture, 0, 3)
				for _, m := range []climate.Mode{climate.ModeAuto, climate.ModeHeat, climate.ModeOff} {
					c := newClimateIPFixture(t, w)
					_ = c.SetMode(ctx, m, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "ClimateIP", setter: "EnableBoost",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				c := newClimateIPFixture(t, w)
				_ = c.EnableBoost(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "ClimateIP", setter: "DisableBoost",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				c := newClimateIPFixture(t, w)
				_ = c.DisableBoost(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		// IrrigationValve
		{
			dpType: "IrrigationValve", setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				v := newIrrigationFixture(t, w)
				_ = v.Open(ctx, 120*time.Second, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "IrrigationValve", setter: "Close",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				v := newIrrigationFixture(t, w)
				v.OnEvent(true)
				_ = v.Close(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		// ModulatingValve
		{
			dpType: "ModulatingValve", setter: "SetLevel",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				out := make([]WireCapture, 0, 3)
				for _, v := range []float64{0.0, 0.5, 1.0} {
					mv := newModulatingFixture(t, w)
					_ = mv.SetLevel(ctx, v, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// Lock
		{
			dpType: "Lock", setter: "Lock",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				l := newLockFixture(t, w)
				_ = l.Lock(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Lock", setter: "Unlock",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				l := newLockFixture(t, w)
				_ = l.Unlock(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Lock", setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				l := newLockFixture(t, w)
				_ = l.Open(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		// Siren
		{
			dpType: "Siren", setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				s := newSirenFixture(t, w)
				acoustic := new("FREQUENCY_RISING")
				optical := new("BLINKING_RED")
				cfg := siren.OnConfig{AcousticSelection: acoustic, OpticalSelection: optical}
				_ = s.TurnOn(ctx, cfg, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		{
			dpType: "Siren", setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				s := newSirenFixture(t, w)
				_ = s.TurnOff(ctx, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		// Light
		{
			dpType: "Light", setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				l := newLightFixture(t, w)
				_ = l.TurnOn(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Light", setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				l := newLightFixture(t, w)
				l.OnLevel(1.0)
				_ = l.TurnOff(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Light", setter: "SetLevel",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				out := make([]WireCapture, 0, 3)
				for _, v := range []float64{0.0, 0.5, 1.0} {
					l := newLightFixture(t, w)
					_ = l.SetLevel(ctx, v, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// ColorLight
		{
			dpType: "ColorLight", setter: "SetColor",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				colors := []struct {
					hue int32
					sat float64
				}{{0, 100}, {120, 80}, {240, 50}}
				out := make([]WireCapture, 0, len(colors))
				for _, c := range colors {
					cl := newColorLightFixture(t, w)
					_ = cl.SetColor(ctx, c.hue, c.sat, pri)
					out = append(out, NormaliseCalls(w.Capture()))
				}
				return out
			},
		},
		// ColorTempLight
		{
			dpType: "ColorTempLight", setter: "SetKelvin",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				out := make([]WireCapture, 0, 3)
				for _, k := range []int32{2700, 4000, 6500} {
					ct := newColorTempLightFixture(t, w)
					_ = ct.SetKelvin(ctx, k, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// FixedColorLight
		{
			dpType: "FixedColorLight", setter: "SetColor",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				fixedColors := []light.FixedColor{
					light.FixedColorWhite,
					light.FixedColorRed,
					light.FixedColorGreen,
					light.FixedColorBlue,
					light.FixedColorCyan,
					light.FixedColorYellow,
					light.FixedColorMagenta,
				}
				out := make([]WireCapture, 0, len(fixedColors))
				for _, c := range fixedColors {
					fc := newFixedColorLightFixture(t, w)
					_ = fc.SetColor(ctx, c, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "FixedColorLight", setter: "SetColorBehaviour",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				behaviours := []light.ColorBehaviour{
					light.ColorBehaviourDoNotCare,
					light.ColorBehaviourOldValue,
					light.ColorBehaviourOn,
				}
				out := make([]WireCapture, 0, len(behaviours))
				for _, b := range behaviours {
					fc := newFixedColorLightFixture(t, w)
					_ = fc.SetColorBehaviour(ctx, b, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// EffectLight
		{
			dpType: "EffectLight", setter: "SetEffect",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				out := make([]WireCapture, 0, 3)
				for _, idx := range []int32{0, 1, 2} {
					el := newEffectLightFixture(t, w)
					_ = el.SetEffect(ctx, idx, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// DRGDaliLight
		{
			dpType: "DRGDaliLight", setter: "SetKelvin",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				dl := newDaliLightFixture(t, w)
				_ = dl.SetKelvin(ctx, 4000, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "DRGDaliLight", setter: "SetEffect",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				out := make([]WireCapture, 0, 3)
				for _, lbl := range []string{"Off", "Flash", "Smooth_fast"} {
					dl := newDaliLightFixture(t, w)
					_ = dl.SetEffect(ctx, lbl, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// RGBWLight
		{
			dpType: "RGBWLight", setter: "SetColor",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				colors := []struct {
					hue int32
					sat float64
				}{{0, 100}, {180, 70}}
				out := make([]WireCapture, 0, len(colors))
				for _, c := range colors {
					r := newRGBWLightFixture(t, w)
					_ = r.SetColor(ctx, c.hue, c.sat, pri)
					out = append(out, NormaliseCalls(w.Capture()))
				}
				return out
			},
		},
		{
			dpType: "RGBWLight", setter: "SetEffect",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				effects := []string{"BLINKING_SLOW", "FLASH_SHORT"}
				out := make([]WireCapture, 0, len(effects))
				for _, e := range effects {
					r := newRGBWLightFixture(t, w)
					_ = r.SetEffect(ctx, e, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// SoundPlayerLED
		{
			dpType: "SoundPlayerLED", setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				led, addr := newSoundPlayerLEDFixture(t, w)
				cfg := light.LedOnConfig{Brightness: 128, FlashTimeMS: 500, Repetitions: 3}
				_ = led.TurnOn(ctx, cfg, w, addr, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		{
			dpType: "SoundPlayerLED", setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				led, addr := newSoundPlayerLEDFixture(t, w)
				_ = led.TurnOff(ctx, w, addr, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		// TextDisplay
		{
			dpType: "TextDisplay", setter: "Write",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				rows := []textdisplay.Row{
					{ID: 1, Text: "Hello"},
					{ID: 2, Text: "World"},
					{ID: 3, Text: ""},
				}
				out := make([]WireCapture, 0, len(rows))
				for _, r := range rows {
					td := newTextDisplayFixture(t, w)
					_ = td.Write(ctx, r, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "TextDisplay", setter: "WriteRows",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				td := newTextDisplayFixture(t, w)
				rows := []textdisplay.Row{
					{ID: 1, Text: "Line one"},
					{ID: 2, Text: "Line two"},
					{ID: 3, Text: "Line three"},
				}
				_ = td.WriteRows(ctx, rows, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "TextDisplay", setter: "WriteWithSound",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				td := newTextDisplayFixture(t, w)
				r := textdisplay.Row{ID: 1, Text: "Alert"}
				opts := textdisplay.SoundOptions{Sound: "LONG_SHORT"}
				_ = td.WriteWithSound(ctx, r, opts, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "TextDisplay", setter: "Clear",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				td := newTextDisplayFixture(t, w)
				_ = td.Clear(ctx, 1, pri)
				return []WireCapture{w.Capture()}
			},
		},
		// ClimateRF
		{
			dpType: "ClimateRF", setter: "SetMode",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				out := make([]WireCapture, 0, 3)
				for _, m := range []climate.Mode{climate.ModeHeat, climate.ModeAuto, climate.ModeOff} {
					c := newClimateRFFixture(t, w)
					_ = c.SetMode(ctx, m, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "ClimateRF", setter: "SetProfile",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				out := make([]WireCapture, 0, 3)
				for _, p := range []climate.Profile{climate.ProfileWeekProgram1, climate.ProfileWeekProgram2, climate.ProfileWeekProgram3} {
					c := newClimateRFFixture(t, w)
					_ = c.SetProfile(ctx, p, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "ClimateRF", setter: "SetTemperature",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				out := make([]WireCapture, 0, 3)
				for _, v := range []float64{5.0, 15.0, 30.0} {
					c := newClimateRFFixture(t, w)
					_ = c.SetTemperature(ctx, v, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// Garage
		{
			dpType: "Garage", setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				g := newGarageFixture(t, w)
				_ = g.Open(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Garage", setter: "Close",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				g := newGarageFixture(t, w)
				_ = g.Close(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Garage", setter: "Vent",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				g := newGarageFixture(t, w)
				_ = g.Vent(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		// SmokeSiren
		{
			dpType: "SmokeSiren", setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				s := newSmokeSirenFixture(t, w)
				_ = s.TurnOn(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "SmokeSiren", setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				s := newSmokeSirenFixture(t, w)
				_ = s.TurnOff(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		// SoundPlayer
		{
			dpType: "SoundPlayer", setter: "PlaySound",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				configs := []siren.PlayConfig{
					{SoundfileIndex: 1, Volume: 0.8, RepetitionsIndex: 0},
					{SoundfileIndex: 3, Volume: 0.5, RepetitionsIndex: 2},
					{SoundfileIndex: 5, Volume: 1.0, Loop: true},
				}
				out := make([]WireCapture, 0, len(configs))
				for _, cfg := range configs {
					sp := newSoundPlayerFixture(t, w)
					_ = sp.PlaySound(ctx, cfg, pri)
					out = append(out, NormaliseCalls(w.Capture()))
				}
				return out
			},
		},
		{
			dpType: "SoundPlayer", setter: "StopSound",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				sp := newSoundPlayerFixture(t, w)
				_ = sp.StopSound(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
	}

	for _, pc := range cases {
		t.Run(pc.dpType+"/"+pc.setter, func(t *testing.T) {
			t.Parallel()

			sf, ok := loadSnapshot(t, dir, pc.dpType, pc.setter)
			if !ok {
				t.Skipf("no snapshot for %s/%s — run: go test -tags=snapshot_gen ./tests/contract/wire_snapshots/", pc.dpType, pc.setter)
			}

			w := NewFakeWriter()
			got := pc.run(t, w)

			if len(got) != len(sf.Inputs) {
				t.Fatalf("input count: snapshot has %d, got %d", len(sf.Inputs), len(got))
			}

			var errs []string
			for i, want := range sf.Inputs {
				if diff := diffCalls(want.Label, want.Calls, []CapturedCall(got[i])); diff != "" {
					errs = append(errs, diff)
				}
			}
			if len(errs) > 0 {
				t.Errorf("wire calls do not match snapshot for %s/%s:\n%s\n\nRegenerate with: go test -tags=snapshot_gen ./tests/contract/wire_snapshots/",
					pc.dpType, pc.setter, strings.Join(errs, "\n"))
			}
		})
	}
}
