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

// addressSplittersOutsideHmtypes names every package that still parses a CCU
// address itself instead of calling pkg/hmtypes.
//
// The grammar — a channel address is a device address plus a ':'-separated
// suffix — is domain identity knowledge, and hmtypes owns it along with the
// separator constant, the format patterns and SplitChannelAddress. Copies do
// not stay equal: the five that existed when this guard was written carried
// FOUR different behaviours between them (first colon vs last colon, and two
// spellings of the leading-colon edge case).
//
// The three entries left are the ones that search BACKWARDS, which is a
// different rule rather than a stale copy of this one — a link address can
// carry more than one separator, and folding them onto hmtypes without
// establishing what they actually receive would be a guess. They are recorded
// here so the question stays open instead of looking settled.
var addressSplittersOutsideHmtypes = map[string]string{
	"internal/central/coordinators/link.go": "searches BACKWARDS — a link address may carry more than one separator, so this is a different rule rather than a stale copy",
	"internal/central/adapter/devices.go":   "searches BACKWARDS; same open question as link.go",
	"internal/central/queryfacade.go":       "searches BACKWARDS; same open question as link.go",
}

// TestAddressSplittingHasOneSource fails when a new package grows its own
// device-address parser.
//
// A private copy is invisible until it disagrees, and by then it is the key of
// something — an ownership gate, a store lookup, an entity id. This guard is
// what makes the sixth copy fail a build rather than ship.
func TestAddressSplittingHasOneSource(t *testing.T) {
	t.Parallel()
	root := "../.."
	var found []string
	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return nil //nolint:nilerr // an unparsable file is not this guard's subject
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			n := strings.ToLower(fn.Name.Name)
			if strings.Contains(n, "deviceaddress") && strings.HasPrefix(n, "device") {
				rel, _ := filepath.Rel(root, path)
				found = append(found, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
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
