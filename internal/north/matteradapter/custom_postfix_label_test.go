// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// stubPostfixSource is a custom data point that names itself with a
// postfix, the way the operating lock does (NamePostfix "BUTTON_LOCK"
// on a thermostat's channel 0).
type stubPostfixSource struct {
	stubEndpointSource
	postfix string
}

func (s *stubPostfixSource) NamePostfix() string { return s.postfix }

// stubTranslator answers one parameter with a fixed label and knows no
// other, like a catalogue that carries the postfix's entry only.
type stubTranslator struct{ parameter, label string }

func (s stubTranslator) ChannelTypedParameterLabelOk(_, parameter string) (string, bool) {
	if parameter == s.parameter {
		return s.label, true
	}
	return "", false
}

// TestAssemble_CustomPostfixReachesTheNodeLabel pins that a custom data
// point's name postfix lands on its endpoint's NodeLabel through the
// same label route REST and MQTT use for it — translated when the
// catalogue has the entry, title-cased otherwise — while a source
// without a postfix keeps the bare channel name. Without it the
// operating lock of a thermostat is a door lock under the thermostat's
// own name on every Matter controller.
func TestAssemble_CustomPostfixReachesTheNodeLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		postfix string
		labels  device.ParameterTranslator
		want    string
	}{
		{name: "catalogue label", postfix: "BUTTON_LOCK", labels: stubTranslator{"BUTTON_LOCK", "Bediensperre"}, want: "Thermostat (Bediensperre)"},
		{name: "no catalogue entry: title-cased postfix", postfix: "BUTTON_LOCK", labels: nil, want: "Thermostat (Button Lock)"},
		{name: "no postfix: channel name alone", postfix: "", labels: stubTranslator{"BUTTON_LOCK", "Bediensperre"}, want: "Thermostat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const chAddr = "THERM001:0"
			dev := newDevice("THERM001", "Thermostat")
			ch := addChannel(dev, chAddr, 0)
			ch.SetCustomDataPoint(&stubPostfixSource{
				stubEndpointSource: stubEndpointSource{key: dpKey(chAddr, "BUTTON_LOCK"), deviceType: 0x000A},
				postfix:            tc.postfix,
			})
			cfg := validConfig()
			cfg.Labels = tc.labels
			a := matteradapter.New(cfg, nil)
			top, err := assembleDevicesWith(context.Background(), a, newFakeStore(), testEndpointConfig(), []matteradapter.DeviceSnapshot{
				{CentralName: "ccu1", Devices: []*device.Device{dev}},
			})
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			if len(top.Endpoints) != 3 {
				t.Fatalf("expected root + aggregator + bridged, got %d endpoints", len(top.Endpoints))
			}
			if got := top.Endpoints[2].FriendlyName; got != tc.want {
				t.Errorf("FriendlyName = %q, want %q", got, tc.want)
			}
		})
	}
}
