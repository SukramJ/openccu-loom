// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// TestHubUniqueIDMatchesAcrossPlanes pins the two implementations of a hub
// entity's `unique_id` against each other: hub.Program / hub.Sysvar serve it
// on the REST/WS plane, and the MQTT discovery builder serves it on the
// broker. Both end up in the same Home Assistant registry, so a consumer
// that reads one plane and a consumer that reads the other must arrive at
// the same key for the same CCU object.
//
// The guard exists because they drifted. The model side moved from the name
// slug to the CCU id, and the MQTT program builder kept the name — so the
// same program was `…_program_prg-42` on one plane and
// `…_program_morning-lights` on the other, and the migration note published
// a rule only half the daemon followed. Nothing failed: each side had a test
// asserting its own answer.
func TestHubUniqueIDMatchesAcrossPlanes(t *testing.T) {
	t.Parallel()

	const (
		central  = "ccu-01"
		serial   = "3014F711A0001234"
		serial10 = "11a0001234"
	)

	builder := mqtt.NewDefaultDiscoveryBuilder(mqtt.NewTopicBuilder("openccu-loom"), central)
	builder.SetHubInfoFor(central, mqtt.HubInfo{Serial: serial})

	t.Run("program", func(t *testing.T) {
		t.Parallel()
		const (
			id   = "PRG_42"
			name = "Morning Lights"
		)
		model := hub.NewProgram(central, id, name, "", false, nil).CanonicalUniqueID(serial10)

		// The roles builder is the live path; a program that declares no
		// roles falls through to the single-switch shape, which is what an
		// ordinary CCU program is.
		items := builder.BuildProgramDiscoveryRoles(central, mqtt.HubProgramSpec{ID: id, Name: name}, nil)
		if len(items) != 1 {
			t.Fatalf("program discovery produced %d items, want 1", len(items))
		}
		broker := discoveryUniqueID(t, items[0])
		if model != broker {
			t.Errorf("program %q/%q: model plane = %q, MQTT plane = %q", id, name, model, broker)
		}
		if model == "" {
			t.Error("both planes produced an empty key — the comparison proves nothing")
		}
	})

	t.Run("sysvar with a vid", func(t *testing.T) {
		t.Parallel()
		const (
			name = "Außen Temperatur"
			vid  = 12345
		)
		sv := hub.NewSysvar(central, name, "", "", nil)
		sv.ApplyMeta(hub.SysvarMeta{Vid: vid})
		model := sv.CanonicalUniqueID(serial10)
		broker := discoveryUniqueID(t, builder.BuildSysvarDiscovery(central, mqtt.HubSysvarSpec{Name: name, Vid: vid}))
		if model != broker {
			t.Errorf("sysvar %q (vid %d): model plane = %q, MQTT plane = %q", name, vid, model, broker)
		}
		if model == "" {
			t.Error("both planes produced an empty key — the comparison proves nothing")
		}
	})

	t.Run("sysvar before the vid resolves", func(t *testing.T) {
		t.Parallel()
		const name = "Außen Temperatur"
		model := hub.NewSysvar(central, name, "", "", nil).CanonicalUniqueID(serial10)
		broker := discoveryUniqueID(t, builder.BuildSysvarDiscovery(central, mqtt.HubSysvarSpec{Name: name}))
		if model != broker {
			t.Errorf("sysvar %q without a vid: model plane = %q, MQTT plane = %q", name, model, broker)
		}
	})
}

// discoveryUniqueID pulls the unique_id out of a built discovery payload.
func discoveryUniqueID(t *testing.T, item mqtt.DiscoveryItem) string {
	t.Helper()
	if !item.OK {
		t.Fatalf("discovery builder returned OK=false; nothing to compare")
	}
	var body map[string]any
	if err := json.Unmarshal(item.Payload, &body); err != nil {
		t.Fatalf("unmarshal discovery payload: %v", err)
	}
	id, _ := body["unique_id"].(string)
	return id
}
