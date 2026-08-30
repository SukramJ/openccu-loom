// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// addressSplittersOutsideHmtypes names every function that parses a CCU device
// address itself instead of delegating to pkg/hmtypes.
//
// The grammar — a channel address is a device address plus a ':'-separated
// suffix — is domain identity knowledge, and hmtypes owns it along with the
// separator, the format patterns and SplitChannelAddress. Copies do not stay
// equal: the twelve that existed when this guard was written carried three
// different behaviours between them, first colon versus last colon versus two
// spellings of the leading-colon edge case.
//
// The entries left search BACKWARDS, which is a different rule rather than a
// stale copy: a link address may carry more than one separator. Folding them
// without establishing what they actually receive would be a guess, so they
// stay recorded and the question stays open.
var addressSplittersOutsideHmtypes = map[string]string{
	"internal/central/coordinators/link.go": "searches backwards; a link address may carry more than one separator",
	"internal/central/adapter/devices.go":   "searches backwards; same open question as link.go",
	"internal/central/queryfacade.go":       "searches backwards; same open question as link.go",
}

// TestAddressSplittingHasOneSource fails when a package grows its own
// device-address parser.
//
// A private copy is invisible until it disagrees, and by then it is the key of
// something — an ownership gate, a store lookup, an entity id. Three of the
// copies this guard first reported were in packages no audit pass had looked
// at, and a fourth (pkg/hmevent) was missed by an earlier, narrower version of
// this very filter, which matched only device-prefixed names.
func TestAddressSplittingHasOneSource(t *testing.T) {
	t.Parallel()
	const root = "../.."
	var found []string

	inspect := func(path string) {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.Contains(strings.ToLower(fn.Name.Name), "deviceaddress") {
				continue
			}
			// hmtypes is the source, and a wrapper that forwards to it is not
			// a copy — pkg/hmevent keeps SecurityDeviceAddress under the name
			// its callers already use, which is fine because it delegates.
			// Recognised by the call it makes, not by its name.
			if strings.Contains(filepath.ToSlash(path), "pkg/hmtypes/") || delegatesToHmtypes(fn) {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			found = append(found, filepath.ToSlash(rel))
		}
	}

	for _, sub := range []string{"internal", "pkg"} {
		err := filepath.Walk(filepath.Join(root, sub), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err //nolint:wrapcheck // walk error is returned as-is
			}
			inspect(path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sub, err)
		}
	}

	if len(found) == 0 && len(addressSplittersOutsideHmtypes) > 0 {
		t.Fatal("no address splitters found at all — the guard lost its subject")
	}
	sort.Strings(found)
	for _, f := range found {
		if _, excused := addressSplittersOutsideHmtypes[f]; !excused {
			t.Errorf("%s parses a device address itself; call pkg/hmtypes.DeviceAddress or record why not", f)
		}
	}
	for f := range addressSplittersOutsideHmtypes {
		if !slices.Contains(found, f) {
			t.Errorf("%s no longer parses an address — drop it from addressSplittersOutsideHmtypes", f)
		}
	}
}

// delegatesToHmtypes reports whether fn calls hmtypes.DeviceAddress, which
// makes it a wrapper rather than a second implementation.
func delegatesToHmtypes(fn *ast.FuncDecl) bool {
	var found bool
	ast.Inspect(fn, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "DeviceAddress" {
			return true
		}
		if pkg, isIdent := sel.X.(*ast.Ident); isIdent && pkg.Name == "hmtypes" {
			found = true
		}
		return true
	})
	return found
}
