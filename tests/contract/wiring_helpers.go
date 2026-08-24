// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/go/ast/astutil"
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

// parsedUnit is the source the pin helpers reason over: the files
// callerPath covers, plus the position information and package name the
// reachability checks need.
type parsedUnit struct {
	files []*ast.File
	fset  *token.FileSet
	// wholePackage records that callerPath named a directory, so the file
	// set is the complete package and a call graph over it is complete
	// too. A single-file unit sees only part of the package, and the
	// reachability check is skipped for it.
	wholePackage bool
	pkgName      string
}

// parseUnit parses callerPath. It is repo-root-relative; it may be a single
// file ("cmd/openccu-loom/daemon.go") or a package directory
// ("cmd/openccu-loom"), in which case all non-_test.go files are parsed.
// Passing the directory keeps a wiring pin valid when the code moves
// between files of the same package.
func parseUnit(t *testing.T, callerPath string) parsedUnit {
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
		unit := parsedUnit{fset: fset, wholePackage: true}
		for name, pkg := range pkgs {
			unit.pkgName = name
			for _, f := range pkg.Files {
				unit.files = append(unit.files, f)
			}
		}
		return unit
	}
	f, err := parser.ParseFile(fset, absPath, nil, 0)
	if err != nil {
		t.Fatalf("wiring_helpers: cannot parse %s: %v", callerPath, err)
	}
	return parsedUnit{files: []*ast.File{f}, fset: fset, pkgName: f.Name.Name}
}

// parseFiles is the file-list view of [parseUnit], for the checks that do
// not need positions or the package name.
func parseFiles(t *testing.T, callerPath string) []*ast.File {
	t.Helper()
	return parseUnit(t, callerPath).files
}

// MustFindCallerInFile asserts that callerFile (repo-root-relative) contains a
// use of calleeIdent that really belongs to calleePackage — resolved through
// the caller file's own import list, so an import alias changing does not
// break the pin.
//
// The name-only search this replaced was a documented trade-off ("purely
// name-based to survive import-alias changes"), and it was a false dilemma:
// reading the alias out of the file gives both properties. Its cost is not
// hypothetical: a pin asserting that the daemon verifies a Matter NOC
// certificate chain was satisfied by `spake2.NewVerifier`, which
// shares nothing with certificate verification but the word `NewVerifier`, so
// deleting all three real calls would have left the pin green. The same
// blindness hid a stale argument: that pin names a package
// (`internal/north/matter/cert`) that does not exist.
//
// The check skips any occurrence of calleeIdent that is the name of a
// top-level FuncDecl in callerFile (i.e. the definition itself), so passing
// the definition file as callerFile does not produce a false positive.
// As a rule, callerFile must be a file that calls the function, not the file
// that defines it — passing the definition file is an anti-pattern that this
// guard makes harmless but does not endorse.
//
// Like [MustFindMethodCall], at least one occurrence must be one the running
// daemon executes — see [callIsExecutable] for what that check can and
// cannot establish.
//
// Usage:
//
//	MustFindCallerInFile(t, "cmd/openccu-loom/daemon.go",
//	    "internal/metrics", "NewMqttCollector")
func MustFindCallerInFile(t *testing.T, callerFile, calleePackage, calleeIdent string) {
	t.Helper()
	unit := parseUnit(t, callerFile)
	var matches []matchedCall
	importedAnywhere := false
	for _, f := range unit.files {
		if calleePackage == "" {
			// No package named: the callee is unqualified — an unexported
			// helper of the caller's own package. There is no import to
			// resolve and no qualifier to check.
			importedAnywhere = true
			for _, use := range identUsesNotDefinition(f, calleeIdent) {
				matches = append(matches, matchedCall{file: f, node: use})
			}
			continue
		}
		local, imported := importLocalName(f, calleePackage)
		switch {
		case imported:
			importedAnywhere = true
			for _, sel := range qualifiedUses(f, local, calleeIdent) {
				matches = append(matches, matchedCall{file: f, node: sel})
			}
		case samePackage(unit, calleePackage):
			// The caller lives in the callee's own package, so the call
			// carries no qualifier and there is no import to resolve.
			importedAnywhere = true
			for _, use := range identUsesNotDefinition(f, calleeIdent) {
				matches = append(matches, matchedCall{file: f, node: use})
			}
		}
	}
	if !importedAnywhere {
		t.Errorf(
			"wiring pin: %s does not import %s at all, so it cannot call %s.%s.\n"+
				"  Either the wiring is gone, or this pin names the wrong package — check\n"+
				"  the import path before assuming the first.",
			callerFile, calleePackage, calleePackage, calleeIdent,
		)
		return
	}
	if len(matches) == 0 {
		t.Errorf(
			"wiring pin: %s imports %s but calls nothing named %s from it\n"+
				"  expected a call to %s.%s",
			callerFile, calleePackage, calleeIdent, calleePackage, calleeIdent,
		)
		return
	}
	assertAnyCallExecutable(t, unit, matches, calleeIdent, callerFile)
}

