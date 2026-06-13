// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"testing"
)

// TestEventSuppressionForIgnoredModel is the parity tripwire for the
// IGNORE_DEVICES_FOR_DATA_POINT_EVENTS gate on the MQTT event-discovery
// path. HmIP-PS* schaltaktoren (HmIP-PSM, HmIP-PS) expose a
// KEY_TRANSCEIVER channel carrying PRESS_* parameters, but the reference
// stack never spawns a keypress event for these models. The discovery
// builder must therefore decline the event entity for them — both the
// aggregated (≥2 PRESS_* params) and the per-parameter single-press case.
func TestEventSuppressionForIgnoredModel(t *testing.T) {
	t.Parallel()

	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")

	t.Run("multi_press_suppressed", func(t *testing.T) {
		t.Parallel()
		ch := multiPressChannel()
		for _, pressParam := range []string{"PRESS_SHORT", "PRESS_LONG"} {
			ev := Event{
				Interface:     "HmIP-RF",
				DeviceAddress: "0001D8A991F2DC",
				DeviceName:    "Belüftungsanlage",
				Model:         "HmIP-PSM",
				ChannelNo:     1,
				Parameter:     pressParam,
				Channel:       ch,
			}
			if _, _, _, _, ok := db.Build(ev); ok {
				t.Fatalf("Build(%q) on HmIP-PSM must be suppressed (no event entity)", pressParam)
			}
		}
	})

	t.Run("single_press_suppressed", func(t *testing.T) {
		t.Parallel()
		ev := Event{
			Interface:     "HmIP-RF",
			DeviceAddress: "0001D8A991F2DC",
			Model:         "HmIP-PS",
			ChannelNo:     1,
			Parameter:     "PRESS_SHORT",
			Channel:       singlePressChannel(),
		}
		if _, _, _, _, ok := db.Build(ev); ok {
			t.Fatalf("single-press PRESS_SHORT on HmIP-PS must be suppressed")
		}
	})

	t.Run("non_ignored_model_still_emits", func(t *testing.T) {
		t.Parallel()
		ev := Event{
			Interface:     "HmIP-RF",
			DeviceAddress: "0034WRC2",
			Model:         "HmIP-WRC2",
			ChannelNo:     1,
			Parameter:     "PRESS_SHORT",
			Channel:       multiPressChannel(),
		}
		comp, _, _, _, ok := db.Build(ev)
		if !ok || comp != string(HAComponentEvent) {
			t.Fatalf("HmIP-WRC2 multi-press must still emit an event entity (ok=%v comp=%q)", ok, comp)
		}
	})
}
