// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build wire_reference

// reference_compare_test.go compares Go Custom-DP setter wire calls
// against the reference wire snapshots under reference_dir/.
//
// These tests deliberately fail for every known wire drift until the
// production code is corrected. They are the ground-truth detector:
// a PASS means the Go wire matches the reference implementation; a FAIL
// means a drift exists that must be fixed in the production code, not here.
//
// Run with:
//
//	go test -tags=wire_reference ./tests/contract/wire_snapshots/ -run TestReferenceCompare -v
//
// Do NOT skip or xfail failing cases in this file. The failing tests
// ARE the drift list. Fix the production code, then re-run.
//
// Build tag wire_reference keeps these tests out of the default
// "make test" pipeline until all drifts are resolved.
package wire_snapshots

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
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

// refSnapshotsDir returns the path to the reference snapshot directory.
func refSnapshotsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "aiohomematic_reference")
}

// ReferenceSnapshotFile is the JSON structure for reference wire-call files
// produced by the reference-snapshot generator script.
type ReferenceSnapshotFile struct {
	DPType              string          `json:"dp_type"`
	Setter              string          `json:"setter"`
	Source              string          `json:"source"`
	AiohomematicVersion string          `json:"aiohomematic_version"`
	Inputs              []SnapshotEntry `json:"inputs"`
}

func loadReferenceSnapshot(t *testing.T, dir, dpType, setter string) (ReferenceSnapshotFile, bool) {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("%s__%s.json", dpType, setter))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ReferenceSnapshotFile{}, false
	}
	if err != nil {
		t.Fatalf("read reference snapshot %s: %v", path, err)
	}
	var sf ReferenceSnapshotFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("parse reference snapshot %s: %v", path, err)
	}
	return sf, true
}

// ── fixture constructors (same as snapshot_pin_test.go) ──────────────────────
// These are compiled only under the wire_reference build tag, so they
// do not conflict with the pin-test fixtures in snapshot_pin_test.go
// which are compiled under the !snapshot_gen tag. The wire_reference and
// !snapshot_gen tags are mutually compatible, but we keep these fixtures
// here to avoid an unintended duplicate-function compile error: when
// both tags are active together, the compiler would see two copies of
// e.g. newSwitchFixtureRef. The ref_ prefix avoids that.

func newSwitchFixtureRef(t *testing.T, w *fakeWriter) *switchdev.Switch {
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
	return switchdev.New(ch)
}

func newBlindFixtureRef(t *testing.T, w *fakeWriter) *cover.Blind {
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

func newDaliLightFixtureRef(t *testing.T, w *fakeWriter) *light.DRGDaliLight {
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

func newRGBWLightFixtureRef(t *testing.T, w *fakeWriter) *light.RGBWLight {
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

func newColorLightFixtureRef(t *testing.T, w *fakeWriter) *light.ColorLight {
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

func newEffectLightFixtureRef(t *testing.T, w *fakeWriter) *light.EffectLight {
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

func newSirenFixtureRef(t *testing.T, w *fakeWriter) *siren.Siren {
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
	// Write-only ENUM (OPERATIONS=2) with a string-labelled DEFAULT — the
	// shape the resolver produces for an alarm selection.
	selValues := []string{"DISABLE_ACOUSTIC_SIGNAL", "FREQUENCY_RISING", "FREQUENCY_FALLING"}
	acousticSel := generic.NewActionSelect(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "ASIR0001:3", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterAcousticAlarmSelection)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsWrite,
			ValueList:  selValues,
			Default:    []byte(`"` + selValues[0] + `"`),
		},
		Writer: w,
	})
	ch.Put(acousticSel)
	// Write-only ENUM (OPERATIONS=2) with a string-labelled DEFAULT — the
	// shape the resolver produces for an alarm selection.
	selValues = []string{"DISABLE_OPTICAL_SIGNAL", "BLINKING_RED", "BLINKING_BLUE"}
	opticalSel := generic.NewActionSelect(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "ASIR0001:3", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterOpticalAlarmSelection)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsWrite,
			ValueList:  selValues,
			Default:    []byte(`"` + selValues[0] + `"`),
		},
		Writer: w,
	})
	ch.Put(opticalSel)
	// DURATION_VALUE (integer seconds) + DURATION_UNIT (0=s,1=m,2=h) are always
	// present on real HmIP-ASIR hardware and always written on TurnOn/TurnOff.
	for _, p := range []hmenum.Parameter{hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit} {
		dp := generic.NewInteger(generic.Spec{
			Key:        hmtypes.DataPointKey{ChannelAddress: "ASIR0001:3", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(p)},
			Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
			Writer:     w,
		})
		ch.Put(dp)
	}
	caps := custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true, SupportsDuration: true}
	return siren.New(siren.Config{Channel: ch, Writer: w, Capabilities: caps})
}

