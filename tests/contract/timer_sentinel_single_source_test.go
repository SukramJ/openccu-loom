// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// timerSentinelHome is the one file allowed to spell the disabled-timer
// sentinel out. custom.TimerNotUsed is a CCU wire fact about the ON_TIME /
// RAMP_TIME / DURATION parameter family, and internal/model/custom is the only
// package the timer's other two readers — internal/model/custom/light and
// internal/model/combined — can both import without a cycle.
const timerSentinelHome = "internal/model/custom/mixins.go"

// timerSentinelValue is the sentinel itself. The guard reads literals
// numerically, so 111600, 111600.0 and 1.116e5 are all recognised as copies.
const timerSentinelValue = 111600.0

// timerSentinelAllowedOutsideHome names files that may carry the literal for a
// reason other than declaring the rule, with that reason.
var timerSentinelAllowedOutsideHome = map[string]string{}

// TestTimerSentinelHasOneDeclaration fails when a package spells the
// disabled-timer sentinel out for itself instead of reading
// custom.TimerNotUsed.
//
// It was declared three times — in internal/model/custom, in
// internal/model/custom/light as the exported NotUsed, and in
// internal/model/combined — and the three were coupled by nothing but float
// equality: a light stages the sentinel as a duration and an encoder in
// another package compares it against its own copy, so a single edited digit
// would have turned "cancel the timer" into a real 31-hour on-time with every
// test still green.
//
// Two limits, so nobody reads more into a green run than it carries. The guard
// enforces a single source, not agreement: a divergent redeclaration such as
// 111700 escapes it entirely, and the value itself is pinned by the encoder's
// own sentinel case in internal/model/custom. And it walks internal/ and pkg/
// only, so cmd/ and the test trees are out of its reach.
func TestTimerSentinelHasOneDeclaration(t *testing.T) {
	t.Parallel()
	const root = "../.."
	var found []string

	inspect := func(rel, path string) {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", rel, err)
			return
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || (lit.Kind != token.INT && lit.Kind != token.FLOAT) {
				return true
			}
			v, err := strconv.ParseFloat(lit.Value, 64)
			if err != nil || v != timerSentinelValue {
				return true
			}
			found = append(found, rel)
			return true
		})
	}

	for _, sub := range []string{"internal", "pkg"} {
		err := filepath.Walk(filepath.Join(root, sub), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err //nolint:wrapcheck // walk error is returned as-is
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr //nolint:wrapcheck // path error is returned as-is
			}
			inspect(filepath.ToSlash(rel), path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sub, err)
		}
	}

	if len(found) == 0 {
		t.Fatalf("the sentinel %v is declared nowhere under internal/ or pkg/ — "+
			"the guard lost its subject", timerSentinelValue)
	}
	sort.Strings(found)
	seenHome := false
	for _, rel := range found {
		if rel == timerSentinelHome {
			seenHome = true
			continue
		}
		if reason, ok := timerSentinelAllowedOutsideHome[rel]; ok {
			t.Logf("%s carries the sentinel: %s", rel, reason)
			continue
		}
		t.Errorf("%s spells the disabled-timer sentinel %v out; read "+
			"custom.TimerNotUsed instead, declared in %s",
			rel, timerSentinelValue, timerSentinelHome)
	}
	if !seenHome {
		t.Fatalf("%s no longer declares the sentinel %v — if the rule moved, "+
			"move timerSentinelHome with it", timerSentinelHome, timerSentinelValue)
	}
}
