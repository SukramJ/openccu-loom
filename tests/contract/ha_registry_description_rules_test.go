// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// TestHARegistryDescriptionRulesNoDuplicates pins the invariant that the
// generated HA-registry rule slice has no two entries that would produce
// the same match for an identical (category, parameter, device, unit,
// postfix, varName) tuple. A duplicate rule is silent dead code — the
// first match wins and the second entry is never reachable — and is a
// sign that the code generator and the rule source have diverged.
//
// The test does NOT compare the generated registry against the hand-written
// EntityDescription maps in entity_description_rules_*.go because those
// two systems serve different lookup purposes:
//   - haRegistryDescriptionRules: full HA-attribute set (device_class,
//     state_class, entity_category, icon, translation_key, unit,
//     precision, enabled_by_default, options, multiplier) used by
//     applyEntityDescription / applyEntityDescriptionStrict.
//   - entity_description_rules_*.go: lightweight DeviceClass + StateClass +
//     Unit overrides used by LookupRulesForComponent for the legacy
//     discovery path.
//
// The invariant guarded here is uniqueness of the generated slice.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// TestHARegistryDescriptionRulesNoDuplicates fails when the generated
// haRegistryDescriptionRules slice contains two or more entries that
// have identical (category, parameters, devices, unit, postfix,
// varNameContains) matching criteria. Such duplicates make the second
// entry unreachable: HARegistryDescriptionLookup returns on first match.
func TestHARegistryDescriptionRulesNoDuplicates(t *testing.T) {
	t.Parallel()

	rules := mqtt.HARegistryDescriptionRules()
	type matchKey struct {
		category        string
		parameters      string // sorted, joined
		devices         string // sorted, joined
		unit            string
		postfix         string
		varNameContains string
	}
	seen := make(map[matchKey]int, len(rules)) // value = first-seen index
	for i, r := range rules {
		key := matchKey{
			category:        r.Category,
			parameters:      strings.Join(r.Parameters, ","),
			devices:         strings.Join(r.Devices, ","),
			unit:            r.Unit,
			postfix:         r.Postfix,
			varNameContains: r.VarNameContains,
		}
		if prev, dup := seen[key]; dup {
			t.Errorf("haRegistryDescriptionRules: duplicate match criteria at index %d and %d: %s",
				prev, i, fmt.Sprintf("category=%q params=%v devices=%v", r.Category, r.Parameters, r.Devices))
		} else {
			seen[key] = i
		}
	}
}

// TestHARegistryDescriptionRulesHaveKeys fails when any entry in the
// generated slice has an empty Description.Key. An empty key means the
// lookup result is indistinguishable from the zero value and callers
// cannot detect a match.
func TestHARegistryDescriptionRulesHaveKeys(t *testing.T) {
	t.Parallel()

	rules := mqtt.HARegistryDescriptionRules()
	for i, r := range rules {
		if r.Description.Key == "" {
			t.Errorf("haRegistryDescriptionRules[%d]: Description.Key is empty (category=%q params=%v devices=%v)",
				i, r.Category, r.Parameters, r.Devices)
		}
	}
}

// TestHARegistryDescriptionRulesCountUnchanged pins the entry count so
// an accidental truncation (e.g. generator emitting an empty slice on
// config error) fails fast. Update the constant when entries are
// intentionally added or removed.
func TestHARegistryDescriptionRulesCountUnchanged(t *testing.T) {
	t.Parallel()

	const expectedCount = 147
	rules := mqtt.HARegistryDescriptionRules()
	if len(rules) != expectedCount {
		t.Errorf("haRegistryDescriptionRules: got %d entries, want %d — update expectedCount if this is intentional",
			len(rules), expectedCount)
	}
}
