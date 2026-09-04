// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// enumCoverageCase is one place in the spec that spells out the members of a
// Go enum, together with the source the members come from.
type enumCoverageCase struct {
	// where names the spec location for the failure message.
	where string
	// enumValues is the declared enum at that location.
	enumValues []any
	// goFile / goType locate the Go const block the values must cover.
	// goPrefix is the alternative for a vocabulary declared as untyped
	// string constants: exactly one of goType and goPrefix is set.
	goFile   string
	goType   string
	goPrefix string
	// omit lists wire values the location legitimately excludes, with the
	// reason. A request schema may narrow a response enum — an arm request
	// cannot ask for "disarmed" — but the exclusion has to be stated.
	omit map[string]string
}

// TestOpenAPIEnumsCoverEveryEmittedValue is the drift detector for hand-written
// enum lists in assets/openapi.yaml.
//
// The spec is authored by hand while the values come from Go const blocks, and
// nothing compared the two: the alarm journal grew a "maintenance" class and
// AlarmTriggeredPayload.mode could carry "disarmed" (an always-on hazard trigger
// on a disarmed zone) while both spec enums still listed the older sets. The
// cost lands outside this repo — request validation rejects the undeclared
// query value with 400 before the handler sees it, and generated client types
// spell a union the daemon then violates on the response leg.
//
// The table is deliberately explicit: a spec enum may narrow a Go enum on
// purpose, so each such exclusion is declared with its reason instead of the
// guard silently allowing every subset.
func TestOpenAPIEnumsCoverEveryEmittedValue(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join(root, "assets", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}

	journalClass := filepath.Join(root, "pkg", "hmenum", "alarm.go")
	cases := []enumCoverageCase{
		{
			where:      "components.schemas.AlarmJournalEntry.class",
			enumValues: propertyEnum(t, doc, "AlarmJournalEntry", "class"),
			goFile:     journalClass, goType: "AlarmJournalClass",
		},
		{
			where:      "components.schemas.AlarmJournalAppendedPayload.class",
			enumValues: propertyEnum(t, doc, "AlarmJournalAppendedPayload", "class"),
			goFile:     journalClass, goType: "AlarmJournalClass",
		},
		{
			where:      "GET /alarm/journal?class",
			enumValues: queryParamEnum(t, doc, "/alarm/journal", "class"),
			goFile:     journalClass, goType: "AlarmJournalClass",
		},
		{
			where:      "components.schemas.AlarmTriggeredPayload.mode",
			enumValues: propertyEnum(t, doc, "AlarmTriggeredPayload", "mode"),
			goFile:     journalClass, goType: "AlarmMode",
		},
		{
			where:      "components.schemas.AlarmStateChangedPayload.mode",
			enumValues: propertyEnum(t, doc, "AlarmStateChangedPayload", "mode"),
			goFile:     journalClass, goType: "AlarmMode",
		},
		{
			where:      "components.schemas.AlarmIncident.mode",
			enumValues: propertyEnum(t, doc, "AlarmIncident", "mode"),
			goFile:     journalClass, goType: "AlarmMode",
		},
		{
			where:      "components.schemas.AlarmArmRequest.mode",
			enumValues: propertyEnum(t, doc, "AlarmArmRequest", "mode"),
			goFile:     journalClass, goType: "AlarmMode",
			omit: map[string]string{
				"disarmed": "arming to 'disarmed' is a disarm, which has its own request",
			},
		},
		{
			// The alarm-panel states are untyped string constants, so this
			// case selects them by Go-name prefix. The spec listed the same
			// nine tokens in the same order and nothing checked it: a tenth
			// HA state added to the daemon would have shipped a response the
			// published enum forbids.
			where:      "components.schemas.AlarmPanelEntity.state",
			enumValues: propertyEnum(t, doc, "AlarmPanelEntity", "state"),
			goFile:     filepath.Join(root, "internal", "model", "alarmpanel", "statemap.go"),
			goPrefix:   "HAAlarmState",
		},
		{
			where:      "components.schemas.Identity.scheme",
			enumValues: propertyEnum(t, doc, "Identity", "scheme"),
			goFile:     filepath.Join(root, "internal", "auth", "auth.go"), goType: "Scheme",
		},
	}

	for _, tc := range cases {
		t.Run(tc.where, func(t *testing.T) {
			t.Parallel()

			declared := make([]any, len(tc.enumValues))
			copy(declared, tc.enumValues)

			emitted := extractEnumConstantsFromSource(t, tc.goFile, tc.goType)
			if tc.goPrefix != "" {
				emitted = extractUntypedStringConstsWithPrefix(t, tc.goFile, tc.goPrefix)
			}
			if len(emitted) == 0 {
				t.Fatalf("%s: no constants extracted from %s — the extraction stopped matching "+
					"and this case is measuring nothing", tc.where, tc.goFile)
			}

			var missing []string
			for _, wire := range emitted {
				if _, omitted := tc.omit[wire]; omitted {
					if slices.Contains(declared, any(wire)) {
						t.Errorf("%s declares %q although it is listed as omitted", tc.where, wire)
					}
					continue
				}
				if !slices.Contains(declared, any(wire)) {
					missing = append(missing, wire)
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				t.Errorf("%s omits %d value(s) the daemon emits: %v", tc.where, len(missing), missing)
			}
		})
	}
}

// propertyEnum returns the enum of one property of a component schema.
func propertyEnum(t *testing.T, doc *openapi3.T, schemaName, property string) []any {
	t.Helper()
	ref, ok := doc.Components.Schemas[schemaName]
	if !ok || ref.Value == nil {
		t.Fatalf("components.schemas.%s missing from openapi.yaml", schemaName)
	}
	prop, ok := ref.Value.Properties[property]
	if !ok || prop.Value == nil {
		t.Fatalf("components.schemas.%s has no property %q", schemaName, property)
	}
	if len(prop.Value.Enum) == 0 {
		t.Fatalf("components.schemas.%s.%s declares no enum", schemaName, property)
	}
	return prop.Value.Enum
}

// queryParamEnum returns the enum of a GET query parameter.
func queryParamEnum(t *testing.T, doc *openapi3.T, path, name string) []any {
	t.Helper()
	item := doc.Paths.Find(path)
	if item == nil || item.Get == nil {
		t.Fatalf("GET %s missing from openapi.yaml", path)
	}
	for _, p := range item.Get.Parameters {
		if p.Value == nil || p.Value.Name != name || p.Value.Schema == nil || p.Value.Schema.Value == nil {
			continue
		}
		if len(p.Value.Schema.Value.Enum) == 0 {
			t.Fatalf("GET %s parameter %q declares no enum", path, name)
		}
		return p.Value.Schema.Value.Enum
	}
	t.Fatalf("GET %s has no parameter %q", path, name)
	return nil
}
