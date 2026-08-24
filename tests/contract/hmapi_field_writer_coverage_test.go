// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// hmapiFieldsNeverWritten records every json-tagged pkg/hmapi field the
// daemon never populates, with the reason.
//
// The field is in assets/openapi.yaml either way, so a generated client
// carries an attribute for it and a reader of the spec has no way to tell
// "sometimes absent" from "never sent". That is what an entry here has to
// answer: not that the field is unused, but why a documented part of the
// contract is permanently empty.
var hmapiFieldsNeverWritten = map[string]string{
	"ConfigSnapshot.Extras": "a declared extension point, not a forgotten field. ConfigSnapshot's own doc says the shape may grow without breaking clients, and this is the slot reserved for that; the sole producer (internal/central/adapter/config.go:59) fills Locale, CallbackPorts, Features, Policies and Centrals and has never had anything to put here",

	"InterfaceState.Note": "no producer writes it: internal/central/adapter/interfaces.go:45 is the only site that builds one and it sets seven other fields. Kept rather than dropped because handlers.anonymiseDiagnostics redacts it (internal/north/rest/handlers/diagnostics.go:296) — removing the field would also delete a privacy scrub whose absence nobody would notice until something started filling it, and removing a published field is a major bump for a value no client has ever received",
}

// TestEveryPublishedDTOFieldHasAWriter pins that a field pkg/hmapi
// declares — and assets/openapi.yaml therefore publishes — is populated
// somewhere in the daemon.
//
// A field nobody writes is not dead code. It is a promise in the published
// contract: `datamodel-codegen` mints an attribute for it, the SPA's
// generated types carry it, and every client sees an optional value that
// is optional in the sense of never arriving. Nothing fails, no test
// changes colour, and the only way to find out is to run the daemon and
// notice the key is missing.
//
// Blind spot, stated because round 7 exists to state them: this measures
// writes, not reachability. A field written only on a branch nothing
// reaches counts as written here. It is the weaker of the two questions
// and the one a type checker can answer.
func TestEveryPublishedDTOFieldHasAWriter(t *testing.T) {
	t.Parallel()

	pkgs := loadProductionPackages(t)

	declared := map[string]bool{} // "Struct.Field"
	written := map[string]bool{}
	produced := map[string]bool{} // structs the daemon builds rather than decodes

	packages.Visit(pkgs, nil, func(p *packages.Package) {
		isHmapi := strings.HasSuffix(p.PkgPath, "/pkg/hmapi")
		for _, file := range p.Syntax {
			if isHmapi {
				collectJSONTaggedFields(file, declared)
			}
			collectFieldWrites(p, file, written, produced)
		}
	})

	if len(declared) < 300 {
		t.Fatalf("read only %d json-tagged hmapi fields — the walk is wrong, and a guard "+
			"that sees too few fields passes by measuring nothing", len(declared))
	}

	var missing, stale []string
	for field := range declared {
		if written[field] {
			continue
		}
		// Inbound types are out of scope, and "the daemon never builds
		// one" is what makes a type inbound. An AlarmArmRequest is
		// decoded into and read; nothing on this side fills it, and a
		// guard that demanded a writer would be asking the daemon to
		// populate the client's own request. Deciding this by a `Request`
		// name suffix would miss SecuritySourceOverride and would rest on
		// a convention rather than on what the code does.
		if !produced[strings.SplitN(field, ".", 2)[0]] {
			continue
		}
		if _, known := hmapiFieldsNeverWritten[field]; known {
			continue
		}
		missing = append(missing, field)
	}
	for field := range hmapiFieldsNeverWritten {
		switch {
		case !declared[field]:
			stale = append(stale, field+" (no longer declared)")
		case written[field]:
			stale = append(stale, field+" (now written — the entry excuses nothing)")
		case !produced[strings.SplitN(field, ".", 2)[0]]:
			stale = append(stale, field+" (inbound type — already out of scope without an entry)")
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)

	if len(missing) > 0 {
		t.Errorf("%d pkg/hmapi field(s) are declared in the published contract and written nowhere:\n  %s\n\n"+
			"Every one of these is an attribute on a generated client that can never hold a value. "+
			"Either populate it, or record in hmapiFieldsNeverWritten why the contract carries a "+
			"field the daemon never sends.",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("%d hmapiFieldsNeverWritten entr(ies) name a field pkg/hmapi no longer declares:\n  %s\n\n"+
			"An entry for a field that does not exist excuses nothing and overstates how much "+
			"of the DTO surface is accounted for.",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// collectJSONTaggedFields records every exported, json-tagged field of
// every exported struct in the file as "Struct.Field".
func collectJSONTaggedFields(file *ast.File, out map[string]bool) {
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || !ts.Name.IsExported() {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, f := range st.Fields.List {
			if f.Tag == nil || !strings.Contains(f.Tag.Value, "json:") {
				continue
			}
			if strings.Contains(f.Tag.Value, `json:"-"`) {
				continue
			}
			for _, nm := range f.Names {
				if nm.IsExported() {
					out[ts.Name.Name+"."+nm.Name] = true
				}
			}
		}
		return false
	})
}

// collectFieldWrites records every field the file assigns, keyed the same
// way — "Struct.Field" with Struct resolved through the type checker, so
// a `Note` on some other type does not excuse hmapi's own.
//
// Two shapes count as a write: a key inside a composite literal, and an
// assignment to a selector. Both are resolved against types.Info, which is
// the whole point: the name-based version of this walk missed
// InterfaceState.Note entirely, because six unrelated types also have a
// field called Note and one of them is assigned.
func collectFieldWrites(p *packages.Package, file *ast.File, out, produced map[string]bool) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CompositeLit:
			named := namedStructOf(p.TypesInfo.TypeOf(v))
			if named == "" {
				return true
			}
			produced[named] = true
			for _, el := range v.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok {
					out[named+"."+key.Name] = true
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range v.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				named := namedStructOf(p.TypesInfo.TypeOf(sel.X))
				if named == "" {
					continue
				}
				out[named+"."+sel.Sel.Name] = true
			}
		case *ast.IncDecStmt:
			// `report.Touched++` — a counter field is written by nothing
			// else, and leaving this out made the guard report all three
			// CentralLinksReport counters and CentralLinksStatus.ActiveChannels
			// as never written while central_links.go fills every one.
			if sel, ok := v.X.(*ast.SelectorExpr); ok {
				if named := namedStructOf(p.TypesInfo.TypeOf(sel.X)); named != "" {
					out[named+"."+sel.Sel.Name] = true
				}
			}
		case *ast.UnaryExpr:
			// `&out.Perms` — handing a field's address to anything is a
			// write the two shapes above cannot see. json.Unmarshal into
			// a field is the common case, and leaving it out made this
			// guard report AlarmCode.Perms and .Zones as never written
			// when alarm_codes.go:490 and :493 fill both.
			if v.Op != token.AND {
				return true
			}
			sel, ok := v.X.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if named := namedStructOf(p.TypesInfo.TypeOf(sel.X)); named != "" {
				out[named+"."+sel.Sel.Name] = true
			}
		}
		return true
	})
}

// namedStructOf returns the bare name of a named struct type, following
// pointers and slices, or "" for anything else.
func namedStructOf(t types.Type) string {
	for range 4 {
		switch v := t.(type) {
		case *types.Pointer:
			t = v.Elem()
		case *types.Slice:
			t = v.Elem()
		case *types.Array:
			t = v.Elem()
		case *types.Named:
			return v.Obj().Name()
		default:
			return ""
		}
	}
	return ""
}
