// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// ws_payload_field_parity_test.go — field-level cross-asset contract guard.
//
// TestWSBroadcastPayloadsHaveOpenAPISchema proves a broadcast's payload
// schema *exists*. It says nothing about the fields inside it, and that is
// where the drift lives: `datapoint.value_changed` emitted `display_value`
// for a whole API line while the schema documented only `value`, so the
// generated client packages had no such field and every consumer that
// seeded a reading from REST and updated it from the push showed the two
// planes disagreeing — the exact failure the field was added to prevent.
//
// The asymmetry is what makes both directions worth checking:
//
//   - a Go field the spec does not document reaches no generated client
//     at all, however faithfully the daemon sends it;
//   - a documented property the Go struct does not emit arrives as a
//     permanent null, which reads to a client as "the daemon has nothing
//     to say" rather than "this was never wired".
//
// Neither is visible from one side alone, and neither breaks a test that
// only round-trips the daemon against itself.
//
// The alarm and security payload families keep their own narrower guards
// (alarm_ws_payload_parity_test.go, security_ws_payload_parity_test.go).
// This one subsumes them: they predate it, carry their own rationale, and
// duplicated coverage costs milliseconds.

// wsPayloadStructs maps an OpenAPI component-schema name to the Go struct
// the daemon marshals into that broadcast's payload. Keyed by the schema
// name rather than the Go type name because the two are allowed to differ
// (AddonUpdateStatus is carried by ws.AddonUpdateStatusPayload) and the
// schema name is what wsapi.json and the generated clients agree on.
var wsPayloadStructs = map[string]any{
	"AddonUpdateStatus": ws.AddonUpdateStatusPayload{},
	// Two handler DTOs that double as broadcast payloads. They were
	// recorded as holes on the grounds that they live outside the ws
	// package — true, and irrelevant: they are exported, so reflection
	// reaches them exactly as it reaches a ws struct.
	"MatterCommissioningWindowResponse":  handlers.MatterCommissioningWindowResponse{},
	"MatterExposureUpdate":               handlers.MatterExposureUpdate{},
	"AlarmCountdownPayload":              ws.AlarmCountdownPayload{},
	"AlarmHealthChangedPayload":          ws.AlarmHealthChangedPayload{},
	"AlarmJournalAppendedPayload":        ws.AlarmJournalAppendedPayload{},
	"AlarmNotificationPayload":           ws.AlarmNotificationPayload{},
	"AlarmPanelChangedPayload":           ws.AlarmPanelChangedPayload{},
	"AlarmReadinessChangedPayload":       ws.AlarmReadinessChangedPayload{},
	"AlarmReminderPayload":               ws.AlarmReminderPayload{},
	"AlarmStateChangedPayload":           ws.AlarmStateChangedPayload{},
	"AlarmTriggeredPayload":              ws.AlarmTriggeredPayload{},
	"AlarmWalkTestProgressPayload":       ws.AlarmWalkTestProgressPayload{},
	"CentralReadinessChangedPayload":     ws.CentralReadinessChangedPayload{},
	"CentralStateChangedPayload":         ws.CentralStateChangedPayload{},
	"CustomDataPointStateChangedPayload": ws.CustomDataPointStateChangedPayload{},
	"DaemonStatusPayload":                ws.DaemonStatusPayload{},
	"DataPointValueChangedPayload":       ws.DataPointValueChangedPayload{},
	"DeviceAvailabilityChangedPayload":   ws.DeviceAvailabilityChangedPayload{},
	"DeviceCreatedPayload":               ws.DeviceCreatedPayload{},
	"DeviceRemovedPayload":               ws.DeviceRemovedPayload{},
	"DeviceTriggerPayload":               ws.DeviceTriggerPayload{},
	"HubConnectivityChangedPayload":      ws.HubConnectivityChangedPayload{},
	"HubCountChangedPayload":             ws.HubCountChangedPayload{},
	"HubMetricChangedPayload":            ws.HubMetricChangedPayload{},
	"HubSystemUpdateChangedPayload":      ws.HubSystemUpdateChangedPayload{},
	"InstallModeChangedPayload":          ws.InstallModeChangedPayload{},
	"OptimisticRollbackPayload":          ws.OptimisticRollbackPayload{},
	"ProgramChangedPayload":              ws.ProgramChangedPayload{},
	"ProgramExecutedPayload":             ws.ProgramExecutedPayload{},
	"SecurityClassChangedPayload":        ws.SecurityClassChangedPayload{},
	"SecurityFaultChangedPayload":        ws.SecurityFaultChangedPayload{},
	"SecurityNotificationPayload":        ws.SecurityNotificationPayload{},
	"SecurityStateChangedPayload":        ws.SecurityStateChangedPayload{},
	"SecurityZoneChangedPayload":         ws.SecurityZoneChangedPayload{},
	"SystemStatusChangedPayload":         ws.SystemStatusChangedPayload{},
	"SysvarChangedPayload":               ws.SysvarChangedPayload{},
}

