// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

var updateOpenAPIRequiredProperties = flag.Bool("update-openapi-required-properties", false,
	"rewrite tests/contract/testdata/openapi_required_properties.json from the current spec "+
		"(refuses while a newly required property has no entry in newlyRequiredProperties)")

// newlyRequiredProperties acknowledges a property that became required on a
// schema older daemons already sent without it.
//
// The entry format is "<api version that made it required> <Schema>.<property>:
// what a client must do for a daemon older than that version". Making a
// property required is free on the wire and costly for a generated client: the
// client validates every answer against the new schema, so it rejects the
// answer of every older daemon that omits the property — it can no longer talk
// to them at all. An entry is the written-down promise of how a client copes,
// and the reason it must exist before the baseline accepts the change.
//
// Entries are never deleted. The Info.deployment entry was recorded after the
// fact: the property was already required when this guard's baseline was first
// written, so the baseline contains it and nothing here looks it up any more.
// It stays as the precedent that made this guard necessary — the Python client
// generated against 13.5.0 refused /info from every older daemon.
var newlyRequiredProperties = []string{
	"13.5.0 Info.deployment: a generated client must accept an /info answer without `deployment` " +
		"from a daemon older than 13.5.0 and treat the deployment as unknown; requiring it rejects " +
		"that daemon's /info outright, so the client cannot connect to it",
}

// TestOpenAPISchemasGainNoRequiredPropertyUnacknowledged refuses a property
// that becomes required on an existing component schema unless
// [newlyRequiredProperties] says how a client copes with older daemons.
//
// A generated client turns `required` into validation. A daemon older than the
// change does not send the property, so the moment a schema the client already
// knew gains a required property, the client fails on that daemon's answer.
// That is what happened when Info gained `deployment` as required: harmless on
// the wire, and the regenerated client could no longer connect to any daemon
// older than 13.5.0. The API-surface baseline cannot see it — it records field
// names and types, not whether a field is required.
//
// What is fine: a brand-new schema (no older daemon sent it), and a property
// that stops being required. Both still make the committed baseline stale, and
// the test says so, so the baseline keeps covering every schema — a schema
// missing from it would be one this guard no longer watches.
func TestOpenAPISchemasGainNoRequiredPropertyUnacknowledged(t *testing.T) {
	spec := loadOpenAPISpec(t)
	current := collectRequiredProperties(spec)
	if len(current) < 100 {
		t.Fatalf("found only %d object schemas under components.schemas — the walk is broken, "+
			"and a guard that sees too few schemas passes by measuring nothing", len(current))
	}

	acknowledged := parseNewlyRequiredProperties(t)

	path := openAPIRequiredPropertiesPath(t)
	baseline, haveBaseline := readRequiredPropertiesBaseline(t, path)

	var gained, unacknowledged, stale []string
	for name, oldReq := range baseline {
		newReq, ok := current[name]
		if !ok {
			stale = append(stale, "schema removed: "+name)
			continue
		}
		for _, p := range newReq {
			if slices.Contains(oldReq, p) {
				continue
			}
			key := name + "." + p
			gained = append(gained, key)
			if !acknowledged[key] {
				unacknowledged = append(unacknowledged, key)
			}
		}
		for _, p := range oldReq {
			if !slices.Contains(newReq, p) {
				stale = append(stale, fmt.Sprintf("no longer required: %s.%s", name, p))
			}
		}
	}
	for name := range current {
		if _, ok := baseline[name]; !ok {
			stale = append(stale, "schema added: "+name)
		}
	}
	sort.Strings(gained)
	sort.Strings(unacknowledged)
	sort.Strings(stale)

	if *updateOpenAPIRequiredProperties {
		// A missing baseline is the bootstrap case and has nothing to protect.
		if haveBaseline && len(unacknowledged) > 0 {
			t.Fatalf("refusing to rewrite %s: %d newly required propert(y/ies) have no entry in "+
				"newlyRequiredProperties:\n  %s\n\nAdd an entry for each, in the form\n  %s",
				path, len(unacknowledged), strings.Join(unacknowledged, "\n  "),
				requiredEntryExample(unacknowledged[0]))
		}
		writeRequiredPropertiesBaseline(t, path, current)
		t.Logf("rewrote %s with %d schemas", path, len(current))
		return
	}
	if !haveBaseline {
		t.Fatalf("%s is missing; generate it with:\n  %s", path, requiredPropertiesUpdateCommand)
	}

	if len(gained) > 0 {
		t.Errorf("%d existing schema(s) gained a required property:\n  %s\n\n"+
			"A generated client validates every answer against `required`, so it rejects the answer "+
			"of every daemon older than this change — it can no longer connect to them. Prefer "+
			"leaving the property optional. If it must be required:\n"+
			"  1. add an entry to newlyRequiredProperties saying what a client does for an older "+
			"daemon, e.g.\n       %s\n"+
			"  2. refresh the baseline with:\n       %s",
			len(gained), strings.Join(gained, "\n  "),
			requiredEntryExample(gained[0]), requiredPropertiesUpdateCommand)
		return
	}
	if len(stale) > 0 {
		t.Errorf("the committed required-property baseline is stale; nothing here needs an "+
			"acknowledgement, refresh it in this same commit:\n  %s\n\n  %s",
			requiredPropertiesUpdateCommand, strings.Join(stale, "\n  "))
	}
}

