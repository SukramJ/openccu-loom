// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rega

import (
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

// The ReGa charset rule is per FIELD, not per package: a value reaches
// internal/client/rega percent-encoded only when its script passes it through
// UriEncode(), and the CCU's runtime being Latin-1 is what makes the
// percent-decode alone insufficient (see that package's doc comment).
//
// A package-level "every string field is percent-encoded" would be a property
// of the data source stated more broadly than the source carries: 30 of the 37
// embedded scripts never call UriEncode, and running url.QueryUnescape over a
// raw field turns '+' into a space. So the authority for "which fields" has to
// be the scripts, and the prose has to follow them.
//
// This guard makes that direction real: it derives the encoded field set from
// the .fn sources and requires each Runner method (or its result type) to name
// those fields as percent-encoded. Adding a UriEncode() to a script without
// documenting the field turns it red, and so does deleting the sentence from a
// method that still returns an encoded field.
var (
	// w2CliRegaAssign matches a ReGa assignment, capturing the target
	// variable and the whole right-hand side up to the statement's ';'.
	w2CliRegaAssign = regexp.MustCompile(`(?m)^[ \t]*(?:string[ \t]+|integer[ \t]+|boolean[ \t]+)?([A-Za-z_]\w*)[ \t]*=[ \t]*([^;]*);`)
	// w2CliRegaWrittenPair matches one JSON member emitted by a Write()
	// concatenation chain — `"key":"' # expr # '"` and the `[' # expr # ']`
	// array form — capturing the key and the interpolated expression.
	w2CliRegaWrittenPair = regexp.MustCompile(`"(\w+)"\s*:\s*"?\[?'\s*#\s*([^#]+?)\s*#\s*'`)
)

// w2CliRegaEncodedKeys returns the JSON keys the script writes from a
// UriEncode()d expression, either inline or through a variable that was
// assigned one.
//
// The extraction under-reports rather than over-reports: a Write shape it does
// not recognise yields no key, which relaxes the guard for that field instead
// of failing a correct comment.
func w2CliRegaEncodedKeys(body string) []string {
	encoded := map[string]bool{}
	for _, m := range w2CliRegaAssign.FindAllStringSubmatch(body, -1) {
		if strings.Contains(m[2], "UriEncode(") {
			encoded[m[1]] = true
		}
	}
	var keys []string
	for _, m := range w2CliRegaWrittenPair.FindAllStringSubmatch(body, -1) {
		expr := strings.TrimSpace(m[2])
		if strings.Contains(expr, "UriEncode(") || encoded[expr] {
			keys = append(keys, m[1])
		}
	}
	return keys
}

func TestW2CliRegaEncodedFieldsAreDocumented(t *testing.T) {
	t.Parallel()

	// The guard runs in the package's own directory; pkg/hmenum is the one
	// source it has to reach outside it.
	regaDir := "."
	hmenumScripts := filepath.Join("..", "..", "..", "pkg", "hmenum", "rega_script.go")
	fset := token.NewFileSet()

	// 1. hmenum.RegaScript<Name> → script file base name. Read from the
	//    declaration, so the constant naming convention is never assumed.
	hmenumFile, err := parser.ParseFile(fset, hmenumScripts, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse pkg/hmenum/rega_script.go: %v", err)
	}
	scriptOfConst := map[string]string{}
	ast.Inspect(hmenumFile, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
			return true
		}
		if id, ok := spec.Type.(*ast.Ident); !ok || id.Name != "RegaScript" {
			return true
		}
		lit, ok := spec.Values[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		scriptOfConst[spec.Names[0].Name] = value
		return true
	})
	if len(scriptOfConst) == 0 {
		t.Fatal("no RegaScript constants parsed — the guard would pass vacuously")
	}

	// 2. runner.go: methods, result struct types and every doc comment.
	runner, err := parser.ParseFile(fset, filepath.Join(regaDir, "runner.go"), nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse runner.go: %v", err)
	}
	structs := map[string]*ast.StructType{}
	typeDoc := map[string]string{}
	ast.Inspect(runner, func(n ast.Node) bool {
		gd, ok := n.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			return true
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			structs[ts.Name.Name] = st
			doc := gd.Doc.Text()
			if ts.Doc != nil {
				doc += ts.Doc.Text()
			}
			typeDoc[ts.Name.Name] = doc
		}
		return true
	})

	enforced := 0
	for _, decl := range runner.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		// Which script does this method run, and into which type?
		script, resultType := "", ""
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "hmenum" {
					if s, ok := scriptOfConst[sel.Sel.Name]; ok {
						script = s
					}
				}
			}
			if vs, ok := n.(*ast.ValueSpec); ok && vs.Type != nil {
				switch typ := vs.Type.(type) {
				case *ast.ArrayType:
					if id, ok := typ.Elt.(*ast.Ident); ok {
						resultType = id.Name
					}
				case *ast.Ident:
					resultType = typ.Name
				}
			}
			return true
		})
		if script == "" || structs[resultType] == nil {
			continue
		}

		body, err := os.ReadFile(filepath.Join(regaDir, "scripts", script+".fn"))
		if err != nil {
			t.Fatalf("read script %s.fn for %s: %v", script, fn.Name.Name, err)
		}
		keys := w2CliRegaEncodedKeys(string(body))
		if len(keys) == 0 {
			continue
		}

		doc := fn.Doc.Text() + typeDoc[resultType]
		for _, field := range structs[resultType].Fields.List {
			if field.Doc != nil {
				doc += field.Doc.Text()
			}
		}

		for _, key := range keys {
			fieldName := w2CliGoFieldForJSONKey(structs[resultType], key)
			if fieldName == "" {
				t.Errorf("%s.fn encodes JSON key %q with UriEncode(), but %s carries no field with that json tag — the encoded value has nowhere to be documented",
					script, key, resultType)
				continue
			}
			enforced++
			named := regexp.MustCompile(`\b` + regexp.QuoteMeta(fieldName) + `\b`).MatchString(doc)
			if !named || !strings.Contains(doc, "percent-encoded") {
				t.Errorf("%s.fn writes %q through UriEncode(), so %s.%s reaches callers percent-encoded Latin-1 — but neither %s's doc comment nor %s's says so (named=%v, phrase=%v). A caller that percent-decodes without the Latin-1 transcode corrupts every non-ASCII value irreversibly; a caller that skips both reads %%FC escapes as text",
					script, key, resultType, fieldName, fn.Name.Name, resultType, named, strings.Contains(doc, "percent-encoded"))
			}
		}
	}
	if enforced == 0 {
		t.Fatal("no encoded (script, field) pair was checked — the guard would pass vacuously")
	}

	// 3. The package doc must name the mechanism the field set comes from.
	//    Without UriEncode() in the text the rule can only be stated as a
	//    universal over "every string field", which the scripts contradict.
	docFile, err := parser.ParseFile(fset, filepath.Join(regaDir, "doc.go"), nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse rega/doc.go: %v", err)
	}
	if !strings.Contains(docFile.Doc.Text(), "UriEncode") {
		t.Error("internal/client/rega's package doc states the charset rule without naming UriEncode(): the scripts apply it to 16 places in 7 of 37 files, so a rule stated over every string field claims more than the source carries")
	}
}

// w2CliGoFieldForJSONKey returns the Go field name whose json tag is key, or
// "" when the struct has no such field.
func w2CliGoFieldForJSONKey(st *ast.StructType, key string) string {
	for _, field := range st.Fields.List {
		if field.Tag == nil || len(field.Names) != 1 {
			continue
		}
		tag, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			continue
		}
		for _, part := range strings.Split(tag, " ") {
			if !strings.HasPrefix(part, `json:"`) {
				continue
			}
			name, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSuffix(part, `"`), `json:"`), ",")
			if name == key {
				return field.Names[0].Name
			}
		}
	}
	return ""
}
