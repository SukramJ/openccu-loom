// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// emptyAbsentFailedCollapseExemptions lists every read function that
// answers a failed lookup with the zero value and a nil error instead of
// propagating the error, each with the reason the collapse is safe.
//
// It is a ratchet: it may shrink freely as entries are tightened to a
// genuine sentinel check or deleted outright. Growing it needs a reason
// in the same commit, and "the caller doesn't check the error anyway" is
// not one — that is exactly the collapse this guard exists to make loud.
// A safe entry names either the specific sentinel the branch narrows to
// (sql.ErrNoRows, a store's own NotFound error) or, for a capability
// probe, the fact that the caller cannot distinguish "unsupported" from
// "absent" and does not need to.
var emptyAbsentFailedCollapseExemptions = map[string]string{
	"internal/client/interface_client_orchestration.go:346:FetchAllDeviceData":                 "guarded by isUnsupported(err): the backend declared it does not implement GetAllDeviceData, which is a capability fact, not an unknown query outcome",
	"internal/client/interface_client_orchestration.go:363:FetchDeviceDetails":                 "guarded by isUnsupported(err): same capability-probe shape as FetchAllDeviceData",
	"internal/client/interface_client_orchestration.go:444:GetDeviceDescriptionWithCoalescing": "guarded by isUnsupported(err) before the coalesced call's error is inspected",
	"internal/client/interface_client_orchestration.go:689:GetParamsetDescriptionOnDemand":     "guarded by isUnsupported(err): LINK paramsets are optional per backend",
	"internal/store/linkprofile/store.go:205:GetLinkProfiles":                                  "the branch narrows on an unknown receiver channel type from Store.load, which the store's own contract treats as \"no profiles registered\", not a query failure",
	"internal/store/sqlite/devices.go:221:GetModel":                                            "the branch narrows via errors.Is(err, sql.ErrNoRows), the specific sentinel for a query that ran and found nothing",
	"internal/store/sqlite/paramsets.go:236:GetParameterData":                                  "the branch narrows via errors.Is(err, ErrParamsetNotFound), the store's own not-found sentinel, documented as the method's nil,nil contract",
	"cmd/openccu-loom/daemon_matter.go:1663:GetByID":                                           "any resumption-lookup failure intentionally falls through to a full CASE handshake (matter.js CaseServer.ts resumption branch); the fallback is the safe direction, never a deletion",
}

// readFuncNameRE matches the read-verb prefixes this guard cares about:
// a function whose name promises to report what a source outside the
// process holds.
var readFuncNameRE = regexp.MustCompile(`^(Get|Load|Fetch|List|Read|Query)[A-Z0-9]`)

// collapseHit is one function-level violation: an error branch that
// returns the zero value with a nil error instead of the error itself.
type collapseHit struct {
	relFile string
	line    int
	fn      string
}

// key identifies one specific branch, not just the enclosing function —
// a function can carry both a narrowed sentinel check (safe) and a
// broad collapse (a defect) at different lines, and the two must never
// share one exemption entry.
func (h collapseHit) key() string {
	return fmt.Sprintf("%s:%d:%s", h.relFile, h.line, h.fn)
}

// isZeroValueWithNilError reports whether rs is `return <zero>, nil`:
// the shape a two-result read function uses to report "found nothing"
// and "could not find out" identically.
func isZeroValueWithNilError(rs *ast.ReturnStmt) bool {
	if len(rs.Results) != 2 {
		return false
	}
	errID, ok := rs.Results[1].(*ast.Ident)
	if !ok || errID.Name != "nil" {
		return false
	}
	switch v := rs.Results[0].(type) {
	case *ast.Ident:
		return v.Name == "nil"
	case *ast.BasicLit:
		return v.Value == `""` || v.Value == "0"
	}
	return false
}

// ifConditionMentionsError reports whether an if-condition tests an
// error-shaped identifier — the guard only cares about branches taken
// because a fallible call reported failure, not about branches taken on
// ordinary business data.
func ifConditionMentionsError(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && (id.Name == "err" || strings.HasSuffix(id.Name, "Err")) {
			found = true
		}
		return true
	})
	return found
}

