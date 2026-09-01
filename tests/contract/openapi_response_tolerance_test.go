// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// TestResponseSchemasToleratePlusFields keeps every schema a client decodes a
// *response* through open to unknown properties.
//
// A generated client turns `additionalProperties: false` into a strict model —
// pydantic's extra="forbid" in openccu-loom-client's wire layer. Adding a field
// to such a response is additive by every diff this repo runs: the surface
// inventory records an addition, oasdiff calls it non-breaking, and it ships as
// a minor. The installed client then rejects the whole payload, because the
// spec told its generator that the listed properties were the complete set.
//
// So the strictness is only sound in the direction where the *daemon* decodes:
// a request body, where an unknown key is a caller's typo the daemon should
// name rather than ignore, and where a rejection reaches somebody who can fix
// it in the same minute. StartupCaptureConfigWrite keeps it for exactly that
// reason; its response twin StartupCaptureConfig does not.
//
// Reachability is transitive on purpose. ScheduleTimeCorrection is never named
// by a response directly — it is only the item type of ScheduleWriteResult's
// array — and it carried the same strictness with the same consequence.
func TestResponseSchemasToleratePlusFields(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	specPath := filepath.Join(repoRoot, "assets", "openapi.yaml")

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	// Walk every response body schema, following $ref and every composition
	// keyword, and collect the closed ones. Named schemas are reported by
	// name; an inline schema is reported by the route that reaches it.
	closed := map[string]string{}
	seen := map[*openapi3.Schema]bool{}

	var walk func(ref *openapi3.SchemaRef, via string)
	walk = func(ref *openapi3.SchemaRef, via string) {
		if ref == nil || ref.Value == nil || seen[ref.Value] {
			return
		}
		seen[ref.Value] = true
		name := via
		if ref.Ref != "" {
			name = schemaNameFromRef(ref.Ref)
			via = name
		}
		if isClosed(ref.Value) {
			closed[name] = via
		}
		for _, sub := range ref.Value.Properties {
			walk(sub, via)
		}
		walk(ref.Value.Items, via)
		if ap := ref.Value.AdditionalProperties.Schema; ap != nil {
			walk(ap, via)
		}
		for _, sub := range ref.Value.AllOf {
			walk(sub, via)
		}
		for _, sub := range ref.Value.AnyOf {
			walk(sub, via)
		}
		for _, sub := range ref.Value.OneOf {
			walk(sub, via)
		}
	}

	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			if op.Responses == nil {
				continue
			}
			for code, respRef := range op.Responses.Map() {
				if respRef == nil || respRef.Value == nil {
					continue
				}
				for mediaType, media := range respRef.Value.Content {
					walk(media.Schema, method+" "+path+" -> "+code+" "+mediaType)
				}
			}
		}
	}

	if len(closed) == 0 {
		return
	}
	names := make([]string, 0, len(closed))
	for name := range closed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Errorf("%s is reachable from a response and sets additionalProperties: false — "+
			"a generated client decodes it strictly, so the next field added to it "+
			"breaks every installed client under a minor bump. Remove the line; keep it "+
			"only on request bodies, where the daemon is the decoder.", name)
	}
}

// isClosed reports whether a schema forbids properties it does not name.
// kin-openapi splits the JSON Schema keyword in two: Has is the boolean form
// this spec writes, Schema is the sub-schema form. Only an explicit false is
// closed — an absent keyword means open, which is what we want.
func isClosed(s *openapi3.Schema) bool {
	return s.AdditionalProperties.Has != nil && !*s.AdditionalProperties.Has
}

// schemaNameFromRef reduces "#/components/schemas/Foo" to "Foo".
func schemaNameFromRef(ref string) string {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '/' {
			return ref[i+1:]
		}
	}
	return ref
}
