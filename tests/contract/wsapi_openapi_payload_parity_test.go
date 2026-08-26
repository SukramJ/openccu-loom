// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

// wsapi_openapi_payload_parity_test.go — cross-asset contract guard.
//
// Every WS broadcast in assets/wsapi.json names its push-payload via the
// `payload` field (e.g. "HubSystemUpdateChangedPayload"). Generated
// client type packages (openccu-loom-types' `gen_ws.py`) resolve that
// name from the OpenAPI components — the payload classes are produced by
// datamodel-codegen from assets/openapi.yaml. If a broadcast names a
// payload that has no `components.schemas` entry, type regeneration
// breaks outright (gen_ws cannot resolve the class) and the daemon ships
// a field clients can't consume.
//
// This is exactly the drift that slipped through in 0.9.0: the D1
// `hub.system_update_changed` broadcast referenced
// `HubSystemUpdateChangedPayload` in wsapi.json, but the schema was never
// added to openapi.yaml — so the types regeneration failed in CI and the
// gap was only caught downstream. This test fails such drift in the
// daemon's own CI instead.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// TestWSBroadcastPayloadsHaveOpenAPISchema asserts that every
// `kind: "broadcast"` entry in assets/wsapi.json names a non-empty
// `payload` and that the named schema exists under `components.schemas`
// in assets/openapi.yaml.
func TestWSBroadcastPayloadsHaveOpenAPISchema(t *testing.T) {
	t.Parallel()

	schema := loadWSSchema(t)
	root := repoRoot(t)
	specPath := filepath.Join(root, "assets", "openapi.yaml")

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if doc.Components == nil || doc.Components.Schemas == nil {
		t.Fatal("openapi.yaml has no components.schemas")
	}

	var missingPayload []string // broadcast without a payload field
	var missingSchema []string  // payload named but no openapi schema
	checked := 0

	for _, cmd := range schema.Commands {
		if cmd.Kind != "broadcast" {
			continue
		}
		if cmd.Payload == "" {
			missingPayload = append(missingPayload, cmd.Name)
			continue
		}
		checked++
		if _, ok := doc.Components.Schemas[cmd.Payload]; !ok {
			missingSchema = append(missingSchema, fmt.Sprintf("%s → %s", cmd.Name, cmd.Payload))
		}
	}
	sort.Strings(missingPayload)
	sort.Strings(missingSchema)

	if len(missingPayload) > 0 {
		t.Errorf("WS broadcasts in assets/wsapi.json with no `payload` field — "+
			"every broadcast must name its push-payload schema:\n  %s",
			strings.Join(missingPayload, "\n  "))
	}
	if len(missingSchema) > 0 {
		t.Errorf("WS broadcast payloads referenced in assets/wsapi.json but absent from "+
			"assets/openapi.yaml `components.schemas` — add the schema (generated client type "+
			"packages resolve broadcast payloads from the OpenAPI components, so a missing one "+
			"breaks type regeneration):\n  %s",
			strings.Join(missingSchema, "\n  "))
	}

	// Validate the test setup itself: a refactor that drops the broadcast
	// catalogue (or the `payload` field) must not let the guard pass empty.
	if checked == 0 && len(missingPayload) == 0 {
		t.Fatal("no broadcast payloads found in wsapi.json — schema shape may have changed")
	}

	if !t.Failed() {
		t.Logf("TestWSBroadcastPayloadsHaveOpenAPISchema: %d broadcast payloads, all have an OpenAPI schema", checked)
	}
}
