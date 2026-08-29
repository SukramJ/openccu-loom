// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"fmt"
	"sort"
	"testing"
)

// responsesWithoutASchema lists the success responses that deliberately
// declare none, with the reason. Everything else must declare one.
//
// An undeclared response is not a cosmetic gap: a generator emits no type for
// it, so a consumer writes the shape by hand from whatever it can find. One
// did exactly that for `GET /devices/{addr}/cdps/{name}` — it validated the
// route against `CustomDPSummary`, which shares a name prefix and nothing
// else. The mistake was invisible until the code path first ran against a
// live daemon, and then failed every custom-data-point refresh on the
// installation that hit it.
//
// One kind belongs here and nothing else does: a verb that answers with an
// empty body has no schema to declare — the handler calls WriteHeader and
// writes nothing, so `content` would be a lie.
//
// "Not transcribed yet" is not a reason to be listed. It was, briefly, for
// GET …/ui-schema; the entry is gone because the schema is written. A
// response whose shape is known but tedious belongs in the specification,
// not here.
var responsesWithoutASchema = map[string]string{
	"POST /devices/{addr}/channels/{no}/config/import 200": "empty body — handlers.ImportChannelConfig calls WriteHeader(200) and writes nothing",
	"POST /devices/{addr}/channels/{no}/reload 200":        "empty body — handlers.ReloadChannel calls WriteHeader(200) and writes nothing",
	"POST /devices/{addr}/reload 200":                      "empty body — handlers.ReloadDevice calls WriteHeader(200) and writes nothing",
}

// TestEverySuccessResponseDeclaresASchema walks every 200/201 response in the
// specification and fails when one declares no schema and is not listed above
// with a reason.
//
// The list is a ratchet, not a parking space: an entry is a claim about the
// handler that a reader can check, and removing one is the normal direction of
// travel.
func TestEverySuccessResponseDeclaresASchema(t *testing.T) {
	t.Parallel()

	spec := loadOpenAPISpec(t)

	var undeclared []string
	seenExempt := map[string]bool{}
	total := 0

	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			for code, ref := range op.Responses.Map() {
				if code != "200" && code != "201" {
					continue
				}
				// A response declared as a shared component ($ref) carries its
				// content there; the component itself is checked by whichever
				// operation renders it inline, so skip the indirection.
				if ref == nil || ref.Value == nil {
					continue
				}
				total++
				declared := false
				for _, media := range ref.Value.Content {
					if media != nil && media.Schema != nil {
						declared = true
						break
					}
				}
				key := fmt.Sprintf("%s %s %s", method, path, code)
				if declared {
					if _, listed := responsesWithoutASchema[key]; listed {
						t.Errorf("%s declares a schema now — remove it from responsesWithoutASchema", key)
					}
					continue
				}
				if _, listed := responsesWithoutASchema[key]; listed {
					seenExempt[key] = true
					continue
				}
				undeclared = append(undeclared, key)
			}
		}
	}

	if total == 0 {
		t.Fatal("walked no success responses — the guard would pass vacuously")
	}

	sort.Strings(undeclared)
	for _, key := range undeclared {
		t.Errorf("%s declares no response schema.\n"+
			"A generator emits no type for it, so a consumer writes the shape by hand and can get it "+
			"wrong without anything failing until the code path runs. Declare it, or list it in "+
			"responsesWithoutASchema with the reason it has no body.", key)
	}

	for key := range responsesWithoutASchema {
		if !seenExempt[key] {
			t.Errorf("responsesWithoutASchema lists %q, which the specification no longer has —\n"+
				"the route was renamed or removed and the entry is now describing nothing.", key)
		}
	}
}
