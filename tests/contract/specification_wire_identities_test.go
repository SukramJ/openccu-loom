// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// TestSpecificationInterfaceConstantsMatchCode pins the Go snippet in
// SPECIFICATION.md §5 that quotes pkg/hmenum/interface.go against the real
// constant block in that file.
//
// The snippet is introduced by a "// pkg/hmenum/interface.go" comment, which
// claims verbatim provenance. That claim decayed: the document listed a sixth
// interface, "Groups", that exists in neither the code nor the reference
// implementation. Nothing failed, because no test compared the two — the
// snippet was prose that happened to be shaped like Go.
//
// Interface strings are a wire contract with the CCU: they are sent verbatim
// in every init() and every callback route. A reader who trusts a documented
// interface that does not exist writes code against a value the daemon can
// never receive.
//
// A failure means one of two things, and both require a decision rather than
// a silenced test:
//   - the constant block changed and the specification was not updated
//   - the specification describes an interface that has not been implemented
//
// The comparison is on the set of (identifier, wire string) pairs. Their
// order is deliberately NOT pinned: the CCU never sees the declaration order,
// so a guard that enforced it would fail on a purely cosmetic re-sort and
// would be ratcheted away long before it caught a real drift.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// interfaceConstPattern matches one `InterfaceFoo Interface = "wire-name"`
// declaration, in the document snippet as well as in the Go source.
var interfaceConstPattern = regexp.MustCompile(`(Interface[A-Za-z0-9_]*)\s+Interface\s*=\s*"([^"]+)"`)

// specInterfaceSnippet extracts the fenced Go block in SPECIFICATION.md that
// declares itself to be pkg/hmenum/interface.go.
func specInterfaceSnippet(t *testing.T, specPath string) []string {
	t.Helper()

	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}

	const provenance = "// pkg/hmenum/interface.go"
	idx := strings.Index(string(raw), provenance)
	if idx < 0 {
		t.Fatalf("%s no longer contains a snippet claiming provenance %q — "+
			"if the snippet moved, point this guard at its new location; "+
			"do not delete the guard", specPath, provenance)
	}

	rest := string(raw)[idx:]
	end := strings.Index(rest, "```")
	if end < 0 {
		t.Fatalf("%s: unterminated code fence after %q", specPath, provenance)
	}

	return pairsFrom(rest[:end])
}

// pairsFrom renders every interface declaration in src as "Ident=wire", in
// source order.
func pairsFrom(src string) []string {
	matches := interfaceConstPattern.FindAllStringSubmatch(src, -1)
	pairs := make([]string, 0, len(matches))
	for _, m := range matches {
		pairs = append(pairs, fmt.Sprintf("%s=%s", m[1], m[2]))
	}
	return pairs
}

// codeInterfaceConstants reads the constant block from the Go source with the
// type checker's own parser, so a value inside a comment or a string literal
// cannot be mistaken for a declaration.
func codeInterfaceConstants(t *testing.T, srcPath string) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, srcPath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", srcPath, err)
	}

	var pairs []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != "Interface" {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			wire, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			pairs = append(pairs, fmt.Sprintf("%s=%s", vs.Names[0].Name, wire))
		}
	}
	return pairs
}

func TestSpecificationInterfaceConstantsMatchCode(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	documented := specInterfaceSnippet(t, filepath.Join(root, "SPECIFICATION.md"))
	implemented := codeInterfaceConstants(t, filepath.Join(root, "pkg", "hmenum", "interface.go"))

	if len(documented) == 0 {
		t.Fatal("SPECIFICATION.md §5 snippet declares no interface constants — " +
			"the extraction broke, which would make this guard silently vacuous")
	}

	documentedSet := map[string]bool{}
	for _, p := range documented {
		documentedSet[p] = true
	}
	implementedSet := map[string]bool{}
	for _, p := range implemented {
		implementedSet[p] = true
	}

	for _, p := range documented {
		if !implementedSet[p] {
			t.Errorf("SPECIFICATION.md documents interface constant %q, "+
				"pkg/hmenum/interface.go does not declare it.\n"+
				"Either the specification describes an unimplemented interface, "+
				"or the constant was renamed and the document was not updated.", p)
		}
	}
	for _, p := range implemented {
		if !documentedSet[p] {
			t.Errorf("pkg/hmenum/interface.go declares interface constant %q, "+
				"the SPECIFICATION.md §5 snippet omits it.\n"+
				"The snippet claims verbatim provenance from that file; add the "+
				"constant to the document.", p)
		}
	}
}
