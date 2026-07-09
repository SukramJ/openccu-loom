// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// ws_broadcast_emitter_test.go — WS-broadcast emitter binding
//
// TestWSCommandsMatchPinnedSchema (wsapi_schema_test.go) exempts every
// `kind: "broadcast"` entry from the registered-command match, because
// broadcasts are server-pushed via hub.Publish rather than dispatched
// through router.Register — there was previously no structural test
// binding the 23 documented broadcasts to a real production emitter.
// TestEventTypeStringsStable (event_catalogue_test.go) only pins the
// wire-level string of 9 of them.
//
// This file closes that gap with a hand-curated wiring table: for each
// broadcast in assets/wsapi.json it names the production source file(s)
// that carry the emitter and a set of literal tokens that must appear
// there. Where the wire identity is a typed Go constant (an
// hmevent.EventType or an exported handlers.MatterTopicXxx string), the
// table additionally pins the constant's value against the schema name
// at compile time — a renamed or re-valued constant fails the build or
// the assertion, not silently. A declared broadcast with no matching
// entry, or an entry whose tokens no longer appear in its file(s), means
// the emitter was renamed or removed without updating the schema — this
// test is the tripwire.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// wsBroadcastEmitter documents where a broadcast's production emitter
// lives and what the wire source must literally contain to prove it is
// still wired to a real hub.Publish / publishMatterEvent call site.
type wsBroadcastEmitter struct {
	// Files lists the repo-relative source file(s) whose concatenated
	// content must contain every entry in Tokens.
	Files []string
	// Tokens are literal (whitespace-normalized) Go source snippets
	// that must each appear somewhere across Files. A missing token
	// means the emitter code moved or was deleted.
	Tokens []string
	// WireValue, when non-empty, is the compile-time-resolved wire
	// identity (an hmevent.EventType string value or an exported
	// handlers.MatterTopicXxx constant) that must equal the broadcast
	// name. Left empty for the three hub-model-singleton broadcasts
	// whose wire identity is an unexported local constant in the ws
	// package (checked only via Tokens below).
	WireValue string
}

