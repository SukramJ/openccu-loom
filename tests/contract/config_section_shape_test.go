// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/configstore"
)

var updateSectionShape = flag.Bool("update-section-shape", false,
	"rewrite the pinned config-section payload shape")

// sectionShapePinPath is the pinned shape of every config sub-tree that gets
// serialised into a config_sections row.
var sectionShapePinPath = filepath.Join("testdata", "config_section_shape.json")

// TestConfigSectionPayloadShapeIsPinned holds the line the config_sections
// schema version has never held.
//
// Every section row is a json.Marshal of a config sub-tree, and the daemon
// decodes it back into whatever that sub-tree looks like TODAY. Rows are kept
// forever: ConfigSectionSchemaVersion gates them, but a bump wipes every
// section for every operator, so it is the wrong tool for a change that only
// affects one key and has never been used. The result is that a struct change
// silently rewrites the meaning of rows written years earlier, and nothing
// fails — not the build, not the tests, not a boot log line.
//
// That is not hypothetical. north.rest.auth.basic_enabled and bearer_enabled
// went from unread `bool` to `*bool` gates where an explicit false REJECTS the
// scheme. Every row written before that carried a literal false, so upgrading
// answered 401 to every HTTP Basic and Bearer client while /health stayed
// green and the SPA login kept working, so nothing pointed at the cause. The
// repair is internal/store/sqlite/migrations/038_config_sections_auth_gates.sql.
//
// So the shape is pinned. Any change inside a persisted section sub-tree fails
// this test with the transition spelled out, and the author decides:
//
//   - a new key: old rows simply lack it and ApplyDefaults fills it in —
//     refresh the pin and move on;
//   - a key whose TYPE changed, or a key that disappeared: an existing row now
//     decodes into something its writer did not mean. A disappeared key counts
//     here because nothing distinguishes a deletion (harmless) from a rename
//     (the operator's stored value is silently dropped) — only the author
//     knows which it is. Add a repair migration guarded on evidence that the
//     row predates the change, or bump ConfigSectionSchemaVersion when no such
//     evidence exists. Then refresh the pin.
//
// Refresh with:
//
//	go test ./tests/contract/ -run TestConfigSectionPayloadShapeIsPinned -update-section-shape
func TestConfigSectionPayloadShapeIsPinned(t *testing.T) {
	t.Parallel()

	current := persistedSectionShape()

	if *updateSectionShape {
		raw, err := json.MarshalIndent(current, "", "  ")
		if err != nil {
			t.Fatalf("marshal shape: %v", err)
		}
		if err := os.WriteFile(sectionShapePinPath, append(raw, '\n'), 0o600); err != nil {
			t.Fatalf("write %s: %v", sectionShapePinPath, err)
		}
		t.Logf("pinned %d leaves in %s", len(current), sectionShapePinPath)
		return
	}

	raw, err := os.ReadFile(sectionShapePinPath)
	if err != nil {
		t.Fatalf("read %s: %v", sectionShapePinPath, err)
	}
	var pinned map[string]string
	if err := json.Unmarshal(raw, &pinned); err != nil {
		t.Fatalf("parse %s: %v", sectionShapePinPath, err)
	}

	var dangerous, additive []string
	for path, want := range pinned {
		got, ok := current[path]
		switch {
		case !ok:
			dangerous = append(dangerous, "  "+path+": "+want+" -> GONE (renamed? then every stored value for it is silently dropped)")
		case got != want:
			dangerous = append(dangerous, "  "+path+": "+want+" -> "+got)
		}
	}
	for path, got := range current {
		if _, ok := pinned[path]; !ok {
			additive = append(additive, "  "+path+": NEW -> "+got)
		}
	}
	sort.Strings(dangerous)
	sort.Strings(additive)

	if len(dangerous) == 0 && len(additive) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("the persisted config-section payload shape changed\n")
	if len(dangerous) > 0 {
		b.WriteString("\nrows written by an older daemon now decode differently — " +
			"add a repair migration next to 038_config_sections_auth_gates.sql, " +
			"or bump ConfigSectionSchemaVersion when nothing in the row can date it:\n")
		b.WriteString(strings.Join(dangerous, "\n"))
		b.WriteString("\n")
	}
	if len(additive) > 0 {
		b.WriteString("\nnew keys (an old row lacks them and ApplyDefaults fills them in — no repair needed):\n")
		b.WriteString(strings.Join(additive, "\n"))
		b.WriteString("\n")
	}
	b.WriteString("\nrefresh the pin once the transitions above are accounted for:\n" +
		"  go test ./tests/contract/ -run TestConfigSectionPayloadShapeIsPinned -update-section-shape\n")
	t.Fatal(b.String())
}

// persistedSectionShape returns every JSON leaf that can appear in a
// config_sections row, keyed by its dotted JSON path and valued by its Go
// type (plus the omitempty/omitzero marker, which decides whether a zero value
// is written out at all — the difference between "unset" and "explicitly off"
// for every tri-state gate).
//
// The covered set is derived from [configstore.AllSections] rather than
// listed, so a section added later is pinned automatically.
func persistedSectionShape() map[string]string {
	all := make(map[string]string)
	walkJSONLeaves(reflect.TypeOf(config.Config{}), "", all)

	out := make(map[string]string, len(all))
	for path, shape := range all {
		for _, sec := range configstore.AllSections() {
			s := string(sec)
			if path == s || strings.HasPrefix(path, s+".") {
				out[path] = shape
				break
			}
		}
	}
	return out
}

// walkJSONLeaves records one entry per leaf reachable by json.Marshal,
// mirroring what encoding/json itself does: unexported and json:"-" fields are
// skipped, pointers are followed, named struct types are recursed into, and
// everything else — including time.Duration and the time package's own structs
// — is a leaf. Slices of structs emit both the slice leaf and its element
// fields under a "[]" segment.
func walkJSONLeaves(rt reflect.Type, prefix string, out map[string]string) {
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct {
		return
	}
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		key, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if key == "-" {
			continue
		}
		if key == "" {
			if f.Anonymous {
				walkJSONLeaves(f.Type, prefix, out)
				continue
			}
			key = f.Name
		}
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		elem := f.Type
		for elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		if elem.Kind() == reflect.Struct && elem.PkgPath() != "time" {
			walkJSONLeaves(elem, path, out)
			continue
		}
		out[path] = jsonLeafShape(f.Type, opts)
		if elem.Kind() == reflect.Slice || elem.Kind() == reflect.Array {
			item := elem.Elem()
			for item.Kind() == reflect.Pointer {
				item = item.Elem()
			}
			if item.Kind() == reflect.Struct && item.PkgPath() != "time" {
				walkJSONLeaves(item, path+"[]", out)
			}
		}
	}
}

// jsonLeafShape renders the leaf's Go type plus the tag options that decide
// whether a zero value reaches the stored payload. Without omitempty/omitzero
// a zero value is always written, which is exactly how an unread `false`
// ended up in every north.rest row.
func jsonLeafShape(ft reflect.Type, opts string) string {
	shape := ft.String()
	var marks []string
	for _, opt := range strings.Split(opts, ",") {
		switch opt {
		case "omitempty", "omitzero", "string":
			marks = append(marks, opt)
		}
	}
	if len(marks) > 0 {
		shape += "," + strings.Join(marks, ",")
	}
	return shape
}
