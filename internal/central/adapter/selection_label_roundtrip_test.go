// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	sirencdp "github.com/SukramJ/openccu-loom/internal/model/custom/siren"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// upperLabeler stands in for the catalogue-backed labeler: it produces a
// label per token that is recognisably not the token.
type upperLabeler struct{}

func (upperLabeler) ValueListLabels(_, _ string, values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = "Label:" + v
	}
	return out
}

// selectionDeclarer is a custom data point that declares one localisable
// list, the shape a siren and an effect light both have.
type selectionDeclarer struct{}

func (selectionDeclarer) LocalisableSelections() []payload.LocalisableSelection {
	return []payload.LocalisableSelection{{
		BodyKey:   "available_tones",
		ArgKey:    "tone",
		Parameter: string(hmenum.ParameterAcousticAlarmSelection),
	}}
}

func selectionChannel(t *testing.T, values []string) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001", Model: "HmIP-ASIR"})
	ch := d.AddChannel("ABC0001:3", 3, "SIREN", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewActionSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterAcousticAlarmSelection),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsWrite,
			ValueList:  values,
		},
	}))
	return ch
}

// TestALocalisedChoiceResolvesBackToItsWireToken pins the reverse half
// of the localisation.
//
// The discovery payload shows the operator a label, so Home Assistant
// hands that label back on the command topic. The domain resolves a
// selection by exact VALUE_LIST match, so a label reaches nothing: the
// tone selector would look translated and change nothing.
//
// The raw token has to keep working in the same breath. It is what the
// REST and WebSocket surfaces send and what existing automations were
// written against, and a localisation that invalidates them trades one
// defect for a worse one.
func TestALocalisedChoiceResolvesBackToItsWireToken(t *testing.T) {
	t.Parallel()

	values := []string{"DISABLE_ACOUSTIC_SIGNAL", "FREQUENCY_RISING"}
	ch := selectionChannel(t, values)
	sink := (&MQTTCommandSink{}).WithSelectionLabeler(upperLabeler{})

	cases := []struct {
		name  string
		given string
		want  string
	}{
		{"the label the operator picked", "Label:FREQUENCY_RISING", "FREQUENCY_RISING"},
		{"the raw token still passes", "FREQUENCY_RISING", "FREQUENCY_RISING"},
		{"an unknown value is left alone", "SOMETHING_ELSE", "SOMETHING_ELSE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sink.resolveSelectionLabels(ch, selectionDeclarer{},
				map[string]any{"tone": tc.given})
			if got["tone"] != tc.want {
				t.Errorf("tone = %v, want %q", got["tone"], tc.want)
			}
		})
	}
}

// TestSelectionLabelsAreLeftAloneWithoutALabeler keeps the pre-
// localisation behaviour intact where no catalogues are wired: every
// command passes through untouched rather than resolving to nothing.
func TestSelectionLabelsAreLeftAloneWithoutALabeler(t *testing.T) {
	t.Parallel()

	ch := selectionChannel(t, []string{"FREQUENCY_RISING"})
	sink := &MQTTCommandSink{}
	got := sink.resolveSelectionLabels(ch, selectionDeclarer{}, map[string]any{"tone": "Label:X"})
	if got["tone"] != "Label:X" {
		t.Errorf("tone = %v, want it untouched when no labeler is wired", got["tone"])
	}
}

// TestInvokeChannelServiceResolvesALocalisedTone pins the call site, not
// the helper.
//
// The tests above construct the sink and call resolveSelectionLabels
// directly, which proves it works and says nothing about whether the
// command path reaches it. The wiring guard does not close that gap
// either: it asserts the setter has a production caller, not that the
// collaborator it installs is used. Driving InvokeChannelService — the
// method the MQTT command subscriber calls — is what fails when the call
// is dropped.
func TestInvokeChannelServiceResolvesALocalisedTone(t *testing.T) {
	t.Parallel()

	values := []string{"DISABLE_ACOUSTIC_SIGNAL", "FREQUENCY_RISING"}
	w := &recordingSelectionWriter{}
	unit, ch := sirenUnitForSink(t, values, w)

	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register central: %v", err)
	}
	sink := NewMQTTCommandSink(reg, nil).WithSelectionLabeler(upperLabeler{})

	err := sink.InvokeChannelService(context.Background(), "ccu", "HmIP-RF", "ABC0001", 3,
		"turn_on", map[string]any{"tone": "Label:FREQUENCY_RISING"}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("InvokeChannelService: %v", err)
	}
	if got := w.lastFor(hmenum.ParameterAcousticAlarmSelection); got != "FREQUENCY_RISING" {
		t.Errorf("acoustic selection on the wire = %q, want %q — the label the operator picked has "+
			"to become the token the device speaks somewhere on this path", got, "FREQUENCY_RISING")
	}
	_ = ch
}

// recordingSelectionWriter captures the strings written per parameter.
type recordingSelectionWriter struct {
	mu    sync.Mutex
	calls map[hmenum.Parameter]string
}

func (w *recordingSelectionWriter) SetValue(
	_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.calls == nil {
		w.calls = map[hmenum.Parameter]string{}
	}
	if s, ok := v.(string); ok {
		w.calls[p] = s
	}
	return nil
}

func (w *recordingSelectionWriter) lastFor(p hmenum.Parameter) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls[p]
}

// sirenUnitForSink builds a central holding one device whose channel
// carries a real siren custom data point.
func sirenUnitForSink(t *testing.T, values []string, w generic.Writer) (*central.Unit, *device.Channel) {
	t.Helper()
	unit, err := central.New(central.Config{Name: "ccu"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	d := device.New(device.Config{
		InterfaceID: "ccu-HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "ABC0001", Model: "HmIP-ASIR",
	})
	ch := d.AddChannel("ABC0001:3", 3, "SIREN", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewActionSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "ccu-HmIP-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterAcousticAlarmSelection),
		},
		Descriptor: hmproto.ParameterData{
			Type: hmenum.ParameterTypeEnum, Operations: hmenum.OperationsWrite, ValueList: values,
		},
		Writer: w,
	}))
	cdp := sirencdp.New(sirencdp.Config{
		Channel: ch, Writer: w,
		Capabilities: custom.SirenCapabilities{SupportsAcoustic: true},
	})
	if cdp == nil {
		t.Fatal("siren custom data point was not constructed")
	}
	ch.SetCustomDataPoint(cdp)
	unit.ModelRegistry.Put(d)
	return unit, ch
}
