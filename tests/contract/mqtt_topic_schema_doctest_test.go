// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// TestMQTTTopicSchemaDocExamples pins every concrete topic string that
// appears verbatim in docs/mqtt-topic-schema.md against the
// live [mqtt.TopicBuilder] output.
//
// A failure means one of two things:
// - TopicBuilder changed without updating the schema doc (code drift).
// - The schema doc was updated with a new shape that has not yet been
// implemented (spec-ahead-of-code drift).
//
// Either case requires a deliberate reconciliation: fix the doc to match
// the code, or fix the code to match the doc. Do NOT just silence the
// failure.
//
// Source: docs/mqtt-topic-schema.md §"Concrete examples" and §"Schema".
//
// Assumption fixture: base="openccu-loom", central="GoOtto",
// iface="HmIP-RF", device address="000C9709AEF157", channel=1.

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// docTopicCase pairs a human-readable label, the topic string from
// docs/mqtt-topic-schema.md, and the string produced by the
// TopicBuilder for the same inputs.
type docTopicCase struct {
	name string
	// docTopic is the verbatim string from the migration doc.
	docTopic string
	// got is the TopicBuilder output for the same semantic inputs.
	got string
}

// TestMQTTTopicSchemaDoc_StateTopics exercises the "State topics" table
// and the "Concrete mapping examples" section for per-DP state.
func TestMQTTTopicSchemaDoc_StateTopics(t *testing.T) {
	t.Parallel()

	b := mqtt.NewTopicBuilder("openccu-loom")
	const (
		central = "GoOtto"
		iface   = "HmIP-RF"
		addr    = "000C9709AEF157"
		ch      = 1
	)

	cases := []docTopicCase{
		{
			// §"State topics" table row 1: Per-DP VALUES state
			// §"Concrete mapping examples" / "Actual temperature"
			name:     "values-state/ACTUAL_TEMPERATURE",
			docTopic: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/1/values/ACTUAL_TEMPERATURE",
			got:      b.ParameterState(central, iface, addr, ch, string(payload.BucketValues), "ACTUAL_TEMPERATURE"),
		},
		{
			// §"State topics" table row 2: Per-DP MASTER state
			name:     "master-state/TEMPERATURE_MINIMUM",
			docTopic: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/1/master/TEMPERATURE_MINIMUM",
			got:      b.ParameterState(central, iface, addr, ch, string(payload.BucketMaster), "TEMPERATURE_MINIMUM"),
		},
		{
			// §"State topics" table row 3: Custom-DP derived state
			name:     "custom-state/climate",
			docTopic: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/1/custom/climate",
			got: b.SlotState(central, iface, payload.TopicSlot{
				Address:   addr,
				Channel:   ch,
				Bucket:    payload.BucketCustom,
				Parameter: "climate",
			}),
		},
		{
			// §"State topics" table row 4: Device availability
			name:     "device-availability",
			docTopic: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/availability",
			got:      b.DeviceAvailability(central, iface, addr),
		},
		{
			// §"State topics" table row 5: Device info snapshot
			name:     "device-info",
			docTopic: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/info",
			got:      b.DeviceInfo(central, iface, addr),
		},
		{
			// §"State topics" table row 6: Device diagnostics
			name:     "device-diagnostics",
			docTopic: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/diagnostics",
			got:      b.DeviceDiagnostics(central, iface, addr),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.docTopic {
				t.Errorf("topic mismatch for %q:\n  doc says: %q\n  builder:  %q\n  → update docs/mqtt-topic-schema.md or fix TopicBuilder",
					tc.name, tc.docTopic, tc.got)
			}
		})
	}
}

// TestMQTTTopicSchemaDoc_CommandTopics exercises the "Command (set) topics"
// table rows.
func TestMQTTTopicSchemaDoc_CommandTopics(t *testing.T) {
	t.Parallel()

	b := mqtt.NewTopicBuilder("openccu-loom")
	const (
		central = "GoOtto"
		iface   = "HmIP-RF"
		addr    = "000C9709AEF157"
		ch      = 1
	)

	cases := []docTopicCase{
		{
			// §"Command topics" table row 1: Write single parameter VALUES
			// §"Concrete mapping examples" / "Set-point temperature"
			name:     "values-set/SET_POINT_TEMPERATURE",
			docTopic: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/1/values/SET_POINT_TEMPERATURE/set",
			got:      b.ParameterCommand(central, iface, addr, ch, string(payload.BucketValues), "SET_POINT_TEMPERATURE"),
		},
		{
			// §"Command topics" table row 2: Write MASTER parameter
			name:     "master-set/TEMPERATURE_MINIMUM",
			docTopic: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/1/master/TEMPERATURE_MINIMUM/set",
			got:      b.ParameterCommand(central, iface, addr, ch, string(payload.BucketMaster), "TEMPERATURE_MINIMUM"),
		},
		{
			// §"Command topics" table row 3: Custom-DP service method
			// §"Concrete mapping examples" / "Climate service method"
			name:     "custom-service-method/climate/set_mode",
			docTopic: "openccu-loom/GoOtto/HmIP-RF/000C9709AEF157/1/custom/climate/set/set_mode",
			got: b.CustomDPServiceMethod(central, iface,
				payload.TopicSlot{Address: addr, Channel: ch, Bucket: payload.BucketCustom, Parameter: "climate"},
				"set_mode"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.docTopic {
				t.Errorf("topic mismatch for %q:\n  doc says: %q\n  builder:  %q\n  → update docs/mqtt-topic-schema.md or fix TopicBuilder",
					tc.name, tc.docTopic, tc.got)
			}
		})
	}
}

