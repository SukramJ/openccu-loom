// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRootForHelpers resolves the repository root relative to this file's
// location (tests/contract/ → two levels up).
func repoRootForHelpers(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatalf("repo root resolution: %v", err)
	}
	return abs
}

// parseFiles parses callerPath and returns the AST of every Go source file it
// covers. callerPath is repo-root-relative; it may be a single file
// ("cmd/openccu-loom/daemon.go") or a package directory ("cmd/openccu-loom"),
// in which case all non-_test.go files are parsed. Passing the directory keeps
// a wiring pin valid when the code moves between files of the same package.
func parseFiles(t *testing.T, callerPath string) []*ast.File {
	t.Helper()
	root := repoRootForHelpers(t)
	absPath := filepath.Join(root, callerPath)
	fset := token.NewFileSet()
	info, err := os.Stat(absPath)
	if err != nil {
		t.Fatalf("wiring_helpers: cannot stat %s: %v", callerPath, err)
	}
	if info.IsDir() {
		pkgs, err := parser.ParseDir(fset, absPath, func(fi fs.FileInfo) bool { //nolint:staticcheck // parser.ParseDir is deprecated in Go 1.25 but sufficient for this lightweight name-based AST scan; go/packages would pull in a full type-checker dependency out of scope for a contract pin
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil {
			t.Fatalf("wiring_helpers: cannot parse dir %s: %v", callerPath, err)
		}
		var out []*ast.File
		for _, pkg := range pkgs {
			for _, f := range pkg.Files {
				out = append(out, f)
			}
		}
		return out
	}
	f, err := parser.ParseFile(fset, absPath, nil, 0)
	if err != nil {
		t.Fatalf("wiring_helpers: cannot parse %s: %v", callerPath, err)
	}
	return []*ast.File{f}
}

// MustFindCallerInFile asserts that callerFile (repo-root-relative) contains
// a call-expression or argument whose selector name equals calleeIdent.
// calleePackage is used only in the failure message to give context; the AST
// search is purely name-based to survive import-alias changes.
//
// The check skips any occurrence of calleeIdent that is the name of a
// top-level FuncDecl in callerFile (i.e. the definition itself), so passing
// the definition file as callerFile does not produce a false positive.
// As a rule, callerFile must be a file that calls the function, not the file
// that defines it — passing the definition file is an anti-pattern that this
// guard makes harmless but does not endorse.
//
// Usage:
//
//	MustFindCallerInFile(t, "cmd/openccu-loom/daemon.go",
//	    "internal/metrics", "NewMqttCollector")
func MustFindCallerInFile(t *testing.T, callerFile, calleePackage, calleeIdent string) {
	t.Helper()
	for _, f := range parseFiles(t, callerFile) {
		if astContainsIdentNotDefinition(f, calleeIdent) {
			return
		}
	}
	t.Errorf(
		"wiring pin: %s not found in %s\n  expected a call to %s.%s",
		calleeIdent, callerFile, calleePackage, calleeIdent,
	)
}

// MustFindMethodCall asserts that callerFile contains a SelectorExpr whose
// X ends with receiverIdent and whose Sel is methodName.  This pins
// method-call wiring such as HubCoordinator.SetProgramExecutor.
func MustFindMethodCall(t *testing.T, callerFile, receiverIdent, methodName string) {
	t.Helper()
	found := false
	for _, f := range parseFiles(t, callerFile) {
		ast.Inspect(f, func(n ast.Node) bool {
			if found {
				return false
			}
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name != methodName {
				return true
			}
			// Accept any expression whose string representation ends with receiverIdent.
			xStr := exprString(sel.X)
			if strings.HasSuffix(xStr, receiverIdent) || strings.Contains(xStr, receiverIdent+".") {
				found = true
			}
			return true
		})
		if found {
			break
		}
	}
	if !found {
		t.Errorf(
			"wiring pin: method call %s.%s not found in %s",
			receiverIdent, methodName, callerFile,
		)
	}
}

// MustFindStructLiteralField asserts that callerFile contains a composite
// literal whose type ends with structName and that sets fieldName.
func MustFindStructLiteralField(t *testing.T, callerFile, structName, fieldName string) {
	t.Helper()
	found := false
	for _, f := range parseFiles(t, callerFile) {
		ast.Inspect(f, func(n ast.Node) bool {
			if found {
				return false
			}
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			typeName := exprString(cl.Type)
			if !strings.HasSuffix(typeName, structName) {
				return true
			}
			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if id, ok := kv.Key.(*ast.Ident); ok && id.Name == fieldName {
					found = true
					return false
				}
			}
			return true
		})
		if found {
			break
		}
	}
	if !found {
		t.Errorf(
			"wiring pin: struct literal %s{%s: ...} not found in %s",
			structName, fieldName, callerFile,
		)
	}
}

// MustFindInterfaceImpl asserts that at least minImpls exported types in
// production Go files (non-_test.go) across the repository implement the
// interface identified by ifaceName inside ifacePkg.  The check is purely
// name-based: it counts exported types whose method set contains every
// method declared on the named interface, using a fast single-pass AST scan
// within the interface's own package directory.
//
// Because a full type-checker pass is out of scope for a lightweight pin
// test, this function uses a heuristic: it verifies that at least minImpls
// types declare all methods listed on the interface declaration.  False
// positives (types that happen to have the same method names) are acceptable;
// false negatives are not.
func MustFindInterfaceImpl(t *testing.T, ifacePkg, ifaceName string, minImpls int) {
	t.Helper()
	root := repoRootForHelpers(t)
	pkgDir := filepath.Join(root, ifacePkg)

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, pkgDir, func(fi fs.FileInfo) bool { //nolint:staticcheck // parser.ParseDir is deprecated in Go 1.25 but sufficient for this lightweight name-based AST scan; go/packages would introduce a full type-checker dependency that is out of scope for a contract pin test
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("wiring_helpers: cannot parse package dir %s: %v", ifacePkg, err)
	}

	// Collect the method names of the target interface.
	ifaceMethods := collectInterfaceMethods(pkgs, ifaceName)
	if len(ifaceMethods) == 0 {
		t.Fatalf("wiring_helpers: interface %s.%s not found or has no methods", ifacePkg, ifaceName)
	}

	// Count types that declare all required methods.
	count := 0
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range fd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					_ = st
					if typeHasMethods(pkg, ts.Name.Name, ifaceMethods) {
						count++
					}
				}
			}
		}
	}

	if count < minImpls {
		t.Errorf(
			"wiring pin: expected at least %d exported implementations of %s.%s, found %d",
			minImpls, ifacePkg, ifaceName, count,
		)
	}
}

