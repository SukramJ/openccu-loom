// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// w2PkgPathDataUnusedMarker is the sentence pkg/hmtypes/path_data.go carries
// while the type has no consumer inside the daemon. It is a claim about
// reachability, so it is measured here rather than trusted.
const w2PkgPathDataUnusedMarker = "no consumer inside the daemon"

// hmtypes.PathData used to document itself as the type that "drives MQTT
// topic generation, REST URL routing, and UI breadcrumbs". None of the three
// is true: the daemon renders those from internal/model/naming.PathData,
// which does not even carry the same fields. A reader of a published package
// was told a rule the daemon does not run, and script/reachability
// auto-whitelists everything under pkg/hmtypes, so no mechanism could report
// it.
//
// This guard measures the reachability claim in both directions: with no
// production consumer the doc must say so, and the moment one appears the doc
// must stop saying it. Reference counting goes through the type checker, so a
// dot-import or a package alias counts like a qualified name.
func TestW2PkgHmtypesPathDataHasNoDaemonConsumer(t *testing.T) {
	t.Parallel()

	pkgs := loadProductionPackages(t)
	var consumers []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if strings.HasSuffix(p.PkgPath, "/pkg/hmtypes") {
			return
		}
		for ident, obj := range p.TypesInfo.Uses {
			tn, ok := obj.(*types.TypeName)
			if !ok || tn.Name() != "PathData" || tn.Pkg() == nil {
				continue
			}
			if !strings.HasSuffix(tn.Pkg().Path(), "/pkg/hmtypes") {
				continue
			}
			consumers = append(consumers, p.Fset.Position(ident.Pos()).String())
		}
	})
	sort.Strings(consumers)

	doc := w2PkgReadRepoFile(t, "pkg/hmtypes/path_data.go")
	claimsUnused := strings.Contains(doc, w2PkgPathDataUnusedMarker)

	switch {
	case len(consumers) == 0 && !claimsUnused:
		t.Errorf("hmtypes.PathData has no production consumer, but pkg/hmtypes/path_data.go does not say so; "+
			"a reader of the published package is left believing the daemon renders paths from this type "+
			"(it uses internal/model/naming.PathData, whose fields differ). State it with %q",
			w2PkgPathDataUnusedMarker)
	case len(consumers) > 0 && claimsUnused:
		t.Errorf("pkg/hmtypes/path_data.go still says %q, but %d production reference(s) now exist: %s",
			w2PkgPathDataUnusedMarker, len(consumers), strings.Join(consumers, ", "))
	}
}

// w2PkgReadRepoFile reads a repo-root-relative file.
func w2PkgReadRepoFile(t *testing.T, rel string) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// tests/contract → repo root.
	root = filepath.Dir(filepath.Dir(root))
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}