const requiredPropertiesUpdateCommand = "GOMAXPROCS=2 go test -p 2 -run TestOpenAPISchemasGainNoRequiredPropertyUnacknowledged ./tests/contract/ -update-openapi-required-properties"

func requiredEntryExample(key string) string {
	return fmt.Sprintf("%q", handlers.APIVersion+" "+key+": <what a client must do for a daemon older than "+handlers.APIVersion+">")
}

// collectRequiredProperties maps every object schema under components.schemas
// to its sorted required list. An inline allOf member contributes its own
// required list; a $ref member does not, because the schema it points at has
// an entry of its own.
func collectRequiredProperties(spec *openapi3.T) map[string][]string {
	out := map[string][]string{}
	for name, ref := range spec.Components.Schemas {
		if ref == nil || ref.Value == nil {
			continue
		}
		v := ref.Value
		if !v.Type.Is(openapi3.TypeObject) && len(v.Properties) == 0 && len(v.AllOf) == 0 {
			continue
		}
		req := append([]string{}, v.Required...)
		for _, member := range v.AllOf {
			if member != nil && member.Ref == "" && member.Value != nil {
				req = append(req, member.Value.Required...)
			}
		}
		sort.Strings(req)
		out[name] = slices.Compact(req)
	}
	return out
}

// parseNewlyRequiredProperties returns the acknowledged "<Schema>.<property>"
// keys and fails on an entry that does not follow the register's format.
func parseNewlyRequiredProperties(t *testing.T) map[string]bool {
	t.Helper()
	currentMajor, currentMinor := majorMinor(t, handlers.APIVersion)
	out := map[string]bool{}
	for _, entry := range newlyRequiredProperties {
		version, rest, ok := strings.Cut(entry, " ")
		key, what, hasColon := strings.Cut(rest, ": ")
		if !ok || !hasColon || !strings.Contains(key, ".") || strings.TrimSpace(what) == "" {
			t.Fatalf("newlyRequiredProperties entry %q is not \"<version> <Schema>.<property>: "+
				"what a client must do for an older daemon\"", entry)
		}
		major, minor := majorMinor(t, version)
		if major > currentMajor || (major == currentMajor && minor > currentMinor) {
			t.Fatalf("newlyRequiredProperties entry %q names API %s, but the API is only at %s",
				entry, version, handlers.APIVersion)
		}
		out[key] = true
	}
	return out
}

func openAPIRequiredPropertiesPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", "openapi_required_properties.json")
}

func readRequiredPropertiesBaseline(t *testing.T, path string) (map[string][]string, bool) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // fixed testdata path
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string][]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m, true
}

func writeRequiredPropertiesBaseline(t *testing.T, path string, m map[string][]string) {
	t.Helper()
	blob, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		t.Fatalf("marshal required properties: %v", err)
	}
	if err := os.WriteFile(path, append(blob, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