// MustFindStringLiteralInFile asserts that callerFile contains a string
// literal whose value equals wantValue.  Use this when the wiring is expressed
// as an RPC method name or a constant string rather than a Go identifier.
//
// Usage:
//
//	MustFindStringLiteralInFile(t,
//	    "internal/client/backends/ccu_extended.go",
//	    "Interface.setInstallModeHMIP")
func MustFindStringLiteralInFile(t *testing.T, callerFile, wantValue string) {
	t.Helper()
	found := false
	for _, f := range parseFiles(t, callerFile) {
		ast.Inspect(f, func(n ast.Node) bool {
			if found {
				return false
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok {
				return true
			}
			if lit.Kind == token.STRING {
				// Strip surrounding quotes.
				v := lit.Value
				if len(v) >= 2 && v[0] == '"' {
					v = v[1 : len(v)-1]
				}
				if v == wantValue {
					found = true
				}
			}
			return true
		})
		if found {
			break
		}
	}
	if !found {
		t.Errorf(
			"wiring pin: string literal %q not found in %s",
			wantValue, callerFile,
		)
	}
}

// --- AST helpers ---

// astContainsIdentNotDefinition reports whether f contains an occurrence of
// ident that is NOT the FuncDecl name at the top level of f.  This prevents
// the self-caller false positive where the definition file is mistakenly
// supplied as callerFile: a file that only defines SetFoo will not match
// even though SetFoo appears as an *ast.Ident.
//
// The check treats a top-level FuncDecl whose Name.Name == ident as the
// "definition" position.  Any other occurrence — CallExpr arguments,
// SelectorExpr selectors, variable initialisers — counts as a usage.
func astContainsIdentNotDefinition(f *ast.File, ident string) bool {
	// Collect the position range of every top-level FuncDecl that is named
	// exactly ident.  Identifiers that fall inside that range are definitions
	// and must be skipped.
	type span struct{ start, end token.Pos }
	var definitionSpans []span
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd.Name != nil && fd.Name.Name == ident {
			definitionSpans = append(definitionSpans, span{fd.Pos(), fd.End()})
		}
	}

	inDefinitionSpan := func(pos token.Pos) bool {
		for _, s := range definitionSpans {
			if pos >= s.start && pos < s.end {
				return true
			}
		}
		return false
	}

	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if found {
			return false
		}
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if id.Name == ident && !inDefinitionSpan(id.Pos()) {
			found = true
		}
		return true
	})
	return found
}

// exprString returns a compact textual representation of an expression,
// sufficient for suffix / contains checks on qualified names.
func exprString(e ast.Expr) string {
	if e == nil {
		return ""
	}
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	case *ast.StarExpr:
		return exprString(v.X)
	case *ast.IndexExpr:
		return exprString(v.X)
	default:
		return ""
	}
}

// collectInterfaceMethods returns the set of method names declared on the
// named interface across all parsed packages.
//
//nolint:staticcheck // ast.Package is deprecated since Go 1.22; acceptable here — see MustFindInterfaceImpl for rationale
func collectInterfaceMethods(pkgs map[string]*ast.Package, ifaceName string) map[string]bool {
	methods := make(map[string]bool)
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || ts.Name.Name != ifaceName {
						continue
					}
					iface, ok := ts.Type.(*ast.InterfaceType)
					if !ok {
						continue
					}
					for _, m := range iface.Methods.List {
						for _, name := range m.Names {
							methods[name.Name] = true
						}
					}
				}
			}
		}
	}
	return methods
}

// typeHasMethods reports whether any function declaration in pkg has a
// receiver whose base type is typeName and a name in required.
//
//nolint:staticcheck // ast.Package is deprecated since Go 1.22; acceptable here — see MustFindInterfaceImpl for rationale
func typeHasMethods(pkg *ast.Package, typeName string, required map[string]bool) bool {
	if len(required) == 0 {
		return false
	}
	found := make(map[string]bool)
	for _, f := range pkg.Files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 {
				continue
			}
			recv := exprString(fd.Recv.List[0].Type)
			if recv != typeName {
				continue
			}
			found[fd.Name.Name] = true
		}
	}
	for m := range required {
		if !found[m] {
			return false
		}
	}
	return true
}