// findCollapseHits walks fn's body for `if <err-condition> { return
// <zero>, nil }` — an error path that answers with the same value a
// genuinely empty success path would produce.
func findCollapseHits(fn *ast.FuncDecl, fset *token.FileSet, relFile string) []collapseHit {
	var hits []collapseHit
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || !ifConditionMentionsError(ifs.Cond) {
			return true
		}
		for _, stmt := range ifs.Body.List {
			rs, ok := stmt.(*ast.ReturnStmt)
			if ok && isZeroValueWithNilError(rs) {
				hits = append(hits, collapseHit{
					relFile: relFile,
					line:    fset.Position(rs.Pos()).Line,
					fn:      fn.Name.Name,
				})
			}
		}
		return true
	})
	return hits
}

// scanForCollapseHits walks the given source roots (relative to repo
// root) for functions matching readFuncNameRE and collects every
// error-swallowing zero-value return found in their bodies.
func scanForCollapseHits(t *testing.T, root string, dirs []string) []collapseHit {
	t.Helper()
	var hits []collapseHit
	fset := token.NewFileSet()
	for _, dir := range dirs {
		walkErr := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				// Propagated rather than skipped, and the distinction is this
				// guard's own subject: a directory that cannot be read is not
				// a directory with nothing in it. Swallowing it here would
				// make the scan quietly cover less and report fewer offenders,
				// which is exactly the collapse the guard exists to find.
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatalf("parse %s: %v", path, perr)
			}
			relFile, relErr := filepath.Rel(root, path)
			if relErr != nil {
				t.Fatalf("rel path for %s: %v", path, relErr)
			}
			relFile = filepath.ToSlash(relFile)
			for _, decl := range src.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || !readFuncNameRE.MatchString(fn.Name.Name) {
					continue
				}
				hits = append(hits, findCollapseHits(fn, fset, relFile)...)
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", dir, walkErr)
		}
	}
	return hits
}

// TestReadFunctionsDoNotCollapseFailureIntoEmpty guards against the
// defect class the round-4 audit found 34 times: a Get*/Load*/Fetch*/
// List*/Read*/Query* function that fetches from a source outside the
// process (the CCU, SQLite, the broker) answering an error branch with
// the zero value and a nil error, so a caller cannot tell "there is
// nothing" from "I could not find out". The cost lands downstream, in a
// destructive sweep that reads the collapsed answer as "safe to delete".
//
// Every hit must be either fixed (narrow the branch to the actual
// not-found sentinel, or propagate the error) or ratcheted into
// emptyAbsentFailedCollapseExemptions with a reason that names the
// specific sentinel or capability fact that makes the collapse safe.
func TestReadFunctionsDoNotCollapseFailureIntoEmpty(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatalf("repo root resolution: %v", err)
	}

	hits := scanForCollapseHits(t, root, []string{"internal", "pkg", "cmd"})

	seen := make(map[string]bool)
	var unexempted []collapseHit
	for _, h := range hits {
		key := h.key()
		if _, ok := emptyAbsentFailedCollapseExemptions[key]; !ok {
			unexempted = append(unexempted, h)
			continue
		}
		seen[key] = true
	}

	if len(unexempted) > 0 {
		sort.Slice(unexempted, func(i, j int) bool {
			if unexempted[i].relFile != unexempted[j].relFile {
				return unexempted[i].relFile < unexempted[j].relFile
			}
			return unexempted[i].line < unexempted[j].line
		})
		var b strings.Builder
		b.WriteString("found read function(s) that collapse a failed lookup into an empty success (zero value, nil error) with no exemption:\n")
		for _, h := range unexempted {
			fmt.Fprintf(&b, "  %s:%d in %s — narrow the branch to the real not-found sentinel, propagate the error, or add %q to emptyAbsentFailedCollapseExemptions with a true reason\n",
				h.relFile, h.line, h.fn, h.key())
		}
		t.Fatal(b.String())
	}

	var stale []string
	for key := range emptyAbsentFailedCollapseExemptions {
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("emptyAbsentFailedCollapseExemptions has entries the scan no longer finds — remove them: %s", strings.Join(stale, ", "))
	}
}
