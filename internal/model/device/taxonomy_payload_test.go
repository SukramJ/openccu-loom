// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestMQTTDeviceInfoCarriesTaxonomy pins the taxonomy on the device and
// channel info payloads the raw MQTT plane publishes: every assignment
// with enum, path, name and parent path, and no member at all for an
// address without assignments.
func TestMQTTDeviceInfoCarriesTaxonomy(t *testing.T) {
	t.Parallel()
	d := New(Config{Address: "VCU0000001", Interface: hmenum.InterfaceHmIPRF, InterfaceID: "HmIP-RF", Model: "HmIP-BSM"})
	ch := d.AddChannel("VCU0000001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	kitchen := []taxonomy.Assignment{{Ref: taxonomy.Ref{Enum: taxonomy.EnumRoom, Path: "eg/kueche"}, Name: "Küche"}}
	d.SetTaxonomy(kitchen)
	ch.SetTaxonomy(kitchen)

	for what, info := range map[string]any{"device": d.Info(), "channel": ch.Info()} {
		raw, err := json.Marshal(info)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if !strings.Contains(string(raw), `"taxonomy":[{"enum":"room","path":"eg/kueche","name":"Küche","parent_path":"eg"}]`) {
			t.Errorf("%s info = %s, want the kitchen assignment", what, raw)
		}
	}
	bare := New(Config{Address: "VCU0000002", Interface: hmenum.InterfaceHmIPRF, InterfaceID: "HmIP-RF", Model: "HmIP-BSM"})
	raw, _ := json.Marshal(bare.Info())
	if strings.Contains(string(raw), `"taxonomy"`) {
		t.Errorf("a device without assignments carries a taxonomy member: %s", raw)
	}
}