// helperModulePath is this module's import prefix. It is spelled here rather
// than borrowed from a _test.go file so this non-test source keeps compiling
// on its own.
const helperModulePath = "github.com/SukramJ/openccu-loom"

// importLocalName reports the name f refers to pkgPath by — the explicit
// alias when there is one, otherwise the path's last segment — and whether f
// imports it at all. pkgPath is module-relative ("internal/model/hub").
//
// Reading the alias out of the file is what lets the pin survive an import
// being renamed while still refusing a same-named identifier from somewhere
// else entirely.
func importLocalName(f *ast.File, pkgPath string) (string, bool) {
	want := helperModulePath + "/" + pkgPath
	for _, spec := range f.Imports {
		if spec.Path == nil {
			continue
		}
		got := strings.Trim(spec.Path.Value, `"`)
		if got != want && got != pkgPath {
			continue
		}
		if spec.Name != nil {
			if spec.Name.Name == "_" || spec.Name.Name == "." {
				// A blank import calls nothing; a dot import erases the
				// qualifier this check depends on. Neither can be pinned.
				return "", false
			}
			return spec.Name.Name, true
		}
		return got[strings.LastIndex(got, "/")+1:], true
	}
	return "", false
}

// samePackage reports whether the parsed unit is itself the callee package,
// in which case calls to it carry no qualifier.
func samePackage(unit parsedUnit, pkgPath string) bool {
	if unit.pkgName == "" {
		return false
	}
	return pkgPath[strings.LastIndex(pkgPath, "/")+1:] == unit.pkgName
}

// qualifiedUses returns every use of ident in f that provably belongs to the
// package imported as local. Unlike a bare identifier search it cannot be
// satisfied by a same-named function from somewhere else.
//
// Two shapes count, because this codebase wires with both:
//
//   - a qualified reference, `local.Ident` — an ordinary call or value;
//   - a field key in a composite literal whose TYPE is qualified,
//     `local.Config{Ident: fn}` — the functional-config shape a good deal of
//     the Matter and MQTT wiring uses. The key itself is a bare identifier, so
//     only the literal's type tells you which package's field it is.
func qualifiedUses(f *ast.File, local, ident string) []ast.Node {
	var found []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if node.Sel == nil || node.Sel.Name != ident {
				return true
			}
			if x, ok := node.X.(*ast.Ident); ok && x.Name == local {
				found = append(found, node)
			}
		case *ast.CompositeLit:
			sel, ok := node.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || x.Name != local {
				return true
			}
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == ident {
					found = append(found, kv)
				}
			}
		}
		return true
	})
	return found
}

// matchedCall is one occurrence a pin's name search found.
type matchedCall struct {
	file *ast.File
	node ast.Node
}

