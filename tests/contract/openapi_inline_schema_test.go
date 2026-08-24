// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestNoRequestOrResponseBodyIsWrittenInline pins that a body with
// properties lives in `components/schemas` and is reached by `$ref`.
//
// The rule is not style. `openccu-loom-types` — and every other client
// generated from this document — is produced from `components/schemas`
// alone, so a schema written inline in a path item reaches no consumer,
// however faithfully the daemon sends it. It is a shape that fails
// silently in the one direction nobody looks: the endpoint works, the
// JSON is right, and the typed client simply has no model for it.
//
// The gap was found by counting: 118 responses used `$ref` and 28 did
// not, and 30 request bodies did not either. All 58 were promoted, which
// is why the exemptions below name shapes rather than endpoints — a
// free-form body has nothing to generate, and a scalar or file upload
// has no model to miss.
func TestNoRequestOrResponseBodyIsWrittenInline(t *testing.T) {
	t.Parallel()

	specPath := filepath.Join(repoRoot(t), "assets", "openapi.yaml")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}
	var doc map[string]any
	if uerr := yaml.Unmarshal(raw, &doc); uerr != nil {
		t.Fatalf("parse %s: %v", specPath, uerr)
	}
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		t.Fatal("parsed no paths — the guard is measuring nothing")
	}

	var offenders []string
	checked := 0
	report := func(where string, schema map[string]any) {
		if schema == nil {
			return
		}
		checked++
		if _, isRef := schema["$ref"]; isRef {
			return
		}
		if generatesAModel(schema) {
			offenders = append(offenders, where)
			return
		}
		// An array of inline objects hides the model one level down.
		if items, ok := schema["items"].(map[string]any); ok {
			if _, isRef := items["$ref"]; isRef {
				return
			}
			if generatesAModel(items) {
				offenders = append(offenders, where+"  (array items)")
			}
		}
	}

	// A path item also carries non-operation keys (`parameters`), so the
	// walk asks what a node is rather than assuming.
	asMap := func(v any) map[string]any {
		m, _ := v.(map[string]any)
		return m
	}
	for path, item := range paths {
		for method, opAny := range asMap(item) {
			op := asMap(opAny)
			if op == nil {
				continue
			}
			name := strings.ToUpper(method) + " " + path
			for ct, body := range asMap(asMap(op["requestBody"])["content"]) {
				report(name+" request ("+ct+")", asMap(asMap(body)["schema"]))
			}
			for code, respAny := range asMap(op["responses"]) {
				for ct, body := range asMap(asMap(respAny)["content"]) {
					report(name+" response "+code+" ("+ct+")", asMap(asMap(body)["schema"]))
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("inspected no bodies — the guard is measuring nothing")
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("%d request/response body(ies) declare properties inline instead of "+
			"referencing components/schemas:\n  %s\n\n"+
			"Move the schema under components/schemas and replace it with a $ref. Generated "+
			"clients are produced from components/schemas alone, so an inline body reaches none "+
			"of them — the endpoint works, the JSON is right, and the typed client has no model "+
			"for it.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// generatesAModel reports whether a schema would produce a named model in
// a generated client — that is, whether it declares properties of its own.
//
// A free-form object (`additionalProperties: true` with no properties) is
// deliberately not one: there is nothing to generate, and forcing it into
// a component would mint an empty type per endpoint. The same goes for a
// scalar, a binary upload, or a bare array of scalars.
func generatesAModel(schema map[string]any) bool {
	props, ok := schema["properties"].(map[string]any)
	return ok && len(props) > 0
}