func newSoundPlayerFixtureRef(t *testing.T, w *fakeWriter) *siren.SoundPlayer {
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
			Type: hmenum.ParameterTypeEnum,
			// Real device value_list — matches the 3-digit format from the reference implementation:
			ValueList: []string{"NO_REPETITION", "REPETITIONS_001", "REPETITIONS_002", "REPETITIONS_003", "REPETITIONS_005", "INFINITE_REPETITIONS"},
		},
		Writer: w,
	})
	ch.Put(repDP)
	return siren.NewSoundPlayer(siren.SoundPlayerConfig{Channel: ch, Writer: w})
}

func newClimateRFFixtureRef(t *testing.T, w *fakeWriter) *climate.Climate {
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

func newLockFixtureRef(t *testing.T, w *fakeWriter) *lock.Lock {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DLD0001"})
	ch := d.AddChannel("DLD0001:1", 1, "LOCK", hmenum.ParamsetKeyValues)
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
	return lock.New(lock.Config{Channel: ch, Writer: w, Capabilities: caps, Kind: lock.KindIP})
}

func newTextDisplayFixtureRef(_ *testing.T, w *fakeWriter) *textdisplay.TextDisplay {
	return textdisplay.New("SDV0001:1", w)
}

func newCoverFixtureRef(t *testing.T, w *fakeWriter) *cover.Cover {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ROLL0001"})
	ch := d.AddChannel("ROLL0001:1", 1, "BLIND", hmenum.ParamsetKeyValues)
	level := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "ROLL0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(level)
	return cover.New(cover.Config{Channel: ch, Writer: w, Capabilities: cover.CoverCaps})
}

func newGarageFixtureRef(t *testing.T, w *fakeWriter) *cover.Garage {
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

func newLightFixtureRef(t *testing.T, w *fakeWriter) *light.Light {
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

func newColorTempLightFixtureRef(t *testing.T, w *fakeWriter) *light.ColorTempLight {
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

func newFixedColorLightFixtureRef(t *testing.T, w *fakeWriter) *light.FixedColorLight {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "FC0001"})
	ch := d.AddChannel("FC0001:1", 1, "COLOR", hmenum.ParamsetKeyValues)
	fp := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "FC0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(fp)
	colorDP := generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "FC0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterColor)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"BLACK", "BLUE", "GREEN", "TURQUOISE", "RED", "PURPLE", "YELLOW", "WHITE"},
		},
		Writer: w,
	})
	ch.Put(colorDP)
	cbDP := generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: "FC0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterColorBehaviour)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  []string{"DO_NOT_CARE", "DO_NOT_CARE_2", "OLD_VALUE", "ON", "OFF", "BLINK"},
		},
		Writer: w,
	})
	ch.Put(cbDP)
	caps := custom.LightCapabilities{Dimmable: true}
	return light.NewFixedColorLight(light.Config{Channel: ch, Writer: w, Capabilities: caps})
}

func newSmokeSirenFixtureRef(t *testing.T, w *fakeWriter) *siren.SmokeSiren {
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

func newIrrigationFixtureRef(t *testing.T, w *fakeWriter) *valve.Irrigation {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "WHS0001"})
	ch := d.AddChannel("WHS0001:1", 1, "VALVE", hmenum.ParamsetKeyValues)
	dp := generic.NewSwitch(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "WHS0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterState)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(dp)
	return valve.NewIrrigation(ch)
}

func newModulatingFixtureRef(t *testing.T, w *fakeWriter) *valve.Modulating {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MOD0001"})
	ch := d.AddChannel("MOD0001:1", 1, "VALVE", hmenum.ParamsetKeyValues)
	dp := generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: "MOD0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	})
	ch.Put(dp)
	return valve.NewModulating(ch)
}

