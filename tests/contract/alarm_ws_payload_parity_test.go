// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// TestAlarmWSPayloadStructsMatchOpenAPISchemas pins the alarm
// broadcast payload structs to their OpenAPI component schemas: every
// schema property must exist as a JSON field on the Go struct, every
// Go field must be documented, and every required property must not
// be omitempty. Without this guard the generated client and the wire
// drift apart silently — the exact defect class where a broadcast
// carried `from`/`to` while the spec promised `old_state`/`new_state`.
func TestAlarmWSPayloadStructsMatchOpenAPISchemas(t *testing.T) {
	t.Parallel()

	payloads := map[string]any{
		"AlarmStateChangedPayload":     ws.AlarmStateChangedPayload{},
		"AlarmCountdownPayload":        ws.AlarmCountdownPayload{},
		"AlarmReadinessChangedPayload": ws.AlarmReadinessChangedPayload{},
		"AlarmTriggeredPayload":        ws.AlarmTriggeredPayload{},
		"AlarmJournalAppendedPayload":  ws.AlarmJournalAppendedPayload{},
		"AlarmWalkTestProgressPayload": ws.AlarmWalkTestProgressPayload{},
		"AlarmHealthChangedPayload":    ws.AlarmHealthChangedPayload{},
	}

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join(repoRoot(t), "assets", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}

	for name, v := range payloads {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ref, ok := doc.Components.Schemas[name]
			if !ok || ref.Value == nil {
				t.Fatalf("components.schemas.%s missing from openapi.yaml", name)
			}
			schema := ref.Value

			fields := jsonFieldTags(reflect.TypeOf(v))
			for prop := range schema.Properties {
				if _, ok := fields[prop]; !ok {
					t.Errorf("schema property %q has no field on ws.%s", prop, name)
				}
			}
			for tag := range fields {
				if _, ok := schema.Properties[tag]; !ok {
					t.Errorf("ws.%s field %q is not documented in the schema", name, tag)
				}
			}
			for _, req := range schema.Required {
				omitempty, ok := fields[req]
				if !ok {
					continue // already reported above
				}
				if omitempty {
					t.Errorf("ws.%s field %q is required by the schema but tagged omitempty — an empty value drops a required property", name, req)
				}
			}
		})
	}
}

// jsonFieldTags maps json tag names to their omitempty flag.
func jsonFieldTags(typ reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := range typ.NumField() {
		f := typ.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		omit := false
		for _, p := range parts[1:] {
			if p == "omitempty" {
				omit = true
			}
		}
		out[parts[0]] = omit
	}
	return out
}
