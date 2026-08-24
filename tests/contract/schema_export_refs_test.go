// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// localRefRE matches a same-document JSON-Schema reference.
var localRefRE = regexp.MustCompile(`"#/definitions/([A-Za-z0-9_]+)"`)

// TestExportedSchemasResolveTheirOwnRefs pins that assets/schemas/*.json
// can be resolved by a consumer that only has the file in front of it.
//
// `types.json` is declared the canonical codegen surface (ADR 0020), and
// its one composite type referenced three definitions the document does
// not contain — `Interface`, `ParamsetKey` and `Parameter`, which are
// enums and live in a different file with a different shape that no
// `$ref` from here can reach. Every one of them dangled. A strict
// JSON-Schema consumer would have failed to resolve the type on the
// first attempt.
//
// Nothing said so because there is no such consumer today, which is the
// whole difficulty with a published artefact: it is checked by being
// used, and an unused one is checked by nothing. This is the check.
func TestExportedSchemasResolveTheirOwnRefs(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(repoRoot(t), "assets", "schemas")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			t.Fatalf("read %s: %v", e.Name(), rerr)
		}
		var doc struct {
			Definitions map[string]json.RawMessage `json:"definitions"`
		}
		if uerr := json.Unmarshal(raw, &doc); uerr != nil {
			t.Fatalf("parse %s: %v", e.Name(), uerr)
		}
		scanned++

		var dangling []string
		seen := map[string]bool{}
		for _, m := range localRefRE.FindAllStringSubmatch(string(raw), -1) {
			name := m[1]
			if seen[name] {
				continue
			}
			seen[name] = true
			if _, ok := doc.Definitions[name]; !ok {
				dangling = append(dangling, name)
			}
		}
		sort.Strings(dangling)
		if len(dangling) > 0 {
			t.Errorf("%s references %d definition(s) it does not contain: %s\n\n"+
				"A same-document $ref has to resolve in that document. If the target is an enum, "+
				"it lives in enums.json under a different shape and cannot be reached from here — "+
				"declare the field as its wire type and name the vocabulary in the description "+
				"instead (see script/export_schemas.go).",
				e.Name(), len(dangling), strings.Join(dangling, ", "))
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no schema files — the guard is measuring nothing")
	}
}
