// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// A channel without a press parameter asks for no event entity, a channel
// without a custom data point for no channel entity, and a device without
// an update surface for no update entity. Each refusal is the absence of
// the thing in the model, read without the MQTT wiring.
func TestDiscoveryEventsAreAbsentWithoutTheirSubject(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: "0001ABCD", Model: "HmIP-STH", Name: "Probe",
	})
	ch := d.AddChannel("0001ABCD:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)

	if _, ok := channelEventDiscoveryEvent("ccu", "HmIP-RF", d, ch); ok {
		t.Error("a channel without a press parameter asks for an event entity")
	}
	b := &EventBridge{}
	if _, ok := b.customDPDiscoveryEvent("ccu", "HmIP-RF", d, ch); ok {
		t.Error("a channel without a custom data point asks for a channel entity")
	}
	if d.Update() == nil {
		if _, ok := updateDiscoveryEvent("ccu", "HmIP-RF", d); ok {
			t.Error("a device without an update surface asks for an update entity")
		}
	}
}