// wsPayloadsDeclaredElsewhere are broadcast payloads whose Go type does not
// live in internal/north/rest/ws, so this guard cannot reflect over them.
// Each entry is a hole in the coverage, not an exemption — the reason says
// what would have to move for it to close.
//
// Never add a name here to silence a failure on a payload that *is* a ws
// struct: registering it in [wsPayloadStructs] is the whole point.
var wsPayloadsDeclaredElsewhere = map[string]string{
	"DeviceMetadataChangedPayload": "unexported adapter struct (internal/central/adapter/eventbridge.go deviceMetadataChangedWSPayload) — export it into the ws package to cover it",
	"ScheduleChangedPayload":       "unexported adapter struct — same as DeviceMetadataChangedPayload",
	// These three have no Go type at all: the publish sites assemble them
	// as `map[string]any` literals (handlers/matter_exposures.go:397,428,
	// handlers/matter_maintenance.go:111, cmd/openccu-loom/
	// matter_event_publisher.go). There is nothing to reflect over, so the
	// hole cannot be closed by registering a struct — it closes by giving
	// the payload one, or by a guard that reads the literal's keys.
	//
	// The earlier reason said they were "built by the Matter north-bound
	// adapter, not the ws package", which reads as a location problem and
	// is not one.
	"MatterCommissioningProgressPayload": "assembled as a map[string]any literal at the publish site; no struct exists to field-check",
	// The schema component is named MatterFabric; no Go type of that name
	// exists. Whatever the fabric-added broadcast publishes is assembled
	// elsewhere, so the component describes a shape nothing in this tree
	// declares — worth naming as that rather than as a package boundary.
	"MatterFabric":                   "no Go type of this name exists; the component describes a shape assembled at the publish site",
	"MatterEndpointAssembledPayload": "assembled as a map[string]any literal at the publish site; no struct exists to field-check",
	"MatterFabricRemovedPayload":     "assembled as a map[string]any literal at the publish site; no struct exists to field-check",
}

// TestWSPayloadStructsMatchOpenAPISchemaFields pins every registered
// broadcast payload struct to the fields of its OpenAPI component schema,
// in both directions.
func TestWSPayloadStructsMatchOpenAPISchemaFields(t *testing.T) {
	t.Parallel()

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join(repoRoot(t), "assets", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if doc.Components == nil || doc.Components.Schemas == nil {
		t.Fatal("openapi.yaml has no components.schemas")
	}

	for name, v := range wsPayloadStructs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ref, ok := doc.Components.Schemas[name]
			if !ok || ref.Value == nil {
				t.Fatalf("components.schemas.%s missing from openapi.yaml", name)
			}
			schema := ref.Value
			typ := reflect.TypeOf(v)

			fields := jsonFieldTags(typ)
			for prop := range schema.Properties {
				if _, ok := fields[prop]; !ok {
					t.Errorf("schema property %q has no field on ws.%s — a documented property nothing emits is a permanent null", prop, typ.Name())
				}
			}
			for tag := range fields {
				if _, ok := schema.Properties[tag]; !ok {
					t.Errorf("ws.%s field %q is not documented in the schema — no generated client can consume it", typ.Name(), tag)
				}
			}
			for _, req := range schema.Required {
				omitempty, ok := fields[req]
				if !ok {
					continue // already reported above
				}
				if omitempty {
					t.Errorf("ws.%s field %q is required by the schema but tagged omitempty — an empty value drops a required property", typ.Name(), req)
				}
			}
		})
	}
}

// TestEveryBroadcastPayloadIsFieldChecked asserts that every payload named
// by a broadcast in assets/wsapi.json is either registered above or
// recorded as declared elsewhere. Without it a new broadcast joins the
// contract with no field-level check at all, which is how the coverage
// hole this guard exists to close was opened in the first place.
func TestEveryBroadcastPayloadIsFieldChecked(t *testing.T) {
	t.Parallel()

	schema := loadWSSchema(t)
	var unchecked []string
	for _, cmd := range schema.Commands {
		if cmd.Kind != "broadcast" || cmd.Payload == "" {
			continue
		}
		if _, ok := wsPayloadStructs[cmd.Payload]; ok {
			continue
		}
		if _, ok := wsPayloadsDeclaredElsewhere[cmd.Payload]; ok {
			continue
		}
		unchecked = append(unchecked, cmd.Name+" → "+cmd.Payload)
	}
	if len(unchecked) > 0 {
		sort.Strings(unchecked)
		t.Errorf("%d broadcast payload(s) have no field-level parity check:\n  %s\n\n"+
			"Register the payload struct in wsPayloadStructs. Only if its Go type genuinely\n"+
			"cannot be reached from this package, record it in wsPayloadsDeclaredElsewhere\n"+
			"with the reason — that entry is a documented hole, not an exemption.",
			len(unchecked), strings.Join(unchecked, "\n  "))
	}
}

// TestWSPayloadsDeclaredElsewhereStayUnreachable keeps the coverage-hole
// list honest. A payload listed there that *is* reachable in the ws package
// would silently skip the field check — the ratchet has to shrink when the
// type moves, not sit there justifying a check that is now free.
func TestWSPayloadsDeclaredElsewhereStayUnreachable(t *testing.T) {
	t.Parallel()

	for name := range wsPayloadsDeclaredElsewhere {
		if _, ok := wsPayloadStructs[name]; ok {
			t.Errorf("%s is registered in wsPayloadStructs and must be dropped from wsPayloadsDeclaredElsewhere", name)
		}
	}
}