// wsBroadcastEmitters is the wiring table: one entry per `kind:
// "broadcast"` name declared in assets/wsapi.json.
var wsBroadcastEmitters = map[string]wsBroadcastEmitter{
	"central.state_changed": {
		Files:     []string{"internal/north/rest/ws/payloads.go"},
		Tokens:    []string{"func (h *Hub) PublishCentralStateChanged", "h.Publish(Event{", "string(hmevent.EventTypeCentralStateChanged)"},
		WireValue: string(hmevent.EventTypeCentralStateChanged),
	},
	"custom_data_point.state_changed": {
		Files:     []string{"internal/north/rest/ws/payloads.go"},
		Tokens:    []string{"func (h *Hub) PublishCustomDataPointStateChangedKind", "h.Publish(Event{", "string(hmevent.EventTypeCustomDataPointStateChanged)"},
		WireValue: string(hmevent.EventTypeCustomDataPointStateChanged),
	},
	"datapoint.value_changed": {
		Files:     []string{"internal/north/rest/ws/payloads.go"},
		Tokens:    []string{"func (h *Hub) PublishDataPointValueChangedKind", "h.Publish(Event{", "string(hmevent.EventTypeDataPointValueChanged)"},
		WireValue: string(hmevent.EventTypeDataPointValueChanged),
	},
	"datapoint.optimistic_rolled_back": {
		Files:     []string{"internal/north/rest/ws/optimistic_rollback.go"},
		Tokens:    []string{"hub.Publish(Event{", "string(hmevent.EventTypeDataPointOptimisticRolled)"},
		WireValue: string(hmevent.EventTypeDataPointOptimisticRolled),
	},
	"device.created": {
		Files:     []string{"internal/north/rest/ws/device_lifecycle.go"},
		Tokens:    []string{"hub.Publish(Event{", "string(hmevent.EventTypeDeviceCreated)"},
		WireValue: string(hmevent.EventTypeDeviceCreated),
	},
	"device.removed": {
		Files:     []string{"internal/north/rest/ws/device_lifecycle.go"},
		Tokens:    []string{"hub.Publish(Event{", "string(hmevent.EventTypeDeviceRemoved)"},
		WireValue: string(hmevent.EventTypeDeviceRemoved),
	},
	"device.trigger": {
		Files:     []string{"internal/north/rest/ws/device_trigger.go"},
		Tokens:    []string{"hub.Publish(Event{", "string(hmevent.EventTypeDeviceTrigger)"},
		WireValue: string(hmevent.EventTypeDeviceTrigger),
	},
	"hub.program_executed": {
		Files:     []string{"internal/north/rest/ws/hub_events.go"},
		Tokens:    []string{"hub.Publish(Event{", "string(hmevent.EventTypeProgramExecuted)"},
		WireValue: string(hmevent.EventTypeProgramExecuted),
	},
	"hub.sysvar_changed": {
		Files:     []string{"internal/north/rest/ws/hub_events.go"},
		Tokens:    []string{"hub.Publish(Event{", "string(hmevent.EventTypeSysvarChanged)"},
		WireValue: string(hmevent.EventTypeSysvarChanged),
	},
	"hub.install_mode_changed": {
		Files:     []string{"internal/north/rest/ws/hub_events.go"},
		Tokens:    []string{"hub.Publish(Event{", "string(hmevent.EventTypeInstallModeChanged)"},
		WireValue: string(hmevent.EventTypeInstallModeChanged),
	},
	"hub.alarm_message": {
		Files:     []string{"internal/north/rest/ws/hub_events.go"},
		Tokens:    []string{"s.hub.Publish(Event{", "string(hmevent.EventTypeAlarmMessage)"},
		WireValue: string(hmevent.EventTypeAlarmMessage),
	},
	"hub.service_message": {
		Files:     []string{"internal/north/rest/ws/hub_events.go"},
		Tokens:    []string{"s.hub.Publish(Event{", "string(hmevent.EventTypeServiceMessage)"},
		WireValue: string(hmevent.EventTypeServiceMessage),
	},
	"hub.inbox_changed": {
		Files:  []string{"internal/north/rest/ws/hub_events.go"},
		Tokens: []string{"s.hub.Publish(Event{", "eventTypeInboxChanged", `eventTypeInboxChanged = "hub.inbox_changed"`},
	},
	"hub.metrics_changed": {
		Files:  []string{"internal/north/rest/ws/hub_events.go"},
		Tokens: []string{"s.hub.Publish(Event{", "eventTypeMetricsChanged", `eventTypeMetricsChanged = "hub.metrics_changed"`},
	},
	"connectivity.changed": {
		Files:     []string{"internal/north/rest/ws/hub_events.go"},
		Tokens:    []string{"hub.Publish(Event{", "string(hmevent.EventTypeConnectivityChanged)"},
		WireValue: string(hmevent.EventTypeConnectivityChanged),
	},
	"hub.system_update_changed": {
		Files:  []string{"internal/north/rest/ws/hub_events.go"},
		Tokens: []string{"s.hub.Publish(Event{", "eventTypeSystemUpdateChanged", `eventTypeSystemUpdateChanged = "hub.system_update_changed"`},
	},
	"system.status_changed": {
		Files:     []string{"internal/north/rest/ws/system_status.go"},
		Tokens:    []string{"hub.Publish(Event{", "string(hmevent.EventTypeSystemStatusChanged)"},
		WireValue: string(hmevent.EventTypeSystemStatusChanged),
	},
	"matter.exposable_changed": {
		Files: []string{
			"internal/north/rest/handlers/matter_events.go",
			"internal/north/rest/handlers/matter_exposures.go",
		},
		Tokens:    []string{`MatterTopicExposableChanged = "matter.exposable_changed"`, "publishMatterEvent(req.Context(), publisher, MatterTopicExposableChanged"},
		WireValue: handlers.MatterTopicExposableChanged,
	},
	"matter.commissioning_window_opened": {
		Files: []string{
			"internal/north/rest/handlers/matter_events.go",
			"internal/north/rest/handlers/matter.go",
		},
		Tokens:    []string{`MatterTopicCommissioningWindowOpened = "matter.commissioning_window_opened"`, "publishMatterEvent(req.Context(), publisher, MatterTopicCommissioningWindowOpened"},
		WireValue: handlers.MatterTopicCommissioningWindowOpened,
	},
	"matter.commissioning_progress": {
		Files: []string{
			"internal/north/rest/handlers/matter_events.go",
			"internal/north/rest/handlers/matter_exposures.go",
		},
		Tokens:    []string{`MatterTopicCommissioningProgress = "matter.commissioning_progress"`, "MatterTopicCommissioningProgress, map[string]any{"},
		WireValue: handlers.MatterTopicCommissioningProgress,
	},
	"matter.fabric_added": {
		Files: []string{
			"internal/north/rest/handlers/matter_events.go",
			"cmd/openccu-loom/matter_event_publisher.go",
			"cmd/openccu-loom/daemon_matter.go",
		},
		Tokens:    []string{`MatterTopicFabricAdded = "matter.fabric_added"`, "Topic:   handlers.MatterTopicFabricAdded", "SetOnFabricAdded(wiring.pub.publishFabricAdded)"},
		WireValue: handlers.MatterTopicFabricAdded,
	},
	"matter.fabric_removed": {
		Files: []string{
			"internal/north/rest/handlers/matter_events.go",
			"internal/north/rest/handlers/matter_exposures.go",
			"cmd/openccu-loom/matter_event_publisher.go",
			"cmd/openccu-loom/daemon_matter.go",
		},
		Tokens: []string{
			`MatterTopicFabricRemoved = "matter.fabric_removed"`,
			"MatterTopicFabricRemoved, map[string]any{",
			"Topic:   handlers.MatterTopicFabricRemoved",
			"SetOnFabricRemoved(wiring.pub.publishFabricRemoved)",
		},
		WireValue: handlers.MatterTopicFabricRemoved,
	},
	"matter.endpoint_assembled": {
		Files: []string{
			"internal/north/rest/handlers/matter_events.go",
			"cmd/openccu-loom/matter_event_publisher.go",
			"cmd/openccu-loom/daemon_matter.go",
		},
		Tokens:    []string{`MatterTopicEndpointAssembled = "matter.endpoint_assembled"`, "Topic:   handlers.MatterTopicEndpointAssembled", "SetOnReassembled(func(count int)"},
		WireValue: handlers.MatterTopicEndpointAssembled,
	},
}

