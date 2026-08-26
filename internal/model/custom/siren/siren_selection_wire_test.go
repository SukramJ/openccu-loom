// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The wire facts these tests are built on, read off the embedded
// HmIP-ASIR / HmIP-ASIR-2 / HmIP-ASIR-O device descriptions:
//
//	ACOUSTIC_ALARM_SELECTION  TYPE=ENUM  OPERATIONS=2  DEFAULT='DISABLE_ACOUSTIC_SIGNAL'
//	OPTICAL_ALARM_SELECTION   TYPE=ENUM  OPERATIONS=2  DEFAULT='DISABLE_OPTICAL_SIGNAL'
//
// OPERATIONS=2 is write-only, so the resolver produces a
// *generic.ActionSelect and the parameter never reports a value. Both
// facts matter: a fixture that models these as readable string sensors
// describes a device that does not exist, and every assertion made
// against it holds for a shape production never sees.
const (
	acousticDisableLabel = "DISABLE_ACOUSTIC_SIGNAL"
	opticalDisableLabel  = "DISABLE_OPTICAL_SIGNAL"
)

var (
	acousticSelectionValues = []string{
		acousticDisableLabel, "FREQUENCY_RISING", "FREQUENCY_FALLING", "FREQUENCY_RISING_AND_FALLING",
	}
	opticalSelectionValues = []string{
		opticalDisableLabel, "BLINKING_ALTERNATELY_REPEATING", "BLINKING_BOTH_REPEATING",
	}
)

// newWireRig builds a siren whose alarm-selection data points carry the
// real wire descriptor — write-only ENUM with a VALUE_LIST and a
// string-labelled DEFAULT — and therefore the shape the resolver
// actually produces for them.
func newWireRig(t *testing.T, w Writer, caps custom.SirenCapabilities) (*Siren, *device.Channel) {
	t.Helper()
	const address = "HmIP-ASIR:3"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001", Model: "HmIP-ASIR"})
	ch := d.AddChannel(address, 3, "SIREN", hmenum.ParamsetKeyValues)

	active := func(p hmenum.Parameter) {
		ch.Put(generic.NewBinarySensor(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeBool,
				Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			},
		}))
	}
	selection := func(p hmenum.Parameter, values []string, def string) {
		ch.Put(generic.NewActionSelect(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeEnum,
				Operations: hmenum.OperationsWrite,
				ValueList:  values,
				Default:    []byte(`"` + def + `"`),
			},
		}))
	}
	active(hmenum.ParameterAcousticAlarmActive)
	active(hmenum.ParameterOpticalAlarmActive)
	selection(hmenum.ParameterAcousticAlarmSelection, acousticSelectionValues, acousticDisableLabel)
	selection(hmenum.ParameterOpticalAlarmSelection, opticalSelectionValues, opticalDisableLabel)

	return New(Config{Channel: ch, Writer: w, Capabilities: caps}), ch
}

// TestSirenTurnOffSendsTheDisableLabel is the reproducer for a silent
// alarm defect: TurnOff has to name the value that silences the device.
//
// The selection parameters are write-only ENUMs, so the resolver builds
// an ActionSelect for them. While the siren looked them up as readable
// string sensors, both fields were nil on every device — and the
// disable-label lookup, which reads the descriptor off that field,
// returned the empty string. TurnOff then wrote "" into an ENUM
// parameter: not "leave it alone", but a value the CCU does not accept.
//
// The whole existing siren suite passed throughout, because its fixture
// created the selections as readable STRING sensors.
func TestSirenTurnOffSendsTheDisableLabel(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	s, _ := newWireRig(t, w, custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true})

	if err := s.TurnOff(context.Background(), hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("TurnOff: %v", err)
	}

	for _, tc := range []struct {
		param hmenum.Parameter
		want  string
	}{
		{hmenum.ParameterAcousticAlarmSelection, acousticDisableLabel},
		{hmenum.ParameterOpticalAlarmSelection, opticalDisableLabel},
	} {
		got, sent := w.has(tc.param)
		if !sent {
			t.Errorf("TurnOff sent no %s at all — the device keeps whatever alarm was selected last", tc.param)
			continue
		}
		if got != tc.want {
			t.Errorf("TurnOff sent %s=%#v, want %q. An empty or wrong value is rejected by the CCU, so the "+
				"siren is not silenced.", tc.param, got, tc.want)
		}
	}
}

// TestSirenTurnOnWithoutSelectionIsDeterministic is the other half of the
// same defect. With no explicit selection, TurnOn falls back to the
// declared default; with the field nil that fallback was empty, so no
// selection was sent and the device reused whatever was last set — which,
// after a TurnOff, is the disable tone. An alarm that silently does not
// sound is worse than one that fails loudly.
func TestSirenTurnOnWithoutSelectionIsDeterministic(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	s, _ := newWireRig(t, w, custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true})

	if err := s.TurnOn(context.Background(), OnConfig{}, hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("TurnOn: %v", err)
	}

	for _, param := range []hmenum.Parameter{
		hmenum.ParameterAcousticAlarmSelection,
		hmenum.ParameterOpticalAlarmSelection,
	} {
		got, sent := w.has(param)
		if !sent {
			t.Errorf("TurnOn sent no %s — the device sounds whatever was selected last, including the "+
				"disable tone a preceding TurnOff left behind", param)
			continue
		}
		if s, _ := got.(string); s == "" {
			t.Errorf("TurnOn sent an empty %s, which the CCU rejects", param)
		}
	}
}

// TestSirenExposesTheAvailableSelections pins that the label lists an
// operator picks from survive the shape change — they come off the same
// data point's VALUE_LIST.
func TestSirenExposesTheAvailableSelections(t *testing.T) {
	t.Parallel()
	s, _ := newWireRig(t, &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true})

	if got := s.AvailableTones(); len(got) != len(acousticSelectionValues) {
		t.Errorf("AvailableTones() = %v, want the parameter's %d VALUE_LIST entries",
			got, len(acousticSelectionValues))
	}
	if got := s.AvailableLights(); len(got) != len(opticalSelectionValues) {
		t.Errorf("AvailableLights() = %v, want the parameter's %d VALUE_LIST entries",
			got, len(opticalSelectionValues))
	}
}
