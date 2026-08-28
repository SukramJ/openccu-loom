// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

// TestHARegistryDescriptionRulesNoDuplicates pins the invariant that the
// HA-registry rule slice has no two entries that would produce
// the same match for an identical (category, parameter, device, unit,
// postfix, varName) tuple. A duplicate rule is silent dead code — the
// first match wins and the second entry is never reachable.
//
// The test does NOT compare the registry against the hand-written
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
// The invariant guarded here is uniqueness. Content coherence is a
// separate guard: TestHARegistryDescriptionRulesMatchTheGolden below.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// TestHARegistryDescriptionRulesNoDuplicates fails when the rule slice
// contains two or more entries that have identical (category, parameters,
// devices, unit, postfix, varNameContains) matching criteria. Such duplicates make the second
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

// TestHARegistryDescriptionRulesHaveKeys fails when any entry has an
// empty Description.Key. An empty key means the
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

// haDescriptionRulesGolden is the named source the coherence test compares
// against. Its predecessor pinned `len(rules) == 147` and nothing else,
// which catches a truncated slice and misses every edit that keeps the
// count: a rule whose device_class changes, a device list that loses a
// model, a priority that flips two rules past each other. Those are the
// changes that alter a discovery payload.
//
// The count guard made sense while a generator owned the file and the only
// plausible failure was the generator emitting nothing. The generator is
// gone (ADR 0063, ADR 0067) and the table is maintained by hand, so the
// coherence test needs a source to be coherent *with*. This file is it.
const haDescriptionRulesGolden = "testdata/ha_registry_description_rules.json"

var updateHADescriptionRules = flag.Bool("update-ha-description-rules", false,
	"rewrite "+haDescriptionRulesGolden+" from the current rule table")

// TestHARegistryDescriptionRulesMatchTheGolden compares every field of
// every rule against the committed golden file.
//
// Refresh it deliberately, in the same commit as the rule change:
//
//	GOMAXPROCS=2 go test -p 2 -run TestHARegistryDescriptionRulesMatchTheGolden \
//	  ./tests/contract/ -update-ha-description-rules
//
// Reviewing that diff is the point: a rule edit surfaces as a field-level
// change a reviewer can check against the device.
func TestHARegistryDescriptionRulesMatchTheGolden(t *testing.T) {
	t.Parallel()

	current, err := json.MarshalIndent(mqtt.HARegistryDescriptionRules(), "", "  ")
	if err != nil {
		t.Fatalf("marshal rules: %v", err)
	}
	current = append(current, '\n')

	if *updateHADescriptionRules {
		if err := os.WriteFile(haDescriptionRulesGolden, current, 0o600); err != nil {
			t.Fatalf("write %s: %v", haDescriptionRulesGolden, err)
		}
		t.Logf("rewrote %s with %d rules", haDescriptionRulesGolden, len(mqtt.HARegistryDescriptionRules()))
		return
	}

	want, err := os.ReadFile(haDescriptionRulesGolden) //nolint:gosec // fixed testdata path
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with -update-ha-description-rules)", haDescriptionRulesGolden, err)
	}

	if bytes.Equal(current, want) {
		return
	}

	// Report the difference per rule key rather than as a byte diff, so the
	// failure names what moved instead of where the bytes stopped matching.
	var golden []mqtt.HARegistryDescriptionRule
	if err := json.Unmarshal(want, &golden); err != nil {
		t.Fatalf("parse %s: %v", haDescriptionRulesGolden, err)
	}
	rules := mqtt.HARegistryDescriptionRules()

	if len(golden) != len(rules) {
		t.Errorf("rule count: golden has %d, table has %d", len(golden), len(rules))
	}
	for i := 0; i < len(golden) && i < len(rules); i++ {
		if reflect.DeepEqual(golden[i], rules[i]) {
			continue
		}
		t.Errorf("rule %d (%s/%s) differs:\n  golden: %+v\n  table:  %+v",
			i, rules[i].Category, rules[i].Description.Key, golden[i], rules[i])
	}
	t.Errorf("%s is stale. If the rule change is intended, refresh it in this same commit:\n"+
		"  GOMAXPROCS=2 go test -p 2 -run TestHARegistryDescriptionRulesMatchTheGolden ./tests/contract/ -update-ha-description-rules",
		haDescriptionRulesGolden)
}