// normalizeWSTokens collapses runs of whitespace to single spaces so a
// token match survives gofmt's struct-literal column re-alignment
// (adding or removing a field elsewhere in the same composite literal
// shifts the padding around every `Key:` in that block, but never the
// token sequence itself).
func normalizeWSTokens(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestWSBroadcastsHaveProductionEmitter asserts every `kind: "broadcast"`
// entry in assets/wsapi.json has a wiring-table entry in
// wsBroadcastEmitters, that the entry's files exist and contain every
// required token, and — where a typed Go wire constant is available —
// that the constant's value equals the schema's broadcast name.
func TestWSBroadcastsHaveProductionEmitter(t *testing.T) {
	t.Parallel()

	schema := loadWSSchema(t)
	root := repoRoot(t)

	schemaBroadcasts := map[string]bool{}
	for _, cmd := range schema.Commands {
		if cmd.Kind == "broadcast" {
			schemaBroadcasts[cmd.Name] = true
		}
	}
	if len(schemaBroadcasts) == 0 {
		t.Fatal("no broadcast entries found in wsapi.json — schema parsing regressed")
	}

	var missingEntry []string
	for name := range schemaBroadcasts {
		if _, ok := wsBroadcastEmitters[name]; !ok {
			missingEntry = append(missingEntry, name)
		}
	}
	sort.Strings(missingEntry)
	if len(missingEntry) > 0 {
		t.Errorf("broadcasts declared in wsapi.json with no wiring-table entry in "+
			"wsBroadcastEmitters — add an entry naming the production emitter:\n  %s",
			strings.Join(missingEntry, "\n  "))
	}

	var orphanEntry []string
	for name := range wsBroadcastEmitters {
		if !schemaBroadcasts[name] {
			orphanEntry = append(orphanEntry, name)
		}
	}
	sort.Strings(orphanEntry)
	if len(orphanEntry) > 0 {
		t.Errorf("wsBroadcastEmitters entries with no matching broadcast in wsapi.json — "+
			"remove the stale entry or restore the schema row:\n  %s",
			strings.Join(orphanEntry, "\n  "))
	}

	names := make([]string, 0, len(wsBroadcastEmitters))
	for name := range wsBroadcastEmitters {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		emitter := wsBroadcastEmitters[name]
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if !schemaBroadcasts[name] {
				// Already reported as an orphan entry above; skip the
				// file/token checks so this sub-test doesn't pile on
				// redundant failures for a stale table row.
				t.Skip("no matching schema broadcast (see orphan-entry failure)")
			}
			if emitter.WireValue != "" && emitter.WireValue != name {
				t.Errorf("wire value %q does not match schema broadcast name %q", emitter.WireValue, name)
			}
			if len(emitter.Files) == 0 {
				t.Fatal("wiring-table entry has no Files")
			}

			var content strings.Builder
			for _, rel := range emitter.Files {
				b, err := os.ReadFile(filepath.Join(root, rel))
				if err != nil {
					t.Fatalf("read %s: %v", rel, err)
				}
				content.Write(b)
				content.WriteByte('\n')
			}
			haystack := normalizeWSTokens(content.String())

			var missingTokens []string
			for _, tok := range emitter.Tokens {
				if !strings.Contains(haystack, normalizeWSTokens(tok)) {
					missingTokens = append(missingTokens, tok)
				}
			}
			if len(missingTokens) > 0 {
				t.Errorf("broadcast %q: emitter token(s) not found in %v — the emitter moved, was "+
					"renamed, or was deleted; update the production code or the wiring table:\n  %s",
					name, emitter.Files, strings.Join(missingTokens, "\n  "))
			}
		})
	}

	t.Logf("TestWSBroadcastsHaveProductionEmitter: %d broadcasts, %d wiring-table entries — all bound",
		len(schemaBroadcasts), len(wsBroadcastEmitters))
}