// MustFindMethodCall asserts that callerFile contains a SelectorExpr whose
// X ends with receiverIdent and whose Sel is methodName, and that the call
// is one the running daemon executes.  This pins method-call wiring such as
// HubCoordinator.SetProgramExecutor.
//
// The second half is not decoration. A pin that only asked whether the
// method name appeared somewhere in the package source stayed green when
// the archetype call was rewritten as `_ = func() { bridge.AttachACLLister(store) }`
// — present in the file, executed by nothing — and so did every Matter unit
// test and the whole contract suite, while the ACL gate had no source and
// every stored AccessControl entry went unenforced.
//
// What "executed" means here is described on [callIsExecutable]. It is
// a source-level approximation, not a proof: the strongest form of this pin
// asserts the capability's effect through the composition root instead (see
// cmd/openccu-loom/daemon_matter_acl_wiring_test.go). Use this helper when
// the effect is out of reach, and say so in the pin's doc comment.
func MustFindMethodCall(t *testing.T, callerFile, receiverIdent, methodName string) {
	t.Helper()
	unit := parseUnit(t, callerFile)
	var matches []matchedCall
	for _, f := range unit.files {
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != methodName {
				return true
			}
			// The receiver must appear as a WHOLE segment of the expression,
			// not as a suffix of one. Suffix matching accepted `ccu` for a
			// pin written against `u`, and `dispatch` for one written against
			// `ch` — which is how a pin on a short receiver came to prove
			// nothing and was mistaken for unpinnable.
			if receiverSegmentMatches(exprString(sel.X), receiverIdent) {
				matches = append(matches, matchedCall{file: f, node: sel})
			}
			return true
		})
	}
	if len(matches) == 0 {
		t.Errorf(
			"wiring pin: method call %s.%s not found in %s",
			receiverIdent, methodName, callerFile,
		)
		return
	}
	assertAnyCallExecutable(t, unit, matches,
		fmt.Sprintf("%s.%s", receiverIdent, methodName), callerFile)
}

// MustFindMethodCallInFunc is [MustFindMethodCall] narrowed to a single
// top-level function's body, for a call that is legitimately made more than
// once in the same file for different purposes. Without the narrowing, a
// mutation that deletes the call from funcName specifically is invisible
// whenever the same receiver-and-method pair still appears at an unrelated
// call site elsewhere in the file — the pin matches the file, not the
// function it names.
func MustFindMethodCallInFunc(t *testing.T, callerFile, funcName, receiverIdent, methodName string) {
	t.Helper()
	unit := parseUnit(t, callerFile)
	var fn *ast.FuncDecl
	var fnFile *ast.File
	for _, f := range unit.files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if ok && fd.Name != nil && fd.Name.Name == funcName {
				fn = fd
				fnFile = f
				break
			}
		}
		if fn != nil {
			break
		}
	}
	if fn == nil || fn.Body == nil {
		t.Errorf("wiring pin: function %s not found in %s", funcName, callerFile)
		return
	}
	var matches []matchedCall
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != methodName {
			return true
		}
		if receiverSegmentMatches(exprString(sel.X), receiverIdent) {
			matches = append(matches, matchedCall{file: fnFile, node: sel})
		}
		return true
	})
	if len(matches) == 0 {
		t.Errorf(
			"wiring pin: method call %s.%s not found inside func %s in %s",
			receiverIdent, methodName, funcName, callerFile,
		)
		return
	}
	assertAnyCallExecutable(t, unit, matches,
		fmt.Sprintf("%s.%s in %s", receiverIdent, methodName, funcName), callerFile)
}

// receiverSegmentMatches reports whether want is one of the dot-separated
// segments of the receiver expression — `b` matches `b` and `p.b`, but not
// `sb` or `web`. A short receiver is then as discriminating as the code makes
// it: within one file `b` is one variable, and pinning it says something.
func receiverSegmentMatches(expr, want string) bool {
	if want == "" {
		return false
	}
	for _, seg := range strings.Split(expr, ".") {
		if seg == want {
			return true
		}
	}
	return false
}

// assertAnyCallExecutable passes when at least one of the occurrences the
// name search found is one the running daemon reaches, and otherwise
// reports why the first one is not.
//
// Every occurrence is considered rather than the first, because a wiring
// call is often written twice — once on the boot path and once on a reload
// or a hook — and failing on whichever the file order happened to yield
// first would be noise rather than a finding.
func assertAnyCallExecutable(t *testing.T, unit parsedUnit, matches []matchedCall, what, callerPath string) {
	t.Helper()
	// The reachability set is derived once: it is a walk over the whole
	// package, and a pin can match a dozen occurrences.
	var reachable map[string]bool
	if unit.wholePackage && unit.pkgName == "main" {
		reachable = reachableFromEntryPoints(unit)
	}
	var firstReason string
	for _, m := range matches {
		ok, reason := callIsExecutable(unit, m, reachable)
		if ok {
			return
		}
		if firstReason == "" {
			firstReason = reason
		}
	}
	t.Errorf("wiring pin: %s in %s %s", what, callerPath, firstReason)
}

