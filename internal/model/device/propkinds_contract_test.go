// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestPropKindsByTypeMatchesLiveReflection verifies that the generator
// output (`propkinds_gen.go`) stays in sync: it asserts that, for
// every entry in `PropKindsByType`, the corresponding live reflection
// over the named struct yields the exact same (kind, field, alt)
// triples in the same sorted order.
//
// Drift sources this catches:
// - somebody edits a `payload:"..."` tag in device.go but forgets
// to re-run `go run ./script/gen_propkinds.go ./internal/model/device`
// - somebody adds a new field with a payload tag but doesn't
// regenerate
// - somebody adds an `alt=...` clause but the static table doesn't
// reflect it
//
// The fix in every drift case is the same one-liner:
//
//	go run ./script/gen_propkinds.go ./internal/model/device
func TestPropKindsByTypeMatchesLiveReflection(t *testing.T) {
	t.Parallel()

	// Map of type-name → live reflect.Type for the structs we expect
	// to be in the generated table.
	live := map[string]reflect.Type{
		"Device": reflect.TypeFor[Device](),
	}

	for name, want := range PropKindsByType {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rt, ok := live[name]
			if !ok {
				t.Fatalf("PropKindsByType has %q but live registry has no entry — extend the live map in this test", name)
			}
			got := reflectEntries(rt)
			if !equalEntries(got, want) {
				t.Fatalf("propkinds drift for %q\n  generated: %v\n  live:      %v\n  fix: go run ./script/gen_propkinds.go ./internal/model/device",
					name, want, got)
			}
		})
	}
}

// reflectEntries walks a struct type and produces (kind, field, alt)
// triples in the same shape and sort order as the generator.
func reflectEntries(t reflect.Type) []PropKindEntry {
	var out []PropKindEntry
	for f := range t.Fields() {
		tag := f.Tag.Get("payload")
		if tag == "" || tag == "-" {
			continue
		}
		kind, rest, _ := strings.Cut(tag, ",")
		var alt string
		for opt := range strings.SplitSeq(rest, ",") {
			opt = strings.TrimSpace(opt)
			if after, ok := strings.CutPrefix(opt, "alt="); ok {
				alt = after
			}
		}
		out = append(out, PropKindEntry{Kind: strings.TrimSpace(kind), Field: f.Name, Alt: alt})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Field < out[j].Field
	})
	return out
}

func equalEntries(a, b []PropKindEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