func newClimateIPFixtureRef(t *testing.T, w *fakeWriter) *climate.Climate {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "BWTH0001"})
	ch := d.AddChannel("BWTH0001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	for _, fp := range []struct {
		param hmenum.Parameter
		ops   hmenum.Operations
	}{
		{hmenum.ParameterSetPointTemperature, hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		{hmenum.ParameterActualTemperature, hmenum.OperationsRead | hmenum.OperationsEvent},
	} {
		dp := generic.NewFloat(generic.Spec{
			Key:         hmtypes.DataPointKey{ChannelAddress: "BWTH0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(fp.param)},
			Descriptor:  hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: fp.ops},
			Writer:      w,
			CentralName: "testcentral",
		})
		ch.Put(dp)
	}
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

// newClimateIPAwayFixtureRef is newClimateIPFixtureRef plus SupportsAway, kept
// as a separate constructor so the away-mode case does not change the
// capability surface exercised by the other ClimateIP cases above.
func newClimateIPAwayFixtureRef(t *testing.T, w *fakeWriter) *climate.Climate {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "BWTH0001"})
	ch := d.AddChannel("BWTH0001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	for _, fp := range []struct {
		param hmenum.Parameter
		ops   hmenum.Operations
	}{
		{hmenum.ParameterSetPointTemperature, hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		{hmenum.ParameterActualTemperature, hmenum.OperationsRead | hmenum.OperationsEvent},
	} {
		dp := generic.NewFloat(generic.Spec{
			Key:         hmtypes.DataPointKey{ChannelAddress: "BWTH0001:1", ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(fp.param)},
			Descriptor:  hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: fp.ops},
			Writer:      w,
			CentralName: "testcentral",
		})
		ch.Put(dp)
	}
	caps := custom.ClimateCapabilities{
		SupportsAway:    true,
		SupportsAuto:    true,
		SupportsHeat:    true,
		SupportsOff:     true,
		MinTemperature:  5,
		MaxTemperature:  30,
		TemperatureStep: 0.5,
	}
	return climate.New(climate.Config{Channel: ch, Writer: w, Capabilities: caps, Kind: climate.KindIP})
}

// partyTimeStartPlaceholder replaces the non-deterministic PARTY_TIME_START
// value (encoded from time.Now() by Climate.SetAway) with a fixed sentinel so
// the reference comparison stays deterministic across runs. Only this one
// field is masked; every other field in the batch is compared verbatim.
const partyTimeStartPlaceholder = "<now>"

func maskPartyTimeStart(wc WireCapture) WireCapture {
	out := make(WireCapture, len(wc))
	for i, call := range wc {
		if _, ok := call.PutValues[string(hmenum.ParameterPartyTimeStart)]; ok {
			cp := make(map[string]any, len(call.PutValues))
			maps.Copy(cp, call.PutValues)
			cp[string(hmenum.ParameterPartyTimeStart)] = partyTimeStartPlaceholder
			call.PutValues = cp
		}
		out[i] = call
	}
	return out
}

func newSoundPlayerLEDFixtureRef(t *testing.T, w *fakeWriter) (*light.SoundPlayerLED, string) {
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
			ValueList: []string{"NO_REPETITION", "REPETITIONS_001", "REPETITIONS_002", "REPETITIONS_003", "INFINITE_REPETITIONS"},
		},
		Writer: w,
	})
	ch.Put(reps)
	caps := custom.LightCapabilities{Dimmable: true}
	return light.NewSoundPlayerLED(light.Config{Channel: ch, Writer: w, Capabilities: caps}), addr
}

// ── reference compare runner ──────────────────────────────────────────────────

// refCase pairs a Go setter invocation with the reference key.
type refCase struct {
	dpType string
	setter string
	// run executes the Go setter and returns one WireCapture per input label.
	run func(t *testing.T, w *fakeWriter) []WireCapture
}