// callIsExecutable reports whether the running daemon can reach the matched
// call, as far as a name-based AST scan can tell, and why not when it
// cannot.
//
// Two things are checked, both of which have been observed to hide a dead
// wiring line:
//
//  1. The call must not sit in a function literal that nothing can run.
//     Assigning the literal to the blank identifier keeps the call in the
//     file — greppable, reviewable, and never executed.
//  2. When callerPath names a whole `main` package, the enclosing top-level
//     function must be reachable from `main`, `init`, or a package-level
//     initialiser. A wiring call that survives inside a helper the
//     composition root has stopped calling is the same defect one
//     indirection further out.
//
// Neither check can prove the branch containing the call is taken; a
// capability behind a config flag that is never set still looks wired from
// here. That gap is why a security-relevant capability belongs in an
// effect assertion through the composition root rather than in a pin.
func callIsExecutable(unit parsedUnit, m matchedCall, reachable map[string]bool) (ok bool, reason string) {
	path, _ := astutil.PathEnclosingInterval(m.file, m.node.Pos(), m.node.End())
	if lit, dead := deadFuncLiteral(path); dead {
		return false, fmt.Sprintf(
			"sits in a function literal nothing runs (%s) — the call is in the source and is "+
				"never executed, which is the exact shape this pin exists to reject",
			unit.fset.Position(lit.Pos()),
		)
	}
	if discardedWithoutCall(path) {
		return false,
			"is assigned to the blank identifier and never called — the reference to it is in the " +
				"source and is never invoked, which is the exact shape this pin exists to reject"
	}
	if reachable == nil {
		// A single file, or a library package: this unit does not contain
		// the entry point, so reachability cannot be decided here.
		return true, ""
	}
	enclosing := enclosingFuncDecl(path)
	if enclosing == nil {
		// A package-level initialiser: it runs before main.
		return true, ""
	}
	if reachable[enclosing.Name.Name] {
		return true, ""
	}
	return false, fmt.Sprintf(
		"sits in %s (%s), which nothing in the composition root reaches — "+
			"the wiring is present but the daemon never runs it",
		enclosing.Name.Name, unit.fset.Position(enclosing.Pos()),
	)
}

// deadFuncLiteral reports the outermost function literal enclosing the
// matched node when that literal is assigned to nothing but blank
// identifiers, which is the one shape that keeps a call in the file while
// guaranteeing it never runs.
//
// A literal handed to a call, stored in a struct, returned, or launched
// with go/defer is NOT reported: those are how callbacks are wired, and
// rejecting them would fail the pins that watch exactly that.
func deadFuncLiteral(path []ast.Node) (*ast.FuncLit, bool) {
	var outermost *ast.FuncLit
	var parent ast.Node
	for i, n := range path {
		if lit, ok := n.(*ast.FuncLit); ok {
			outermost = lit
			if i+1 < len(path) {
				parent = path[i+1]
			}
		}
		if _, ok := n.(*ast.FuncDecl); ok {
			break
		}
	}
	if outermost == nil {
		return nil, false
	}
	switch p := parent.(type) {
	case *ast.AssignStmt:
		return outermost, allBlank(p.Lhs)
	case *ast.ValueSpec:
		names := make([]ast.Expr, 0, len(p.Names))
		for _, n := range p.Names {
			names = append(names, n)
		}
		return outermost, allBlank(names)
	default:
		return nil, false
	}
}

// discardedWithoutCall reports whether the matched node itself — path[0],
// the identifier or selector the pin found — is the direct right-hand side
// of an assignment or declaration whose every target is the blank
// identifier, without being invoked there.
//
// deadFuncLiteral catches a call wrapped in a function literal that is
// itself discarded (`_ = func() { real.Call() }`); this catches the
// simpler and easier-to-miss sibling shape, a bare reference to the named
// function or method discarded the same way (`_ = wireCUxDInterface`). Both
// keep the identifier greppable in the file while guaranteeing it never
// runs. A node that is actually called has a *ast.CallExpr as its
// immediate parent instead, which this check does not match, so a real
// invocation is never rejected by it.
//
// What it cannot decide is a reference bound to a *named* variable
// (`var keep = wireCUxDInterface`): that variable may legitimately be
// invoked from another file, and this scan sees one. The blank
// identifier is the only discard that is provably terminal from a single
// file, so the check stops there rather than guessing — the pin's claim
// is "no live call in this file", not "this function is unreachable".
func discardedWithoutCall(path []ast.Node) bool {
	if len(path) < 2 {
		return false
	}
	switch p := path[1].(type) {
	case *ast.AssignStmt:
		return allBlank(p.Lhs)
	case *ast.ValueSpec:
		names := make([]ast.Expr, 0, len(p.Names))
		for _, n := range p.Names {
			names = append(names, n)
		}
		return allBlank(names)
	default:
		return false
	}
}

