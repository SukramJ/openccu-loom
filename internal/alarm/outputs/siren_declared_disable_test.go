// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package outputs

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	sirencdp "github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// The descriptor these tests build is SYNTHETIC and says so out loud.
//
// The sourced fact is the opposite: every HmIP-ASIR variant carried by
// any device description in this tree declares
// ACOUSTIC_ALARM_SELECTION with DEFAULT == VALUE_LIST[0] ==
// "DISABLE_ACOUSTIC_SIGNAL" (see
// internal/model/custom/siren/siren_selection_wire_test.go). On those
// devices the declared default and the list head are the same string,
// so no test built on them can tell the two rules apart — which is
// precisely how a second, positional copy of the rule survived in the
// output driver.
//
// Putting the disable entry at index 1 is therefore not a claim about
// hardware. It is the only way to make the assertion discriminate: it
// separates "the selection the descriptor declares" from "whatever the
// value list happens to list first", and no device is needed to decide
// which of those the driver must ask the model for.
const (
	sirenDisableDeclaredLabel = "DISABLE_ACOUSTIC_SIGNAL"
	sirenDisableHeadLabel     = "FREQ_HIGH"
	sirenDisableOpticalHead   = "DISABLE_OPTICAL_SIGNAL"
	sirenDisableOpticalTail   = "BLINKING_ALTERNATELY_REPEATING"
)

// sirenDisableWriter records every parameter write a real
// *siren.Siren issues, so the assertion lands on the wire effect
// rather than on the OnConfig the driver assembled.
type sirenDisableWriter struct {
	mu    sync.Mutex
	calls map[hmenum.Parameter]any
}

func newSirenDisableWriter() *sirenDisableWriter {
	return &sirenDisableWriter{calls: make(map[hmenum.Parameter]any)}
}

func (w *sirenDisableWriter) SetValue(
	_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls[p] = v
	return nil
}

func (w *sirenDisableWriter) sent(p hmenum.Parameter) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	v, ok := w.calls[p]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, true
}

// newSirenDisableRig builds a real *siren.Siren on the given channel
// whose ACOUSTIC_ALARM_SELECTION descriptor declares its DEFAULT at
// VALUE_LIST index 1. The selections are write-only ENUMs, the shape
// the resolver actually produces for them.
func newSirenDisableRig(t *testing.T, channelAddress string) (*sirencdp.Siren, *sirenDisableWriter) {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "0001D8A9BC7654", Model: "HmIP-ASIR"})
	ch := d.AddChannel(channelAddress, 3, "SIREN", hmenum.ParamsetKeyValues)

	active := func(p hmenum.Parameter) {
		ch.Put(generic.NewBinarySensor(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: channelAddress,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeBool,
				Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			},
		}))
	}
	selectionDP := func(p hmenum.Parameter, values []string, def string) {
		ch.Put(generic.NewActionSelect(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: channelAddress,
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
	selectionDP(hmenum.ParameterAcousticAlarmSelection,
		[]string{sirenDisableHeadLabel, sirenDisableDeclaredLabel}, sirenDisableDeclaredLabel)
	selectionDP(hmenum.ParameterOpticalAlarmSelection,
		[]string{sirenDisableOpticalHead, sirenDisableOpticalTail}, sirenDisableOpticalHead)

	w := newSirenDisableWriter()
	return sirencdp.New(sirencdp.Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: sirencdp.BasicSirenCaps,
	}), w
}

// TestSilentCycleWritesTheSirensDeclaredDisableSelectionNotValueListZero
// pins the acoustic half of the atomic write a silent cycle sends.
//
// A silent mode drops the acoustic row before the grouping, so the
// remaining optical row alone reaches the device — and an ASIR ignores
// partial paramset writes, so that write must itself name the selection
// that silences the tone. Which selection that is, is the device's
// answer, read off the parameter descriptor; the driver used to answer
// it from the head of the flattened tone list, a projection with the
// declared DEFAULT already lost.
func TestSilentCycleWritesTheSirensDeclaredDisableSelectionNotValueListZero(t *testing.T) {
	h := newHarness(t)
	h.seedOutputs(sharedSirenChannelRows())
	dev, w := newSirenDisableRig(t, asirChannel)
	h.resolver.addSiren(testCentral, asirChannel, dev)

	opts := engine.FireOptions{Policy: engine.OutputPolicy{Silent: true}}
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(32, hmenum.AlarmModeFull), opts); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	got, sent := w.sent(hmenum.ParameterAcousticAlarmSelection)
	if !sent {
		t.Fatalf("the silent cycle sent no %s at all: the device keeps whatever tone was selected last",
			hmenum.ParameterAcousticAlarmSelection)
	}
	if got != sirenDisableDeclaredLabel {
		t.Errorf("acoustic selection = %q, want %q (the selection the descriptor declares). "+
			"%q is only the VALUE_LIST head — on this device a real tone, so a silent cycle sounds "+
			"the siren the mode exists to keep quiet.",
			got, sirenDisableDeclaredLabel, sirenDisableHeadLabel)
	}
}

// TestTestFireOpticalOnlyUsesTheDeclaredDisableSelection pins the same
// rule on the second driver path: an operator's optical-only test fire
// writes the same atomic paramset, so it silences the tone the same
// way or it makes the house howl during a configuration check.
func TestTestFireOpticalOnlyUsesTheDeclaredDisableSelection(t *testing.T) {
	h := newHarness(t)
	row := outputRow("sirO", hmenum.AlarmOutputClassOpticalSiren, OutputConfig{})
	h.seedOutputs(row)
	dev, w := newSirenDisableRig(t, row.ChannelAddress)
	h.resolver.addSiren(testCentral, row.ChannelAddress, dev)

	if err := h.mgr.TestFire(h.ctx, row.ID, true); err != nil {
		t.Fatalf("TestFire: %v", err)
	}

	got, sent := w.sent(hmenum.ParameterAcousticAlarmSelection)
	if !sent {
		t.Fatalf("the optical-only test fire sent no %s at all: the write re-sends the tone "+
			"selected last", hmenum.ParameterAcousticAlarmSelection)
	}
	if got != sirenDisableDeclaredLabel {
		t.Errorf("acoustic selection = %q, want %q (the selection the descriptor declares); "+
			"%q is the VALUE_LIST head, a real tone on this device",
			got, sirenDisableDeclaredLabel, sirenDisableHeadLabel)
	}
}
