// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// TestSecurityWSPayloadStructsMatchOpenAPISchemas pins the Security &
// Safety broadcast payload structs to their OpenAPI component schemas.
//
// The generated client packages read their models out of the OpenAPI
// components — wsapi.json only names them — so a Go field the spec does
// not document never reaches a typed consumer, and a documented
// property the Go struct does not emit arrives as a permanent null.
// Both failures are invisible from either side alone.
func TestSecurityWSPayloadStructsMatchOpenAPISchemas(t *testing.T) {
	t.Parallel()

	payloads := map[string]any{
		"SecurityStateChangedPayload": ws.SecurityStateChangedPayload{},
		"SecurityClassChangedPayload": ws.SecurityClassChangedPayload{},
		"SecurityZoneChangedPayload":  ws.SecurityZoneChangedPayload{},
		"SecurityFaultChangedPayload": ws.SecurityFaultChangedPayload{},
		"SecurityNotificationPayload": ws.SecurityNotificationPayload{},
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