// TestMQTTTopicSchemaDoc_BridgeHubTopics exercises the "Bridge / hub status"
// table and the concrete hub examples.
func TestMQTTTopicSchemaDoc_BridgeHubTopics(t *testing.T) {
	t.Parallel()

	b := mqtt.NewTopicBuilder("openccu-loom")
	const central = "GoOtto"

	cases := []docTopicCase{
		{
			// §"Bridge / hub status" row: Bridge online/offline (LWT)
			name:     "bridge-status",
			docTopic: "openccu-loom/bridge/status",
			got:      b.BridgeStatus(),
		},
		{
			// §"Bridge / hub status" row: Bridge health
			name:     "bridge-health",
			docTopic: "openccu-loom/bridge/health",
			got:      b.BridgeHealth(),
		},
		{
			// §"Bridge / hub status" row: CCU connection status
			// §"Concrete mapping examples" / "CCU online status"
			name:     "hub-status",
			docTopic: "openccu-loom/GoOtto/hub/status",
			got:      b.HubStatus(central),
		},
		{
			// §"Bridge / hub status" row: CCU info snapshot
			name:     "hub-info",
			docTopic: "openccu-loom/GoOtto/hub/info",
			got:      b.HubInfo(central),
		},
		{
			// §"Bridge / hub status" row: System-variable state
			// §"Concrete mapping examples" / "System variable"
			name:     "hub-sysvar-state/Presence",
			docTopic: "openccu-loom/GoOtto/hub/sysvars/Presence/state",
			got:      naming.MQTTHubSysvarState(b.Base, central, "Presence"),
		},
		{
			// §"Bridge / hub status" row: System-variable set
			name:     "hub-sysvar-set/Presence",
			docTopic: "openccu-loom/GoOtto/hub/sysvars/Presence/set",
			got:      naming.MQTTHubSysvarCommand(b.Base, central, "Presence"),
		},
		{
			// §"Bridge / hub status" row: Program trigger
			name:     "hub-program-trigger/12",
			docTopic: "openccu-loom/GoOtto/hub/programs/12/trigger",
			got:      naming.MQTTHubProgramTrigger(b.Base, central, "12"),
		},
		{
			// §"Bridge / hub status" row: Interface connectivity
			name:     "hub-connectivity/HmIP-RF",
			docTopic: "openccu-loom/GoOtto/hub/connectivity/HmIP-RF",
			got:      naming.MQTTHubConnectivity(b.Base, central, "HmIP-RF"),
		},
		{
			// §"Bridge / hub status" row: System status event
			name:     "system-status",
			docTopic: "openccu-loom/GoOtto/system/status",
			got:      b.SystemStatus(central),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.docTopic {
				t.Errorf("topic mismatch for %q:\n  doc says: %q\n  builder:  %q\n  → update docs/mqtt-topic-schema.md or fix TopicBuilder",
					tc.name, tc.docTopic, tc.got)
			}
		})
	}
}

// TestMQTTTopicSchemaDoc_DiscoveryTopic exercises the HA Discovery config
// Topic, which the doc states is identical
func TestMQTTTopicSchemaDoc_DiscoveryTopic(t *testing.T) {
	t.Parallel()

	b := mqtt.NewTopicBuilder("openccu-loom")

	// §"HA Discovery" table: homeassistant/<component>/<node_id>/<object_id>/config
	got := b.DiscoveryConfig("climate", "gootto_000c9709aef157", "1_climate")
	want := "homeassistant/climate/gootto_000c9709aef157/1_climate/config"
	if got != want {
		t.Errorf("DiscoveryConfig mismatch:\n  doc says: %q\n  builder:  %q\n  → update docs/mqtt-topic-schema.md or fix TopicBuilder",
			want, got)
	}
}