// TestReferenceCompare runs every Go Custom-DP setter covered by a
// reference wire snapshot and fails when the wire calls differ.
//
// Failing tests indicate production-code drift that must be corrected.
// Passing tests confirm equivalence with the reference implementation.
func TestReferenceCompare(t *testing.T) {
	refDir := refSnapshotsDir(t)
	ctx := context.Background()
	pri := hmenum.CommandPriorityHigh

	cases := []refCase{
		// ── known-equivalent (expected to PASS) ─────────────────────────────
		{
			dpType: "Switch", setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				sw := newSwitchFixtureRef(t, w)
				_ = sw.TurnOn(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Switch", setter: "TurnOnFor",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				sw := newSwitchFixtureRef(t, w)
				_ = sw.TurnOnFor(ctx, 60*time.Second, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Lock", setter: "Lock",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				l := newLockFixtureRef(t, w)
				_ = l.Lock(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Lock", setter: "Unlock",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				l := newLockFixtureRef(t, w)
				_ = l.Unlock(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Lock", setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				l := newLockFixtureRef(t, w)
				_ = l.Open(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		// ── drift cases (expected to FAIL until production code is fixed) ───
		//
		// DRGDaliLight SetEffect: Go sends INT index; reference sends STRING for HmIP devices.
		// Root cause: ActionSelect.TriggerLabel maps to integer index unconditionally.
		{
			dpType: "DRGDaliLight", setter: "SetEffect",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, lbl := range []string{"Off", "Flash", "Smooth_fast"} {
					dl := newDaliLightFixtureRef(t, w)
					_ = dl.SetEffect(ctx, lbl, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// RGBWLight SetEffect: same ActionSelect INT vs STRING drift as DRGDaliLight.
		{
			dpType: "RGBWLight", setter: "SetEffect",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				effects := []string{"BLINKING_SLOW", "FLASH_SHORT"}
				var out []WireCapture
				for _, e := range effects {
					r := newRGBWLightFixtureRef(t, w)
					_ = r.SetEffect(ctx, e, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// EffectLight SetEffect: PROGRAM parameter — HM integer index; equivalent to reference.
		// This should PASS (HM device uses index encoding).
		{
			dpType: "EffectLight", setter: "SetEffect",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, idx := range []int32{0, 1, 2} {
					el := newEffectLightFixtureRef(t, w)
					_ = el.SetEffect(ctx, idx, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// SoundPlayer PlaySound: REPETITIONS format drift ("NO_REP" vs "NO_REPETITION" etc.).
		{
			dpType: "SoundPlayer", setter: "PlaySound",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				configs := []siren.PlayConfig{
					{SoundfileIndex: 1, Volume: 0.8, RepetitionsIndex: 0},
					{SoundfileIndex: 3, Volume: 0.5, RepetitionsIndex: 2},
					{SoundfileIndex: 5, Volume: 1.0, Loop: true},
				}
				var out []WireCapture
				for _, cfg := range configs {
					sp := newSoundPlayerFixtureRef(t, w)
					_ = sp.PlaySound(ctx, cfg, pri)
					out = append(out, NormaliseCalls(w.Capture()))
				}
				return out
			},
		},
		// Siren TurnOff: Go sends "" instead of DP-default string.
		{
			dpType: "Siren", setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				s := newSirenFixtureRef(t, w)
				_ = s.TurnOff(ctx, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		// Siren TurnOn: Go misses DURATION in the paramset.
		{
			dpType: "Siren", setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				s := newSirenFixtureRef(t, w)
				acoustic := ptrRef("FREQUENCY_RISING")
				optical := ptrRef("BLINKING_RED")
				cfg := siren.OnConfig{AcousticSelection: acoustic, OpticalSelection: optical}
				_ = s.TurnOn(ctx, cfg, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		// ClimateRF SetProfile: Go sends 3 SetValues; reference sends 1 PutParamset (atomicity drift).
		{
			dpType: "ClimateRF", setter: "SetProfile",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, p := range []climate.Profile{climate.ProfileWeekProgram1, climate.ProfileWeekProgram2, climate.ProfileWeekProgram3} {
					c := newClimateRFFixtureRef(t, w)
					_ = c.SetProfile(ctx, p, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// ColorLight SetColor: Go sends 2 SetValues; reference sends 1 PutParamset (atomicity drift).
		{
			dpType: "ColorLight", setter: "SetColor",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				colors := []struct {
					hue int32
					sat float64
				}{{0, 100}, {120, 80}, {240, 50}}
				var out []WireCapture
				for _, c := range colors {
					cl := newColorLightFixtureRef(t, w)
					_ = cl.SetColor(ctx, c.hue, c.sat, pri)
					out = append(out, NormaliseCalls(w.Capture()))
				}
				return out
			},
		},
		// RGBWLight SetColor: same atomicity drift (2 SetValues vs 1 PutParamset) as ColorLight SetColor.
		{
			dpType: "RGBWLight", setter: "SetColor",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				colors := []struct {
					hue int32
					sat float64
				}{{0, 100}, {180, 70}}
				var out []WireCapture
				for _, c := range colors {
					r := newRGBWLightFixtureRef(t, w)
					_ = r.SetColor(ctx, c.hue, c.sat, pri)
					out = append(out, NormaliseCalls(w.Capture()))
				}
				return out
			},
		},
		// Blind SetCombined: Go sends STOP unconditionally; reference omits it on first call.
		{
			dpType: "Blind", setter: "SetCombined",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				b := newBlindFixtureRef(t, w)
				_ = b.SetCombined(ctx, 0.5, 0.25, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		// Blind SetTilt: same STOP drift.
		{
			dpType: "Blind", setter: "SetTilt",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, v := range []float64{0.0, 0.5, 1.0} {
					b := newBlindFixtureRef(t, w)
					// Seed LEVEL=1.0 (fully open) so SetTilt holds the current
					// level position when no level target has been staged.
					b.OnLevel(1.0)
					_ = b.SetTilt(ctx, v, pri)
					out = append(out, NormaliseCalls(w.Capture()))
				}
				return out
			},
		},
		// TextDisplay WriteRows: Go sends 10 SetValues; reference sends per-row PutParamsets (atomicity drift).
		{
			dpType: "TextDisplay", setter: "WriteRows",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				td := newTextDisplayFixtureRef(t, w)
				rows := []textdisplay.Row{
					{ID: 1, Text: "Line one"},
					{ID: 2, Text: "Line two"},
					{ID: 3, Text: "Line three"},
				}
				_ = td.WriteRows(ctx, rows, pri)
				return []WireCapture{w.Capture()}
			},
		},
		// ── additional known-equivalent cases ───────────────────────────────
		{
			dpType: "Switch", setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				sw := newSwitchFixtureRef(t, w)
				sw.OnEvent(true)
				_ = sw.TurnOff(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Blind", setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				b := newBlindFixtureRef(t, w)
				_ = b.Open(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Blind", setter: "Close",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				b := newBlindFixtureRef(t, w)
				_ = b.Close(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Blind", setter: "OpenTilt",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				b := newBlindFixtureRef(t, w)
				_ = b.OpenTilt(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Blind", setter: "CloseTilt",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				b := newBlindFixtureRef(t, w)
				_ = b.CloseTilt(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Cover", setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				c := newCoverFixtureRef(t, w)
				_ = c.Open(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Cover", setter: "Close",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				c := newCoverFixtureRef(t, w)
				_ = c.Close(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Cover", setter: "SetPosition",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, lvl := range []float64{0.0, 0.5, 1.0} {
					c := newCoverFixtureRef(t, w)
					_ = c.SetPosition(ctx, lvl, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "Garage", setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				g := newGarageFixtureRef(t, w)
				_ = g.Open(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Garage", setter: "Close",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				g := newGarageFixtureRef(t, w)
				_ = g.Close(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Garage", setter: "Vent",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				g := newGarageFixtureRef(t, w)
				_ = g.Vent(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Light", setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				l := newLightFixtureRef(t, w)
				_ = l.TurnOn(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Light", setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				l := newLightFixtureRef(t, w)
				l.OnLevel(1.0)
				_ = l.TurnOff(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "Light", setter: "SetLevel",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, lvl := range []float64{0.0, 0.5, 1.0} {
					l := newLightFixtureRef(t, w)
					_ = l.SetLevel(ctx, lvl, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "ColorTempLight", setter: "SetKelvin",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, k := range []int32{2700, 4000, 6500} {
					ct := newColorTempLightFixtureRef(t, w)
					_ = ct.SetKelvin(ctx, k, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "DRGDaliLight", setter: "SetKelvin",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				dl := newDaliLightFixtureRef(t, w)
				_ = dl.SetKelvin(ctx, 4000, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "FixedColorLight", setter: "SetColor",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, c := range []light.FixedColor{
					light.FixedColorWhite,
					light.FixedColorRed,
					light.FixedColorGreen,
					light.FixedColorBlue,
					light.FixedColorCyan,
					light.FixedColorYellow,
					light.FixedColorMagenta,
				} {
					fc := newFixedColorLightFixtureRef(t, w)
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
				var out []WireCapture
				for _, cb := range []light.ColorBehaviour{
					light.ColorBehaviourDoNotCare,
					light.ColorBehaviourOldValue,
					light.ColorBehaviourOn,
				} {
					fc := newFixedColorLightFixtureRef(t, w)
					_ = fc.SetColorBehaviour(ctx, cb, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "SmokeSiren", setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				ss := newSmokeSirenFixtureRef(t, w)
				_ = ss.TurnOn(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "SmokeSiren", setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				ss := newSmokeSirenFixtureRef(t, w)
				_ = ss.TurnOff(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "IrrigationValve", setter: "Open",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				iv := newIrrigationFixtureRef(t, w)
				_ = iv.Open(ctx, 120*time.Second, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "IrrigationValve", setter: "Close",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				iv := newIrrigationFixtureRef(t, w)
				iv.OnEvent(true)
				_ = iv.Close(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "ModulatingValve", setter: "SetLevel",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, lvl := range []float64{0.0, 0.5, 1.0} {
					mv := newModulatingFixtureRef(t, w)
					_ = mv.SetLevel(ctx, lvl, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "ClimateIP", setter: "SetMode",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, m := range []climate.Mode{climate.ModeAuto, climate.ModeHeat, climate.ModeOff} {
					c := newClimateIPFixtureRef(t, w)
					_ = c.SetMode(ctx, m, pri)
					out = append(out, NormaliseCalls(w.Capture()))
				}
				return out
			},
		},
		{
			dpType: "ClimateIP", setter: "SetTemperature",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, temp := range []float64{5, 20, 30} {
					c := newClimateIPFixtureRef(t, w)
					_ = c.SetTemperature(ctx, temp, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "ClimateIP", setter: "EnableBoost",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				c := newClimateIPFixtureRef(t, w)
				_ = c.EnableBoost(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "ClimateIP", setter: "DisableBoost",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				c := newClimateIPFixtureRef(t, w)
				_ = c.DisableBoost(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		// ClimateIP SetProfile: KindIP writes 1-based ACTIVE_PROFILE as a single
		// SetValue once the device reports AUTO mode (the default state of a
		// freshly-constructed fixture). Mirrors the reference implementation's
		// CustomDpIpThermostat.set_profile (model/custom/climate.py:880-893);
		// confirmed by tests/test_model_climate.py:1228-1237, which asserts
		// `set_value(parameter="ACTIVE_PROFILE", value=1)` for WEEK_PROGRAM_1
		// once SET_POINT_MODE reports AUTO.
		{
			dpType: "ClimateIP", setter: "SetProfile",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, p := range []climate.Profile{climate.ProfileWeekProgram1, climate.ProfileWeekProgram2, climate.ProfileWeekProgram3} {
					c := newClimateIPFixtureRef(t, w)
					_ = c.SetProfile(ctx, p, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		// ClimateIP SetAway: KindIP batches the away-mode fields into one
		// PutParamset. Mirrors the reference implementation's
		// CustomDpIpThermostat.enable_away_mode_by_calendar
		// (model/custom/climate.py:841-852); confirmed by
		// tests/test_model_climate.py:1253-1265, which asserts
		// put_paramset(values={"SET_POINT_MODE": 2, "SET_POINT_TEMPERATURE": 17.0,
		// "PARTY_TIME_START": ..., "PARTY_TIME_END": ...}).
		//
		// Two known drifts are pinned here until the production code is
		// corrected: Go writes PARTY_TEMPERATURE instead of
		// SET_POINT_TEMPERATURE, and Go's PARTY_TIME_* encoding
		// ("02.01.06 15:04") does not match the reference's "%Y_%m_%d %H:%M".
		// PARTY_TIME_START is masked to a fixed placeholder (see
		// maskPartyTimeStart) because Climate.SetAway encodes it from
		// time.Now() and would otherwise make this case non-deterministic
		// regardless of the drift.
		{
			dpType: "ClimateIP", setter: "SetAway",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				c := newClimateIPAwayFixtureRef(t, w)
				until := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)
				_ = c.SetAway(ctx, until, 17.0, pri)
				return []WireCapture{maskPartyTimeStart(w.Capture())}
			},
		},
		{
			dpType: "ClimateRF", setter: "SetMode",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, m := range []climate.Mode{climate.ModeHeat, climate.ModeAuto, climate.ModeOff} {
					c := newClimateRFFixtureRef(t, w)
					_ = c.SetMode(ctx, m, pri)
					out = append(out, NormaliseCalls(w.Capture()))
				}
				return out
			},
		},
		{
			dpType: "ClimateRF", setter: "SetTemperature",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				var out []WireCapture
				for _, temp := range []float64{5, 15, 30} {
					c := newClimateRFFixtureRef(t, w)
					_ = c.SetTemperature(ctx, temp, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "SoundPlayer", setter: "StopSound",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				sp := newSoundPlayerFixtureRef(t, w)
				_ = sp.StopSound(ctx, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "SoundPlayerLED", setter: "TurnOn",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				led, addr := newSoundPlayerLEDFixtureRef(t, w)
				cfg := light.LedOnConfig{
					Brightness:  128,
					FlashTimeMS: 500,
					Repetitions: 3,
				}
				_ = led.TurnOn(ctx, cfg, w, addr, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		{
			dpType: "SoundPlayerLED", setter: "TurnOff",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				led, addr := newSoundPlayerLEDFixtureRef(t, w)
				_ = led.TurnOff(ctx, w, addr, pri)
				return []WireCapture{NormaliseCalls(w.Capture())}
			},
		},
		{
			dpType: "TextDisplay", setter: "Write",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				td := newTextDisplayFixtureRef(t, w)
				var out []WireCapture
				for _, row := range []textdisplay.Row{
					{ID: 1, Text: "Hello"},
					{ID: 2, Text: "World"},
					{ID: 3, Text: ""},
				} {
					_ = td.Write(ctx, row, pri)
					out = append(out, w.Capture())
				}
				return out
			},
		},
		{
			dpType: "TextDisplay", setter: "Clear",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				td := newTextDisplayFixtureRef(t, w)
				_ = td.Clear(ctx, 1, pri)
				return []WireCapture{w.Capture()}
			},
		},
		{
			dpType: "TextDisplay", setter: "WriteWithSound",
			run: func(t *testing.T, w *fakeWriter) []WireCapture {
				t.Helper()
				td := newTextDisplayFixtureRef(t, w)
				row := textdisplay.Row{ID: 1, Text: "Alert"}
				_ = td.WriteWithSound(ctx, row, textdisplay.SoundOptions{Sound: "LONG_SHORT"}, pri)
				return []WireCapture{w.Capture()}
			},
		},
	}

	for _, rc := range cases {
		rc := rc
		t.Run(rc.dpType+"/"+rc.setter, func(t *testing.T) {
			t.Parallel()

			// A missing reference file for a registered case must fail the
			// build, not silently skip: an unreviewed setter would otherwise
			// carry no ground-truth comparison at all and nobody would
			// notice. Every dpType/setter pair added to `cases` above must
			// ship a matching aiohomematic_reference/<DPType>__<Setter>.json
			// file in the same change.
			ref, ok := loadReferenceSnapshot(t, refDir, rc.dpType, rc.setter)
			if !ok {
				t.Fatalf("missing aiohomematic reference snapshot for %s/%s — every registered case must ship one; "+
					"run: python3 script/aiohomematic_wire_snapshots.py, or hand-author aiohomematic_reference/%s__%s.json "+
					"from the aiohomematic source (see README.md)", rc.dpType, rc.setter, rc.dpType, rc.setter)
			}

			w := NewFakeWriter()
			got := rc.run(t, w)

			if len(got) != len(ref.Inputs) {
				t.Fatalf("input count mismatch for %s/%s: reference has %d inputs, Go produces %d",
					rc.dpType, rc.setter, len(ref.Inputs), len(got))
			}

			var errs []string
			for i, want := range ref.Inputs {
				if diff := diffCalls(want.Label, want.Calls, []CapturedCall(got[i])); diff != "" {
					errs = append(errs, fmt.Sprintf("[aiohomematic_version=%s] %s", ref.AiohomematicVersion, diff))
				}
			}
			if len(errs) > 0 {
				t.Errorf("WIRE DRIFT for %s/%s against aiohomematic reference:\n%s\n\nFix the production code, then re-run.",
					rc.dpType, rc.setter, strings.Join(errs, "\n"))
			}
		})
	}
}

// ptrRef is a local helper (avoids redeclaration conflict with ptr in other files).
func ptrRef[T any](v T) *T { return &v }
