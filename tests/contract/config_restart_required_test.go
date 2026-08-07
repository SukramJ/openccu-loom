// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// restartPathsNotDiffableByMutation lists restart-required paths whose
// detection cannot be exercised by mutating a single leaf, with the reason.
// Everything else must be provably diffable — see
// TestEveryRestartRequiredFieldIsDetectedByDiff.
var restartPathsNotDiffableByMutation = map[string]string{
	// A central present in only one config is a pure add or remove, both of
	// which the orchestrator performs live. Only an in-place modification is
	// restart-required, so the rule needs two configs sharing a central name —
	// TestCentralsRestartRuleNeedsAnInPlaceModification covers it directly.
	"centrals": "add/remove are live operations; only an in-place change counts",
}

// TestEveryRestartRequiredFieldIsDetectedByDiff is the guard that keeps the
// schema's restart-required badge and config.RestartRequiredDiff from drifting
// apart. They were maintained as two independent lists and disagreed: the whole
// alarm block and the Basic/Bearer auth gates were badged in the schema but
// never compared by the diff, so a save answered restart_required:false and
// /restart-pending never lit up — the operator got no hint that the change sat
// inert until the next boot.
//
// The check is behavioural rather than a set comparison: for every badged
// field it mutates exactly that leaf and asserts the diff reports the rule that
// owns it. A set comparison would pass on two lists that agree on names while
// the comparison behind one of them is missing.
func TestEveryRestartRequiredFieldIsDetectedByDiff(t *testing.T) {
	t.Parallel()

	ruleFor := ownerRuleByField(t)

	for _, path := range sortedRestartPaths(config.RestartRequiredFieldPaths()) {
		if reason, skip := restartPathsNotDiffableByMutation[path]; skip {
			t.Logf("skipping %s: %s", path, reason)
			continue
		}
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			boot := config.Default()
			eff := config.Clone(boot)
			if err := mutateFieldAtPath(eff, path); err != nil {
				t.Fatalf("could not mutate %s: %v", path, err)
			}
			got := config.RestartRequiredDiff(boot, eff)
			want := ruleFor[path]
			if !slices.Contains(got, want) {
				t.Errorf("changing %s is badged restart-required but the diff reported %v (want %q) — "+
					"the save would answer restart_required:false and /restart-pending would stay empty",
					path, got, want)
			}
		})
	}
}

// TestCentralsRestartRuleNeedsAnInPlaceModification covers the one rule the
// single-leaf mutation cannot express: adding or removing a central is a live
// orchestrator operation, only changing one in place is restart-required.
func TestCentralsRestartRuleNeedsAnInPlaceModification(t *testing.T) {
	t.Parallel()

	boot := config.Default()
	boot.Centrals = []config.CentralConfig{{Name: "ccu1", Host: "192.0.2.10"}}

	added := config.Clone(boot)
	added.Centrals = append(added.Centrals, config.CentralConfig{Name: "ccu2", Host: "192.0.2.11"})
	if slices.Contains(config.RestartRequiredDiff(boot, added), "centrals") {
		t.Error("adding a central must not require a restart — the orchestrator adopts it live")
	}

	modified := config.Clone(boot)
	modified.Centrals[0].Host = "192.0.2.99"
	if !slices.Contains(config.RestartRequiredDiff(boot, modified), "centrals") {
		t.Error("modifying a central in place must require a restart")
	}
}

