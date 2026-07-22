// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWireMethodsCanonical AST-walks internal/client/backends/*.go and
// verifies that every string-literal method name passed to *.Call(ctx,
// "<method>", ...) is either:
//
//  1. In the allowlist of methods verified against the CCU JSON-RPC or
//     XML-RPC wire surface, OR
//  2. The surrounding function carries a "wire:inline reason=" comment
//     marker that documents why the direct call is acceptable (e.g.
//     p1-rega-migration-pending for methods whose canonical form is a
//     ReGa script path not yet implemented).
//
// This test makes accidental wire-method invention visible at review time
// rather than at live-CCU runtime.
func TestWireMethodsCanonical(t *testing.T) {
	t.Parallel()

	// canonicalMethods is the set of JSON-RPC and XML-RPC wire-method
	// strings that are confirmed against the CCU API surface or the
	// Python reference implementation. XML-RPC names are lower-camelCase
	// positional; JSON-RPC names are Namespace.method.
	canonicalMethods := map[string]bool{
		// XML-RPC (HomeMatic Interface spec §3)
		"ping":                    true,
		"listDevices":             true,
		"getParamsetDescription":  true,
		"getParamset":             true,
		"putParamset":             true,
		"setValue":                true,
		"getValue":                true,
		"setInstallMode":          true,
		"installFirmware":         true,
		"updateFirmware":          true,
		"restoreConfigToDevice":   true,
		"listReplaceableDevices":  true,
		"replaceDevice":           true,
		"getLinks":                true,
		"getLinkPeers":            true,
		"addLink":                 true,
		"removeLink":              true,
		"reportValueUsage":        true,
		"deleteDevice":            true,
		"determineParameter":      true,
		"getDeviceDescription":    true,
		"getMetadata":             true,
		"setMetadata":             true,
		"clientServerInitialized": true, // Homegear-specific ping variant

		// JSON-RPC — System
		"System.getSystemInformation": true,
		"System.listMethods":          true,

		// JSON-RPC — Session
		"Session.login":  true,
		"Session.logout": true,
		"Session.renew":  true,

		// JSON-RPC — CCU
		"CCU.getAuthEnabled":          true,
		"CCU.getHttpsRedirectEnabled": true,
		"CCU.getHeatingGroupList":     true,

		// JSON-RPC — Interface
		"Interface.getInstallMode":               true,
		"Interface.setInstallModeHMIP":           true,
		"Interface.setInstallMode":               true,
		"Interface.isPresent":                    true,
		"Interface.listDevices":                  true,
		"Interface.listInterfaces":               true,
		"Interface.getParamset":                  true,
		"Interface.getParamsetDescription":       true,
		"Interface.putParamset":                  true,
		"Interface.setValue":                     true,
		"Interface.getValue":                     true,
		"Interface.getMasterValue":               true,
		"Interface.getDeviceDescription":         true,
		"Interface.getLinkInfo":                  true,
		"Interface.setLinkInfo":                  true,
		"Interface.suppressServiceMessages":      true,
		"Interface.getSuppressedServiceMessages": true,
		"Interface.getIseIDByAddress":            true,
		"Interface.listBidcosInterfaces":         true,

		// JSON-RPC — Channel
		"Channel.setName":       true,
		"Channel.hasProgramIds": true,

		// JSON-RPC — Device
		"Device.setName":       true,
		"Device.listAllDetail": true,

		// JSON-RPC — SysVar
		"SysVar.createBool":         true,
		"SysVar.createEnum":         true,
		"SysVar.createFloat":        true,
		"SysVar.deleteSysVarByName": true,
		"SysVar.getAll":             true,
		"SysVar.getValueByName":     true,
		"SysVar.setBool":            true,
		"SysVar.setFloat":           true,

		// JSON-RPC — Program
		"Program.getAll":           true,
		"Program.setActive":        true,
		"Program.execute":          true,
		"Program.getByID":          true,
		"Program.assignProgramIDs": true,
		"Program.deleteProgramID":  true,
		"Program.readProgram":      true,
		"Program.updateProgram":    true,

		// JSON-RPC — Room / Subsection (ReGa service objects)
		"Room.getAll":       true,
		"Subsection.getAll": true,

		// JSON-RPC — Message (acknowledge only; getAll/suppress use ReGa)
		"Message.acknowledge": true,

		// JSON-RPC — Metadata
		"Metadata.setMetadata":    true,
		"Metadata.getMetadata":    true,
		"Metadata.deleteMetadata": true,

		// JSON-RPC — ReGa script runner
		"ReGa.runScript": true,

		// Homegear-specific XML-RPC extensions (not present on standard CCU).
		// Homegear exposes system-variable and metadata operations directly
		// over XML-RPC rather than through a JSON-RPC tier.
		"getSystemVariable":     true,
		"getAllSystemVariables": true,
		"setSystemVariable":     true,
		"deleteSystemVariable":  true,
		"deleteMetadata":        true,
	}

	// inlineMarker is the comment text that exempts a direct call from
	// needing a canonical-methods entry. The backend function must contain
	// this substring in a comment line.
	const inlineMarker = "wire:inline reason="

	repoRoot := filepath.Join("..", "..")
	backendsDir := filepath.Join(repoRoot, "internal", "client", "backends")

	entries, err := os.ReadDir(backendsDir)
	if err != nil {
		t.Fatalf("read backends dir: %v", err)
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fpath := filepath.Join(backendsDir, e.Name())
		f, err := parser.ParseFile(fset, fpath, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		files = append(files, f)
	}

	for _, f := range files {
		filename := fset.File(f.Pos()).Name()
		// Build a map: FuncDecl.Pos() → bool, true if the function body
		// carries a wire:inline comment anywhere in its doc or body comments.
		inlineByFunc := map[token.Pos]bool{}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fd.Doc != nil {
				for _, c := range fd.Doc.List {
					if strings.Contains(c.Text, inlineMarker) {
						inlineByFunc[fd.Pos()] = true
					}
				}
			}
		}

		// Also scan all comment groups in the file for inline markers
		// that appear inside function bodies (not just doc-comments).
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if strings.Contains(c.Text, inlineMarker) {
					// Mark all enclosing FuncDecl as inline-exempt.
					for _, decl := range f.Decls {
						fd, ok := decl.(*ast.FuncDecl)
						if !ok || fd.Body == nil {
							continue
						}
						if fd.Body.Lbrace <= c.Slash && c.Slash <= fd.Body.Rbrace {
							inlineByFunc[fd.Pos()] = true
						}
					}
				}
			}
		}

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			// Match selector calls: x.Call(ctx, "<method>", ...)
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Call" {
				return true
			}
			// Second argument (index 1) must be a string literal.
			if len(call.Args) < 2 {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			method := strings.Trim(lit.Value, `"`)

			if canonicalMethods[method] {
				return true // known-good
			}

			// Check whether the enclosing function is inline-exempt.
			callPos := call.Pos()
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				if fd.Body.Lbrace <= callPos && callPos <= fd.Body.Rbrace {
					if inlineByFunc[fd.Pos()] {
						return true // explicitly exempted
					}
				}
			}

			pos := fset.Position(call.Pos())
			t.Errorf(
				"%s:%d: unrecognised wire method %q — add to canonical list or mark function with // wire:inline reason=...",
				filepath.Base(filename), pos.Line, method,
			)
			return true
		})
	}
}