// allBlank reports whether every expression is the blank identifier.
func allBlank(exprs []ast.Expr) bool {
	if len(exprs) == 0 {
		return false
	}
	for _, e := range exprs {
		id, ok := e.(*ast.Ident)
		if !ok || id.Name != "_" {
			return false
		}
	}
	return true
}

// enclosingFuncDecl returns the top-level declaration containing the
// matched node, or nil when it sits in a package-level initialiser.
func enclosingFuncDecl(path []ast.Node) *ast.FuncDecl {
	for _, n := range path {
		if fd, ok := n.(*ast.FuncDecl); ok {
			return fd
		}
	}
	return nil
}

// reachableFromEntryPoints returns the names of every top-level function
// the package's entry points can reach.
//
// The edge relation is deliberately permissive: a function counts as
// reached when its name appears anywhere in a reachable body, not only in
// call position. A method value handed to a setter, a function stored in a
// table and invoked later, an interface dispatch — all of those are real
// paths a call-position-only graph would report as dead, and a pin that
// cries wolf gets deleted. What survives the permissiveness is the case
// that matters: a function nothing in the package mentions at all.
func reachableFromEntryPoints(unit parsedUnit) map[string]bool {
	// One name can carry several declarations — a plain function and a
	// method of the same name, or two methods on different receivers. All
	// of them are followed: keeping only the last would silently drop the
	// edge that leads to the composition root, and every pin under it
	// would report a live wiring line as dead.
	decls := map[string][]*ast.FuncDecl{}
	var roots []string
	for _, f := range unit.files {
		for _, d := range f.Decls {
			switch decl := d.(type) {
			case *ast.FuncDecl:
				if decl.Name == nil || decl.Body == nil {
					continue
				}
				decls[decl.Name.Name] = append(decls[decl.Name.Name], decl)
				if decl.Name.Name == "main" || decl.Name.Name == "init" {
					roots = append(roots, decl.Name.Name)
				}
			case *ast.GenDecl:
				// A package-level initialiser runs before main, so every
				// name it mentions is an entry point too.
				for _, spec := range decl.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, v := range vs.Values {
						roots = append(roots, identsIn(v)...)
					}
				}
			}
		}
	}
	seen := map[string]bool{}
	queue := roots
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		for _, decl := range decls[name] {
			for _, next := range identsIn(decl.Body) {
				if !seen[next] {
					queue = append(queue, next)
				}
			}
		}
	}
	return seen
}

// identsIn returns every identifier name mentioned in a node, including
// selector selectors so a method call reaches the method's declaration.
func identsIn(n ast.Node) []string {
	var out []string
	ast.Inspect(n, func(node ast.Node) bool {
		switch v := node.(type) {
		case *ast.Ident:
			out = append(out, v.Name)
		case *ast.SelectorExpr:
			out = append(out, v.Sel.Name)
		}
		return true
	})
	return out
}

// MustFindStructLiteralField asserts that callerFile contains a composite
// literal whose type ends with structName and that sets fieldName.
//
// The suffix match is deliberate for a bare type name, but it is also
// its trap: two packages routinely name their wiring struct the same
// thing, and `rest.Deps` and `mcp.Deps` both end in `Deps` and share
// most of their field names. Pass the qualified type
// ("mcp.Deps") whenever the file constructs more than one — an exact
// match then applies, because a qualified name identifies the type.
func MustFindStructLiteralField(t *testing.T, callerFile, structName, fieldName string) {
	t.Helper()
	// A qualified name is matched exactly; a bare one by suffix, so a
	// caller that writes "Deps" still matches "rest.Deps".
	qualified := strings.Contains(structName, ".")
	found := false
	nilled := false
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
			if qualified {
				if typeName != structName {
					return true
				}
			} else if !strings.HasSuffix(typeName, structName) {
				return true
			}
			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				id, ok := kv.Key.(*ast.Ident)
				if !ok || id.Name != fieldName {
					continue
				}
				// `Field: nil` is the field being present and the
				// collaborator not being handed over — the exact state a
				// pin exists to rule out. Counting it as found makes the
				// pin assert the spelling of a key rather than a wiring
				// fact, and all three MCP/backup/section pins were
				// satisfiable that way until this check existed.
				if v, isIdent := kv.Value.(*ast.Ident); isIdent && v.Name == "nil" {
					nilled = true
					return false
				}
				found = true
				return false
			}
			return true
		})
		if found {
			break
		}
	}
	switch {
	case nilled:
		t.Errorf(
			"wiring pin: %s{%s: nil} in %s — the field is spelled out and the collaborator is "+
				"not handed over, which is the state the pin exists to rule out",
			structName, fieldName, callerFile,
		)
	case !found:
		t.Errorf(
			"wiring pin: struct literal %s{%s: ...} not found in %s",
			structName, fieldName, callerFile,
		)
	}
}