// TestConfigSchemaRestartBadgeMatchesRestartRules crosses the package boundary
// the drift lived on: the badge the SPA renders comes out of the REST schema
// handler, the pending-restart banner out of config.RestartRequiredDiff. This
// asserts the two describe the same set of fields, going through the production
// handler rather than the package-level variable behind it — so re-introducing
// a hand-maintained map in the handler fails here.
//
// north.rest.listen and the SQLite-managed credentials are filtered out of the
// schema on purpose (they are not editable through the section editor), so they
// are excluded from the comparison rather than expected in it.
func TestConfigSchemaRestartBadgeMatchesRestartRules(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/schema", http.NoBody)
	w := httptest.NewRecorder()
	handlers.GetConfigSchema().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("schema endpoint returned %d", w.Code)
	}
	var resp struct {
		Fields []struct {
			Path            string `json:"path"`
			RestartRequired bool   `json:"restart_required"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("schema response is not JSON: %v", err)
	}

	badged := make(map[string]struct{})
	schemaPaths := make(map[string]struct{}, len(resp.Fields))
	for _, f := range resp.Fields {
		schemaPaths[f.Path] = struct{}{}
		if f.RestartRequired {
			badged[f.Path] = struct{}{}
		}
	}

	unmanaged := configstore.UnmanagedFieldPaths()
	for _, path := range sortedRestartPaths(config.RestartRequiredFieldPaths()) {
		if _, skip := unmanaged[path]; skip {
			continue
		}
		if _, inSchema := schemaPaths[path]; !inSchema {
			// A rule path that is not a renderable config leaf (a block or the
			// centrals pseudo-path) carries no badge; nothing to compare.
			continue
		}
		if _, ok := badged[path]; !ok {
			t.Errorf("%s is restart-required but the schema does not badge it", path)
		}
	}
	for path := range badged {
		if _, ok := config.RestartRequiredFieldPaths()[path]; !ok {
			t.Errorf("%s is badged restart-required in the schema but no rule diffs it — "+
				"the save would report restart_required:false", path)
		}
	}
}

// TestRestartRequiredFieldsExistInConfigSchema keeps the rule table honest
// against config renames: every annotated path must still resolve to a real
// cfg-tagged leaf (or, for block rules, be a prefix of one).
func TestRestartRequiredFieldsExistInConfigSchema(t *testing.T) {
	t.Parallel()

	known := make(map[string]struct{})
	for _, f := range config.ClassifyFields(&config.Config{}) {
		known[f.Path] = struct{}{}
	}
	for _, path := range sortedRestartPaths(config.RestartRequiredFieldPaths()) {
		if _, ok := known[path]; !ok {
			t.Errorf("restart-required path %q is not a config field — renamed or misspelled", path)
		}
	}
}

// ownerRuleByField maps every annotated field path to the rule path the diff
// reports for it.
func ownerRuleByField(t *testing.T) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, r := range config.RestartRules() {
		fields := r.Fields
		if len(fields) == 0 {
			fields = []string{r.Path}
		}
		for _, f := range fields {
			out[f] = r.Path
		}
	}
	return out
}

func sortedRestartPaths(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// mutateFieldAtPath walks cfg by yaml tag to the leaf named by the dotted path
// and replaces its value with a different one of the same type, so the caller
// can assert that the change is observable. Reflection keeps the guard free of
// a per-field mutator table, which would be the very drift it exists to catch.
func mutateFieldAtPath(cfg *config.Config, path string) error {
	v, err := fieldByYAMLPath(reflect.ValueOf(cfg).Elem(), strings.Split(path, "."))
	if err != nil {
		return err
	}
	return mutateValue(v)
}

func fieldByYAMLPath(v reflect.Value, parts []string) (reflect.Value, error) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, errNoSuchField(parts)
	}
	rt := v.Type()
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag == "" {
			tag = f.Name
		}
		if tag != parts[0] {
			continue
		}
		if len(parts) == 1 {
			return v.Field(i), nil
		}
		return fieldByYAMLPath(v.Field(i), parts[1:])
	}
	return reflect.Value{}, errNoSuchField(parts)
}

type fieldPathError string

func (e fieldPathError) Error() string { return string(e) }

func errNoSuchField(parts []string) error {
	return fieldPathError("no config field for yaml path " + strings.Join(parts, "."))
}

// mutateValue writes a value different from the current one. A nil pointer
// becomes an explicit false / zero value, which flips every tri-state gate in
// the config away from its nil default.
func mutateValue(v reflect.Value) error {
	if !v.CanSet() {
		return fieldPathError("field is not settable")
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
			return nil // the zero value differs from "unset" for every tri-state gate
		}
		return mutateValue(v.Elem())
	}
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.String:
		v.SetString(v.String() + "-changed")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + 1)
	case reflect.Slice:
		v.Set(reflect.Append(v, reflect.New(v.Type().Elem()).Elem()))
	case reflect.Map:
		if v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
		v.SetMapIndex(reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem())
	default:
		return fieldPathError("no mutation strategy for kind " + v.Kind().String())
	}
	return nil
}
