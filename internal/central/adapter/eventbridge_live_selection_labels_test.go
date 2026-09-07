// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// liveLabeler is the full [mqtt.ParameterLabeler] + [mqtt.ValueListLabeler]
// pair the daemon installs; the boot snapshot and the live path must see
// the same localised selections through it.
type liveLabeler struct{ upperLabeler }

func (liveLabeler) ParameterLabel(_, parameter string) string { return parameter }

func (liveLabeler) ParameterLabelOk(_, parameter string) (string, bool) { return parameter, true }

// TestLivePublishEventCarriesLocalisedSelectionLabels pins that the event
// the live path builds for a custom data point carries the localised
// selection labels the boot snapshot already carries. A discovery payload
// re-published after a value change used to fall back to the raw enum
// tokens because only publishCustomDPDiscoverySnapshot resolved them.
func TestLivePublishEventCarriesLocalisedSelectionLabels(t *testing.T) {
	t.Parallel()

	values := []string{"DISABLE_ACOUSTIC_SIGNAL", "FREQUENCY_RISING"}
	unit, ch := sirenUnitForSink(t, values, &recordingSelectionWriter{})
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register central: %v", err)
	}
	b := NewEventBridge(reg, nil, nil).WithParameterLabels(liveLabeler{})

	key := hmtypes.DataPointKey{
		InterfaceID: "ccu-HmIP-RF", ChannelAddress: ch.Address,
		ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterAcousticAlarmSelection),
	}
	ev, _, ok, _ := b.buildPublishEvent("ccu", "HmIP-RF", "ABC0001", ch.Address, 3,
		"HmIP-ASIR", "Sirene", key, "FREQUENCY_RISING", hmenum.ParamsetKeyValues)
	if !ok {
		t.Fatal("buildPublishEvent rejected the siren selection event; the pin cannot reach the labels")
	}
	if ev.Source == nil {
		t.Fatal("the live event carries no custom-DP source; the labels are resolved from it")
	}
	labels := ev.SelectionLabels["available_tones"]
	if len(labels) != len(values) || labels[1] != "Label:FREQUENCY_RISING" {
		t.Fatalf("live event selection labels = %v, want the localised list %v — "+
			"the live discovery payload falls back to raw enum tokens", ev.SelectionLabels, []string{"Label:DISABLE_ACOUSTIC_SIGNAL", "Label:FREQUENCY_RISING"})
	}
}