// MustFindStructFieldDecl asserts that callerFile declares a struct type
// named structName with a field named fieldName.
//
// This is not [MustFindCallerInFile]: a struct field is not a call, and
// searching for the bare identifier fieldName in the file — what
// MustFindCallerInFile's same-package fallback does — matches any other
// field of the same name on any other struct in the file just as readily as
// the one this pin names. Scoping to the enclosing TypeSpec is what makes
// the pin about this struct's field rather than about the identifier
// existing somewhere in the source.
func MustFindStructFieldDecl(t *testing.T, callerFile, structName, fieldName string) {
	t.Helper()
	found := false
	for _, f := range parseFiles(t, callerFile) {
		ast.Inspect(f, func(n ast.Node) bool {
			if found {
				return false
			}
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Name == nil || ts.Name.Name != structName {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if name.Name == fieldName {
						found = true
						return false
					}
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
			"wiring pin: struct %s has no field %s in %s",
			structName, fieldName, callerFile,
		)
	}
}

// MustFindStructFieldTag asserts that structName.fieldName in callerFile
// carries wantTag as its full struct tag literal.
//
// It exists because a field declaration and the tag that binds it to the
// wire are two different facts, and only the second one fails silently:
// deleting the field breaks the build at every reader, while renaming
// `json:"label_key"` to anything else compiles, passes a
// field-declaration pin, and simply decodes nothing — which is precisely
// the defect such a pin's doc comment usually claims to prevent.
//
// wantTag is the whole back-quoted tag content, so a partial match
// cannot pass: `json:"label_key"` and `json:"label_key,omitempty"` are
// different bindings and the pin names which one it means.
func MustFindStructFieldTag(t *testing.T, callerFile, structName, fieldName, wantTag string) {
	t.Helper()
	var got string
	declared := false
	for _, f := range parseFiles(t, callerFile) {
		ast.Inspect(f, func(n ast.Node) bool {
			if declared {
				return false
			}
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Name == nil || ts.Name.Name != structName {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if name.Name != fieldName {
						continue
					}
					declared = true
					if field.Tag != nil {
						got = strings.Trim(field.Tag.Value, "`")
					}
					return false
				}
			}
			return true
		})
		if declared {
			break
		}
	}
	switch {
	case !declared:
		t.Errorf("wiring pin: struct %s has no field %s in %s", structName, fieldName, callerFile)
	case got != wantTag:
		t.Errorf(
			"wiring pin: %s.%s in %s carries tag `%s`, want `%s` — the field is declared but bound to a different wire name, so the value decodes as empty",
			structName, fieldName, callerFile, got, wantTag,
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

// identUsesNotDefinition returns every occurrence of ident in f that is
// NOT the FuncDecl name at the top level of f.  This prevents
// the self-caller false positive where the definition file is mistakenly
// supplied as callerFile: a file that only defines SetFoo will not match
// even though SetFoo appears as an *ast.Ident.
//
// The check treats a top-level FuncDecl whose Name.Name == ident as the
// "definition" position.  Any other occurrence — CallExpr arguments,
// SelectorExpr selectors, variable initialisers — counts as a usage.
func identUsesNotDefinition(f *ast.File, ident string) []*ast.Ident {
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

	var found []*ast.Ident
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if id.Name == ident && !inDefinitionSpan(id.Pos()) {
			found = append(found, id)
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
