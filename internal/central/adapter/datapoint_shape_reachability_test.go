// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// accessorCastsWithoutReachableShape exempts an (accessor, parameter) pair
// whose cast the resolver can never satisfy, with the reason that is
// correct. An entry is a claim that the call is deliberately dead — not
// that nobody has looked at it yet.
//
// It is empty on purpose. A pair belongs here only when the parameter is
// known to exist on no device the daemon can reach, and the call is kept
// for a documented reason; the fix for every other case is to cast to the
// shape the resolver actually produces.
var accessorCastsWithoutReachableShape = map[string]string{}

// TestCustomFieldAccessorsCastToAShapeTheResolverCanProduce asserts that
// every custom.<X>Field(channel, hmenum.Parameter<Y>) call in production
// asks for a shape that resolveDataPointWithUnIgnore can actually produce
// for that parameter.
//
// The two halves of this seam are written in different packages and never
// meet at compile time. resolveDataPointWithUnIgnore picks the concrete
// shape from (type, operations, value-list, parameter *name*); the
// accessors in internal/model/custom/resolve.go type-assert onto one
// specific shape. A failed assertion is a runtime nil, not a compile
// error, so the consumer reports "not supported" and every test around it
// stays green.
//
// That is how 0.58.0 shipped the motion reset with no effect at all:
// RESET_MOTION is in buttonActionParameters, so the resolver produces a
// *generic.Button for it under every wire constellation, while the
// consumer asserted *generic.Action. Supports() was false forever,
// /alarm/triggered-motion returned [] forever, and the CI was green
// because every test injected its own port fake.
//
// The resolver itself is the oracle here — the test enumerates the whole
// wire space and asks it, rather than restating its rules. A rule this
// test restated would drift away from the rule the daemon applies, which
// is the exact failure it exists to prevent. Four of those rules are
// name-based and override the type entirely (buttonActionParameters,
// Parameter.IsClickEvent, ImpulseEvents, isDeviceErrorEvent), which is
// why a per-parameter answer is needed at all.
func TestCustomFieldAccessorsCastToAShapeTheResolverCanProduce(t *testing.T) {
	t.Parallel()
	root := adapterRepoRoot(t)

	accessors := customFieldAccessorShapes(t, root)
	if len(accessors) == 0 {
		t.Fatal("no *Field accessors found in internal/model/custom/resolve.go — the walk is broken and this test would pass vacuously")
	}
	parameters := hmenumParameterValues(t, root)
	if len(parameters) == 0 {
		t.Fatal("no Parameter constants found in pkg/hmenum — the walk is broken and this test would pass vacuously")
	}

	sites, unresolved := accessorCallsites(t, root, accessors)
	if len(sites) == 0 {
		t.Fatal("no accessor call sites resolved to a constant parameter — the walk is broken and this test would pass vacuously")
	}
	t.Logf("checked %d accessor call sites (%d call sites pass a non-constant parameter and cannot be checked statically)",
		len(sites), unresolved)

	// Cache per parameter: several accessors ask for the same one.
	reachable := make(map[string]map[string]bool)

	for _, s := range sites {
		value, ok := parameters[s.parameterConst]
		if !ok {
			// A parameter constant this walk cannot resolve to its wire
			// string is a gap in the walk, not a defect in the caller.
			t.Errorf("%s:%d calls %s with hmenum.%s, which this test cannot resolve to a wire parameter name — "+
				"extend hmenumParameterValues so the pair is checked instead of silently skipped",
				s.file, s.line, s.accessor, s.parameterConst)
			continue
		}
		if _, done := reachable[value]; !done {
			reachable[value] = reachableShapesForParameter(value)
		}
		want := accessors[s.accessor]
		key := s.accessor + "/" + s.parameterConst
		reason, exempt := accessorCastsWithoutReachableShape[key]

		switch {
		case reachable[value][want] && exempt:
			t.Errorf("%s is listed in accessorCastsWithoutReachableShape (%q) but the resolver does produce %s for %s — "+
				"drop the entry so the list keeps meaning what it says", key, reason, want, value)
		case !reachable[value][want] && !exempt:
			t.Errorf("%s:%d asserts %s for parameter %s, which the resolver never produces for it.\n"+
				"  The call resolves to nil on every device, so the feature behind it is silently dead — "+
				"a failed type assertion is a runtime false, not a compile error.\n"+
				"  Shapes the resolver can produce for %s: %s\n"+
				"  Fix the cast (or reach the value through a capability interface); exempt it only with a "+
				"reason in accessorCastsWithoutReachableShape.",
				s.file, s.line, want, value, value, describeShapes(reachable[value]))
		}
	}

	for key := range accessorCastsWithoutReachableShape {
		found := false
		for _, s := range sites {
			if s.accessor+"/"+s.parameterConst == key {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("accessorCastsWithoutReachableShape names %q, which no production call site matches any more — "+
				"a stale entry silently exempts nothing and hides the next real one", key)
		}
	}
}

// reachableShapesForParameter returns every concrete data-point type
// resolveDataPointWithUnIgnore produces for the named parameter across the
// entire wire space: all parameter types, all OPERATIONS bit combinations,
// representative value lists, and both un-ignore states.
//
// Exhaustive enumeration is what makes the answer a proof rather than a
// sample: a shape absent from the result cannot be produced by any device
// description the CCU could send for this parameter.
func reachableShapesForParameter(parameter string) map[string]bool {
	parameterTypes := []hmenum.ParameterType{
		hmenum.ParameterTypeAction,
		hmenum.ParameterTypeBool,
		hmenum.ParameterTypeDummy,
		hmenum.ParameterTypeEnum,
		hmenum.ParameterTypeFloat,
		hmenum.ParameterTypeInteger,
		hmenum.ParameterTypeString,
		hmenum.ParameterTypeEmpty,
	}
	// One list per branch isBinarySensor can take: absent, a pair that
	// marks a binary sensor, a pair that does not, and a longer list.
	valueLists := [][]string{
		nil,
		{"CLOSED", "OPEN"},
		{"LEFT", "RIGHT"},
		{"OFF", "SLOW", "FAST"},
	}

	shapes := make(map[string]bool)
	// OPERATIONS is a bitmask over READ|WRITE|EVENT|DETERMINE (1|2|4|8).
	for ops := range 0b1_0000 {
		for _, pt := range parameterTypes {
			for _, vl := range valueLists {
				for _, unIgnored := range []bool{false, true} {
					cfg := generic.Spec{
						Key: hmtypes.DataPointKey{
							ChannelAddress: "TESTDEV:1",
							ParamsetKey:    hmenum.ParamsetKeyValues,
							Parameter:      parameter,
						},
						Descriptor: hmproto.ParameterData{
							Type:       pt,
							Operations: hmenum.Operations(ops),
							ValueList:  vl,
						},
					}
					if dp := resolveDataPointWithUnIgnore(cfg, unIgnored); dp != nil {
						shapes[fmt.Sprintf("%T", dp)] = true
					}
				}
			}
		}
	}
	return shapes
}

// describeShapes renders a shape set for a failure message.
func describeShapes(shapes map[string]bool) string {
	if len(shapes) == 0 {
		return "none at all — the resolver drops this parameter under every wire constellation"
	}
	out := make([]string, 0, len(shapes))
	for s := range shapes {
		out = append(out, s)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// accessorCallsite is one production call of a custom.<X>Field accessor
// with a constant parameter argument.
type accessorCallsite struct {
	file           string
	line           int
	accessor       string
	parameterConst string
}

// customFieldAccessorShapes maps every `*Field` accessor declared in
// internal/model/custom/resolve.go to the shape it returns, read off its
// own signature. Reading the signature rather than hard-coding the table
// keeps the guard correct when an accessor's return type changes.
func customFieldAccessorShapes(t *testing.T, root string) map[string]string {
	t.Helper()
	path := filepath.Join(root, "internal", "model", "custom", "resolve.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := make(map[string]string)
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name == nil || !strings.HasSuffix(fd.Name.Name, "Field") {
			continue
		}
		if fd.Type.Results == nil || len(fd.Type.Results.List) != 1 {
			continue
		}
		out[fd.Name.Name] = types.ExprString(fd.Type.Results.List[0].Type)
	}
	return out
}

// hmenumParameterValues maps every `Parameter<Name>` constant in pkg/hmenum
// to its wire string.
func hmenumParameterValues(t *testing.T, root string) map[string]string {
	t.Helper()
	dir := filepath.Join(root, "pkg", "hmenum")
	out := make(map[string]string)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return perr
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				name := vs.Names[0].Name
				if !strings.HasPrefix(name, "Parameter") {
					continue
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, uerr := strconv.Unquote(lit.Value)
				if uerr != nil {
					continue
				}
				out[name] = value
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// accessorCallsites walks every production Go file under internal/ and
// cmd/ and returns each call of one of the named accessors whose parameter
// argument is an `hmenum.Parameter<Name>` constant, plus a count of the
// calls that pass a non-constant parameter (a loop variable, a struct
// field) and therefore cannot be checked statically.
func accessorCallsites(t *testing.T, root string, accessors map[string]string) (sites []accessorCallsite, unresolved int) {
	t.Helper()
	for _, tree := range []string{"internal", "cmd"} {
		dir := filepath.Join(root, tree)
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "spa_dist" || d.Name() == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return perr
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = path
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := calleeIdentName(call.Fun)
				if _, isAccessor := accessors[name]; !isAccessor {
					return true
				}
				if len(call.Args) != 2 {
					return true
				}
				sel, ok := call.Args[1].(*ast.SelectorExpr)
				if !ok || !strings.HasPrefix(sel.Sel.Name, "Parameter") || !isHmenumQualified(sel) {
					// Not a constant `hmenum.ParameterX`: either a plain
					// variable, or a struct field that merely happens to
					// be named `Parameter` — which is what a slot
					// resolved through the profile schema passes. The
					// qualifier check matters: without it such a field
					// read is mistaken for the constant `hmenum.Parameter`
					// and reported as an unresolvable constant.
					//
					// A schema-resolved binding cannot be checked from
					// the call site, because the parameter is chosen per
					// device family at runtime. The per-family bindings
					// are pinned end-to-end instead — see the profile
					// materialisation tests in
					// internal/model/custom/climate.
					unresolved++
					return true
				}
				sites = append(sites, accessorCallsite{
					file:           rel,
					line:           fset.Position(call.Pos()).Line,
					accessor:       name,
					parameterConst: sel.Sel.Name,
				})
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return sites, unresolved
}

// isHmenumQualified reports whether the selector reads a constant off the
// hmenum package (`hmenum.ParameterState`) rather than a field off some
// value that happens to carry a Parameter-prefixed name.
func isHmenumQualified(sel *ast.SelectorExpr) bool {
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "hmenum"
}

// calleeIdentName returns the called function's own name, whether the call
// is package-qualified (custom.FloatField) or local (FloatField).
func calleeIdentName(fun ast.Expr) string {
	switch v := fun.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	default:
		return ""
	}
}

// adapterRepoRoot resolves the repository root from this file's location
// (internal/central/adapter/ → three levels up).
func adapterRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	if err != nil {
		t.Fatalf("repo root resolution: %v", err)
	}
	return abs
}
